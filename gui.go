//go:build gui

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/pocketbase/pocketbase"
	qrcode "github.com/skip2/go-qrcode"
	"github.com/spf13/cobra"
)

// The desktop front end, behind the `gui` build tag.
//
// Behind a tag because the toolkit needs cgo, and the plain build's ability to
// cross-compile to four platforms from one machine with no C compiler anywhere
// is worth more than having one binary. `go build .` is still exactly what it
// was; `go build -tags gui .` is that plus this window.
func registerGUI(app *pocketbase.PocketBase) {
	var addr string

	cmd := &cobra.Command{
		Use:   "gui",
		Short: "Open the desktop window",
		Run: func(cmd *cobra.Command, args []string) {
			// Only records what was asked for. The window itself is opened by
			// startGUI, back in main — see the comment there.
			wantWindow, windowAddr, windowDir = true, addr, app.DataDir()
		},
	}
	cmd.Flags().StringVar(&addr, "http", "127.0.0.1:8099",
		"the address the server should listen on")

	app.RootCmd.AddCommand(cmd)
}

// What the gui command asked for, read back by main once cobra is done.
var (
	wantWindow bool
	windowAddr string
	windowDir  string
)

// startGUI opens the window, on the process's first thread and with nothing
// else holding the database. Called from main after Start has returned.
func startGUI() {
	if wantWindow {
		runGUI(windowAddr, windowDir)
	}
}

// server is the child process the window supervises.
//
// A child process, not this process. It runs the same argv the systemd unit
// runs, so the desktop path and the service path cannot drift into two
// different servers; the server's own log.Fatal cannot take the window down
// with it; and the blocking Start() stays off the main thread, which on macOS
// belongs to the UI and nothing else.
type server struct {
	dir, token string
	cmd        *exec.Cmd
	done       chan struct{}

	// The bind address moves while the window is open, and the goroutine that
	// moves it is not the one polling /metrics two seconds later. One mutex
	// around one string, because "it is only a string" is how a torn read gets
	// shipped.
	mu   sync.Mutex
	addr string
}

func (s *server) bind() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addr
}

func (s *server) setBind(addr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addr = addr
}

// managed is true when a systemd unit exists, which makes that unit the owner
// of the server and this window a remote control for it. Never both: two
// processes writing one SQLite file is how a sync server corrupts itself, and
// the window has no way to notice it has happened.
func (s *server) managed() bool { return serviceInstalled() }

func (s *server) running() bool {
	if s.managed() {
		return serviceActive()
	}
	if s.cmd == nil {
		return false
	}
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

func (s *server) start() error {
	if s.managed() {
		return systemctl("start", unitName)
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}

	cmd := exec.Command(exe, "serve", "--http="+s.bind(), "--dir="+s.dir)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr

	// The status pane reads the child's own /metrics rather than opening the
	// database beside it. A token minted per window and never written anywhere
	// is what switches that endpoint on; two writers on one SQLite file is how
	// a sync server corrupts itself, and the counts already exist over HTTP.
	cmd.Env = append(os.Environ(), "SUMMAREADER_METRICS_TOKEN="+s.token)

	if err := cmd.Start(); err != nil {
		return err
	}

	s.cmd, s.done = cmd, make(chan struct{})
	done := s.done
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	return nil
}

func (s *server) stop() {
	if s.managed() {
		_ = systemctl("stop", unitName)
		return
	}
	if !s.running() {
		return
	}
	// Interrupt first, so PocketBase closes the database instead of being cut
	// off mid-write. Windows has no interrupt to deliver and a server that
	// ignores one has to be killed regardless, so both roads end at Kill.
	if err := s.cmd.Process.Signal(os.Interrupt); err != nil {
		_ = s.cmd.Process.Kill()
	}
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		_ = s.cmd.Process.Kill()
		<-s.done
	}
}

