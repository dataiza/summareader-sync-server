package main

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// The window asks one question and gets the whole answer, including the parts
// that are easy to leave out: a device that has never sent anything, and one
// that has been revoked. Both belong on screen — a device missing from the
// list is indistinguishable from a device that was never paired.
func TestOverviewCountsAndLists(t *testing.T) {
	app, account := newTestApp(t)

	devices, err := app.FindCollectionByNameOrId(collDevices)
	if err != nil {
		t.Fatal(err)
	}
	add := func(label string, revoked bool) string {
		record := core.NewRecord(devices)
		record.Set("account", account)
		record.Set("token", "token-"+label)
		record.Set("label", label)
		record.Set("revoked", revoked)
		if err := app.Save(record); err != nil {
			t.Fatal(err)
		}
		return record.Id
	}

	busy := add("Desktop", false)
	add("Never synced", false)
	add("Old phone", true)

	for i := 0; i < 4; i++ {
		if _, err := appendEntry(app, account, busy, "payload"); err != nil {
			t.Fatal(err)
		}
	}

	out, err := collectOverview(app)
	if err != nil {
		t.Fatal(err)
	}

	if out.Entries != 4 {
		t.Fatalf("entries = %d, want 4", out.Entries)
	}
	if out.Bytes != 4*int64(len("payload")) {
		t.Fatalf("bytes = %d, want %d", out.Bytes, 4*len("payload"))
	}
	if len(out.Devices) != 3 {
		t.Fatalf("listed %d devices, want 3", len(out.Devices))
	}

	byLabel := map[string]overviewDevice{}
	for _, device := range out.Devices {
		byLabel[device.Label] = device
	}
	if byLabel["Desktop"].Entries != 4 {
		t.Fatalf("Desktop appended %d, want 4", byLabel["Desktop"].Entries)
	}
	// The quiet device is the one worth seeing: new, or not getting through.
	quiet, ok := byLabel["Never synced"]
	if !ok {
		t.Fatal("a device that has sent nothing was left out of the list")
	}
	if quiet.Entries != 0 {
		t.Fatalf("quiet device appended %d, want 0", quiet.Entries)
	}
	if !byLabel["Old phone"].Revoked {
		t.Fatal("a revoked device must still be listed, and say so")
	}
}
