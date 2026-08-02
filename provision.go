package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// newToken returns an opaque bearer token.
//
// 32 random bytes. It is not derived from anything and means nothing — the
// server looks it up, and that is the whole of its job. Nothing about the
// account or the device can be recovered from it.
func newToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// Device is what provisioning hands back.
type Device struct {
	AccountID string `json:"account_id"`
	DeviceID  string `json:"device_id"`
	Token     string `json:"token"`
	Label     string `json:"label"`
}

// createAccount makes an account and its first device.
//
// This is the bootstrap that has to happen outside the app, because until a
// device has a token there is nobody to authorise the request. Every device
// after this one is enrolled by an existing device instead.
func createAccount(app core.App, label, deviceLabel string) (*Device, error) {
	token, err := newToken()
	if err != nil {
		return nil, err
	}

	var device *Device
	err = app.RunInTransaction(func(tx core.App) error {
		accounts, err := tx.FindCollectionByNameOrId(collAccounts)
		if err != nil {
			return err
		}
		account := core.NewRecord(accounts)
		account.Set("seq", 0)
		account.Set("label", label)
		if err := tx.Save(account); err != nil {
			return err
		}

		devices, err := tx.FindCollectionByNameOrId(collDevices)
		if err != nil {
			return err
		}
		record := core.NewRecord(devices)
		record.Set("account", account.Id)
		record.Set("token", token)
		record.Set("label", deviceLabel)
		record.Set("revoked", false)
		if err := tx.Save(record); err != nil {
			return err
		}

		device = &Device{
			AccountID: account.Id,
			DeviceID:  record.Id,
			Token:     token,
			Label:     deviceLabel,
		}
		return nil
	})

	return device, err
}

// enrollDevice issues a token for a new device on an existing account.
//
// Called by a device that already has one. The QR a user scans during pairing
// carries the master key — which is what makes the library readable — but the
// new device still needs its own *server* token, and this is where it comes
// from. Separate tokens are what make revoking one device possible at all.
func enrollDevice(app core.App, accountID, label string) (*Device, error) {
	token, err := newToken()
	if err != nil {
		return nil, err
	}

	devices, err := app.FindCollectionByNameOrId(collDevices)
	if err != nil {
		return nil, err
	}

	record := core.NewRecord(devices)
	record.Set("account", accountID)
	record.Set("token", token)
	record.Set("label", label)
	record.Set("revoked", false)
	if err := app.Save(record); err != nil {
		return nil, err
	}

	return &Device{
		AccountID: accountID,
		DeviceID:  record.Id,
		Token:     token,
		Label:     label,
	}, nil
}

// listDevices returns an account's devices, without their tokens.
//
// The token is deliberately omitted. A settings screen listing paired devices
// has no use for it, and a token that appears in a list is a token that ends up
// in a screenshot.
func listDevices(app core.App, accountID string) ([]Device, error) {
	records := []*core.Record{}
	err := app.RecordQuery(collDevices).
		AndWhere(dbx.HashExp{"account": accountID}).
		All(&records)
	if err != nil {
		return nil, err
	}

	devices := make([]Device, 0, len(records))
	for _, record := range records {
		devices = append(devices, Device{
			AccountID: accountID,
			DeviceID:  record.Id,
			Label:     record.GetString("label"),
		})
	}
	return devices, nil
}

// revokeDevice stops a device syncing.
//
// Nominal, and that is the whole feature: the token stops resolving, so the
// device receives nothing further and can send nothing new. Whatever it has
// already downloaded stays readable, because it still holds its own copy of the
// master key. Nothing here can reach into a device, and the client copy says so.
func revokeDevice(app core.App, accountID, deviceID string) error {
	record, err := app.FindRecordById(collDevices, deviceID)
	if err != nil {
		return err
	}
	// Scoped to the account, or one account could revoke another's devices.
	if record.GetString("account") != accountID {
		return fmt.Errorf("no such device on this account")
	}
	record.Set("revoked", true)
	return app.Save(record)
}

// countDevices is used by the wipe confirmation, which must state the blast
// radius in real numbers rather than "every device".
func countDevices(app core.App, accountID string) (int, error) {
	devices, err := listDevices(app, accountID)
	if err != nil {
		return 0, err
	}
	return len(devices), nil
}
