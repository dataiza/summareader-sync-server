package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"

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
	// Omitted when empty, which is every listing: a token is issued once, to
	// the device it belongs to, and a list of everyone's tokens is the one
	// answer this endpoint must never give.
	Token   string `json:"token,omitempty"`
	Label   string `json:"label"`
	Revoked bool   `json:"revoked,omitempty"`
	// RFC3339, to the minute, and empty for a device that has not been heard
	// from since the column existed. Absent rather than zero, so a client can
	// say "not since we started recording" instead of 1 January year one.
	LastSeen string `json:"last_seen,omitempty"`
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

// labelTaken says whether another device on this account already answers to
// a name.
//
// Trimmed and folded, because "Phone" and "phone " are the collision people
// actually hit — two devices whose names differ only in case are two rows a
// person cannot tell apart in a list, which is the whole point of refusing.
//
// ponytail: read-then-write, so two simultaneous enrolments could both pass.
// Harmless here — the loser is renamed by hand — and a unique index is not an
// option; see the note on freeLabel.
func labelTaken(app core.App, accountID, label, exceptDeviceID string) (bool, error) {
	needle := strings.ToLower(strings.TrimSpace(label))
	if needle == "" {
		return false, nil
	}
	records := []*core.Record{}
	err := app.RecordQuery(collDevices).
		AndWhere(dbx.HashExp{"account": accountID}).
		All(&records)
	if err != nil {
		return false, err
	}
	for _, record := range records {
		if record.Id == exceptDeviceID {
			continue
		}
		if strings.ToLower(strings.TrimSpace(record.GetString("label"))) == needle {
			return true, nil
		}
	}
	return false, nil
}

// freeLabel turns a wanted name into one nothing else answers to.
//
// Enrolling and joining number rather than refuse, and that asymmetry is
// deliberate: the app sends "A new device" whenever nobody typed a name, so a
// refusal here would fail the second unnamed pairing in a dialogue that has no
// name field in view — pairing would look broken. There is nobody at a
// keyboard mid-pairing to read an error. A rename is a person typing, so that
// refuses and says why.
//
// No unique index backs this. Every install made before this existed already
// holds several rows reading "A new device", and PocketBase refuses to save a
// collection whose unique index is violated by the rows already in it — the
// same lesson join_verifier taught, in this same file.
func freeLabel(app core.App, accountID, want string) (string, error) {
	taken, err := labelTaken(app, accountID, want, "")
	if err != nil || !taken {
		return want, err
	}
	for n := 2; n < 1000; n++ {
		candidate := fmt.Sprintf("%s %d", want, n)
		taken, err := labelTaken(app, accountID, candidate, "")
		if err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
	}
	// A thousand devices called the same thing is not a case worth a design.
	return "", ErrLabelTaken
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

	// The caller is expected to have passed the wanted name through freeLabel.
	// This is the backstop for anything that did not.
	taken, err := labelTaken(app, accountID, label, "")
	if err != nil {
		return nil, err
	}
	if taken {
		return nil, ErrLabelTaken
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

// setJoinVerifier records what a typed recovery code must prove to join.
//
// Called by a device that already has a token, at the moment it mints a code.
// The verifier is a hash of a value derived from the code; the code itself
// never reaches the server, and neither does the key that unwraps the master.
// Overwriting it is how rotation revokes the old code — the wrapped key under
// `recovery-v1` is replaced in the same act, so a stale code loses both halves
// together rather than keeping one and silently half-working.
func setJoinVerifier(app core.App, accountID, verifier string) error {
	record, err := app.FindRecordById(collAccounts, accountID)
	if err != nil {
		return err
	}
	record.Set("join_verifier", verifier)
	return app.Save(record)
}

// joinDevice issues a token to a device that holds nothing but the code.
//
// The one route that takes no token, because there is nobody to be yet: this
// is how a device with no key material and no enrolment gets in. What it
// proves is knowledge of the recovery code, which is ~147 bits, so guessing is
// not a threat worth rate-limiting. Nothing is created on a failed attempt.
//
// The empty check is load-bearing rather than tidiness. Every account made
// before join_verifier existed holds "", so a filter on the verifier alone
// would match all of them at once and hand a token to whoever asked with
// nothing — the first account on the server, to someone who knows no code.
func joinDevice(app core.App, proof, label string) (*Device, error) {
	if strings.TrimSpace(proof) == "" {
		return nil, ErrNoAccount
	}

	sum := sha256.Sum256([]byte(proof))
	verifier := base64.RawURLEncoding.EncodeToString(sum[:])

	record, err := app.FindFirstRecordByFilter(
		collAccounts,
		"join_verifier = {:verifier} && join_verifier != ''",
		dbx.Params{"verifier": verifier},
	)
	if err != nil || record == nil {
		return nil, ErrNoAccount
	}

	// The same enrolment an existing device would have performed, so a joined
	// device is an ordinary device: revocable, listable, nothing special about
	// how it got here — including the name being made free rather than
	// refused, since a device joining from a typed code has even less of a
	// person watching than one being paired.
	free, err := freeLabel(app, record.Id, label)
	if err != nil {
		return nil, err
	}
	return enrollDevice(app, record.Id, free)
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
			Revoked:   record.GetBool("revoked"),
			LastSeen:  record.GetString("last_seen"),
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

// renameDevice sets a device's own label.
//
// Its own, and nobody else's: the caller is identified by the token it
// authenticated with, so this cannot be used to relabel a sibling. A label is
// otherwise written once, at enrolment, by whichever device minted the token —
// which is fine for a phone somebody is holding and useless for a headless
// one, whose operator is not there at that moment and whose name lives in its
// config file.
func renameDevice(app core.App, accountID, deviceID, label string) error {
	record, err := app.FindRecordById(collDevices, deviceID)
	if err != nil {
		return err
	}
	// Scoped to the account as well as to the device, for the same reason
	// revokeDevice is: an id from one account must not reach another's rows.
	if record.GetString("account") != accountID {
		return fmt.Errorf("no such device on this account")
	}
	// Excluding itself, so renaming a device to the name it already has — or
	// to a different case of it — is not refused as a clash with itself.
	taken, err := labelTaken(app, accountID, label, deviceID)
	if err != nil {
		return err
	}
	if taken {
		return ErrLabelTaken
	}
	record.Set("label", label)
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
