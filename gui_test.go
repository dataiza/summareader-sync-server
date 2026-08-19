//go:build gui

package main

import (
	"net"
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

// A machine with Docker on it holds several private addresses and only some of
// them lead anywhere a phone can follow. Ordered against a written-down list
// rather than against whatever this machine happens to have plugged in, which
// is a different list on every machine that runs this.
func TestTheVirtualInterfacesAreOfferedLast(t *testing.T) {
	addrs := []lanAddr{
		{"172.18.0.1", "br-27ee9d724738"},
		{"172.17.0.1", "docker0"},
		{"10.10.20.1", "enp7s0"},
		{"192.168.122.1", "virbr0"},
		{"10.30.0.100", "enp5s0f0"},
		{"192.168.1.24", "wlan0"},
		{"172.20.0.2", "veth7f21a3c"},
	}
	orderLANAddrs(addrs)

	want := []string{"enp7s0", "enp5s0f0", "wlan0",
		"br-27ee9d724738", "docker0", "virbr0", "veth7f21a3c"}
	for i, name := range want {
		if addrs[i].iface != name {
			t.Fatalf("position %d: got %s, want %s", i, addrs[i].iface, name)
		}
	}

	// The address is what goes in the code; the interface is only how the
	// person tells them apart, so both have to reach the dialog.
	if got := addrs[0].String(); got != "10.10.20.1 (enp7s0)" {
		t.Fatalf("the interface name is missing from the label: %q", got)
	}
}

// An address someone typed is a decision, and the dialog opens on it rather
// than on whatever this machine's first interface happens to be.
func TestTheChosenBindAddressIsOfferedFirst(t *testing.T) {
	if got := pairingHosts("10.30.0.100:8099"); len(got) == 0 || got[0].ip != "10.30.0.100" {
		t.Fatalf("the bind address was not offered first: %v", got)
	}
	if got := pairingHosts("sync.example:8099"); len(got) == 0 || got[0].ip != "sync.example" {
		t.Fatalf("a hostname was not offered first: %v", got)
	}
	for _, addr := range []string{"127.0.0.1:8099", "0.0.0.0:8099", "[::]:8099"} {
		for _, host := range pairingHosts(addr) {
			if ip := net.ParseIP(host.ip); ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
				t.Fatalf("%s produced %q", addr, host.ip)
			}
		}
	}
}

// The unit file is written by the window and read by systemd, and nothing in
// between ever looks at it. A wrong ExecStart is a service that fails at the
// next login, silently, in a log nobody has open — so the bytes are asserted
// here rather than by installing one and hoping.
func TestTheUnitRunsWhatTheWindowWasAskedFor(t *testing.T) {
	native := renderUnit(serviceConfig{
		exe:  "/home/you/.local/bin/summareader-sync",
		addr: "10.10.20.1:8099", dir: "/home/you/pb_data", token: "sesame",
	})

	for _, want := range []string{
		"ExecStart=/home/you/.local/bin/summareader-sync serve --http=10.10.20.1:8099 --dir=/home/you/pb_data",
		`Environment="SUMMAREADER_METRICS_TOKEN=sesame"`,
		"ReadWritePaths=/home/you/pb_data",
		"WantedBy=default.target",
	} {
		if !strings.Contains(native, want) {
			t.Fatalf("the unit is missing %q:\n%s", want, native)
		}
	}

	container := renderUnit(serviceConfig{
		exe:  "/home/you/.local/bin/summareader-sync",
		addr: "10.10.20.1:9000", dir: "/home/you/pb_data",
		compose: "/home/you/src/sync/docker-compose.yml", token: "sesame",
		uid: 1000, gid: 1000,
	})

	// The bind chosen in the window reaches compose as two variables, because
	// a container's own port is fixed by the image and only the mapping moves.
	for _, want := range []string{
		"ExecStart=docker compose -f /home/you/src/sync/docker-compose.yml up",
		"ExecStop=docker compose -f /home/you/src/sync/docker-compose.yml down",
		`Environment="SYNC_BIND=10.10.20.1"`,
		`Environment="SYNC_PORT=9000"`,
		`Environment="SYNC_UID=1000"`,
		"WorkingDirectory=/home/you/src/sync",
	} {
		if !strings.Contains(container, want) {
			t.Fatalf("the container unit is missing %q:\n%s", want, container)
		}
	}
	// ProtectSystem=strict on the Docker path is a unit that cannot reach the
	// socket it needs. The image confines itself; this would only break it.
	if strings.Contains(container, "ProtectSystem") {
		t.Fatalf("the container unit hardened the host side:\n%s", container)
	}

	// What the window reads back on the next launch, so it reopens on the
	// server that is actually running rather than on its own defaults.
	if got := serviceUnitPort(container); got != "9000" {
		t.Fatalf("port read back as %q", got)
	}
}

// The third button asks two different questions and used to ask the wrong one
// twice. With a library already on the server, offering to create a first
// device again creates a second, unrelated one — which syncs nothing and looks
// like it worked.
func TestTheButtonKnowsWhetherThereIsAlreadyALibrary(t *testing.T) {
	if got := pairButtonText(0); got != "Create first device" {
		t.Fatalf("empty server offers %q", got)
	}
	if got := pairButtonText(3); got != "Add a device" {
		t.Fatalf("server with three devices offers %q", got)
	}
}

// Loopback and everything are decisions rather than addresses, so they are
// offered even though no interface answers to them — and an address already in
// use survives the list not containing it.
func TestTheAddressMenuOffersBothDecisionsAndKeepsAnUnknownBind(t *testing.T) {
	offered := bindHosts("127.0.0.1")
	if offered[0].ip != "127.0.0.1" || offered[1].ip != "0.0.0.0" {
		t.Fatalf("the two decisions are not first: %v", offered)
	}

	kept := bindHosts("sync.example")
	if kept[0].ip != "sync.example" {
		t.Fatalf("a hostname somebody chose was dropped: %v", kept)
	}

	if host, port := splitBind("10.10.20.1:8099"); host != "10.10.20.1" || port != "8099" {
		t.Fatalf("split: %q %q", host, port)
	}
	if host, port := splitBind("[::1]:8099"); host != "::1" || port != "8099" {
		t.Fatalf("split v6: %q %q", host, port)
	}
}
