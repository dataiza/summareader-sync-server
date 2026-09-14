package main

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestOperatorTokenIsItsOwnCredential(t *testing.T) {
	// The whole point of the separation: a monitoring system holding the
	// metrics token must not be able to rename or revoke anything.
	os.Setenv("SUMMAREADER_METRICS_TOKEN", "scraper")
	t.Cleanup(func() { os.Unsetenv("SUMMAREADER_METRICS_TOKEN") })
	settings.OperatorToken = ""
	t.Cleanup(func() { settings.OperatorToken = "" })

	if operatorToken() != "" {
		t.Fatal("the metrics token was accepted as the operator's")
	}

	os.Setenv("SUMMAREADER_OPERATOR_TOKEN", "  sesame\n")
	t.Cleanup(func() { os.Unsetenv("SUMMAREADER_OPERATOR_TOKEN") })
	// Trimmed, for the reason the metrics one is: a token pasted out of a
	// file arrives with a newline on it more often than not.
	if operatorToken() != "sesame" {
		t.Fatalf("operator token is %q, want %q", operatorToken(), "sesame")
	}
}

func TestOperatorTokenFallsBackToTheConfigFile(t *testing.T) {
	os.Unsetenv("SUMMAREADER_OPERATOR_TOKEN")
	settings.OperatorToken = " from-the-file "
	t.Cleanup(func() { settings.OperatorToken = "" })
	if operatorToken() != "from-the-file" {
		t.Fatalf("operator token is %q", operatorToken())
	}
}

