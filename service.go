//go:build gui

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Leaving the server running when the window is closed, as a systemd *user*
// service — the same thing scripts/install.sh does, written from the window
// so it works for somebody who has the binary and not the repository.
//
// A user service and not a system one, for the reason the script gives: this
// holds one person's ciphertext on one person's machine, and a unit under
// ~/.config needs no root to install, inspect or remove.
//
// The unit runs either this binary or `docker compose`, because "run it as a
// container" and "keep it running" are different questions and the answer to
// the second is systemd either way. Docker's own `restart: unless-stopped`
// would do it too; a unit means one switch in the window covers both, and one
// place to look when it did not come back.

const unitName = "summareader-sync.service"

// serviceConfig is everything that differs between one installation and the
// next. compose empty means the unit runs the binary directly.
type serviceConfig struct {
	exe, addr, dir, compose, token string
	uid, gid                       int
}

// unitPath honours XDG_CONFIG_HOME, because os.UserConfigDir does and systemd
// does not look anywhere else.
func unitPath() (string, error) {
	config, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(config, "systemd", "user", unitName), nil
}

// renderUnit writes the unit out. A pure function of the config so the thing
// that ends up on disk can be asserted in a test rather than by installing it
// and reading it back.
//
// Every value is quoted in Environment= lines: a data directory with a space
// in it is unremarkable on a desktop and unquoted would silently become two
// variables, one of them empty.
func renderUnit(c serviceConfig) string {
	var b strings.Builder

	b.WriteString("[Unit]\n")
	b.WriteString("# Written by the SummaReader sync server window. Turning off\n")
	b.WriteString("# \"Start at login\" there removes this file again.\n")
	b.WriteString("Description=SummaReader sync server\n")
	b.WriteString("Documentation=https://github.com/dataiza/summareader-sync-server\n")
	b.WriteString("After=network-online.target\n")
	b.WriteString("Wants=network-online.target\n\n")

	b.WriteString("[Service]\n")
	if c.token != "" {
		fmt.Fprintf(&b, "Environment=\"SUMMAREADER_METRICS_TOKEN=%s\"\n", c.token)
	}

	if c.compose == "" {
		fmt.Fprintf(&b, "ExecStart=%s serve --http=%s --dir=%s\n", c.exe, c.addr, c.dir)
	} else {
		// The compose file offers one port on loopback and one on a named
		// address, so the bind chosen in the window reaches the container as
		// those two variables rather than as a flag.
		host, port := splitBind(c.addr)
		fmt.Fprintf(&b, "Environment=\"SYNC_BIND=%s\"\n", host)
		fmt.Fprintf(&b, "Environment=\"SYNC_PORT=%s\"\n", port)
		fmt.Fprintf(&b, "Environment=\"SYNC_UID=%d\"\n", c.uid)
		fmt.Fprintf(&b, "Environment=\"SYNC_GID=%d\"\n", c.gid)
		fmt.Fprintf(&b, "WorkingDirectory=%s\n", filepath.Dir(c.compose))
		fmt.Fprintf(&b, "ExecStart=docker compose -f %s up --abort-on-container-exit\n", c.compose)
		fmt.Fprintf(&b, "ExecStop=docker compose -f %s down\n", c.compose)
	}

	b.WriteString("Restart=on-failure\nRestartSec=5\n")

	// Hardening for the native path only. The database is the one thing the
	// server writes and everything else being read-only costs nothing here.
	// The container path is left alone: it needs the Docker socket and the
	// image does its own confining, so this would only be a way to break it.
	if c.compose == "" {
		b.WriteString("\nNoNewPrivileges=true\nPrivateTmp=true\nProtectSystem=strict\n")
		fmt.Fprintf(&b, "ReadWritePaths=%s\n", c.dir)
	}

	b.WriteString("\n[Install]\nWantedBy=default.target\n")
	return b.String()
}

// splitBind is host and port, forgiving about being handed one without the
// other — the port field in the window can be empty for a keystroke.
func splitBind(addr string) (host, port string) {
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return strings.Trim(addr[:i], "[]"), addr[i+1:]
	}
	return addr, "8099"
}

