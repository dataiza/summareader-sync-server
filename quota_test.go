package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// setQuota puts a ceiling on the account. Nothing else in the suite does this,
// which is the point: every other test runs against an account with no ceiling
// and must keep passing exactly as it did.
func setQuota(t *testing.T, app core.App, accountID string, bytes int) {
	t.Helper()

	account, err := app.FindRecordById(collAccounts, accountID)
	if err != nil {
		t.Fatal(err)
	}
	account.Set("quota_bytes", bytes)
	if err := app.Save(account); err != nil {
		t.Fatal(err)
	}
}

func TestNoQuotaByDefault(t *testing.T) {
	app, account := newTestApp(t)

	// A self-hosted server never sets one, so "no ceiling" is not a special
	// case to be configured — it is what the code does when left alone.
	if _, err := appendEntry(app, account, "device-1", strings.Repeat("x", 100000)); err != nil {
		t.Fatalf("append under no quota failed: %v", err)
	}

	quota, err := quotaFor(app, account)
	if err != nil {
		t.Fatal(err)
	}
	if quota != 0 {
		t.Fatalf("default quota = %d, want 0", quota)
	}
}

func TestAppendRefusedOverCeiling(t *testing.T) {
	app, account := newTestApp(t)
	setQuota(t, app, account, 100)

	if _, err := appendEntry(app, account, "device-1", strings.Repeat("x", 80)); err != nil {
		t.Fatalf("append inside the ceiling failed: %v", err)
	}

	_, err := appendEntry(app, account, "device-1", strings.Repeat("x", 40))
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("append over the ceiling: err = %v, want ErrQuotaExceeded", err)
	}

	// And the refusal left nothing behind: a rejected append must not consume
	// a sequence number, or the next reader sees a gap it will wait for
	// forever.
	entries, err := readFrom(app, account, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries after a refusal = %d, want 1", len(entries))
	}
	if entries[0].Seq != 1 {
		t.Fatalf("seq after a refusal = %d, want 1", entries[0].Seq)
	}
}

func TestUsageCountsBlobsToo(t *testing.T) {
	app, account := newTestApp(t)

	if _, err := appendEntry(app, account, "device-1", strings.Repeat("x", 50)); err != nil {
		t.Fatal(err)
	}
	if err := putBlob(app, account, "recovery", strings.Repeat("y", 30)); err != nil {
		t.Fatal(err)
	}

	used, err := usageFor(app, account)
	if err != nil {
		t.Fatal(err)
	}
	if used != 80 {
		t.Fatalf("usage = %d, want 80", used)
	}
}

func TestReuploadingTheSameBlobIsAlwaysAllowed(t *testing.T) {
	app, account := newTestApp(t)

	if err := putBlob(app, account, "recovery", strings.Repeat("y", 90)); err != nil {
		t.Fatal(err)
	}
	setQuota(t, app, account, 100)

	// Content-addressed and already stored, so this adds nothing. Refusing it
	// would break recovery-blob refresh on exactly the account that cannot
	// make room for a second copy.
	if err := putBlob(app, account, "recovery", strings.Repeat("y", 90)); err != nil {
		t.Fatalf("re-uploading an existing blob was refused: %v", err)
	}

	if err := putBlob(app, account, "other", strings.Repeat("z", 20)); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("a new blob over the ceiling: err = %v, want ErrQuotaExceeded", err)
	}
}

func TestRoomFreedIsRoomReturned(t *testing.T) {
	app, account := newTestApp(t)
	setQuota(t, app, account, 100)

	if _, err := appendEntry(app, account, "device-1", strings.Repeat("x", 95)); err != nil {
		t.Fatal(err)
	}
	if _, err := appendEntry(app, account, "device-1", "xxxxxxxxxx"); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("expected the account to be full, got %v", err)
	}

	if _, err := wipeAccount(app, account, "Desktop", false); err != nil {
		t.Fatal(err)
	}

	// Summed rather than counted, so emptying the account is enough on its
	// own: there is no counter that also had to be told.
	if _, err := appendEntry(app, account, "device-1", "xxxxxxxxxx"); err != nil {
		t.Fatalf("append after making room failed: %v", err)
	}
}

func TestQuotaSurvivesAnUpgrade(t *testing.T) {
	app, _ := newTestApp(t)

	// The field is added to a collection that already exists, because a server
	// that predates quotas must gain the column without its owner running a
	// migration they did not ask for.
	accounts, err := app.FindCollectionByNameOrId(collAccounts)
	if err != nil {
		t.Fatal(err)
	}
	accounts.Fields.RemoveByName("quota_bytes")
	if err := app.Save(accounts); err != nil {
		t.Fatal(err)
	}

	if err := ensureSchema(app); err != nil {
		t.Fatal(err)
	}

	accounts, err = app.FindCollectionByNameOrId(collAccounts)
	if err != nil {
		t.Fatal(err)
	}
	if accounts.Fields.GetByName("quota_bytes") == nil {
		t.Fatal("quota_bytes was not added to an existing collection")
	}
}
