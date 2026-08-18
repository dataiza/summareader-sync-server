//go:build gui

package main

import (
	"strings"
	"testing"
)

// The window reads its numbers out of the server's own /metrics rather than
// out of the database the server is writing. That only works while the two
// spellings agree, and nothing else would notice if one of them changed: the
// pane would go on showing zero devices for ever, which reads exactly like a
// server nobody has paired with yet.
func TestTheWindowFindsTheNumbersItShows(t *testing.T) {
	app, account := newTestApp(t)
	t.Setenv("SUMMAREADER_METRICS_TOKEN", "sesame")

	if _, err := appendEntry(app, account, "device-1", "ciphertext"); err != nil {
		t.Fatal(err)
	}

	body := scrape(t, app, "sesame").Body.String()
	if !strings.Contains(body, "summareader_server_log_entries") {
		t.Fatalf("no entries metric to read: %q", body)
	}
	if got := gauge(body, "summareader_server_log_entries"); got != 1 {
		t.Fatalf("entries: got %v, want 1", got)
	}
	if got := gauge(body, "summareader_server_devices"); got < 0 {
		t.Fatalf("devices: got %v", got)
	}
}
