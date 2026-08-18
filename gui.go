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
	"strconv"
	"strings"
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
	addr, dir, token string
	cmd              *exec.Cmd
	done             chan struct{}
}

func (s *server) running() bool {
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
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	cmd := exec.Command(exe, "serve", "--http="+s.addr, "--dir="+s.dir)
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
	req, err := http.NewRequest("GET", "http://"+s.addr+"/metrics", nil)
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

// guiPane is the window's body: the widgets that change while the server runs,
// and the layout holding them.
//
// Built from plain text and a flag rather than from a running server, so the
// same layout can be rendered without a screen — gui_screenshot_test.go does
// that to produce the picture in the README. A window whose screenshot can
// only be taken by hand is a window whose screenshot is quietly out of date.
type guiPane struct {
	status, counts          *widget.Label
	toggle, dashboard, pair *widget.Button
	content                 fyne.CanvasObject
}

func newGUIPane(status, counts, dir string, running bool) *guiPane {
	pane := &guiPane{
		status:    widget.NewLabel(status),
		counts:    widget.NewLabel(counts),
		toggle:    widget.NewButton("Start", nil),
		dashboard: widget.NewButton("Open dashboard", nil),
		pair:      widget.NewButton("Pair a device", nil),
	}

	// The dashboard is PocketBase's, served by the child process, so there is
	// nothing to open until that process is up.
	if running {
		pane.toggle.SetText("Stop")
	} else {
		pane.dashboard.Disable()
	}

	// The one line that can be long enough to matter, and the one nothing
	// updates afterwards, so it needs no field.
	where := widget.NewLabel(dir)
	where.Wrapping = fyne.TextWrapBreak

	pane.content = container.NewVBox(
		pane.status,
		pane.counts,
		widget.NewSeparator(),
		widget.NewLabel("Data directory"),
		where,
		widget.NewSeparator(),
		container.NewGridWithColumns(3, pane.toggle, pane.dashboard, pane.pair),
	)
	return pane
}

func runGUI(addr, dir string) {
	token, err := newToken()
	if err != nil {
		log.Fatal(err)
	}
	srv := &server{addr: addr, dir: dir, token: token}

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

	pane := newGUIPane("Stopped", "", dir, false)

	// Everything below runs from a background goroutine, so every widget it
	// touches goes through fyne.Do — the toolkit owns the main thread and
	// updating a label off it is a race that shows up as a redraw glitch
	// months later rather than as a crash now.
	refresh := func() {
		up, devices, entries := srv.stats()
		size := float64(dirSize(dir)) / (1 << 20)

		fyne.Do(func() {
			if up {
				pane.status.SetText("Running on http://" + addr)
				pane.toggle.SetText("Stop")
				pane.dashboard.Enable()
			} else {
				pane.status.SetText("Stopped")
				pane.toggle.SetText("Start")
				pane.dashboard.Disable()
			}
			pane.counts.SetText(fmt.Sprintf("%s · %s · %.1f MB",
				plural(devices, "device"), plural(entries, "entry"), size))
		})
	}

	pane.toggle.OnTapped = func() {
		go func() {
			if srv.running() {
				srv.stop()
			} else if err := srv.start(); err != nil {
				fyne.Do(func() { dialog.ShowError(err, window) })
			}
			refresh()
		}()
	}

	// PocketBase's admin interface, which is the real one. Nothing here
	// reimplements any of it — the window exists for the three things it has
	// no answer for: is it up, start it, and get a token onto a device.
	pane.dashboard.OnTapped = func() {
		if link, err := url.Parse("http://" + addr + "/_/"); err == nil {
			_ = ui.OpenURL(link)
		}
	}

	pane.pair.OnTapped = func() {
		go func() {
			device, err := runPair(srv)
			fyne.Do(func() {
				if err != nil {
					dialog.ShowError(err, window)
					return
				}
				showToken(device, addr, window)
			})
			refresh()
		}()
	}

	window.SetContent(pane.content)
	window.Resize(fyne.NewSize(460, 300))

	go func() {
		for {
			refresh()
			time.Sleep(2 * time.Second)
		}
	}()

	window.ShowAndRun()

	// Closing the window stops the server it started. Leaving a child running
	// with nothing supervising it means the next Start finds the port taken
	// and no way from here to see why.
	srv.stop()
}

// runPair issues a first device by running this same binary's `pair`.
//
// The server steps aside while it does. `pair` writes the account into the
// very database the server has open, and rather than reason about two writers
// on one SQLite file, this stops the child, runs the command that already
// exists, and puts the server back if it was up.
func runPair(srv *server) (*Device, error) {
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

	// --json, because the human-readable form prints a wall of migration
	// output around the token and this needs the token itself.
	out, err := exec.Command(exe, "pair", "--dir="+srv.dir, "--json").Output()
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
func pairingPayload(serverURL, token string) (string, error) {
	payload, err := json.Marshal(struct {
		V int    `json:"v"`
		U string `json:"u"`
		T string `json:"t"`
	}{1, serverURL, token})
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
// The first private IPv4 on an interface that is up, which on an ordinary
// desktop is the only one there is. A machine on two networks at once gets
// whichever the system lists first, and that is a guess — but it is the same
// guess the user would make, and the dialog shows the address it chose.
func lanIP() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
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
				return ip.String()
			}
		}
	}
	return ""
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

	code, note := pairingCode(addr, device.Token)
	body := container.NewVBox(side, widget.NewLabel(note))
	if code != nil {
		// The code on the left and the text beside it, so the dialog reads as
		// one thing with two ways in rather than as two offers.
		body = container.NewVBox(
			container.NewBorder(nil, nil, code, nil, side),
			widget.NewLabel(note),
		)
	}

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
