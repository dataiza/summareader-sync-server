package main

import (
	"fmt"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// DeletionResult is what the client gets back, after a read-back rather than
// an optimistic assumption.
type DeletionResult struct {
	EntriesRemoved int    `json:"entries_removed"`
	BlobsRemoved   int    `json:"blobs_removed"`
	DeletedAt      string `json:"deleted_at"`
	DeletedBy      string `json:"deleted_by"`
	Replacement    bool   `json:"replacement"`
	Verified       bool   `json:"verified"`
}

// wipeAccount removes an account's log and blobs in one server-side operation.
//
// Deliberately not driven from the client row by row. A client with a partial
// cursor misses entries it never saw, deletes what it knows about, and reports
// success while data remains — the worst possible outcome for an operation
// whose entire purpose is that nothing is left.
//
// The receipt survives on purpose. It holds no user content, only when and by
// which device, which turns another device's "no data found" into a sentence
// its owner can act on.
func wipeAccount(app core.App, accountID, deviceName string, replacement bool) (*DeletionResult, error) {
	result := &DeletionResult{
		DeletedAt:   time.Now().UTC().Format(time.RFC3339),
		DeletedBy:   deviceName,
		Replacement: replacement,
	}

	err := app.RunInTransaction(func(tx core.App) error {
		entries := []*core.Record{}
		if err := tx.RecordQuery(collEntries).
			AndWhere(dbx.HashExp{"account": accountID}).
			All(&entries); err != nil {
			return err
		}
		for _, entry := range entries {
			if err := tx.Delete(entry); err != nil {
				return err
			}
		}
		result.EntriesRemoved = len(entries)

		blobs := []*core.Record{}
		if err := tx.RecordQuery(collBlobs).
			AndWhere(dbx.HashExp{"account": accountID}).
			All(&blobs); err != nil {
			return err
		}
		for _, blob := range blobs {
			if err := tx.Delete(blob); err != nil {
				return err
			}
		}
		result.BlobsRemoved = len(blobs)

		account, err := tx.FindRecordById(collAccounts, accountID)
		if err != nil {
			return err
		}
		// The counter resets with the log. Leaving it high would hand the next
		// upload sequence numbers above a cursor some device still holds, and
		// that device would never see the new entries.
		account.Set("seq", 0)
		account.Set("deleted_at", result.DeletedAt)
		account.Set("deleted_by", deviceName)
		account.Set("deleted_replacement", replacement)
		return tx.Save(account)
	})
	if err != nil {
		return nil, err
	}

	// Read back rather than reporting an optimistic success. An operation that
	// says "done" without checking is exactly the one nobody trusts twice.
	remaining, err := countForAccount(app, collEntries, accountID)
	if err != nil {
		return result, err
	}
	remainingBlobs, err := countForAccount(app, collBlobs, accountID)
	if err != nil {
		return result, err
	}
	result.Verified = remaining == 0 && remainingBlobs == 0
	if !result.Verified {
		return result, fmt.Errorf(
			"deletion incomplete: %d entries and %d blobs remain",
			remaining, remainingBlobs,
		)
	}

	return result, nil
}

func countForAccount(app core.App, collection, accountID string) (int, error) {
	records := []*core.Record{}
	err := app.RecordQuery(collection).
		AndWhere(dbx.HashExp{"account": accountID}).
		Limit(1).
		All(&records)
	if err != nil {
		return 0, err
	}
	return len(records), nil
}

// receiptFor returns what a device should be told about an empty log.
//
// Nil when the log was never deliberately emptied — which keeps "we do not
// know why this is empty" distinguishable from "someone deleted it", and those
// deserve different words.
func receiptFor(app core.App, accountID string) (map[string]any, error) {
	account, err := app.FindRecordById(collAccounts, accountID)
	if err != nil {
		return nil, err
	}
	deletedAt := account.GetString("deleted_at")
	if deletedAt == "" {
		return nil, nil
	}
	return map[string]any{
		"deleted_at":  deletedAt,
		"deleted_by":  account.GetString("deleted_by"),
		"replacement": account.GetBool("deleted_replacement"),
	}, nil
}