// stats asks the running server what it holds. Zeroes when it is not up,
// which is the same thing the pane wants to show anyway.
func (s *server) stats() (up bool, devices, entries float64) {
	req, err := http.NewRequest("GET", "http://"+s.bind()+"/metrics", nil)
	if err != nil {
		return false, 0, 0
	}
	req.Header.Set("Authorization", "Bearer "+s.token)

	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return false, 0, 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, 0, 0
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return true, 0, 0
	}
	text := string(body)
	return true, gauge(text, "summareader_server_devices"),
		gauge(text, "summareader_server_log_entries")
}

// overview is who is paired and what they have sent, in one request.
//
// The same credential as stats(), because it is the same endpoint family and
// the window already holds the token. Errors are a nil slice rather than a
// dialog: this runs every couple of seconds, and a server that is down is
// already saying so on the line above.
func (s *server) overview() *overview {
	req, err := http.NewRequest("GET", "http://"+s.bind()+"/overview", nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+s.token)

	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var out overview
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out) != nil {
		return nil
	}
	return &out
}

// ago says how long ago something was, in the roughest terms that are still
// true. Minutes are the finest resolution the server records, so a seconds
// figure here would be a precision the data does not have.
func ago(stamp string) string {
	if stamp == "" {
		return "never synced"
	}
	when, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return stamp
	}
	since := time.Since(when)
	switch {
	case since < 2*time.Minute:
		return "just now"
	case since < time.Hour:
		return fmt.Sprintf("%d minutes ago", int(since.Minutes()))
	case since < 48*time.Hour:
		return fmt.Sprintf("%d hours ago", int(since.Hours()))
	default:
		return fmt.Sprintf("%d days ago", int(since.Hours()/24))
	}
}

// gauge picks one unlabelled sample out of the Prometheus text format. Enough
// for three numbers on a window — a parser for the whole format would be a
// dependency for something the server prints ten lines of.
func gauge(text, name string) float64 {
	for _, line := range strings.Split(text, "\n") {
		if rest, ok := strings.CutPrefix(line, name+" "); ok {
			value, _ := strconv.ParseFloat(strings.TrimSpace(rest), 64)
			return value
		}
	}
	return 0
}

// plural keeps the pane from saying "1 devices". Two words of English rather
// than a pluralisation library, because these are the only two nouns it counts.
func plural(n float64, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	if noun == "entry" {
		noun = "entries"
	} else {
		noun += "s"
	}
	return fmt.Sprintf("%.0f %s", n, noun)
}

// dirSize is what the database weighs on disk, walked from outside the server.
// Reading file sizes is not a second writer, so this one is safe to do here.
func dirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if info, err := entry.Info(); err == nil && !entry.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// paneState is everything the body is drawn from: text, and a handful of
// flags. A struct rather than six positional arguments, because that is what
// six positional booleans and strings turn into at the third call site.
type paneState struct {
	status, counts, dir, addr string
	running                   bool
	devices                   int
	paired                    []overviewDevice
	compose                   bool // a docker-compose.yml was found next to us
	service                   bool // "Start at login" is on
	docker                    bool // ...and the unit runs the container
}

// guiPane is the window's body: the widgets that change while the server runs,
// and the layout holding them.
//
// Built from plain text and a flag rather than from a running server, so the
// same layout can be rendered without a screen — gui_screenshot_test.go does
// that to produce the picture in the README. A window whose screenshot can
// only be taken by hand is a window whose screenshot is quietly out of date.
type guiPane struct {
	status, counts *widget.Label
	devices        *fyne.Container
	// What the device rows were last drawn from, so a poll every two seconds
	// does not rebuild widgets that have not changed — and so the list does
	// not flicker under somebody reading it.
	drawn                   string
	toggle, dashboard, pair *widget.Button
	bind                    *widget.Select
	port                    *widget.Entry
	atLogin                 *widget.Check
	runAs                   *widget.RadioGroup
	content                 fyne.CanvasObject
}

// The two things a unit can run, spelled once. They are radio labels and they
// are also what the window reads back to decide which is selected.
const (
	runDirect = "this binary"
	runDocker = "docker compose"
)

