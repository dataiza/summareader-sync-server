package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// A cache with a clock the test moves and a pass that only counts itself, so
// what is checked is when passes run and not what they find.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func countingCache(release <-chan struct{}) (*statsCache, *fakeClock, *atomic.Int32) {
	clock := &fakeClock{t: time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)}
	var passes atomic.Int32
	cache := &statsCache{now: clock.now, collect: func(core.App) (serverStats, error) {
		passes.Add(1)
		if release != nil {
			<-release
		}
		return serverStats{Entries: int64(passes.Load())}, nil
	}}
	return cache, clock, &passes
}

func TestTwoFirstRequestsShareOnePass(t *testing.T) {
	// Both arrive before any result exists, while the first pass is still
	// reading. Two passes would be the overlapping scans this cache exists to
	// stop.
	release := make(chan struct{})
	cache, _, passes := countingCache(release)

	var wg sync.WaitGroup
	results := make([]serverStats, 2)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := cache.get(nil)
			if err != nil {
				t.Error(err)
			}
			results[i] = out
		}()
	}
	// Give both goroutines time to reach the pass before letting it finish.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	if n := passes.Load(); n != 1 {
		t.Fatalf("two concurrent first requests ran %d passes, want 1", n)
	}
	if results[0].ComputedAt.IsZero() || results[0].ComputedAt != results[1].ComputedAt {
		t.Fatalf("the two requests got different results: %v and %v",
			results[0].ComputedAt, results[1].ComputedAt)
	}
}

func TestARequestWithinTheMinuteDoesNotRecompute(t *testing.T) {
	cache, clock, passes := countingCache(nil)

	first, err := cache.get(nil)
	if err != nil {
		t.Fatal(err)
	}
	clock.add(59 * time.Second)
	second, err := cache.get(nil)
	if err != nil {
		t.Fatal(err)
	}

	if n := passes.Load(); n != 1 {
		t.Fatalf("%d passes within a minute, want 1", n)
	}
	if second.ComputedAt != first.ComputedAt {
		t.Fatal("a request within the minute got a new result")
	}
}

func TestComputedAtAdvancesAfterTheInterval(t *testing.T) {
	cache, clock, passes := countingCache(nil)

	first, err := cache.get(nil)
	if err != nil {
		t.Fatal(err)
	}
	clock.add(statsEvery)

	// The request that finds the result stale is answered from it at once and
	// starts the new pass behind itself.
	stale, err := cache.get(nil)
	if err != nil {
		t.Fatal(err)
	}
	if stale.ComputedAt != first.ComputedAt {
		t.Fatal("a stale request waited for the new pass instead of being answered")
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		out, _ := cache.get(nil)
		if out.ComputedAt.After(first.ComputedAt) {
			if !out.ComputedAt.Equal(first.ComputedAt.Add(statsEvery)) {
				t.Fatalf("computed at %v, want %v", out.ComputedAt,
					first.ComputedAt.Add(statsEvery))
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no new pass ran after the interval")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if n := passes.Load(); n != 2 {
		t.Fatalf("%d passes, want 2", n)
	}
}

func TestTheStatsCountWhatIsHeld(t *testing.T) {
	// The pass against a real database: octet_length has to agree with what
	// was stored, and a device with no entries must not appear in the map as
	// anything but zero.
	app, account := newTestApp(t)
	for _, payload := range []string{"abc", "defgh"} {
		if _, err := appendEntry(app, account, "device-1", payload); err != nil {
			t.Fatal(err)
		}
	}
	if err := putBlob(app, account, "blob-1", "0123456789"); err != nil {
		t.Fatal(err)
	}

	out, err := collectStats(app)
	if err != nil {
		t.Fatal(err)
	}
	if out.Entries != 2 || out.EntryBytes != 8 {
		t.Fatalf("entries %d / %d bytes, want 2 / 8", out.Entries, out.EntryBytes)
	}
	if out.Blobs != 1 || out.BlobBytes != 10 {
		t.Fatalf("blobs %d / %d bytes, want 1 / 10", out.Blobs, out.BlobBytes)
	}
	if out.AccountEntries[account] != 2 || out.AccountBytes[account] != 8 {
		t.Fatalf("per account %d / %d, want 2 / 8",
			out.AccountEntries[account], out.AccountBytes[account])
	}
	if out.DeviceEntries["device-1"] != 2 {
		t.Fatalf("device-1 sent %d, want 2", out.DeviceEntries["device-1"])
	}
}
