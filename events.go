package main

import (
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// What happened to an account that its devices should hear about. Card 715.
//
// Any device's token can enrol another device, replace the recovery code or
// wipe the library, so a stolen token could quietly mint a sibling that
// outlives the revocation of the stolen one, and the only trace was one more
// row in a list nobody was looking at. Now each of those writes a line here,
// and every device on the account reads the lines it has not seen.
//
// Beside the log rather than in it. The log is ciphertext under a key this
// server has never held, so an entry written here would be one no device can
// open and every device would skip as unreadable; and a wipe deletes the log,
// which would take the record of the wipe with it. A short list the server
// writes in the clear, which a device asks for after each sync, fits both.
//
// Nothing in it is user content: a kind, a device id, a device label, which
// device did it, and when. The same things /devices already shows.
const collEvents = "account_events"

const (
	// A device enrolled another, or the operator did. Actor is the device,
	// or "operator".
	eventEnrolled = "enrolled"
	// A device let itself in with the recovery code. No actor: the new
	// device is the one that did it.
	eventJoined = "joined"
	// A device replaced the recovery code.
	eventRecovery = "recovery"
	// A device emptied the library.
	eventWiped = "wiped"
)

// ErrRateLimited is returned when an account has enrolled as many devices as
// it may for now.
var ErrRateLimited = errors.New("too many new devices on this library for now")

// AccountEvent is one line, as a device reads it.
type AccountEvent struct {
	ID string `json:"id"`
	// One of the event constants.
	Kind string `json:"kind"`
	// The device the event is about: the new one, or the one that acted.
	Device string `json:"device"`
	Label  string `json:"label"`
	// Who did it, where that is not Device itself.
	Actor string `json:"actor,omitempty"`
	// UTC, to the microsecond, fixed width, so that a string comparison is a
	// time comparison. It is the cursor a device asks from.
	At string `json:"at"`
}

const eventTimeFormat = "2006-01-02T15:04:05.000000Z"

func ensureEvents(app core.App) error {
	if _, err := app.FindCollectionByNameOrId(collEvents); err == nil {
		return nil
	}
	c := core.NewBaseCollection(collEvents)
	c.Fields.Add(&core.TextField{Name: "account", Required: true, Max: 100})
	c.Fields.Add(&core.TextField{Name: "kind", Required: true, Max: 20})
	c.Fields.Add(&core.TextField{Name: "device", Max: 100})
	c.Fields.Add(&core.TextField{Name: "label", Max: 200})
	c.Fields.Add(&core.TextField{Name: "actor", Max: 100})
	c.Fields.Add(&core.TextField{Name: "at", Required: true, Max: 40})
	c.AddIndex("idx_events_account_at", false, "account, at", "")
	return app.Save(c)
}

// recordEvent writes one line.
//
// ponytail: nothing is ever pruned. These are rare and rate-limited, a few a
// year on an ordinary account; trim to the newest few hundred per account if
// one ever grows enough to matter.
func recordEvent(app core.App, accountID, kind, deviceID, label, actor string) error {
	events, err := app.FindCollectionByNameOrId(collEvents)
	if err != nil {
		return err
	}
	record := core.NewRecord(events)
	record.Set("account", accountID)
	record.Set("kind", kind)
	record.Set("device", deviceID)
	record.Set("label", label)
	record.Set("actor", actor)
	record.Set("at", time.Now().UTC().Format(eventTimeFormat))
	return app.Save(record)
}

// enrollAndTell enrols a device and writes the line about it in one
// transaction, so there is never a device the other devices were not told of.
func enrollAndTell(app core.App, accountID, label, kind, actor string) (*Device, error) {
	var device *Device
	err := app.RunInTransaction(func(tx core.App) error {
		var err error
		device, err = enrollDevice(tx, accountID, label)
		if err != nil {
			return err
		}
		return recordEvent(tx, accountID, kind, device.DeviceID, label, actor)
	})
	return device, err
}

// deviceLabel is what a device is called on this server, for a line about
// something it did. Its own row rather than anything it said about itself: a
// stolen token can call itself whatever it likes in a request body.
func deviceLabel(app core.App, deviceID string) string {
	record, err := app.FindRecordById(collDevices, deviceID)
	if err != nil || record == nil {
		return ""
	}
	return record.GetString("label")
}

// eventsSince returns an account's lines after a cursor, oldest first.
func eventsSince(app core.App, accountID, since string, limit int) ([]AccountEvent, error) {
	records := []*core.Record{}
	query := app.RecordQuery(collEvents).
		AndWhere(dbx.HashExp{"account": accountID}).
		OrderBy("at ASC", "id ASC").
		Limit(int64(limit))
	if since != "" {
		query = query.AndWhere(dbx.NewExp("at > {:since}", dbx.Params{"since": since}))
	}
	if err := query.All(&records); err != nil {
		return nil, err
	}
	out := make([]AccountEvent, 0, len(records))
	for _, record := range records {
		out = append(out, AccountEvent{
			ID:     record.Id,
			Kind:   record.GetString("kind"),
			Device: record.GetString("device"),
			Label:  record.GetString("label"),
			Actor:  record.GetString("actor"),
			At:     record.GetString("at"),
		})
	}
	return out, nil
}

// handleEvents answers `GET /events?since=<at>`: the lines after the cursor,
// and which device is asking, so it can leave out what it did itself.
func handleEvents(e *core.RequestEvent) error {
	accountID, deviceID, err := authenticate(e)
	if err != nil {
		return authFailed(e, err)
	}
	events, err := eventsSince(e.App, accountID, e.Request.URL.Query().Get("since"), 100)
	if err != nil {
		return e.JSON(http.StatusInternalServerError, map[string]string{
			"error": "unavailable",
		})
	}
	return e.JSON(http.StatusOK, map[string]any{
		"events": events,
		"self":   deviceID,
	})
}

// How many devices one account may add in an hour, by any route but the
// operator's. Generous for somebody pairing a household's worth in an
// evening, and a ceiling on how fast a stolen token can mint siblings.
var enrolLimit = newRateLimit(10, time.Hour)

// What a refused enrolment is told. 429 rather than 403: the request was
// allowed, and will be again.
var rateLimitError = map[string]string{
	"error": "too many new devices have been added to this library in the " +
		"last hour — try again later",
}

// rateLimit counts events per key over a sliding window, in memory.
//
// ponytail: forgotten on restart, which hands a token that can restart the
// server a fresh allowance — and a token cannot. A table, if that changes.
type rateLimit struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	seen   map[string][]time.Time
	now    func() time.Time
}

func newRateLimit(max int, window time.Duration) *rateLimit {
	return &rateLimit{max: max, window: window, seen: map[string][]time.Time{}, now: time.Now}
}

func (r *rateLimit) allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	kept := r.seen[key][:0]
	for _, at := range r.seen[key] {
		if now.Sub(at) < r.window {
			kept = append(kept, at)
		}
	}
	if len(kept) >= r.max {
		r.seen[key] = kept
		return false
	}
	r.seen[key] = append(kept, now)
	return true
}
