package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The file's name, beside the data directory. One directory is then the whole
// installation: the database, and the settings that decide where it is served.
const configName = "summareader-sync.json"

// Config is every option this server has, in the one place they can be
// written down. There is nothing here that was not already a flag or an
// environment variable — this gives the existing ones a home, and adds none.
//
// Zero values mean "not set", not "set to zero": an unset field falls through
// to whatever the layer below decided, which for `http` and `dir` is
// PocketBase's own default. That is what keeps a server with no config file
// behaving exactly as it did before there was one.
type Config struct {
	// The address `serve` binds. Empty leaves PocketBase's default alone.
	HTTP string `json:"http,omitempty"`
	// Where the database lives. Empty means defaultDataDir().
	Dir string `json:"dir,omitempty"`
	// The credential /metrics wants. Empty means the endpoint is off.
	MetricsToken string `json:"metrics_token,omitempty"`
	// The credential the operator routes want. Empty means they are off.
	//
	// Separate from MetricsToken on purpose. That one is read-only and gets
	// handed to a monitoring system; this one can rename and revoke devices,
	// and a scraper has no business holding it.
	OperatorToken string `json:"operator_token,omitempty"`
	// What this server calls itself, on the network and in its own dashboard.
	//
	// Display only — the identity `/instance` answers with is generated and
	// stored, and is not this. Two servers may be called the same thing
	// without either being confused for the other, which is the arrangement
	// that was missing when the name *was* the identity.
	//
	// Empty leaves PocketBase's default, which is "Acme".
	Name string `json:"name,omitempty"`
	// Do not advertise this server on the local network.
	NoAnnounce bool `json:"no_announce,omitempty"`
	// How long /subscribe holds a request open, in seconds.
	//
	// Zero — the default — means [defaultHold]. A deliberate zero is spelled
	// by setting it negative, which switches the wait off and restores the
	// old answer-at-once behaviour; a deployment behind something that cannot
	// hold a connection at all needs that, and "unset" and "off" have to be
	// different answers.
	HoldSeconds int `json:"hold_seconds,omitempty"`
}

// Precedence, everywhere below: command-line flag > environment variable >
// config file > default. That is the order people expect, and the order that
// keeps `docker compose` and the systemd unit working untouched — both pass
// environment, and neither has to learn about a file to keep doing what it
// does.
type env func(string) string

// argValue reads a flag out of an argv this process has not parsed yet.
//
// It has to be read before cobra sees it, because the config file's job is to
// supply the *default* for flags PocketBase registers inside Start() — by the
// time cobra could tell us whether --dir was given, the data directory has
// already been chosen. Both spellings, `--name=value` and `--name value`.
func argValue(args []string, name string) string {
	for i, arg := range args {
		if strings.HasPrefix(arg, name+"=") {
			return arg[len(name)+1:]
		}
		if arg == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func hasArg(args []string, name string) bool {
	for _, arg := range args {
		if arg == name || strings.HasPrefix(arg, name+"=") {
			return true
		}
	}
	return false
}

// configPath is where the file is looked for.
//
// The data directory decides it, and the data directory is therefore settled
// from the flag, the environment or the built-in default *only* — never from
// the file, which would be circular. A `dir` in the file still works; it just
// cannot also be what says where the file is. `--config` and
// SUMMAREADER_CONFIG name it outright and skip all of that.
func configPath(args []string, getenv env) string {
	if path := argValue(args, "--config"); path != "" {
		return path
	}
	if path := getenv("SUMMAREADER_CONFIG"); path != "" {
		return path
	}
	dir := argValue(args, "--dir")
	if dir == "" {
		dir = getenv("SUMMAREADER_DIR")
	}
	if dir == "" {
		dir = defaultDataDir()
	}
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, configName)
}

// loadConfig reads the file, or reports that there is none.
//
// A missing file is not an error: it is the ordinary state of every install
// that predates this and every one that never needed a setting. A file that
// exists and does not parse *is* an error, and says which file and what is
// wrong with it — a config the operator wrote and this silently ignored is the
// bug that takes an afternoon to find.
func loadConfig(path string) (Config, error) {
	var cfg Config
	if path == "" {
		return cfg, nil
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("reading %s: %w", path, err)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// resolveConfig folds the three layers into what this process will use.
//
// Flags are not applied here — they are left to cobra, which applies them last
// and therefore wins by construction. What comes back is the *default* each
// flag should have.
func resolveConfig(args []string, getenv env) (Config, error) {
	cfg, err := loadConfig(configPath(args, getenv))
	if err != nil {
		return cfg, err
	}
	if v := getenv("SUMMAREADER_HTTP"); v != "" {
		cfg.HTTP = v
	}
	if v := getenv("SUMMAREADER_DIR"); v != "" {
		cfg.Dir = v
	}
	if v := strings.TrimSpace(getenv("SUMMAREADER_METRICS_TOKEN")); v != "" {
		cfg.MetricsToken = v
	}
	if v := strings.TrimSpace(getenv("SUMMAREADER_OPERATOR_TOKEN")); v != "" {
		cfg.OperatorToken = v
	}
	if v := strings.TrimSpace(getenv("SUMMAREADER_NAME")); v != "" {
		cfg.Name = v
	}
	if v := getenv("SUMMAREADER_NO_ANNOUNCE"); v != "" {
		cfg.NoAnnounce = truthy(v)
	}
	if v := strings.TrimSpace(getenv("SUMMAREADER_HOLD")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.HoldSeconds = n
		}
	}
	return cfg, nil
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// saveConfig writes back only the keys given, and keeps everything else.
//
// Everything else includes keys this version has never heard of: a file
// written by a newer server, or a comment key like the example file's, must
// survive the console changing a port. So the file is decoded into a map
// rather than into Config, which would quietly drop whatever it could not name.
//
// A malformed existing file is refused rather than replaced. The whole reason
// to hand-edit this file is to change something by hand, and a typo answered
// by overwriting the file is the operator's settings gone.
//
// Temporary file and rename, so a full disk or a crash mid-write leaves the
// old file intact rather than a truncated one.
func saveConfig(path string, updates map[string]any) error {
	values := map[string]any{}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &values); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	case !os.IsNotExist(err):
		return fmt.Errorf("reading %s: %w", path, err)
	}

	for key, value := range updates {
		values[key] = value
	}

	encoded, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// In the same directory as the target: rename is only atomic within one
	// filesystem, and /tmp is routinely a different one.
	tmp, err := os.CreateTemp(filepath.Dir(path), configName+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(encoded); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
