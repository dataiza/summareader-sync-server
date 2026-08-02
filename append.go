package main

import (
	"errors"
	"fmt"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// ErrNoAccount is returned when the token does not resolve.
var ErrNoAccount = errors.New("unknown or revoked device")

// appendEntry writes one log entry and returns its sequence number.
//
// # Why this is a Go hook and not a collection rule
//
// The contract is: seq is strictly monotonic per account, gap-free, and
// visible to a reader only after commit.
//
// A bare auto-increment column cannot provide it. Identity values are assigned
// at INSERT, not at COMMIT. With two devices appending at once, a reader can
// observe seq 5 while seq 4 is still in flight, advance its cursor past 4, and
// never see entry 4 again. Nothing errors. The entry is simply gone from that
// device's view forever, and only under concurrency — which is to say, only in
// production and never in a test written by hand.
//
// The fix is a counter row bumped inside the same transaction as the insert.
// Both become visible at the same commit, so a reader can never see a gap.
// It serializes appends per account, which costs nothing when an account has
// three devices.
func appendEntry(app core.App, accountID, deviceID, payload string) (int64, error) {
	var seq int64

	err := app.RunInTransaction(func(tx core.App) error {
		account, err := tx.FindRecordById(collAccounts, accountID)
		if err != nil {
			return fmt.Errorf("account: %w", err)
		}

		// Read-modify-write inside the transaction. SQLite's single writer
		// makes this a genuine serialization point rather than a race.
		seq = int64(account.GetInt("seq")) + 1
		account.Set("seq", seq)
		if err := tx.Save(account); err != nil {
			return fmt.Errorf("counter: %w", err)
		}

		collection, err := tx.FindCollectionByNameOrId(collEntries)
		if err != nil {
			return err
		}

		entry := core.NewRecord(collection)
		entry.Set("account", accountID)
		entry.Set("seq", seq)
		entry.Set("payload", payload)
		entry.Set("device", deviceID)

		// The unique index on (account, seq) is the belt to this braces: if
		// the counter were ever wrong, this fails loudly instead of silently
		// duplicating a sequence number.
		return tx.Save(entry)
	})

	if err != nil {
		return 0, err
	}
	return seq, nil
}

// LogEntry is what a reader gets back. Deliberately four fields: anything more
// would be the server understanding the data.
type LogEntry struct {
	Seq     int64  `json:"seq"`
	Payload string `json:"payload"`
	Device  string `json:"device"`
}

// readFrom returns entries strictly after the given seq, in order.
//
// Exclusive on purpose: a client stores "the last seq I have" and asks for
// what follows. Inclusive would re-deliver the last entry on every poll.
func readFrom(app core.App, accountID string, after int64, limit int) ([]LogEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}

	records := []*core.Record{}
	err := app.RecordQuery(collEntries).
		AndWhere(dbx.HashExp{"account": accountID}).
		AndWhere(dbx.NewExp("seq > {:after}", dbx.Params{"after": after})).
		OrderBy("seq ASC").
		Limit(int64(limit)).
		All(&records)
	if err != nil {
		return nil, err
	}

	entries := make([]LogEntry, 0, len(records))
	for _, record := range records {
		entries = append(entries, LogEntry{
			Seq:     int64(record.GetInt("seq")),
			Payload: record.GetString("payload"),
			Device:  record.GetString("device"),
		})
	}
	return entries, nil
}

// putBlob stores content-addressed bytes. Idempotent: the name is derived from
// the content, so re-uploading is a no-op rather than a duplicate.
func putBlob(app core.App, accountID, name, payload string) error {
	existing, _ := app.FindFirstRecordByFilter(
		collBlobs,
		"account = {:account} && name = {:name}",
		dbx.Params{"account": accountID, "name": name},
	)
	if existing != nil {
		return nil
	}

	collection, err := app.FindCollectionByNameOrId(collBlobs)
	if err != nil {
		return err
	}

	record := core.NewRecord(collection)
	record.Set("account", accountID)
	record.Set("name", name)
	record.Set("payload", payload)
	return app.Save(record)
}

func getBlob(app core.App, accountID, name string) (string, error) {
	record, err := app.FindFirstRecordByFilter(
		collBlobs,
		"account = {:account} && name = {:name}",
		dbx.Params{"account": accountID, "name": name},
	)
	if err != nil {
		return "", err
	}
	return record.GetString("payload"), nil
}

// accountForToken resolves a device token to its account.
//
// A revoked device resolves to nothing, which is the whole of revocation on
// the server side. It cannot reach into the device to remove what was already
// downloaded, and the client copy says so.
func accountForToken(app core.App, token string) (string, string, error) {
	if token == "" {
		return "", "", ErrNoAccount
	}
	record, err := app.FindFirstRecordByFilter(
		collDevices,
		"token = {:token} && revoked = false",
		dbx.Params{"token": token},
	)
	if err != nil || record == nil {
		return "", "", ErrNoAccount
	}
	return record.GetString("account"), record.Id, nil
}
