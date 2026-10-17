package main

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// The window's view of the server, in one request.
//
// # Why this is not /metrics and not /devices
//
// `/metrics` is Prometheus text and deliberately counts only: turning it into
// a per-device table would mean labels carrying names, in an endpoint whose
// whole discipline is that it says nothing about anybody. `/devices` answers a
// *device* token and is scoped to that device's account, which the window does
// not have — it supervises the server, it is not paired with it.
//
// So: the same credential as /metrics, because the window already holds it and
// it is the operator's credential; the same discipline about content, because
// this server cannot read an entry and this endpoint must not become the
// reason it starts wanting to. What it adds over /metrics is who is paired,
// when each was last heard from, and how much each has sent — all of it
// plaintext bookkeeping the server writes itself, and none of it about what
// any entry says.
type overview struct {
	Accounts int64   `json:"accounts"`
	Entries  int64   `json:"entries"`
	Blobs    int64   `json:"blobs"`
	Bytes    int64   `json:"bytes"`
	Uptime   float64 `json:"uptime_seconds"`
	// When the counts above were taken, in RFC 3339 UTC. They are computed at
	// most once a minute (see stats.go), so the console says "as of" this.
	ComputedAt string           `json:"computed_at"`
	Devices    []overviewDevice `json:"devices"`
}

// One paired device, as the operator's window shows it.
type overviewDevice struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Account  string `json:"account"`
	Revoked  bool   `json:"revoked"`
	LastSeen string `json:"last_seen,omitempty"`
	// How many entries carry this device's id.
	//
	// This is what "syncs" means on a server that keeps a log and not
	// sessions. Next to last-seen it separates the two quiet cases: a device
	// heard from this morning with nothing appended has nothing to say, and
	// one not heard from in a week is not getting through. Entries carry no
	// timestamp of their own and are not going to start — when each entry was
	// written is a record of when somebody reads, which is the sort of thing
	// this server exists not to keep.
	Entries int64 `json:"entries"`
}

func handleOverview(e *core.RequestEvent) error {
	want := metricsToken()
	if want == "" {
		return e.NotFoundError("", nil)
	}
	token := strings.TrimPrefix(e.Request.Header.Get("Authorization"), "Bearer ")
	if subtleCompare(strings.TrimSpace(token), want) != 1 {
		return e.UnauthorizedError("", nil)
	}

	out, err := collectOverview(e.App)
	if err != nil {
		return e.JSON(http.StatusInternalServerError, map[string]string{
			"error": "unavailable",
		})
	}
	return e.JSON(http.StatusOK, out)
}

// collectOverview is the whole of the reading, kept out of the handler so a
// test can call it without standing up a router.
//
// The totals and the per-device entry counts come from the cached pass in
// stats.go, so they can be up to a minute old and ComputedAt says how old. The
// device rows are read here, on every request: they are a handful of small
// rows, and a device just renamed or revoked must show the change at once.
func collectOverview(app core.App) (overview, error) {
	out := overview{Uptime: time.Since(started).Seconds(), Devices: []overviewDevice{}}

	stats, err := statsFor(app)
	if err != nil {
		return out, err
	}
	out.Accounts, out.Entries = stats.Accounts, stats.Entries
	out.Blobs, out.Bytes = stats.Blobs, stats.EntryBytes
	out.ComputedAt = stats.ComputedAt.UTC().Format(time.RFC3339)

	// Every device, including one that has never sent anything. That device
	// is the interesting one: it is either brand new or it is not getting
	// through, and leaving it out would hide both.
	var rows []struct {
		ID       string `db:"id"`
		Label    string `db:"label"`
		Account  string `db:"account"`
		Revoked  bool   `db:"revoked"`
		LastSeen string `db:"last_seen"`
	}
	err = app.DB().NewQuery(
		"SELECT id, COALESCE(label, '') AS label, account, revoked," +
			" COALESCE(last_seen, '') AS last_seen" +
			" FROM " + collDevices + " ORDER BY label, id",
	).All(&rows)
	if err != nil {
		return out, err
	}
	for _, row := range rows {
		out.Devices = append(out.Devices, overviewDevice{
			ID:       row.ID,
			Label:    row.Label,
			Account:  row.Account,
			Revoked:  row.Revoked,
			LastSeen: row.LastSeen,
			Entries:  stats.DeviceEntries[row.ID],
		})
	}
	return out, nil
}

// operatorToken is the credential the /operator routes want.
//
// Deliberately not metricsToken(). That one is read-only — counts, ages and
// who is paired — and is the sort of thing handed to a monitoring system and
// forgotten about. These routes rename and revoke devices. The operator can
// already stop the process and read the file, so this is not new power; it is
// the same power written down under a name that says what it does, so a
// scraper's credential does not silently acquire it.
func operatorToken() string {
	if token := strings.TrimSpace(os.Getenv("SUMMAREADER_OPERATOR_TOKEN")); token != "" {
		return token
	}
	return strings.TrimSpace(settings.OperatorToken)
}

// operatorOK answers whether this request carries the operator credential.
//
// Off rather than open when no token is set, and 404 rather than 401 for the
// same reason handleOverview does it: a route that answers "unauthorized" has
// told you it exists.
func operatorOK(e *core.RequestEvent) (bool, error) {
	want := operatorToken()
	if want == "" {
		return false, e.NotFoundError("", nil)
	}
	token := strings.TrimPrefix(e.Request.Header.Get("Authorization"), "Bearer ")
	if subtleCompare(strings.TrimSpace(token), want) != 1 {
		return false, e.UnauthorizedError("", nil)
	}
	return true, nil
}