func newGUIPane(state paneState) *guiPane {
	pane := &guiPane{
		status:    widget.NewLabel(state.status),
		counts:    widget.NewLabel(state.counts),
		toggle:    widget.NewButton("Start", nil),
		dashboard: widget.NewButton("Open dashboard", nil),
		pair:      widget.NewButton(pairButtonText(state.devices), nil),
		devices:   container.NewVBox(),
	}
	pane.setDevices(state.paired)

	// The dashboard is PocketBase's, served by the child process, so there is
	// nothing to open until that process is up.
	if state.running {
		pane.toggle.SetText("Stop")
	} else {
		pane.dashboard.Disable()
	}

	// The address, offered rather than typed. Loopback and everything are the
	// two answers that are not addresses, and after them every address this
	// machine actually answers on — the same list, found the same way, that
	// the pairing dialog offers a phone.
	host, port := splitBind(state.addr)
	hosts := bindHosts(host)
	labels := make([]string, len(hosts))
	for i, candidate := range hosts {
		labels[i] = candidate.String()
	}
	pane.bind = widget.NewSelect(labels, nil)
	for i, candidate := range hosts {
		if candidate.ip == host {
			pane.bind.SetSelectedIndex(i)
			break
		}
	}
	// Given a width rather than left to the layout: in a Border the port sits
	// in the trailing slot at its own minimum size, and an entry's minimum is
	// narrower than the four digits it is holding.
	pane.port = widget.NewEntry()
	pane.port.SetText(port)
	portBox := container.NewGridWrap(
		fyne.NewSize(80, pane.port.MinSize().Height), pane.port)

	// The one line that can be long enough to matter, and the one nothing
	// updates afterwards, so it needs no field.
	where := widget.NewLabel(state.dir)
	where.Wrapping = fyne.TextWrapBreak

	body := []fyne.CanvasObject{
		pane.status,
		pane.counts,
		widget.NewSeparator(),
		pane.devices,
		widget.NewSeparator(),
		widget.NewLabel("Address"),
		container.NewBorder(nil, nil, nil, portBox, pane.bind),
		widget.NewLabel("Data directory"),
		where,
	}

	// systemd only, so the row appears on the platform where the switch does
	// something. A checkbox that cannot work is worse than an absent one: it
	// invites the question of why it did nothing.
	if runtime.GOOS == "linux" {
		pane.atLogin = widget.NewCheck("Start at login (systemd user service)", nil)
		pane.atLogin.SetChecked(state.service)

		options := []string{runDirect}
		if state.compose {
			options = append(options, runDocker)
		}
		pane.runAs = widget.NewRadioGroup(options, nil)
		pane.runAs.Horizontal = true
		if state.docker && state.compose {
			pane.runAs.SetSelected(runDocker)
		} else {
			pane.runAs.SetSelected(runDirect)
		}

		body = append(body, widget.NewSeparator(), pane.atLogin,
			container.NewHBox(widget.NewLabel("running"), pane.runAs))
		if !state.compose {
			note := widget.NewLabel("No docker-compose.yml beside this binary,\nso only the binary can be run as a service.")
			note.Wrapping = fyne.TextWrapWord
			body = append(body, note)
		}
	}

	body = append(body, widget.NewSeparator(),
		container.NewGridWithColumns(3, pane.toggle, pane.dashboard, pane.pair))

	pane.content = container.NewVBox(body...)
	return pane
}

// setDevices redraws the list, and only when it has something new to say.
//
// Every device is listed, revoked ones included: a device that has been
// stopped is something whoever stopped it should still be able to see. So is
// one that has never sent anything — that device is either new or not getting
// through, and leaving it out hides both.
func (p *guiPane) setDevices(devices []overviewDevice) {
	fingerprint := deviceFingerprint(devices)
	if fingerprint == p.drawn {
		return
	}
	p.drawn = fingerprint

	p.devices.RemoveAll()
	if len(devices) == 0 {
		p.devices.Add(widget.NewLabel("No devices paired yet."))
		p.devices.Refresh()
		return
	}
	for _, device := range devices {
		name := device.Label
		if name == "" {
			// A device whose token was minted without a label. Its id is not
			// a name, but it is what distinguishes it from the others.
			name = device.ID
		}
		title := widget.NewLabel(name)
		title.TextStyle = fyne.TextStyle{Bold: true}
		if device.Revoked {
			name += " · revoked"
			title.SetText(name)
		}
		detail := widget.NewLabel(fmt.Sprintf("%s · %s sent",
			ago(device.LastSeen), plural(float64(device.Entries), "entry")))
		detail.TextStyle = fyne.TextStyle{Italic: true}
		p.devices.Add(container.NewVBox(title, detail))
	}
	p.devices.Refresh()
}

