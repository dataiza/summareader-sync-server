package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A getenv that reads from a map rather than the process, so precedence can be
// asserted without a test setting variables another test can see.
func envOf(pairs map[string]string) env {
	return func(name string) string { return pairs[name] }
}

func writeConfig(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, configName)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Nothing set anywhere is the state of every install that predates the file,
// and it has to resolve to nothing rather than to an invented address.
func TestNoConfigChangesNothing(t *testing.T) {
	cfg, err := resolveConfig(
		[]string{"summareader-sync", "serve", "--dir", t.TempDir()},
		envOf(nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	if cfg != (Config{}) {
		t.Fatalf("wanted a zero config, got %+v", cfg)
	}
}

func TestFileSuppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `{"http":"10.0.0.1:9000","metrics_token":"t","no_announce":true}`)

	cfg, err := resolveConfig([]string{"x", "serve", "--dir=" + dir}, envOf(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP != "10.0.0.1:9000" || cfg.MetricsToken != "t" || !cfg.NoAnnounce {
		t.Fatalf("file was not read: %+v", cfg)
	}
}

// The whole order, on one value: flag beats environment beats file beats
// default. The flag layer is cobra's and is asserted through isServe/hasArg,
// which is what decides whether the file's address is offered at all.
func TestPrecedence(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `{"http":"from-file:1"}`)
	args := []string{"x", "serve", "--dir=" + dir}

	cfg, err := resolveConfig(args, envOf(nil))
	if err != nil || cfg.HTTP != "from-file:1" {
		t.Fatalf("file should decide when nothing else does: %+v %v", cfg, err)
	}

	cfg, err = resolveConfig(args, envOf(map[string]string{
		"SUMMAREADER_HTTP": "from-env:2",
	}))
	if err != nil || cfg.HTTP != "from-env:2" {
		t.Fatalf("environment should beat the file: %+v %v", cfg, err)
	}

	// And a flag beats both by never asking: the address is only appended to
	// argv when --http is absent.
	withFlag := append(args, "--http=from-flag:3")
	if !hasArg(withFlag, "--http") {
		t.Fatal("an explicit --http was not seen")
	}
	if hasArg(args, "--http") {
		t.Fatal("an --http that is not there was seen")
	}
}

func TestDirFromEnvBeatsFile(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `{"dir":"/from/file"}`)
	cfg, err := resolveConfig([]string{"x", "serve"}, envOf(map[string]string{
		"SUMMAREADER_CONFIG": filepath.Join(dir, configName),
		"SUMMAREADER_DIR":    "/from/env",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Dir != "/from/env" {
		t.Fatalf("wanted /from/env, got %q", cfg.Dir)
	}
}

func TestConfigPathFollowsTheDataDirectory(t *testing.T) {
	if got := configPath([]string{"x", "serve", "--dir=/data"}, envOf(nil)); got != "/data/"+configName {
		t.Fatalf("beside --dir: %q", got)
	}
	if got := configPath([]string{"x", "serve", "--dir", "/data"}, envOf(nil)); got != "/data/"+configName {
		t.Fatalf("beside a separated --dir: %q", got)
	}
	named := configPath([]string{"x", "serve", "--config=/etc/s.json", "--dir=/data"}, envOf(nil))
	if named != "/etc/s.json" {
		t.Fatalf("--config should win outright: %q", named)
	}
	fromEnv := configPath([]string{"x", "serve"}, envOf(map[string]string{
		"SUMMAREADER_CONFIG": "/etc/e.json",
	}))
	if fromEnv != "/etc/e.json" {
		t.Fatalf("SUMMAREADER_CONFIG should be honoured: %q", fromEnv)
	}
}

// Write, reload, same values — the console's whole reason for writing.
func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, configName)
	if err := saveConfig(path, map[string]any{"http": "192.168.1.5:8099"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP != "192.168.1.5:8099" {
		t.Fatalf("did not come back: %+v", cfg)
	}
}

// Keys this version has never heard of belong to somebody — a newer server, or
// the example file's comments. Changing a port must not eat them.
func TestSaveKeepsUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `{"_comment":"hands off","http":"old:1","future":{"a":[1,2]}}`)

	if err := saveConfig(path, map[string]any{"http": "new:2"}); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if err := json.Unmarshal(raw, &values); err != nil {
		t.Fatal(err)
	}
	if values["_comment"] != "hands off" {
		t.Fatalf("a comment key was dropped: %v", values)
	}
	if values["future"] == nil {
		t.Fatalf("an unknown key was dropped: %v", values)
	}
	if values["http"] != "new:2" {
		t.Fatalf("the change did not land: %v", values)
	}
}

// A typo is refused, and the file is still there afterwards.
func TestMalformedFileIsRefusedNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	const broken = `{"http": "1.2.3.4:1",}`
	path := writeConfig(t, dir, broken)

	if _, err := loadConfig(path); err == nil {
		t.Fatal("a malformed file loaded without complaint")
	} else if !strings.Contains(err.Error(), path) {
		t.Fatalf("the error does not say which file: %v", err)
	}

	if err := saveConfig(path, map[string]any{"http": "5.6.7.8:2"}); err == nil {
		t.Fatal("a malformed file was overwritten")
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != broken {
		t.Fatalf("the file was not left alone: %q %v", raw, err)
	}

	// And the server refuses to start on it rather than serving the defaults.
	if _, err := resolveConfig([]string{"x", "serve", "--dir=" + dir}, envOf(nil)); err == nil {
		t.Fatal("startup accepted a malformed config")
	}
}

func TestSaveIsAtomicAndLeavesNoLitter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, configName)
	if err := saveConfig(path, map[string]any{"http": "a:1"}); err != nil {
		t.Fatal(err)
	}
	if err := saveConfig(path, map[string]any{"http": "b:2"}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("temporary files were left behind: %v", entries)
	}
}

// --http belongs to `serve` and nothing else: appending it to a provisioning
// command is an unknown-flag error and a first device nobody can create.
func TestIsServe(t *testing.T) {
	for _, c := range []struct {
		args []string
		want bool
	}{
		{[]string{"x", "serve"}, true},
		{[]string{"x", "serve", "--dir=/d"}, true},
		{[]string{"x", "--dir", "/d", "serve"}, true},
		{[]string{"x", "first-device", "My library"}, false},
		{[]string{"x", "--dir", "/d", "first-device"}, false},
		{[]string{"x"}, false},
	} {
		if got := isServe(c.args); got != c.want {
			t.Fatalf("isServe(%v) = %v", c.args, got)
		}
	}
}

// The example file in the repository has to be a file this server can read.
func TestExampleConfigParses(t *testing.T) {
	if _, err := loadConfig("summareader-sync.example.json"); err != nil {
		t.Fatal(err)
	}
}
