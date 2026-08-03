package main

import (
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

func seenOf(t *testing.T, app core.App, deviceID string) string {
	t.Helper()

	record, err := app.FindRecordById(collDevices, deviceID)
	if err != nil {
		t.Fatal(err)
	}
	return record.GetString("last_seen")
}

func TestLastSeenIsRecordedToTheMinute(t *testing.T) {
	app, _ := newTestApp(t)
	device, err := createAccount(app, "My library", "Desktop")
	if err != nil {
		t.Fatal(err)
	}

	at := time.Date(2026, 8, 4, 10, 30, 45, 0, time.UTC)
	touchDevice(app, device.DeviceID, at)

	if got, want := seenOf(t, app, device.DeviceID), "2026-08-04T10:30:00Z"; got != want {
		t.Fatalf("last_seen = %q, want %q — seconds are not kept", got, want)
	}
}

func TestASecondRequestInTheSameMinuteWritesNothing(t *testing.T) {
	app, _ := newTestApp(t)
	device, err := createAccount(app, "My library", "Desktop")
	if err != nil {
		t.Fatal(err)
	}

	at := time.Date(2026, 8, 4, 10, 30, 0, 0, time.UTC)
	touchDevice(app, device.DeviceID, at)

	// Written behind the server's back. If a second touch in the same minute
	// wrote, this would be overwritten — and every poll would be a write on
	// the busiest path the server has.
	record, err := app.FindRecordById(collDevices, device.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	record.Set("last_seen", "sentinel")
	if err := app.Save(record); err != nil {
		t.Fatal(err)
	}

	touchDevice(app, device.DeviceID, at.Add(30*time.Second))

	if got := seenOf(t, app, device.DeviceID); got != "sentinel" {
		t.Fatalf("last_seen = %q — the second touch wrote when it should not", got)
	}

	// The next minute does write.
	touchDevice(app, device.DeviceID, at.Add(90*time.Second))
	if got, want := seenOf(t, app, device.DeviceID), "2026-08-04T10:31:00Z"; got != want {
		t.Fatalf("last_seen = %q, want %q", got, want)
	}
}

func TestAnUnknownDeviceIsNotRecorded(t *testing.T) {
	app, _ := newTestApp(t)

	// A rejected token must leave no trace: otherwise the column becomes a
	// record of who has been guessing.
	touchDevice(app, "", time.Now())
	touchDevice(app, "no-such-device", time.Now())
}

func TestListingCarriesLastSeen(t *testing.T) {
	app, _ := newTestApp(t)
	device, err := createAccount(app, "My library", "Desktop")
	if err != nil {
		t.Fatal(err)
	}

	devices, err := listDevices(app, device.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	// Empty until it has been heard from, which is a different thing from
	// "never" and reads as absent rather than as the year one.
	if devices[0].LastSeen != "" {
		t.Fatalf("last_seen = %q before anything happened", devices[0].LastSeen)
	}

	touchDevice(app, device.DeviceID, time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC))
	devices, err = listDevices(app, device.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if devices[0].LastSeen != "2026-08-04T09:00:00Z" {
		t.Fatalf("last_seen = %q after a touch", devices[0].LastSeen)
	}
}