func deviceFingerprint(devices []overviewDevice) string {
	var out strings.Builder
	for _, device := range devices {
		fmt.Fprintf(&out, "%s|%s|%t|%s|%d\n",
			device.ID, device.Label, device.Revoked, device.LastSeen, device.Entries)
	}
	return out.String()
}

// pairButtonText is the whole difference between the two questions this button
// can be asked. With no devices there is no library and one has to be made
// from here, because until a device has a token there is nobody to authorise
// the request. With devices, the library exists and the key that makes it
// readable lives on those devices — so the honest answer is an explanation,
// not another library.
func pairButtonText(devices int) string {
	if devices > 0 {
		return "Add a device"
	}
	return "Create first device"
}

// bindHosts is what the Address menu offers.
//
// The two non-addresses first, because they are the two decisions: only this
// machine, or every interface on it. Then every address the machine actually
// answers on, which is the list the pairing dialog draws from — a bind chosen
// here is usually chosen so that a phone can reach it.
func bindHosts(current string) []lanAddr {
	hosts := append([]lanAddr{{ip: "127.0.0.1"}, {ip: "0.0.0.0"}}, lanAddrs()...)

	for _, candidate := range hosts {
		if candidate.ip == current {
			return hosts
		}
	}
	// A hostname, or an address on an interface that is down: keep it rather
	// than silently rebinding a running server to something else.
	if current != "" {
		return append([]lanAddr{{ip: current}}, hosts...)
	}
	return hosts
}

