package main

import (
	"net/http"
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
	Accounts int64            `json:"accounts"`
	Entries  int64            `json:"entries"`
	Blobs    int64            `json:"blobs"`
	Bytes    int64            `json:"bytes"`
	Uptime   float64          `json:"uptime_seconds"`
	Devices  []overviewDevice `json:"devices"`
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
func collectOverview(app core.App) (overview, error) {
	out := overview{Uptime: time.Since(started).Seconds(), Devices: []overviewDevice{}}

	var totals struct {
		Accounts int64 `db:"accounts"`
		Entries  int64 `db:"entries"`
		Blobs    int64 `db:"blobs"`
		Bytes    int64 `db:"bytes"`
	}
	// One query rather than five round-trips through the same file: this runs
	// every couple of seconds behind an open window.
	err := app.DB().NewQuery(
		"SELECT (SELECT COUNT(*) FROM " + collAccounts + ") AS accounts," +
			" (SELECT COUNT(*) FROM " + collEntries + ") AS entries," +
			" (SELECT COUNT(*) FROM " + collBlobs + ") AS blobs," +
			" (SELECT COALESCE(SUM(LENGTH(payload)), 0) FROM " + collEntries +
			") AS bytes",
	).One(&totals)
	if err != nil {
		return out, err
	}
	out.Accounts, out.Entries = totals.Accounts, totals.Entries
	out.Blobs, out.Bytes = totals.Blobs, totals.Bytes

	// Left join, so a device that has never sent anything is still in the
	// list. That device is the interesting one: it is either brand new or it
	// is not getting through, and leaving it out would hide both.
	var rows []struct {
		ID       string `db:"id"`
		Label    string `db:"label"`
		Account  string `db:"account"`
		Revoked  bool   `db:"revoked"`
		LastSeen string `db:"last_seen"`
		Entries  int64  `db:"entries"`
	}
	err = app.DB().NewQuery(
		"SELECT d.id AS id, COALESCE(d.label, '') AS label, d.account AS account," +
			" d.revoked AS revoked, COALESCE(d.last_seen, '') AS last_seen," +
			" COUNT(e.id) AS entries" +
			" FROM " + collDevices + " d LEFT JOIN " + collEntries + " e" +
			" ON e.device = d.id GROUP BY d.id ORDER BY d.label, d.id",
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
			Entries:  row.Entries,
		})
	}
	return out, nil
}
