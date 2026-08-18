//go:build gui

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
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
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/pocketbase/pocketbase"
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

	status := widget.NewLabel("Stopped")
	counts := widget.NewLabel("")
	where := widget.NewLabel(dir)
	where.Wrapping = fyne.TextWrapBreak

	var toggle, dashboard, pair *widget.Button

	// Everything below runs from a background goroutine, so every widget it
	// touches goes through fyne.Do — the toolkit owns the main thread and
	// updating a label off it is a race that shows up as a redraw glitch
	// months later rather than as a crash now.
	refresh := func() {
		up, devices, entries := srv.stats()
		size := float64(dirSize(dir)) / (1 << 20)

		fyne.Do(func() {
			if up {
				status.SetText("Running on http://" + addr)
				toggle.SetText("Stop")
				dashboard.Enable()
			} else {
				status.SetText("Stopped")
				toggle.SetText("Start")
				dashboard.Disable()
			}
			counts.SetText(fmt.Sprintf("%s · %s · %.1f MB",
				plural(devices, "device"), plural(entries, "entry"), size))
		})
	}

	toggle = widget.NewButton("Start", func() {
		go func() {
			if srv.running() {
				srv.stop()
			} else if err := srv.start(); err != nil {
				fyne.Do(func() { dialog.ShowError(err, window) })
			}
			refresh()
		}()
	})

	// PocketBase's admin interface, which is the real one. Nothing here
	// reimplements any of it — the window exists for the three things it has
	// no answer for: is it up, start it, and get a token onto a device.
	dashboard = widget.NewButton("Open dashboard", func() {
		if link, err := url.Parse("http://" + addr + "/_/"); err == nil {
			_ = ui.OpenURL(link)
		}
	})
	dashboard.Disable()

	pair = widget.NewButton("Pair a device", func() {
		go func() {
			device, err := runPair(srv)
			fyne.Do(func() {
				if err != nil {
					dialog.ShowError(err, window)
					return
				}
				showToken(device, window)
			})
			refresh()
		}()
	})

	window.SetContent(container.NewVBox(
		status,
		counts,
		widget.NewSeparator(),
		widget.NewLabel("Data directory"),
		where,
		widget.NewSeparator(),
		container.NewGridWithColumns(3, toggle, dashboard, pair),
	))
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

// showToken puts the token somewhere it can be selected and copied. It is
// shown once and is not recoverable, which is the whole reason this button
// exists: printing it to a terminal nobody has open helps nobody.
func showToken(device *Device, window fyne.Window) {
	field := widget.NewEntry()
	field.SetText(device.Token)

	copyButton := widget.NewButton("Copy", func() {
		window.Clipboard().SetContent(device.Token)
	})

	dialog.ShowCustom("Paste this into SummaReader", "Done", container.NewVBox(
		widget.NewLabel("Account "+device.AccountID),
		field,
		copyButton,
		widget.NewLabel("Shown once. Other devices are paired from this one."),
	), window)
}
