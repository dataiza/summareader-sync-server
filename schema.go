package main

import (
	"github.com/pocketbase/pocketbase/core"
)

// The server's entire view of the world.
//
// It knows account ids, device tokens, sequence numbers and ciphertext. It
// does not know what any of it means, and cannot: everything of substance is
// encrypted before it arrives. Billing identity lives in a separate table
// outside the sync path, so the sync path never needs to know who anyone is.
const (
	collAccounts = "accounts"
	collEntries  = "log_entries"
	collBlobs    = "blobs"
	collDevices  = "devices"
)

// ensureSchema creates the collections on first run.
//
// Written in Go rather than as migrations-by-dashboard because the server is
// meant to be a single binary someone self-hosts without ever opening an admin
// UI.
func ensureSchema(app core.App) error {
	if err := ensureAccounts(app); err != nil {
		return err
	}
	if err := ensureEntries(app); err != nil {
		return err
	}
	if err := ensureBlobs(app); err != nil {
		return err
	}
	return ensureDevices(app)
}

// The storage ceiling, in bytes. Zero — the default for every account this
// server creates — means no ceiling at all. See quota.go for why that is the
// default and why it stays one.
func quotaField() *core.NumberField {
	return &core.NumberField{Name: "quota_bytes", Required: false}
}

// What a device must show to join by typed code: SHA-256 of the proof the
// recovery code derives, base64. Never the code, and never anything that opens
// the wrapped master key — see provision.go.
//
// Empty on every account made before this existed, and on every account whose
// owner has not made a recovery code. joinDevice refuses an empty one rather
// than matching all of them.
func joinVerifierField() *core.TextField {
	return &core.TextField{Name: "join_verifier", Max: 100}
}

func ensureAccounts(app core.App) error {
	if existing, err := app.FindCollectionByNameOrId(collAccounts); err == nil {
		// A server that predates quotas has every other field already. Adding
		// the missing one here is what upgrades an existing deployment: the
		// alternative is a migration to run by hand, on servers whose owners
		// did not ask for a ceiling and will not be expecting a chore.
		//
		// Every missing field in one pass, and one save. Returning after the
		// first one meant a server old enough to be missing two of them got
		// the second only on its next boot — a route answering 500 for a day
		// with nothing anywhere to say why.
		changed := false
		if existing.Fields.GetByName("quota_bytes") == nil {
			existing.Fields.Add(quotaField())
			changed = true
		}
		if existing.Fields.GetByName("join_verifier") == nil {
			existing.Fields.Add(joinVerifierField())
			// Added with the field rather than beside it: the two arrive
			// together on every path, so a collection holding one and not the
			// other cannot happen. Not unique — every account that predates
			// this holds the same empty string, and a unique index over them
			// refuses to save at all.
			existing.AddIndex("idx_accounts_join", false, "join_verifier", "")
			changed = true
		}
		if !changed {
			return nil
		}
		return app.Save(existing)
	}

	c := core.NewBaseCollection(collAccounts)
	// The counter that makes seq gap-free. It is a column on a row we update
	// inside the same transaction as the insert, never an identity column —
	// see append.go for why that distinction is the whole design.
	c.Fields.Add(&core.NumberField{Name: "seq", Required: false})
	// Opaque. Set at pairing time; the server never derives meaning from it.
	c.Fields.Add(&core.TextField{Name: "label", Max: 200})

	// The deletion receipt. No user content — just enough for another device
	// to be told "deleted from your desktop on 20 July" instead of being left
	// to guess why its log is empty.
	c.Fields.Add(&core.TextField{Name: "deleted_at", Max: 40})
	c.Fields.Add(&core.TextField{Name: "deleted_by", Max: 200})
	c.Fields.Add(&core.BoolField{Name: "deleted_replacement"})

	c.Fields.Add(quotaField())
	c.Fields.Add(joinVerifierField())
	c.AddIndex("idx_accounts_join", false, "join_verifier", "")

	// No collection rules: every route is a Go handler that checks the device
	// token itself. Rules are evaluated per record and cannot express "this
	// counter must move in the same transaction as that insert".
	return app.Save(c)
}

func ensureEntries(app core.App) error {
	if _, err := app.FindCollectionByNameOrId(collEntries); err == nil {
		return nil
	}

	c := core.NewBaseCollection(collEntries)
	c.Fields.Add(&core.TextField{Name: "account", Required: true, Max: 100})
	c.Fields.Add(&core.NumberField{Name: "seq", Required: true})
	// Base64 ciphertext. The server has no key and no opinion about it.
	c.Fields.Add(&core.TextField{Name: "payload", Required: true, Max: 2000000})
	c.Fields.Add(&core.TextField{Name: "device", Max: 100})

	c.AddIndex("idx_entries_account_seq", true, "account, seq", "")
	return app.Save(c)
}

func ensureBlobs(app core.App) error {
	if _, err := app.FindCollectionByNameOrId(collBlobs); err == nil {
		return nil
	}

	c := core.NewBaseCollection(collBlobs)
	c.Fields.Add(&core.TextField{Name: "account", Required: true, Max: 100})
	// The HMAC name from the client. Content-addressed, so re-uploading the
	// same blob is idempotent and two of the user's devices converge on one
	// copy — while the server still cannot tell whether two *accounts* hold
	// the same file.
	c.Fields.Add(&core.TextField{Name: "name", Required: true, Max: 200})
	c.Fields.Add(&core.TextField{Name: "payload", Required: true, Max: 20000000})

	c.AddIndex("idx_blobs_account_name", true, "account, name", "")
	return app.Save(c)
}

// When this device last presented its token, to the minute. See seen.go for
// why it is only to the minute, and why an empty value is normal rather than
// an error.
func lastSeenField() *core.TextField {
	return &core.TextField{Name: "last_seen", Max: 40}
}

func ensureDevices(app core.App) error {
	if existing, err := app.FindCollectionByNameOrId(collDevices); err == nil {
		// Added to a collection that already exists, for the same reason
		// quota_bytes is: an upgrade must not need its owner to run anything.
		if existing.Fields.GetByName("last_seen") == nil {
			existing.Fields.Add(lastSeenField())
			return app.Save(existing)
		}
		return nil
	}

	c := core.NewBaseCollection(collDevices)
	c.Fields.Add(&core.TextField{Name: "account", Required: true, Max: 100})
	c.Fields.Add(&core.TextField{Name: "token", Required: true, Max: 200})
	c.Fields.Add(&core.TextField{Name: "label", Max: 200})
	// Revocation is nominal: dropping the token stops this device syncing.
	// Whatever it already downloaded stays readable, because it still holds
	// its own copy of the master key. Nothing here can change that.
	c.Fields.Add(&core.BoolField{Name: "revoked"})

	c.Fields.Add(lastSeenField())

	c.AddIndex("idx_devices_token", true, "token", "")
	return app.Save(c)
}
