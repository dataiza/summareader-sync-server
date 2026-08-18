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

// The exact bytes the QR carries, because they are a contract with the app.
//
// A renamed field or a stray one shows up nowhere on this side: the code still
// draws, still scans, and the phone quietly refuses it. And the missing "k" is
// the point — the app's own pairing code carries the library key, this one
// must not, because the server has never had it.
func TestThePairingCodeCarriesTheAddressAndTokenAndNoKey(t *testing.T) {
	payload, err := pairingPayload("http://192.168.1.24:8099", "tok-43-chars")
	if err != nil {
		t.Fatal(err)
	}

	want := `{"v":1,"u":"http://192.168.1.24:8099","t":"tok-43-chars"}`
	if payload != want {
		t.Fatalf("payload:\n got %s\nwant %s", payload, want)
	}
	if strings.Contains(payload, `"k"`) {
		t.Fatalf("the server put a key in a pairing code: %s", payload)
	}
}

// A code is only worth drawing at an address the scanning phone can open, and
// the window's own default is the one address it cannot.
func TestTheCodeAddressIsNeverLoopback(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:8099", "0.0.0.0:8099", "[::]:8099"} {
		if got := reachableURL(addr); strings.Contains(got, "127.0.0.1") || strings.Contains(got, "::") {
			t.Fatalf("%s produced %q", addr, got)
		}
	}
	if got := reachableURL("192.168.1.24:8099"); got != "http://192.168.1.24:8099" {
		t.Fatalf("an address someone chose was rewritten to %q", got)
	}
	if got := reachableURL("sync.example:8099"); got != "http://sync.example:8099" {
		t.Fatalf("a hostname was rewritten to %q", got)
	}
}
