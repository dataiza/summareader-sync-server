package main

import (
	"sync"
	"time"
)

// Waiting devices, and how they are told something arrived.
//
// `/subscribe` used to answer at once with the head of the log, which meant a
// device learned about a change only when it next asked — on its own timer,
// fifteen minutes by default. A phone marking an article read and a desktop
// finding out about it were a quarter of an hour apart.
//
// So the request waits. This is the registry it waits in: a channel per
// waiting device, closed when something is appended to that account. Closed
// rather than sent on, because a close wakes every reader and cannot block a
// writer — an append must never be slowed down, or even delayed, by whoever
// happens to be listening.
//
// **Not PocketBase's realtime API**, which was the first plan. That gates
// subscriptions on `@request.auth` and therefore needs a PocketBase auth
// record per subscriber; this server authenticates a device token of its own
// against its own tables, so using realtime would have meant inventing a
// second identity for every device purely to listen. Its events also carry the
// record, and a hint here carries nothing.
type hints struct {
	mu sync.Mutex
	// Per account, the channel each waiting device is parked on. The device
	// is the value because the one device that must *not* be woken is the one
	// that caused the append: it already knows, and telling it would start a
	// loop with a five-second period.
	waiting map[string]map[chan struct{}]string
}

func newHints() *hints {
	return &hints{waiting: map[string]map[chan struct{}]string{}}
}

// wait parks a device on this account until something is posted for it.
//
// The returned function must be called — it is what removes the channel from
// the registry, and without it a server that has answered a million requests
// is holding a million dead channels.
func (h *hints) wait(accountID, deviceID string) (<-chan struct{}, func()) {
	ch := make(chan struct{})

	h.mu.Lock()
	if h.waiting[accountID] == nil {
		h.waiting[accountID] = map[chan struct{}]string{}
	}
	h.waiting[accountID][ch] = deviceID
	h.mu.Unlock()

	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if devices := h.waiting[accountID]; devices != nil {
			delete(devices, ch)
			// The map itself goes when its last waiter does. An account with
			// no devices listening should cost nothing to have.
			if len(devices) == 0 {
				delete(h.waiting, accountID)
			}
		}
	}
}

// post wakes every device on an account except the one that caused it.
//
// Best-effort and never blocking: it holds the lock only long enough to
// collect the channels, and closing a channel cannot block. An append that
// nobody is listening for does a map lookup and returns.
func (h *hints) post(accountID, exceptDeviceID string) {
	h.mu.Lock()
	var wake []chan struct{}
	for ch, device := range h.waiting[accountID] {
		if device != exceptDeviceID {
			wake = append(wake, ch)
		}
	}
	// Removed under the same lock that found them, so a second post cannot
	// close an already-closed channel — which would panic, in a goroutine
	// serving somebody else's append.
	for _, ch := range wake {
		delete(h.waiting[accountID], ch)
	}
	if len(h.waiting[accountID]) == 0 {
		delete(h.waiting, accountID)
	}
	h.mu.Unlock()

	for _, ch := range wake {
		close(ch)
	}
}

// waitingFor reports how many devices are parked on an account. Tests only.
func (h *hints) waitingFor(accountID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.waiting[accountID])
}

// How long `/subscribe` holds a request open before answering "nothing new".
//
// Under a minute on purpose. Reverse proxies commonly close an idle request at
// sixty seconds, and a proxy timing out looks to the client like a server that
// has gone away — so the server answers first, and the answer is an ordinary
// one. Somebody behind something stricter can lower it; SUMMAREADER_HOLD, in
// seconds.
const defaultHold = 45 * time.Second

// The bounds it is clamped to.
//
// A negative number is outside it and means "do not wait at all", restoring
// the old answer-at-once behaviour for a deployment that cannot hold a
// connection open.
const (
	minHold = 5 * time.Second
	maxHold = 5 * time.Minute
)

// holdFor turns the configured number into a duration.
//
// Unset is the default wait; a negative number is a deliberate "do not wait at
// all", which is not the same answer and has to be spellable.
func holdFor(seconds int) time.Duration {
	if seconds == 0 {
		return defaultHold
	}
	if seconds < 0 {
		return 0
	}
	hold := time.Duration(seconds) * time.Second
	if hold < minHold {
		return minHold
	}
	if hold > maxHold {
		return maxHold
	}
	return hold
}