func runGUI(addr, dir string) {
	exe, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}

	// A service installed on an earlier run owns the server, and its address
	// and metrics token are on disk. Read both back rather than starting from
	// this window's defaults: a fresh token would leave the pane reporting
	// zero devices against a server full of them, which reads exactly like a
	// server nobody has paired with.
	token := serviceMetricsToken()
	if token == "" {
		if token, err = newToken(); err != nil {
			log.Fatal(err)
		}
	}
	if bind := serviceBind(); bind != "" {
		addr = bind
	}

	srv := &server{addr: addr, dir: dir, token: token}
	compose := composeFile(exe, dir)

	// An id, because the toolkit stores window preferences under one and
	// complains without. The migration flag alongside it is a statement that
	// every widget update from a background goroutine goes through fyne.Do —
	// which they do, below — and without it the toolkit assumes otherwise and
	// warns about it at every launch.
	fyneapp.SetMetadata(fyne.AppMetadata{
		ID:         "sk.dataiza.summareader.sync",
		Name:       "SummaReader sync server",
		Migrations: map[string]bool{"fyneDo": true},
	})
	ui := fyneapp.New()
	window := ui.NewWindow("SummaReader sync server")

	pane := newGUIPane(paneState{
		status:  "Stopped",
		dir:     dir,
		addr:    addr,
		compose: compose != "",
		service: serviceInstalled(),
		docker:  serviceDocker(),
	})

	// How many devices the server holds, which decides what the third button
	// is for. Written and read on the main thread only — refresh writes it
	// inside fyne.Do, and a tap handler runs there too — so it needs no lock.
	devices := 0

	// Everything below runs from a background goroutine, so every widget it
	// touches goes through fyne.Do — the toolkit owns the main thread and
	// updating a label off it is a race that shows up as a redraw glitch
	// months later rather than as a crash now.
	refresh := func() {
		up, deviceCount, entries := srv.stats()
		size := float64(dirSize(dir)) / (1 << 20)
		managed := srv.managed()
		where := srv.bind()
		// Who is paired and what each has sent. Nil when the server is down,
		// which leaves the last list on screen rather than blanking it — the
		// devices did not stop existing because the server stopped.
		detail := srv.overview()

		fyne.Do(func() {
			devices = int(deviceCount)
			if up {
				// Which of the two is running it, said out loud: with a unit
				// installed, Start and Stop here drive systemctl, and somebody
				// who does not know that has no way to find out.
				status := "Running on http://" + where
				if managed {
					status += " (systemd)"
				}
				pane.status.SetText(status)
				pane.toggle.SetText("Stop")
				pane.dashboard.Enable()
			} else {
				pane.status.SetText("Stopped")
				pane.toggle.SetText("Start")
				pane.dashboard.Disable()
			}
			pane.counts.SetText(fmt.Sprintf("%s · %s · %.1f MB",
				plural(deviceCount, "device"), plural(entries, "entry"), size))
			if detail != nil {
				pane.setDevices(detail.Devices)
			}
			pane.pair.SetText(pairButtonText(devices))
		})
	}

	fail := func(err error) {
		fyne.Do(func() { dialog.ShowError(err, window) })
	}

	// serviceOf reads the config out of the widgets, on the main thread, so
	// the goroutine that installs a unit is never reading a widget the toolkit
	// owns. Cheap, and the alternative is a lock around three strings.
	serviceOf := func() serviceConfig {
		config := serviceConfig{
			exe: exe, addr: srv.bind(), dir: dir, token: srv.token,
			uid: os.Getuid(), gid: os.Getgid(),
		}
		if pane.runAs != nil && pane.runAs.Selected == runDocker {
			config.compose = compose
		}
		return config
	}

	pane.toggle.OnTapped = func() {
		go func() {
			if srv.running() {
				srv.stop()
			} else if err := srv.start(); err != nil {
				fail(err)
			}
			refresh()
		}()
	}

	// Rebinding is a restart, because a listening socket cannot be moved. The
	// unit is rewritten too when there is one — otherwise the address changes
	// here and comes back the old one at the next login, with nothing said.
	applyBind := func() {
		host := srv.bind()
		if pane.bind.Selected != "" {
			host = strings.Fields(pane.bind.Selected)[0]
		}
		port := strings.TrimSpace(pane.port.Text)
		if port == "" {
			port = "8099"
		}
		next := net.JoinHostPort(host, port)
		if next == srv.bind() {
			return
		}
		config := serviceOf()
		config.addr = next

		go func() {
			wasRunning := srv.running()
			srv.stop()
			srv.setBind(next)
			if serviceInstalled() {
				if err := installService(config); err != nil {
					fail(err)
				}
			} else if wasRunning {
				if err := srv.start(); err != nil {
					fail(err)
				}
			}
			refresh()
		}()
	}
	pane.bind.OnChanged = func(string) { applyBind() }
	pane.port.OnSubmitted = func(string) { applyBind() }

	if pane.atLogin != nil {
		pane.atLogin.OnChanged = func(on bool) {
			config := serviceOf()
			go func() {
				var err error
				if on {
					// The window's own child lets go first: installing enables
					// and starts the unit, and the port and the database can
					// only have one owner.
					srv.stop()
					err = installService(config)
				} else {
					err = uninstallService()
				}
				if err != nil {
					fail(err)
					fyne.Do(func() { pane.atLogin.SetChecked(!on) })
				}
				refresh()
			}()
		}

		// Switching between binary and container is the same install, with a
		// different ExecStart. Only meaningful once the switch above is on.
		pane.runAs.OnChanged = func(string) {
			if !pane.atLogin.Checked {
				return
			}
			config := serviceOf()
			go func() {
				if err := installService(config); err != nil {
					fail(err)
				}
				refresh()
			}()
		}
	}

	// PocketBase's admin interface, which is the real one. Nothing here
	// reimplements any of it — the window exists for the three things it has
	// no answer for: is it up, start it, and get a token onto a device.
	pane.dashboard.OnTapped = func() {
		if link, err := url.Parse("http://" + srv.bind() + "/_/"); err == nil {
			_ = ui.OpenURL(link)
		}
	}

	pane.pair.OnTapped = func() {
		// Read on the main thread, where refresh wrote it.
		if devices > 0 {
			showAddDevice(devices, srv.bind(), window)
			return
		}
		go func() {
			device, err := runFirstDevice(srv)
			fyne.Do(func() {
				if err != nil {
					dialog.ShowError(err, window)
					return
				}
				showToken(device, srv.bind(), window)
			})
			refresh()
		}()
	}

	// Scrolled, because the device list grows: ten paired devices would
	// otherwise push Start and Add a device off the bottom of a window whose
	// size was chosen when the body was fixed.
	window.SetContent(container.NewVScroll(pane.content))
	window.Resize(fyne.NewSize(480, 560))

	go func() {
		for {
			refresh()
			time.Sleep(2 * time.Second)
		}
	}()

	window.ShowAndRun()

	// Closing the window stops the server it started. Leaving a child running
	// with nothing supervising it means the next Start finds the port taken
	// and no way from here to see why. A service is left alone: being left
	// running is the entire point of having installed one.
	if !srv.managed() {
		srv.stop()
	}
}

