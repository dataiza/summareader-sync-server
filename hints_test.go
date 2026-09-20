package main

import (
	"testing"
	"time"
)

// A held request is only worth holding if it is woken by the right things and
// released by all of them.
func TestAHintWakesTheOtherDevices(t *testing.T) {
	h := newHints()

	desktop, doneDesktop := h.wait("account", "desktop")
	defer doneDesktop()
	tablet, doneTablet := h.wait("account", "tablet")
	defer doneTablet()

	h.post("account", "phone")

	select {
	case <-desktop:
	case <-time.After(time.Second):
		t.Fatal("the desktop was not woken")
	}
	select {
	case <-tablet:
	case <-time.After(time.Second):
		t.Fatal("the tablet was not woken")
	}
}

// The device that caused the append already knows. Waking it means it syncs in
// response to itself, and does so again on the next append, for ever.
func TestTheDeviceThatWroteIsNotWoken(t *testing.T) {
	h := newHints()

	phone, done := h.wait("account", "phone")
	defer done()

	h.post("account", "phone")

	select {
	case <-phone:
		t.Fatal("the device that wrote was woken by its own append")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestAnotherAccountIsNotWoken(t *testing.T) {
	h := newHints()

	mine, done := h.wait("mine", "desktop")
	defer done()

	h.post("theirs", "")

	select {
	case <-mine:
		t.Fatal("woken by an append to somebody else's account")
	case <-time.After(50 * time.Millisecond):
	}
}

// Without this a server that has answered a million requests is holding a
// million dead channels, and the map they are in never shrinks.
func TestWaitingIsForgotten(t *testing.T) {
	h := newHints()

	_, done := h.wait("account", "desktop")
	if h.waitingFor("account") != 1 {
		t.Fatalf("expected one waiter, got %d", h.waitingFor("account"))
	}

	done()
	if h.waitingFor("account") != 0 {
		t.Fatalf("waiter outlived its request: %d", h.waitingFor("account"))
	}

	// And a woken waiter is forgotten too, by post rather than by done.
	hinted, doneAgain := h.wait("account", "desktop")
	h.post("account", "phone")
	<-hinted
	if h.waitingFor("account") != 0 {
		t.Fatalf("woken waiter was kept: %d", h.waitingFor("account"))
	}
	// Calling it anyway must be safe: every caller defers it.
	doneAgain()
}

// A second append while the first is still being delivered must not close an
// already-closed channel, which panics in a goroutine serving somebody else.
func TestTwoAppendsInARowDoNotPanic(t *testing.T) {
	h := newHints()

	hinted, done := h.wait("account", "desktop")
	defer done()

	h.post("account", "phone")
	h.post("account", "phone")

	<-hinted
}

func TestHowLongToHold(t *testing.T) {
	// Unset is the default, so an existing deployment starts holding without
	// anybody editing a file.
	if got := holdFor(0); got != defaultHold {
		t.Fatalf("unset should be the default hold, got %s", got)
	}
	// Negative is the deliberate "do not hold", which is a different answer
	// from "not configured" and has to be spellable.
	if got := holdFor(-1); got != 0 {
		t.Fatalf("negative should switch the wait off, got %s", got)
	}
	if got := holdFor(1); got != minHold {
		t.Fatalf("clamped up, got %s", got)
	}
	if got := holdFor(100000); got != maxHold {
		t.Fatalf("clamped down, got %s", got)
	}
	if got := holdFor(20); got != 20*time.Second {
		t.Fatalf("a sensible number should be taken as given, got %s", got)
	}
	// Under a minute, because a reverse proxy closing an idle request at
	// sixty seconds looks to a device like a server that has gone away.
	if defaultHold >= time.Minute {
		t.Fatalf("the default hold must stay under a minute, it is %s", defaultHold)
	}
}