// installService writes the unit and enables it. Writing over an existing one
// is the update path: the bind address changed in the window has to reach the
// unit too, or the service comes back on the old one.
func installService(c serviceConfig) error {
	path, err := unitPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(renderUnit(c)), 0o644); err != nil {
		return err
	}
	if err := systemctl("daemon-reload"); err != nil {
		return err
	}
	return systemctl("enable", "--now", unitName)
}

func uninstallService() error {
	path, err := unitPath()
	if err != nil {
		return err
	}
	// Disable before removing: the enable symlink outlives the unit file and
	// leaves systemd complaining about it at every login.
	_ = systemctl("disable", "--now", unitName)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return systemctl("daemon-reload")
}

// serviceInstalled decides who owns the server: with a unit on disk the
// window drives systemctl, and without one it supervises a child of its own.
// Never both — two processes on one SQLite file is how a sync server
// corrupts itself.
func serviceInstalled() bool {
	path, err := unitPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

func serviceActive() bool {
	return systemctl("is-active", "--quiet", unitName) == nil
}

// serviceMetricsToken reads back the token the unit was installed with, so a
// window opened later can still read the counts out of a server it did not
// start. Minting a fresh one per window would leave the pane reporting zero
// devices, which looks exactly like a server nobody has paired with.
func serviceMetricsToken() string {
	path, err := unitPath()
	if err != nil {
		return ""
	}
	unit, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(unit), "\n") {
		if rest, ok := strings.CutPrefix(line, `Environment="SUMMAREADER_METRICS_TOKEN=`); ok {
			return strings.TrimSuffix(strings.TrimSpace(rest), `"`)
		}
	}
	return ""
}

// serviceBind is the address the installed unit serves on, so the window
// opens showing where the server actually is rather than its own default.
func serviceBind() string {
	path, err := unitPath()
	if err != nil {
		return ""
	}
	unit, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(unit), "\n") {
		for _, marker := range []string{"--http=", `Environment="SYNC_BIND=`} {
			if i := strings.Index(line, marker); i >= 0 {
				value := strings.Fields(line[i+len(marker):])[0]
				value = strings.TrimSuffix(value, `"`)
				if marker == "--http=" {
					return value
				}
				return value + ":" + serviceUnitPort(string(unit))
			}
		}
	}
	return ""
}

func serviceUnitPort(unit string) string {
	for _, line := range strings.Split(unit, "\n") {
		if rest, ok := strings.CutPrefix(line, `Environment="SYNC_PORT=`); ok {
			return strings.TrimSuffix(strings.TrimSpace(rest), `"`)
		}
	}
	return "8099"
}

// serviceDocker says which of the two the installed unit runs, so the window
// reopens on the choice that was made rather than on the default.
func serviceDocker() bool {
	path, err := unitPath()
	if err != nil {
		return false
	}
	unit, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(unit), "ExecStart=docker compose")
}

// composeFile is the compose file this installation would run, or empty when
// there is none — the window greys the Docker choice out rather than offering
// something that cannot start. Beside the binary first, then beside the data
// directory, which are the two places a copy of the repository puts it.
func composeFile(exe, dir string) string {
	candidates := []string{
		filepath.Join(filepath.Dir(exe), "docker-compose.yml"),
		filepath.Join(filepath.Dir(filepath.Dir(exe)), "docker-compose.yml"),
		filepath.Join(filepath.Dir(dir), "docker-compose.yml"),
		"docker-compose.yml",
	}
	for _, candidate := range candidates {
		path, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}

// systemctl runs one command against the user manager, and reports what it
// said when it fails — "exit status 1" in a dialog tells nobody anything.
func systemctl(args ...string) error {
	out, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
	if err != nil {
		if message := strings.TrimSpace(string(out)); message != "" {
			return fmt.Errorf("systemctl --user %s: %s", strings.Join(args, " "), message)
		}
		return fmt.Errorf("systemctl --user %s: %w", strings.Join(args, " "), err)
	}
	return nil
}