// showAddDevice answers the question the button used to answer wrongly.
//
// Clicking it a second time used to mint a second library — a new account, a
// new token, and no relation to the one the devices are already sharing. It
// looked like it worked and it synced nothing. What actually adds a device is
// a pairing code from an app, because that code carries the master key and
// this server has never held one.
func showAddDevice(devices int, addr string, window fyne.Window) {
	where := reachableURL(addr)
	if where == "" {
		where = "http://" + addr
	}
	command := "curl -X POST " + where + "/enroll \\\n" +
		"  -H 'Authorization: Bearer <a token this account already has>' \\\n" +
		`  -d '{"label":"Phone"}'`

	explain := widget.NewLabel(fmt.Sprintf(
		"This server already holds a library — %s.\n\n"+
			"A new device joins it by scanning a pairing code in SummaReader, on a "+
			"device that is already set up. That code carries the master key, and the "+
			"key is what makes the library readable. This server has never held it and "+
			"is not supposed to, so there is nothing here that can replace it.\n\n"+
			"The app also asks the server for the new device's own token while it does "+
			"that. By hand, that request is:",
		plural(float64(devices), "device")))
	explain.Wrapping = fyne.TextWrapWord

	field := widget.NewMultiLineEntry()
	field.SetText(command)

	body := container.NewVBox(
		explain,
		field,
		widget.NewButton("Copy", func() { window.Clipboard().SetContent(command) }),
		widget.NewLabel("Creating a first device again would make a second, separate\nlibrary — which is why this button is no longer offering to."),
	)

	dialog.ShowCustom("Add a device", "Done", body, window)
}

// runFirstDevice issues a first device by running this same binary's
// `first-device`.
//
// The server steps aside while it does. `pair` writes the account into the
// very database the server has open, and rather than reason about two writers
// on one SQLite file, this stops the child, runs the command that already
// exists, and puts the server back if it was up.
func runFirstDevice(srv *server) (*Device, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}

	wasRunning := srv.running()
	srv.stop()
	defer func() {
		if wasRunning {
			_ = srv.start()
		}
	}()

	// `first-device` and not the `pair` alias it still answers to: the alias
	// prints a deprecation note, PocketBase points cobra's writers at stdout,
	// and so the note lands in front of the JSON and nothing here can parse
	// it. That was this button reporting "pairing produced no token".
	//
	// --json, because the human-readable form prints a wall of migration
	// output around the token and this needs the token itself.
	out, err := exec.Command(exe, "first-device", "--dir="+srv.dir, "--json").Output()
	if err != nil {
		return nil, err
	}

	var device Device
	if err := json.Unmarshal(out, &device); err != nil {
		return nil, fmt.Errorf("pairing produced no token")
	}
	return &device, nil
}

