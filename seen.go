package main

import (
	"sync"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// How coarse "last seen" is.
//
// Every authenticated request would otherwise be a write: a client polling on
// a schedule turns every read into a read plus an UPDATE, on the busiest path
// the server has. A minute is finer than any answer this feeds — "which of
// these is the phone I lost" does not improve with seconds — and it collapses
// a poll storm into one write per device per minute.
const seenGranularity = time.Minute

// What each device was last recorded as, so a second request inside the same
// minute costs nothing at all.
//
// ponytail: a map with a lock, because the alternative is a write per request
// and the alternative to *that* is a background flusher nobody asked for. Per
// process, so it is rebuilt on restart and the first request after one always
// writes — which is correct rather than merely acceptable.
var (
	seenMu   sync.Mutex
	lastSeen = map[string]time.Time{}
)

// touchDevice records that this device was heard from, at minute resolution.
//
// Deliberately best-effort: a device that syncs successfully must not be told
// its sync failed because a bookkeeping column would not write. The error is
// dropped rather than returned, which is the one place in this server where
// that is the right thing to do.
func touchDevice(app core.App, deviceID string, now time.Time) {
	if deviceID == "" {
		return
	}

	rounded := now.UTC().Truncate(seenGranularity)

	seenMu.Lock()
	previous, known := lastSeen[deviceID]
	if known && !rounded.After(previous) {
		seenMu.Unlock()
		return
	}
	lastSeen[deviceID] = rounded
	seenMu.Unlock()

	record, err := app.FindRecordById(collDevices, deviceID)
	if err != nil {
		return
	}
	record.Set("last_seen", rounded.Format(time.RFC3339))
	_ = app.Save(record)
}
