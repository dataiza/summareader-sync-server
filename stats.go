package main

import (
	"sync"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// What /overview and /metrics report about the database, read in one pass.
//
// # Why the numbers are cached
//
// The console asked for them every two seconds, and every answer read the
// whole log: on a 1.1 GB library that was gigabytes of disk reads a minute,
// a write-ahead log held at its limit because a scan was always open, and
// device syncs waiting behind the scans. Nothing on that screen changes fast
// enough to need it. So the counting runs at most once a minute, one pass at
// a time, and every request in between answers from the last result and says
// when that result was taken.
type serverStats struct {
	ComputedAt time.Time

	Accounts   int64
	Devices    int64
	Entries    int64
	Blobs      int64
	EntryBytes int64
	BlobBytes  int64

	// Per account, for the Prometheus rows that say which library grew.
	AccountEntries map[string]int64
	AccountBytes   map[string]int64

	// Per device, for the console's "entries received" beside each device.
	// The device rows themselves are read per request, because they are a
	// handful of small rows and a rename or a revoke must show at once.
	DeviceEntries map[string]int64
}

// statsEvery is how old the numbers may get before a request asks for new
// ones. A minute is far below anything a person watching the console or a
// dashboard drawing a daily curve can tell apart.
const statsEvery = time.Minute

// statsCache holds the last pass and makes sure only one runs at a time.
type statsCache struct {
	// pass is held for the whole of a pass. A request that finds no result at
	// all waits on it, and the re-check under it is what turns two concurrent
	// first requests into one pass rather than two.
	pass sync.Mutex

	// mu guards the fields below and is never held while the database is read.
	mu      sync.Mutex
	last    *serverStats
	running bool

	now     func() time.Time
	collect func(core.App) (serverStats, error)
}

func newStatsCache() *statsCache {
	return &statsCache{now: time.Now, collect: collectStats}
}

// statsFor answers from the cache that belongs to this app.
//
// Kept in the app's own store rather than in a package variable, so each test
// app gets a cache of its own and one test's counts never answer another's.
func statsFor(app core.App) (serverStats, error) {
	cache := app.Store().GetOrSet("summareader.stats", func() any {
		return newStatsCache()
	}).(*statsCache)
	return cache.get(app)
}

// get returns the last result, starting a new pass in the background when it
// is older than statsEvery.
//
// Only the first request after start waits for the database. After that a
// stale result is served at once and replaced when the background pass ends:
// the console's request has a deadline of its own, and a pass over a large
// library on a cold disk can take longer than that.
func (c *statsCache) get(app core.App) (serverStats, error) {
	c.mu.Lock()
	if c.last != nil {
		out := *c.last
		if c.now().Sub(out.ComputedAt) >= statsEvery && !c.running {
			c.running = true
			go c.refresh(app)
		}
		c.mu.Unlock()
		return out, nil
	}
	c.mu.Unlock()

	if err := c.refresh(app); err != nil {
		return serverStats{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return *c.last, nil
}

// refresh runs one pass, unless another finished while this one waited.
func (c *statsCache) refresh(app core.App) error {
	c.pass.Lock()
	defer c.pass.Unlock()

	c.mu.Lock()
	fresh := c.last != nil && c.now().Sub(c.last.ComputedAt) < statsEvery
	c.mu.Unlock()
	if fresh {
		c.mu.Lock()
		c.running = false
		c.mu.Unlock()
		return nil
	}

	out, err := c.collect(app)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.running = false
	if err != nil {
		// The last good result keeps being served; the next request after the
		// interval tries again.
		return err
	}
	out.ComputedAt = c.now()
	c.last = &out
	return nil
}

// collectStats is the pass itself: a few cheap counts, two walks of the log
// and one of the blobs.
//
// # Why octet_length and not length
//
// The payload is base64 text, and SQLite's length() on text counts characters,
// so it loads every payload from its overflow pages to count them: measured on
// a 50,000-entry, 1 GB test database, 976 MB read for one SUM. octet_length()
// takes the size from the record header and never loads the value, which read
// 195 MB, only the table's leaf pages. For base64 the two numbers are the same.
// A running total would read nothing, but every write path (append, batch,
// blob, wipe, device removal) would have to keep it right, and quota.go
// explains why a counter that drifts silently is the worse trade here.
// SQLite's dbstat table is in this build, but it walks every page to sum them,
// and read 784 MB on the same database.
//
// ponytail: the walks of log_entries are O(entries) once a minute while
// somebody asks; on the test database the whole pass took about 0.4 s and read
// 393 MB. An index on device would make the per-device walk free (7 ms against
// 139 ms measured), at the cost of a schema change and a write per append.
func collectStats(app core.App) (serverStats, error) {
	out := serverStats{
		AccountEntries: map[string]int64{},
		AccountBytes:   map[string]int64{},
		DeviceEntries:  map[string]int64{},
	}

	var counts struct {
		Accounts int64 `db:"accounts"`
		Devices  int64 `db:"devices"`
	}
	err := app.DB().NewQuery(
		"SELECT (SELECT COUNT(*) FROM " + collAccounts + ") AS accounts," +
			" (SELECT COUNT(*) FROM " + collDevices + ") AS devices",
	).One(&counts)
	if err != nil {
		return out, err
	}
	out.Accounts, out.Devices = counts.Accounts, counts.Devices

	// Two walks of the log rather than one GROUP BY account, device. Grouped
	// by both, SQLite sorts the rows first and copies each payload into the
	// sorter to measure it afterwards, which read the whole 1 GB test database
	// again. Grouped by account alone it walks the (account, seq) index in
	// order and needs no sorter, and the per-device count never touches the
	// payload: about 195 MB each, a fifth of the file.
	var accounts []struct {
		Account string `db:"account"`
		N       int64  `db:"n"`
		Bytes   int64  `db:"bytes"`
	}
	err = app.DB().NewQuery(
		"SELECT account, COUNT(*) AS n," +
			" COALESCE(SUM(octet_length(payload)), 0) AS bytes" +
			" FROM " + collEntries + " GROUP BY account",
	).All(&accounts)
	if err != nil {
		return out, err
	}
	for _, row := range accounts {
		out.Entries += row.N
		out.EntryBytes += row.Bytes
		out.AccountEntries[row.Account] = row.N
		out.AccountBytes[row.Account] = row.Bytes
	}

	var devices []struct {
		Device string `db:"device"`
		N      int64  `db:"n"`
	}
	err = app.DB().NewQuery(
		"SELECT COALESCE(device, '') AS device, COUNT(*) AS n" +
			" FROM " + collEntries + " GROUP BY device",
	).All(&devices)
	if err != nil {
		return out, err
	}
	for _, row := range devices {
		out.DeviceEntries[row.Device] = row.N
	}

	var blobs struct {
		N     int64 `db:"n"`
		Bytes int64 `db:"bytes"`
	}
	err = app.DB().NewQuery(
		"SELECT COUNT(*) AS n, COALESCE(SUM(octet_length(payload)), 0) AS bytes" +
			" FROM " + collBlobs,
	).One(&blobs)
	if err != nil {
		return out, err
	}
	out.Blobs, out.BlobBytes = blobs.N, blobs.Bytes
	return out, nil
}