// pairingPayload is what the QR code carries: the address of this server and
// a device token, and deliberately nothing else.
//
// The app's own pairing code carries the library's master key as well, and
// scanning one of those means "join this library". The server has never held
// that key — it stores ciphertext and could not read a library if it wanted
// to — so a code minted here says only "here is my server, here is a token",
// which is exactly what typing those two things by hand would say. The shape
// is a contract with the app; a renamed field is a phone that refuses to scan.
//
// Version 2 spells the fields out. They were `v`, `u` and `t`, which is fine
// for a QR and useless to the person reading the same code as text, deciding
// whether it is the one that carries a key. The app reads both spellings, so
// an older phone scanning this still pairs; a version older than that reads
// neither and says the code is not one it knows, which is the honest answer.
func pairingPayload(serverURL, token string) (string, error) {
	payload, err := json.Marshal(struct {
		Version int    `json:"version"`
		Server  string `json:"server"`
		Token   string `json:"device_token"`
	}{2, serverURL, token})
	return string(payload), err
}

// reachableURL turns the address this window serves on into one the phone
// scanning the code can actually open.
//
// The window's default is loopback, and a QR saying 127.0.0.1 works on every
// device except the one it was drawn for. So when the server is bound to
// loopback or to everything, this looks up the machine's own address on the
// local network — the same address the mDNS announcement leads clients to,
// and what SYNC_BIND names for the container. An address someone chose
// explicitly is left alone: they know where they put it.
//
// Empty when there is nothing a phone could reach, which the dialog says out
// loud. A code that silently cannot work is worse than no code.
func reachableURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}

	ip := net.ParseIP(host)
	if ip == nil && host != "" {
		return "http://" + net.JoinHostPort(host, port)
	}
	if ip != nil && !ip.IsLoopback() && !ip.IsUnspecified() {
		return "http://" + net.JoinHostPort(host, port)
	}

	lan := lanIP()
	if lan == "" {
		return ""
	}
	return "http://" + net.JoinHostPort(lan, port)
}

// lanIP is this machine's address on the network the phone is also on.
//
// The first of the candidates below, which on an ordinary desktop is the only
// one there is. Every caller that has a person in front of it offers the whole
// list instead; this is the answer for the places that can only take one.
func lanIP() string {
	if addrs := lanAddrs(); len(addrs) > 0 {
		return addrs[0].ip
	}
	return ""
}

// lanAddr is one address a code could name, and the interface it sits on.
type lanAddr struct{ ip, iface string }

// The interface name is what makes the choice obvious to somebody who has
// never thought about networking: 10.10.20.1 and 172.17.0.1 look equally
// plausible until one of them says docker0 beside it.
func (a lanAddr) String() string {
	if a.iface == "" {
		return a.ip
	}
	return a.ip + " (" + a.iface + ")"
}

// lanAddrs is every private IPv4 this machine answers on, likeliest first.
//
// A desktop with Docker or a VM manager installed has several, and only some
// of them lead anywhere a phone can follow. The virtual ones are sorted to the
// back rather than dropped, because somebody running this inside a container
// may well need the one on the bridge — but nobody should be handed it first.
func lanAddrs() []lanAddr {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	var found []lanAddr
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			network, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			if ip := network.IP.To4(); ip != nil && ip.IsPrivate() {
				found = append(found, lanAddr{ip.String(), iface.Name})
			}
		}
	}

	orderLANAddrs(found)
	return found
}

// orderLANAddrs moves the virtual interfaces to the back, leaving the order
// the system gave everything else — that order is the machine's own opinion
// about which network matters, and this has no better one.
func orderLANAddrs(addrs []lanAddr) {
	sort.SliceStable(addrs, func(i, j int) bool {
		return !virtualIface(addrs[i].iface) && virtualIface(addrs[j].iface)
	})
}

