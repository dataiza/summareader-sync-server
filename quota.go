package main

import (
	"errors"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// ErrQuotaExceeded is returned when an account is at its ceiling.
var ErrQuotaExceeded = errors.New("account is full")

// Storage ceilings, off unless somebody sets one.
//
// # Why zero is the default and stays the default
//
// A self-hosted server's disk belongs to whoever bought it, and they can
// already see how full it is. A limit there would be a limiter every
// self-hoster has to be told to ignore. So `quota_bytes` is 0 on every account
// this server creates, 0 means no ceiling, and nothing below runs at all until
// an operator sets a number.
//
// The number exists for the case where somebody else's growth is our bill: an
// instance hosting accounts we are paying for. Set it per account, in the
// PocketBase dashboard or by whatever provisions the account.
//
// # Why the usage is summed rather than counted
//
// The obvious design is a `used_bytes` column bumped in the append
// transaction, next to `seq`. It is also the design where wipe, blob dedup
// and any future compaction each have to remember to adjust it, and where the
// failure is silent: a counter that has drifted low reports room that is not
// there, and is discovered by an account exceeding a ceiling it was told it
// had not reached.
//
// Summing cannot drift, and costs nothing on a server with no quotas set,
// because the query never runs.
//
// ponytail: SUM per append, which is O(rows in the account). Fine while an
// account is a log of small deltas; if it ever measures slow, the upgrade is
// the counter column, and then wipe and compaction have to maintain it.
func quotaFor(app core.App, accountID string) (int64, error) {
	account, err := app.FindRecordById(collAccounts, accountID)
	if err != nil {
		return 0, ErrNoAccount
	}
	return int64(account.GetInt("quota_bytes")), nil
}

// usageFor is what the account currently holds, in bytes of payload.
//
// Payload only: the row overhead, the indexes and the ids are real disk too,
// but they are ours to predict and not something a user can act on. A ceiling
// someone can reason about is worth more than one that is exactly right.
func usageFor(app core.App, accountID string) (int64, error) {
	var total int64
	for _, collection := range []string{collEntries, collBlobs} {
		var result struct {
			Total *int64 `db:"total"`
		}
		err := app.DB().
			Select("COALESCE(SUM(LENGTH(payload)), 0) AS total").
			From(collection).
			Where(dbx.HashExp{"account": accountID}).
			One(&result)
		if err != nil {
			return 0, err
		}
		if result.Total != nil {
			total += *result.Total
		}
	}
	return total, nil
}

// checkQuota reports whether `incoming` more bytes would put the account over.
//
// Returns nil immediately when no quota is set, which is every self-hosted
// account and the whole point of the design above.
//
// Checked before the write rather than after: refusing an append is a thing a
// device can be told and can retry after making room, where discovering the
// ceiling afterwards would mean either accepting the write anyway or undoing
// one that has already been given a sequence number.
func checkQuota(app core.App, accountID string, incoming int) error {
	quota, err := quotaFor(app, accountID)
	if err != nil {
		return err
	}
	if quota <= 0 {
		return nil
	}

	used, err := usageFor(app, accountID)
	if err != nil {
		return err
	}
	if used+int64(incoming) > quota {
		return ErrQuotaExceeded
	}
	return nil
}
