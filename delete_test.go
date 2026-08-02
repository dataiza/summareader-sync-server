package main

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

func TestWipeRemovesEverythingAndVerifies(t *testing.T) {
	app, account := newTestApp(t)

	for i := 0; i < 5; i++ {
		if _, err := appendEntry(app, account, "d", "ciphertext"); err != nil {
			t.Fatal(err)
		}
	}
	if err := putBlob(app, account, "blob-a", "x"); err != nil {
		t.Fatal(err)
	}
	if err := putBlob(app, account, "blob-b", "y"); err != nil {
		t.Fatal(err)
	}

	result, err := wipeAccount(app, account, "Desktop", false)
	if err != nil {
		t.Fatal(err)
	}

	if result.EntriesRemoved != 5 || result.BlobsRemoved != 2 {
		t.Fatalf("removed %d entries and %d blobs, want 5 and 2",
			result.EntriesRemoved, result.BlobsRemoved)
	}
	// Read-back, not an optimistic success. An operation that reports "done"
	// without checking is the one nobody trusts twice.
	if !result.Verified {
		t.Fatal("wipe reported success without verifying")
	}

	entries, err := readFrom(app, account, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("%d entries survived the wipe", len(entries))
	}
}

func TestWipeResetsTheCounter(t *testing.T) {
	app, account := newTestApp(t)
	for i := 0; i < 4; i++ {
		if _, err := appendEntry(app, account, "d", "x"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := wipeAccount(app, account, "Desktop", false); err != nil {
		t.Fatal(err)
	}

	// If the counter kept climbing, the next upload would get sequence numbers
	// above a cursor another device still holds — and that device would never
	// see the new entries at all.
	seq, err := appendEntry(app, account, "d", "after the wipe")
	if err != nil {
		t.Fatal(err)
	}
	if seq != 1 {
		t.Fatalf("first seq after wipe = %d, want 1", seq)
	}
}

func TestWipeLeavesAReceiptWithNoContent(t *testing.T) {
	app, account := newTestApp(t)
	if _, err := appendEntry(app, account, "d", "something private"); err != nil {
		t.Fatal(err)
	}
	if _, err := wipeAccount(app, account, "Paul's Desktop", false); err != nil {
		t.Fatal(err)
	}

	receipt, err := receiptFor(app, account)
	if err != nil {
		t.Fatal(err)
	}
	if receipt == nil {
		t.Fatal("no receipt left — another device would only see an empty log")
	}
	if receipt["deleted_by"] != "Paul's Desktop" {
		t.Fatalf("receipt names %v", receipt["deleted_by"])
	}
	if receipt["deleted_at"] == "" {
		t.Fatal("receipt has no timestamp")
	}
	// The receipt must carry no user content, or deletion is weakened.
	for key, value := range receipt {
		if s, ok := value.(string); ok && s == "something private" {
			t.Fatalf("receipt leaked content in %q", key)
		}
	}
}

func TestReplacementReceiptSaysReplacedNotDeleted(t *testing.T) {
	app, account := newTestApp(t)
	if _, err := appendEntry(app, account, "d", "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := wipeAccount(app, account, "Desktop", true); err != nil {
		t.Fatal(err)
	}

	receipt, err := receiptFor(app, account)
	if err != nil {
		t.Fatal(err)
	}
	// After "make this device the source of truth", other devices must be told
	// what actually happened rather than that their library was deleted.
	if receipt["replacement"] != true {
		t.Fatal("a replacement was recorded as a deletion")
	}
}

func TestNoReceiptWhenNothingWasEverDeleted(t *testing.T) {
	app, account := newTestApp(t)

	// "We do not know why this is empty" and "someone deleted it" deserve
	// different words, so they must stay distinguishable.
	receipt, err := receiptFor(app, account)
	if err != nil {
		t.Fatal(err)
	}
	if receipt != nil {
		t.Fatalf("a never-deleted account produced a receipt: %v", receipt)
	}
}

func TestWipeDoesNotTouchOtherAccounts(t *testing.T) {
	app, mine := newTestApp(t)
	accounts, _ := app.FindCollectionByNameOrId(collAccounts)
	other := core.NewRecord(accounts)
	other.Set("seq", 0)
	if err := app.Save(other); err != nil {
		t.Fatal(err)
	}

	if _, err := appendEntry(app, mine, "d", "mine"); err != nil {
		t.Fatal(err)
	}
	if _, err := appendEntry(app, other.Id, "d", "theirs"); err != nil {
		t.Fatal(err)
	}
	if err := putBlob(app, other.Id, "theirs", "blob"); err != nil {
		t.Fatal(err)
	}

	if _, err := wipeAccount(app, mine, "Desktop", false); err != nil {
		t.Fatal(err)
	}

	survivors, err := readFrom(app, other.Id, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(survivors) != 1 {
		t.Fatalf("another account lost %d entries to this wipe", 1-len(survivors))
	}
	if _, err := getBlob(app, other.Id, "theirs"); err != nil {
		t.Fatal("another account lost a blob to this wipe")
	}
}

func TestWipingAnEmptyAccountIsHarmless(t *testing.T) {
	app, account := newTestApp(t)
	result, err := wipeAccount(app, account, "Desktop", false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified {
		t.Fatal("wiping nothing did not verify")
	}
}