// virtualIface recognises the interfaces that exist for software on this
// machine to talk to itself. Named by prefix because that is how the tools
// that create them name them, and a phone can reach none of them.
func virtualIface(name string) bool {
	for _, prefix := range []string{"docker", "br-", "bridge", "veth", "virbr", "vboxnet", "vmnet", "tun", "tap"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// pairingHosts is what the dialog offers, in the order it offers them.
//
// An address someone typed on the command line goes first: that is a stated
// intention, not a guess, and the rest of the list is guesswork by comparison.
// Everything the machine holds still follows it, because the bind address and
// the address a phone should dial are not always the same thing.
func pairingHosts(addr string) []lanAddr {
	hosts := lanAddrs()

	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return hosts
	}
	ip := net.ParseIP(host)
	if ip != nil && (ip.IsLoopback() || ip.IsUnspecified()) {
		return hosts
	}

	chosen := []lanAddr{{ip: host}}
	for _, candidate := range hosts {
		if candidate.ip != host {
			chosen = append(chosen, candidate)
		}
	}
	return chosen
}

// showToken puts the token somewhere it can be selected and copied, and beside
// it the same token as a QR code. It is shown once and is not recoverable,
// which is the whole reason this button exists: printing it to a terminal
// nobody has open helps nobody.
//
// Two ways into one library: a desktop copies the text, a phone scans the
// code rather than typing forty-three characters off another screen.
func showToken(device *Device, addr string, window fyne.Window) {
	field := widget.NewEntry()
	field.SetText(device.Token)

	copyButton := widget.NewButton("Copy", func() {
		window.Clipboard().SetContent(device.Token)
	})

	side := container.NewVBox(
		widget.NewLabel("Account "+device.AccountID),
		field,
		copyButton,
	)

	_, port, _ := net.SplitHostPort(addr)
	hosts := pairingHosts(addr)
	if len(hosts) == 0 {
		_, note := pairingCode(addr, device.Token)
		dialog.ShowCustom("Paste this into SummaReader", "Done",
			container.NewVBox(side, widget.NewLabel(note)), window)
		return
	}

	// Which address the code names is a question only the person in front of
	// the screen can answer — the machine cannot tell which network the phone
	// is on. So the code is redrawn on every change of the choice rather than
	// drawn once from a guess.
	note := widget.NewLabel("")
	image := container.NewStack()
	draw := func(chosen lanAddr) {
		code, text := pairingCode(net.JoinHostPort(chosen.ip, port), device.Token)
		image.Objects = nil
		if code != nil {
			image.Objects = []fyne.CanvasObject{code}
		}
		image.Refresh()
		note.SetText(text)
	}

	options := make([]string, len(hosts))
	for i, host := range hosts {
		options[i] = host.String()
	}
	choose := widget.NewSelect(options, func(selected string) {
		for _, host := range hosts {
			if host.String() == selected {
				draw(host)
				return
			}
		}
	})
	choose.SetSelected(options[0])

	// The code on the left and the text beside it, so the dialog reads as one
	// thing with two ways in rather than as two offers.
	body := container.NewVBox(
		container.NewBorder(nil, nil, container.NewVBox(image, choose), nil, side),
		note,
	)

	dialog.ShowCustom("Paste this into SummaReader", "Done", body, window)
}

// pairingCode draws the QR, or returns nothing and a line saying why.
func pairingCode(addr, token string) (fyne.CanvasObject, string) {
	serverURL := reachableURL(addr)
	if serverURL == "" {
		return nil, "Shown once. No address on this network to put in a code —\n" +
			"the server is on loopback, so type the token in by hand."
	}

	payload, err := pairingPayload(serverURL, token)
	if err != nil {
		return nil, "Shown once. Other devices are paired from this one."
	}
	png, err := qrcode.New(payload, qrcode.Medium)
	if err != nil {
		return nil, "Shown once. Other devices are paired from this one."
	}

	// Drawn at more pixels than it is shown at and told to fit rather than
	// stretch, because a QR resampled off its module grid is a QR a camera
	// hesitates over.
	image := canvas.NewImageFromImage(png.Image(512))
	image.FillMode = canvas.ImageFillContain
	image.SetMinSize(fyne.NewSize(180, 180))

	return image, "Scan on a phone, or copy the token. Shown once, for\n" + serverURL + "."
}
