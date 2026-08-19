package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// started is when this process came up, for the uptime gauge and so a
// Prometheus can tell a counter reset from a rollback.
var started = time.Now()

// The token a scraper must present: SUMMAREADER_METRICS_TOKEN, or
// `metrics_token` in the config file when the environment is silent.
//
// Empty means the endpoint is off. Off by default and never derived from a
// device token: a scraper is not a device, and a monitoring system holding a
// credential that can also read the log is a monitoring system that has the
// library.
//
// Read per request rather than once, so the environment still decides at the
// moment it is asked — which is what the tests rely on and what a `docker
// compose up` with a new value gets without a rebuild.
func metricsToken() string {
	if token := strings.TrimSpace(os.Getenv("SUMMAREADER_METRICS_TOKEN")); token != "" {
		return token
	}
	return strings.TrimSpace(settings.MetricsToken)
}

// What the server can honestly say about itself.
//
// **It holds ciphertext and knows it.** Everything here is a count, a byte
// total or an age — there is nothing about what any entry contains, because
// the server cannot read one and this endpoint must not become the reason it
// starts wanting to. Per-account rows are labelled by account id, which the
// operator already has in the admin interface.
func handleMetrics(e *core.RequestEvent) error {
	want := metricsToken()
	if want == "" {
		return e.NotFoundError("", nil)
	}
	token := strings.TrimPrefix(e.Request.Header.Get("Authorization"), "Bearer ")
	if subtleCompare(strings.TrimSpace(token), want) != 1 {
		return e.UnauthorizedError("", nil)
	}

	var out strings.Builder
	metric := func(name, help, kind string, samples map[string]float64) {
		fmt.Fprintf(&out, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, kind)
		for labels, value := range samples {
			fmt.Fprintf(&out, "%s%s %g\n", name, labels, value)
		}
	}

	count := func(collection string) float64 {
		var total struct {
			N float64 `db:"n"`
		}
		err := e.App.DB().NewQuery("SELECT COUNT(*) AS n FROM " + collection).
			One(&total)
		if err != nil {
			return 0
		}
		return total.N
	}

	metric("summareader_server_accounts", "Accounts on this server.", "gauge",
		map[string]float64{"": count(collAccounts)})
	metric("summareader_server_devices", "Devices enrolled, across accounts.",
		"gauge", map[string]float64{"": count(collDevices)})
	metric("summareader_server_log_entries", "Entries in the sync log.",
		"gauge", map[string]float64{"": count(collEntries)})
	metric("summareader_server_blobs", "Blobs held.", "gauge",
		map[string]float64{"": count(collBlobs)})

	// What it all weighs. The payload is the encrypted body, so this is the
	// number that decides whether a disk is big enough — the only size
	// question the operator can answer without reading anything.
	var bytes struct {
		Entries float64 `db:"entries"`
		Blobs   float64 `db:"blobs"`
	}
	if err := e.App.DB().NewQuery(
		"SELECT (SELECT COALESCE(SUM(LENGTH(payload)), 0) FROM " + collEntries +
			") AS entries, (SELECT COALESCE(SUM(LENGTH(payload)), 0) FROM " +
			collBlobs + ") AS blobs",
	).One(&bytes); err == nil {
		metric("summareader_server_stored_bytes", "Ciphertext held, by kind.",
			"gauge", map[string]float64{
				`{kind="entries"}`: bytes.Entries,
				`{kind="blobs"}`:   bytes.Blobs,
			})
	}

	// Per account, because "the server is full" is never the useful form of
	// that question — "which library grew" is.
	var perAccount []struct {
		Account string  `db:"account"`
		N       float64 `db:"n"`
		Bytes   float64 `db:"bytes"`
	}
	if err := e.App.DB().NewQuery(
		"SELECT account, COUNT(*) AS n, COALESCE(SUM(LENGTH(payload)), 0) AS bytes " +
			"FROM " + collEntries + " GROUP BY account",
	).All(&perAccount); err == nil {
		entries := map[string]float64{}
		size := map[string]float64{}
		for _, row := range perAccount {
			label := fmt.Sprintf("{account=%q}", row.Account)
			entries[label] = row.N
			size[label] = row.Bytes
		}
		metric("summareader_server_account_entries", "Log entries per account.",
			"gauge", entries)
		metric("summareader_server_account_bytes", "Ciphertext per account.",
			"gauge", size)
	}

	// The process. Standard names, because every dashboard written for a Go
	// service expects these spellings.
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	metric("process_resident_memory_bytes", "Memory this process is holding.",
		"gauge", map[string]float64{"": float64(mem.Sys)})
	metric("go_goroutines", "Goroutines currently running.", "gauge",
		map[string]float64{"": float64(runtime.NumGoroutine())})
	metric("process_start_time_seconds", "When this process started.", "gauge",
		map[string]float64{"": float64(started.Unix())})
	metric("summareader_server_uptime_seconds", "How long it has been up.",
		"gauge", map[string]float64{"": time.Since(started).Seconds()})

	e.Response.Header().Set("Content-Type",
		"text/plain; version=0.0.4; charset=utf-8")
	_, err := e.Response.Write([]byte(out.String()))
	return err
}

// Length-constant comparison, the same rule the device tokens get: comparing
// secrets with == leaks where they first differ to anybody who can time it.
func subtleCompare(a, b string) int {
	if len(a) != len(b) {
		return 0
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	if diff == 0 {
		return 1
	}
	return 0
}
