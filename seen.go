package main

import (
	"regexp"
	"strings"
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
	lastSeen = map[string]seen{}
)

// What was last written for one device: the minute, and the client it named.
type seen struct {
	at     time.Time
	client string
}

// The User-Agent the app sends, card 1004: `SummaReader/0.137.2 (Android;
// premium)`. Only this shape is kept, so a script holding a copied token, or
// Dart's own default, never passes for a build of the app.
var appAgent = regexp.MustCompile(`^SummaReader/([0-9A-Za-z.+-]{1,40}) \(([A-Za-z]{1,20}); ([a-z]{1,20})\)$`)

// clientLabel turns the app's User-Agent into the line the operator reads,
// `SummaReader 0.137.2 · Android · premium`, or "" for anything else.
func clientLabel(agent string) string {
	m := appAgent.FindStringSubmatch(agent)
	if m == nil {
		return ""
	}
	return strings.Join([]string{"SummaReader " + m[1], m[2], m[3]}, " · ")
}

// touchDevice records that this device was heard from, at minute resolution,
// and which build of the app it said it was. A new build writes at once, even
// inside the minute: an update between two syncs should not wait for the
// clock to show.
//
// Deliberately best-effort: a device that syncs successfully must not be told
// its sync failed because a bookkeeping column would not write. The error is
// dropped rather than returned, which is the one place in this server where
// that is the right thing to do.
func touchDevice(app core.App, deviceID string, now time.Time, agent string) {
	if deviceID == "" {
		return
	}

	rounded := now.UTC().Truncate(seenGranularity)
	client := clientLabel(agent)

	seenMu.Lock()
	previous, known := lastSeen[deviceID]
	newClient := client != "" && client != previous.client
	if known && !rounded.After(previous.at) && !newClient {
		seenMu.Unlock()
		return
	}
	next := seen{at: rounded, client: previous.client}
	if client != "" {
		next.client = client
	}
	lastSeen[deviceID] = next
	seenMu.Unlock()

	record, err := app.FindRecordById(collDevices, deviceID)
	if err != nil {
		return
	}
	record.Set("last_seen", rounded.Format(time.RFC3339))
	if client != "" {
		record.Set("client", client)
	}
	_ = app.Save(record)
}
