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

func ensureAccounts(app core.App) error {
	if _, err := app.FindCollectionByNameOrId(collAccounts); err == nil {
		return nil
	}

	c := core.NewBaseCollection(collAccounts)
	// The counter that makes seq gap-free. It is a column on a row we update
	// inside the same transaction as the insert, never an identity column —
	// see append.go for why that distinction is the whole design.
	c.Fields.Add(&core.NumberField{Name: "seq", Required: false})
	// Opaque. Set at pairing time; the server never derives meaning from it.
	c.Fields.Add(&core.TextField{Name: "label", Max: 200})

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

func ensureDevices(app core.App) error {
	if _, err := app.FindCollectionByNameOrId(collDevices); err == nil {
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

	c.AddIndex("idx_devices_token", true, "token", "")
	return app.Save(c)
}
