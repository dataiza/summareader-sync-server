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
