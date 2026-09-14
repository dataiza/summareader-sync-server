package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
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

func TestListingDevicesNeverCarriesTokens(t *testing.T) {
	app, _ := newTestApp(t)

	first, err := createAccount(app, "My library", "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enrollDevice(app, first.AccountID, "Phone"); err != nil {
		t.Fatal(err)
	}

	devices, err := listDevices(app, first.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 2 {
		t.Fatalf("devices = %d, want 2", len(devices))
	}

	// A list of everyone's tokens is the one answer this must never give: any
	// device could then act as any other, and revocation would mean nothing.
	for _, device := range devices {
		if device.Token != "" {
			t.Fatalf("%s came back carrying a token", device.Label)
		}
	}

	encoded, err := json.Marshal(devices)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "token") {
		t.Fatalf("the JSON has a token field in it: %s", encoded)
	}
}

func TestRevokedDevicesSayThatTheyAre(t *testing.T) {
	app, _ := newTestApp(t)

	first, err := createAccount(app, "My library", "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	phone, err := enrollDevice(app, first.AccountID, "Phone")
	if err != nil {
		t.Fatal(err)
	}
	if err := revokeDevice(app, first.AccountID, phone.DeviceID); err != nil {
		t.Fatal(err)
	}

	// Listed rather than hidden: a device that has been stopped is something
	// the person doing the stopping should still be able to see.
	devices, err := listDevices(app, first.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 2 {
		t.Fatalf("devices = %d, want 2 — a revoked device is still a device", len(devices))
	}
	for _, device := range devices {
		if device.DeviceID == phone.DeviceID && !device.Revoked {
			t.Fatal("the revoked device does not say so")
		}
		if device.DeviceID == first.DeviceID && device.Revoked {
			t.Fatal("the wrong device is marked revoked")
		}
	}
}

func TestADeviceCanRenameItself(t *testing.T) {
	app, _ := newTestApp(t)

	account, _ := createAccount(app, "My library", "Desktop")
	device, err := enrollDevice(app, account.AccountID, "A new device")
	if err != nil {
		t.Fatal(err)
	}

	// The label is written once, at enrolment, by whichever device minted the
	// token. That is fine for a phone somebody is holding and useless for a
	// headless one, whose name is in a config file nobody was reading at that
	// moment — so every such device is listed as "A new device".
	if err := renameDevice(app, account.AccountID, device.DeviceID, "MCP mirror"); err != nil {
		t.Fatal(err)
	}

	devices, err := listDevices(app, account.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range devices {
		if d.DeviceID == device.DeviceID && d.Label != "MCP mirror" {
			t.Fatalf("label is %q, want %q", d.Label, "MCP mirror")
		}
	}
}

func TestRenamingIsScopedToTheAccount(t *testing.T) {
	app, _ := newTestApp(t)

	mine, _ := createAccount(app, "Mine", "Desktop")
	theirs, _ := createAccount(app, "Theirs", "Their desktop")

	// An id from another account must not reach these rows, for the same
	// reason revoking is scoped: a device id is not a secret.
	err := renameDevice(app, mine.AccountID, theirs.DeviceID, "Mine now")
	if err == nil {
		t.Fatal("renamed a device on another account")
	}

	devices, _ := listDevices(app, theirs.AccountID)
	for _, d := range devices {
		if d.Label == "Mine now" {
			t.Fatal("the other account's device was renamed anyway")
		}
	}
}

// The verifier a code with this proof would have stored. The client derives
// the proof from the typed code; the server only ever sees this hash of it.
func verifierFor(proof string) string {
	sum := sha256.Sum256([]byte(proof))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// Every device on the server, not one account's — a refused join must not
// enrol anywhere, and the account it would have picked is the thing in doubt.
func countAllDevices(t *testing.T, app core.App) int {
	t.Helper()
	records := []*core.Record{}
	if err := app.RecordQuery(collDevices).All(&records); err != nil {
		t.Fatal(err)
	}
	return len(records)
}

func TestJoinIssuesATokenForTheRightAccount(t *testing.T) {
	app, _ := newTestApp(t)

	first, err := createAccount(app, "Someone else", "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	second, err := createAccount(app, "My library", "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	if err := setJoinVerifier(app, first.AccountID, verifierFor("their-proof")); err != nil {
		t.Fatal(err)
	}
	if err := setJoinVerifier(app, second.AccountID, verifierFor("my-proof")); err != nil {
		t.Fatal(err)
	}

	joined, err := joinDevice(app, "my-proof", "Phone")
	if err != nil {
		t.Fatalf("a correct proof was refused: %v", err)
	}
	if joined.AccountID != second.AccountID {
		t.Fatal("the proof let the device into the wrong account")
	}

	// A joined device is an ordinary device: its token works like any other.
	accountID, deviceID, err := accountForToken(app, joined.Token)
	if err != nil {
		t.Fatalf("the token just issued does not resolve: %v", err)
	}
	if accountID != second.AccountID {
		t.Fatal("the token resolves to the wrong account")
	}
	if _, err := appendEntry(app, accountID, deviceID, "ciphertext"); err != nil {
		t.Fatalf("a freshly joined device cannot append: %v", err)
	}
}

func TestJoinWithAWrongProofCreatesNoDevice(t *testing.T) {
	app, _ := newTestApp(t)

	account, err := createAccount(app, "My library", "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	if err := setJoinVerifier(app, account.AccountID, verifierFor("my-proof")); err != nil {
		t.Fatal(err)
	}

	before := countAllDevices(t, app)
	if _, err := joinDevice(app, "not-my-proof", "Phone"); err == nil {
		t.Fatal("a wrong proof was let in")
	}
	// An implementation that enrols first and checks afterwards passes the
	// line above and fails this one.
	if after := countAllDevices(t, app); after != before {
		t.Fatalf("a refused join left %d devices behind", after-before)
	}
}

func TestJoinIgnoresAccountsWithNoVerifier(t *testing.T) {
	app, _ := newTestApp(t)

	// Two accounts as they arrive from an older server: join_verifier is "".
	for _, label := range []string{"Older library", "Another one"} {
		if _, err := createAccount(app, label, "Desktop"); err != nil {
			t.Fatal(err)
		}
	}

	before := countAllDevices(t, app)
	// A filter on the verifier alone matches every one of them at once, and
	// the empty proof is what someone sending nothing at all would send.
	for _, proof := range []string{"", "   ", "\t\n", "anything-at-all"} {
		if _, err := joinDevice(app, proof, "Phone"); err == nil {
			t.Fatalf("proof %q was let into an account that has no code", proof)
		}
	}
	if after := countAllDevices(t, app); after != before {
		t.Fatal("a refused join enrolled a device anyway")
	}
}

func TestRotatingTheCodeLocksOutTheOldOne(t *testing.T) {
	app, _ := newTestApp(t)

	account, err := createAccount(app, "My library", "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	if err := setJoinVerifier(app, account.AccountID, verifierFor("old-proof")); err != nil {
		t.Fatal(err)
	}
	if err := setJoinVerifier(app, account.AccountID, verifierFor("new-proof")); err != nil {
		t.Fatal(err)
	}

	if _, err := joinDevice(app, "old-proof", "Phone"); err == nil {
		t.Fatal("the replaced code still joins")
	}
	if _, err := joinDevice(app, "new-proof", "Phone"); err != nil {
		t.Fatalf("the current code does not join: %v", err)
	}
}

// A server old enough to be missing more than one field has to gain all of
// them on the boot that notices, not one per boot.
func TestSchemaUpgradeAddsEveryMissingField(t *testing.T) {
	app, _ := newTestApp(t)

	accounts, err := app.FindCollectionByNameOrId(collAccounts)
	if err != nil {
		t.Fatal(err)
	}
	accounts.Fields.RemoveByName("quota_bytes")
	accounts.Fields.RemoveByName("join_verifier")
	// The index goes with the column, which is what a server that predates
	// both actually looks like — and SQLite refuses an index over a column
	// that is not there, so leaving it would test the test.
	accounts.RemoveIndex("idx_accounts_join")
	if err := app.Save(accounts); err != nil {
		t.Fatal(err)
	}

	if err := ensureSchema(app); err != nil {
		t.Fatal(err)
	}

	upgraded, err := app.FindCollectionByNameOrId(collAccounts)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"quota_bytes", "join_verifier"} {
		if upgraded.Fields.GetByName(name) == nil {
			t.Fatalf("%s is still missing after an upgrade boot", name)
		}
	}
}

func TestDuplicateLabelsAreRefusedOnRename(t *testing.T) {
	// Two devices a person cannot tell apart in a list is the thing being
	// prevented, so case and surrounding space do not make a name different.
	for _, tc := range []struct {
		existing, wanted string
		refused          bool
	}{
		{"Phone", "Phone", true},
		{"Phone", "phone", true},
		{"Phone", "  Phone  ", true},
		{"Phone", "PHONE", true},
		{"Phone", "Tablet", false},
	} {
		app, _ := newTestApp(t)
		first, err := createAccount(app, "My library", tc.existing)
		if err != nil {
			t.Fatal(err)
		}
		second, err := enrollDevice(app, first.AccountID, "Somewhere else")
		if err != nil {
			t.Fatal(err)
		}

		err = renameDevice(app, first.AccountID, second.DeviceID, tc.wanted)
		if tc.refused && !errors.Is(err, ErrLabelTaken) {
			t.Fatalf("renaming to %q beside %q: want ErrLabelTaken, got %v",
				tc.wanted, tc.existing, err)
		}
		if !tc.refused && err != nil {
			t.Fatalf("renaming to %q beside %q: %v", tc.wanted, tc.existing, err)
		}
	}
}

func TestTheSameNameOnAnotherAccountIsFine(t *testing.T) {
	// Accounts are separate libraries. What one calls its devices is none of
	// the other's business.
	app, _ := newTestApp(t)
	mine, err := createAccount(app, "Mine", "Phone")
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := createAccount(app, "Theirs", "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enrollDevice(app, theirs.AccountID, "Phone"); err != nil {
		t.Fatalf("a name used on another account was refused: %v", err)
	}
	_ = mine
}

func TestRenamingToItsOwnNameIsFine(t *testing.T) {
	// Without the except-itself clause every rename is refused, because every
	// device already answers to the name it is being asked to keep.
	app, _ := newTestApp(t)
	first, err := createAccount(app, "My library", "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	if err := renameDevice(app, first.AccountID, first.DeviceID, "Desktop"); err != nil {
		t.Fatalf("renaming a device to its own name: %v", err)
	}
	// And a different case of its own name, which is the same device.
	if err := renameDevice(app, first.AccountID, first.DeviceID, "desktop"); err != nil {
		t.Fatalf("renaming a device to its own name in another case: %v", err)
	}
}

func TestAnUnlabelledDeviceGetsAFreeName(t *testing.T) {
	// The app sends "A new device" whenever nobody typed a name, so this is
	// the ordinary path rather than an edge case. Refusing it would break the
	// second pairing in a dialogue with no name field in view.
	app, _ := newTestApp(t)
	first, err := createAccount(app, "My library", "A new device")
	if err != nil {
		t.Fatal(err)
	}

	second, err := freeLabel(app, first.AccountID, "A new device")
	if err != nil {
		t.Fatal(err)
	}
	if second != "A new device 2" {
		t.Fatalf("second unnamed device is %q, want \"A new device 2\"", second)
	}
	if _, err := enrollDevice(app, first.AccountID, second); err != nil {
		t.Fatal(err)
	}

	third, err := freeLabel(app, first.AccountID, "A new device")
	if err != nil {
		t.Fatal(err)
	}
	if third != "A new device 3" {
		t.Fatalf("third unnamed device is %q, want \"A new device 3\"", third)
	}
}

func TestLegacyDuplicatesStillSave(t *testing.T) {
	// Every install made before this existed holds several rows reading
	// "A new device". A unique index over them would refuse to save and the
	// server would not boot — which is why the check is in the application
	// and not in the schema. This is that decision, written down.
	app, _ := newTestApp(t)
	account, err := createAccount(app, "My library", "A new device")
	if err != nil {
		t.Fatal(err)
	}

	devices, err := app.FindCollectionByNameOrId(collDevices)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		token, err := newToken()
		if err != nil {
			t.Fatal(err)
		}
		record := core.NewRecord(devices)
		record.Set("account", account.AccountID)
		record.Set("token", token)
		record.Set("label", "A new device")
		record.Set("revoked", false)
		if err := app.Save(record); err != nil {
			t.Fatalf("a duplicate label could not be stored directly: %v", err)
		}
	}

	// And the schema still comes up clean over them.
	if err := ensureSchema(app); err != nil {
		t.Fatalf("ensureSchema over duplicate labels: %v", err)
	}
}