// An operator is paired with nobody, so there is no account to scope them to.
// The handler reads the account off the device record and hands it to the same
// renameDevice an ordinary device uses — which is what keeps that function's
// own scope check meaningful instead of bypassed.
func TestTheOperatorReachesAnyAccount(t *testing.T) {
	app, _ := newTestApp(t)

	mine, err := createAccount(app, "Mine", "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := createAccount(app, "Theirs", "Their laptop")
	if err != nil {
		t.Fatal(err)
	}

	for _, target := range []*Device{mine, theirs} {
		record, err := app.FindRecordById(collDevices, target.DeviceID)
		if err != nil {
			t.Fatal(err)
		}
		// Exactly what the handler does.
		err = renameDevice(app, record.GetString("account"), record.Id, "Renamed")
		if err != nil {
			t.Fatalf("operator rename on %s: %v", target.AccountID, err)
		}
		if err := renameDevice(app, record.GetString("account"), record.Id, "Renamed"); err != nil {
			t.Fatalf("renaming to the same name again: %v", err)
		}
	}
}

func TestTheOperatorIsRefusedADuplicateName(t *testing.T) {
	app, _ := newTestApp(t)
	account, err := createAccount(app, "My library", "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	phone, err := enrollDevice(app, account.AccountID, "Phone")
	if err != nil {
		t.Fatal(err)
	}

	record, err := app.FindRecordById(collDevices, phone.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	err = renameDevice(app, record.GetString("account"), record.Id, "desktop")
	if !errors.Is(err, ErrLabelTaken) {
		t.Fatalf("want ErrLabelTaken, got %v", err)
	}
}

func TestOperatorRevokeStopsTheDevice(t *testing.T) {
	app, _ := newTestApp(t)
	account, err := createAccount(app, "My library", "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	phone, err := enrollDevice(app, account.AccountID, "Phone")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := accountForToken(app, phone.Token); err != nil {
		t.Fatalf("the token did not work before revoking: %v", err)
	}

	record, err := app.FindRecordById(collDevices, phone.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := revokeDevice(app, record.GetString("account"), record.Id); err != nil {
		t.Fatal(err)
	}

	if _, _, err := accountForToken(app, phone.Token); !errors.Is(err, ErrNoAccount) {
		t.Fatal("a revoked device's token still resolves")
	}
}

func TestTheDeviceCommandsAreRegistered(t *testing.T) {
	// The one thing a compiler cannot catch here: registerDeviceCommands is
	// called from registerCommands, and forgetting that line leaves a CLI
	// that builds and has no devices command.
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "registerDeviceCommands(app)") {
		t.Fatal("registerCommands no longer calls registerDeviceCommands")
	}
}

func TestStoppingIsReversible(t *testing.T) {
	// The column was always a boolean; only the interface made it one-way. A
	// device stopped by mistake, or stopped while somebody was away, needed
	// pairing again from scratch to come back.
	app, _ := newTestApp(t)
	account, err := createAccount(app, "My library", "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	phone, err := enrollDevice(app, account.AccountID, "Phone")
	if err != nil {
		t.Fatal(err)
	}

	if err := revokeDevice(app, account.AccountID, phone.DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := accountForToken(app, phone.Token); !errors.Is(err, ErrNoAccount) {
		t.Fatal("a stopped device still syncs")
	}

	if err := resumeDevice(app, account.AccountID, phone.DeviceID); err != nil {
		t.Fatal(err)
	}
	// The same token: nothing had to be carried back to the device.
	if _, _, err := accountForToken(app, phone.Token); err != nil {
		t.Fatalf("a resumed device cannot sync: %v", err)
	}
}

func TestRemovingIsNotStopping(t *testing.T) {
	// Stopping keeps the row, which is the honest state for a phone somebody
	// still owns. Removing is for one that is gone, and takes the token with
	// it — so it cannot be resumed.
	app, _ := newTestApp(t)
	account, err := createAccount(app, "My library", "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	phone, err := enrollDevice(app, account.AccountID, "Phone")
	if err != nil {
		t.Fatal(err)
	}

	if err := removeDevice(app, account.AccountID, phone.DeviceID); err != nil {
		t.Fatal(err)
	}

	devices, err := listDevices(app, account.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].Label != "Desktop" {
		t.Fatalf("removed device is still listed: %v", devices)
	}
	if err := resumeDevice(app, account.AccountID, phone.DeviceID); err == nil {
		t.Fatal("a removed device was resumed")
	}
}

func TestRemovingIsScopedToTheAccount(t *testing.T) {
	// The same guard revoke and rename carry: an id from one account must not
	// reach another's rows.
	app, _ := newTestApp(t)
	mine, err := createAccount(app, "Mine", "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := createAccount(app, "Theirs", "Their laptop")
	if err != nil {
		t.Fatal(err)
	}

	if err := removeDevice(app, mine.AccountID, theirs.DeviceID); err == nil {
		t.Fatal("removed a device belonging to another account")
	}
	if _, _, err := accountForToken(app, theirs.Token); err != nil {
		t.Fatal("the other account's device stopped working anyway")
	}
}

func TestTheNameIsNotTheIdentity(t *testing.T) {
	// Two servers may be called the same thing without either being mistaken
	// for the other. That was the arrangement missing when the name *was* the
	// identity and every install was called "Acme".
	first, _ := newTestApp(t)
	second, _ := newTestApp(t)

	settings.Name = "Home"
	t.Cleanup(func() { settings.Name = "" })
	if err := applyServerName(first); err != nil {
		t.Fatal(err)
	}
	if err := applyServerName(second); err != nil {
		t.Fatal(err)
	}

	if first.Settings().Meta.AppName != "Home" {
		t.Fatalf("name is %q", first.Settings().Meta.AppName)
	}
	if instanceId(first) == instanceId(second) {
		t.Fatal("two servers sharing a name share an identity")
	}
}

func TestTheOperatorCanIssueATokenIntoTheOneLibrary(t *testing.T) {
	// Not a second library — the mistake this button used to make. A token
	// into the account that is already there, which is what a device coming
	// back to a library it already holds the key for needs.
	app, _ := newTestApp(t)
	account, err := createAccount(app, "My library", "Desktop")
	if err != nil {
		t.Fatal(err)
	}

	before, err := listDevices(app, account.AccountID)
	if err != nil {
		t.Fatal(err)
	}

	issued, err := enrollDevice(app, account.AccountID, "Coming back")
	if err != nil {
		t.Fatal(err)
	}
	if issued.AccountID != account.AccountID {
		t.Fatal("issued a token into a different library")
	}

	after, err := listDevices(app, account.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before)+1 {
		t.Fatalf("devices went from %d to %d", len(before), len(after))
	}
	// And it works, which is the whole point of issuing it.
	if _, _, err := accountForToken(app, issued.Token); err != nil {
		t.Fatalf("the issued token does not resolve: %v", err)
	}
}
