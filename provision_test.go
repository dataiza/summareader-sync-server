package main

import (
	"strings"
	"testing"
)

func TestCreateAccountIssuesAWorkingToken(t *testing.T) {
	app, _ := newTestApp(t)

	device, err := createAccount(app, "My library", "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	if device.Token == "" {
		t.Fatal("no token issued — sync would be unusable")
	}

	// The bootstrap has to actually work, or the whole flow is theatre.
	accountID, deviceID, err := accountForToken(app, device.Token)
	if err != nil {
		t.Fatalf("the token just issued does not resolve: %v", err)
	}
	if accountID != device.AccountID || deviceID != device.DeviceID {
		t.Fatal("the token resolves to the wrong account or device")
	}

	if _, err := appendEntry(app, accountID, deviceID, "ciphertext"); err != nil {
		t.Fatalf("a freshly paired device cannot append: %v", err)
	}
}

func TestTokensAreUnguessableAndUnique(t *testing.T) {
	app, _ := newTestApp(t)

	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		device, err := createAccount(app, "acct", "dev")
		if err != nil {
			t.Fatal(err)
		}
		if len(device.Token) < 32 {
			t.Fatalf("token is only %d characters", len(device.Token))
		}
		// A token that encodes the account would leak it to whoever holds it.
		if strings.Contains(device.Token, device.AccountID) {
			t.Fatal("the token contains the account id")
		}
		if seen[device.Token] {
			t.Fatal("a token was issued twice")
		}
		seen[device.Token] = true
	}
}

func TestEnrolledDeviceSharesTheAccountButNotTheToken(t *testing.T) {
	app, _ := newTestApp(t)

	first, err := createAccount(app, "My library", "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	second, err := enrollDevice(app, first.AccountID, "Phone")
	if err != nil {
		t.Fatal(err)
	}

	if second.AccountID != first.AccountID {
		t.Fatal("the enrolled device landed on a different account")
	}
	// Separate tokens are what make revoking one device possible at all.
	if second.Token == first.Token {
		t.Fatal("both devices share a token — neither could be revoked alone")
	}

	// Both can now read the same log.
	if _, err := appendEntry(app, first.AccountID, first.DeviceID, "from desktop"); err != nil {
		t.Fatal(err)
	}
	entries, err := readFrom(app, second.AccountID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("the enrolled device sees %d entries, want 1", len(entries))
	}
}

func TestRevokingOneDeviceLeavesTheOther(t *testing.T) {
	app, _ := newTestApp(t)

	desktop, _ := createAccount(app, "My library", "Desktop")
	phone, err := enrollDevice(app, desktop.AccountID, "Phone")
	if err != nil {
		t.Fatal(err)
	}

	if err := revokeDevice(app, desktop.AccountID, phone.DeviceID); err != nil {
		t.Fatal(err)
	}

	if _, _, err := accountForToken(app, phone.Token); err == nil {
		t.Fatal("a revoked device still resolves")
	}
	if _, _, err := accountForToken(app, desktop.Token); err != nil {
		t.Fatal("revoking one device locked out the other")
	}
}

func TestRevocationIsScopedToTheAccount(t *testing.T) {
	app, _ := newTestApp(t)

	mine, _ := createAccount(app, "mine", "Desktop")
	theirs, _ := createAccount(app, "theirs", "Their laptop")

	// One account must not be able to revoke another's devices.
	if err := revokeDevice(app, mine.AccountID, theirs.DeviceID); err == nil {
		t.Fatal("an account revoked a device belonging to someone else")
	}
	if _, _, err := accountForToken(app, theirs.Token); err != nil {
		t.Fatal("the other account's device was revoked anyway")
	}
}

func TestListingDevicesNeverReturnsTokens(t *testing.T) {
	app, _ := newTestApp(t)

	first, _ := createAccount(app, "My library", "Desktop")
	if _, err := enrollDevice(app, first.AccountID, "Phone"); err != nil {
		t.Fatal(err)
	}

	devices, err := listDevices(app, first.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 2 {
		t.Fatalf("listed %d devices, want 2", len(devices))
	}
	// A token that appears in a list is a token that ends up in a screenshot.
	for _, device := range devices {
		if device.Token != "" {
			t.Fatalf("device %q was listed with its token", device.Label)
		}
	}
}

func TestDeviceCountIsRealForTheWipeConfirmation(t *testing.T) {
	app, _ := newTestApp(t)

	account, _ := createAccount(app, "My library", "Desktop")
	for _, label := range []string{"Phone", "Laptop"} {
		if _, err := enrollDevice(app, account.AccountID, label); err != nil {
			t.Fatal(err)
		}
	}

	// The wipe confirmation states a blast radius, and "every device" is not
	// a number someone can weigh.
	count, err := countDevices(app, account.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("counted %d devices, want 3", count)
	}
}

func TestAccountsAreIsolatedAfterProvisioning(t *testing.T) {
	app, _ := newTestApp(t)

	mine, _ := createAccount(app, "mine", "Desktop")
	theirs, _ := createAccount(app, "theirs", "Desktop")

	if _, err := appendEntry(app, mine.AccountID, mine.DeviceID, "mine"); err != nil {
		t.Fatal(err)
	}

	entries, err := readFrom(app, theirs.AccountID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("a new account could see another account's log")
	}
}
