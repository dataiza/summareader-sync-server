package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

// newTestApp gives a throwaway PocketBase with our schema applied.
func newTestApp(t *testing.T) (core.App, string) {
	t.Helper()

	dir, err := os.MkdirTemp("", "summareader_sync")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	app, err := tests.NewTestApp(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Cleanup() })

	if err := ensureSchema(app); err != nil {
		t.Fatal(err)
	}

	accounts, err := app.FindCollectionByNameOrId(collAccounts)
	if err != nil {
		t.Fatal(err)
	}
	account := core.NewRecord(accounts)
	account.Set("seq", 0)
	account.Set("label", "test")
	if err := app.Save(account); err != nil {
		t.Fatal(err)
	}

	return app, account.Id
}

func TestSeqStartsAtOne(t *testing.T) {
	app, account := newTestApp(t)

	seq, err := appendEntry(app, account, "device-1", "ciphertext")
	if err != nil {
		t.Fatal(err)
	}
	if seq != 1 {
		t.Fatalf("first seq = %d, want 1", seq)
	}
}

func TestSeqIsMonotonicAndGapFree(t *testing.T) {
	app, account := newTestApp(t)

	for i := 1; i <= 25; i++ {
		seq, err := appendEntry(app, account, "device-1", fmt.Sprintf("entry %d", i))
		if err != nil {
			t.Fatal(err)
		}
		if seq != int64(i) {
			t.Fatalf("seq = %d, want %d", seq, i)
		}
	}
}

// The case the whole design exists for.
//
// With a bare identity column this test would usually still pass, which is
// precisely the danger: identity is assigned at INSERT, so the failure only
// appears when a reader observes a later seq while an earlier one is still
// in flight. What is asserted here is the invariant that makes that
// impossible — every seq from 1..N present exactly once, no duplicates and no
// gaps, no matter how the writes interleave.
func TestConcurrentAppendsLeaveNoGaps(t *testing.T) {
	app, account := newTestApp(t)

	const writers = 8
	const perWriter = 12
	const total = writers * perWriter

	var wg sync.WaitGroup
	seen := make(chan int64, total)
	errs := make(chan error, total)

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(writer int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				seq, err := appendEntry(
					app, account,
					fmt.Sprintf("device-%d", writer),
					fmt.Sprintf("w%d-e%d", writer, i),
				)
				if err != nil {
					errs <- err
					return
				}
				seen <- seq
			}
		}(w)
	}

	wg.Wait()
	close(seen)
	close(errs)

	for err := range errs {
		t.Fatalf("append failed under concurrency: %v", err)
	}

	counts := map[int64]int{}
	for seq := range seen {
		counts[seq]++
	}

	if len(counts) != total {
		t.Fatalf("got %d distinct seqs, want %d", len(counts), total)
	}
	for i := int64(1); i <= total; i++ {
		switch counts[i] {
		case 1:
			// exactly what we want
		case 0:
			t.Fatalf("seq %d was never assigned — a reader would skip it", i)
		default:
			t.Fatalf("seq %d assigned %d times", i, counts[i])
		}
	}
}

// A reader must never see a gap, because it advances its cursor past whatever
// it sees and would never come back for the missing one.
func TestReaderSeesEveryEntryExactlyOnce(t *testing.T) {
	app, account := newTestApp(t)

	const writers = 6
	const perWriter = 10
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(writer int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				if _, err := appendEntry(app, account, "d", fmt.Sprintf("%d-%d", writer, i)); err != nil {
					t.Error(err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	// Drain the way a client does: read from the cursor, advance, repeat.
	cursor := int64(0)
	collected := []LogEntry{}
	for {
		batch, err := readFrom(app, account, cursor, 7)
		if err != nil {
			t.Fatal(err)
		}
		if len(batch) == 0 {
			break
		}
		for _, entry := range batch {
			if entry.Seq <= cursor {
				t.Fatalf("readFrom returned seq %d at cursor %d", entry.Seq, cursor)
			}
			cursor = entry.Seq
		}
		collected = append(collected, batch...)
	}

	if len(collected) != writers*perWriter {
		t.Fatalf("drained %d entries, want %d", len(collected), writers*perWriter)
	}
	for i, entry := range collected {
		if entry.Seq != int64(i+1) {
			t.Fatalf("entry %d has seq %d — the log is not gap-free", i, entry.Seq)
		}
	}
}

func TestReadFromIsExclusive(t *testing.T) {
	app, account := newTestApp(t)
	for i := 0; i < 3; i++ {
		if _, err := appendEntry(app, account, "d", "x"); err != nil {
			t.Fatal(err)
		}
	}

	// A client stores "the last seq I have" and asks for what follows.
	// Inclusive would re-deliver the last entry on every single poll.
	entries, err := readFrom(app, account, 2, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Seq != 3 {
		t.Fatalf("got %d entries starting at %v, want 1 starting at 3",
			len(entries), entries)
	}
}

func TestEmptyLogIsNotAnError(t *testing.T) {
	app, account := newTestApp(t)

	// An empty server must never look like a failure, and above all must never
	// be read as "everything was deleted".
	entries, err := readFrom(app, account, 0, 100)
	if err != nil {
		t.Fatalf("empty log returned an error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("got %d entries from an empty log", len(entries))
	}
}

func TestAccountsAreIsolated(t *testing.T) {
	app, first := newTestApp(t)

	accounts, _ := app.FindCollectionByNameOrId(collAccounts)
	other := core.NewRecord(accounts)
	other.Set("seq", 0)
	if err := app.Save(other); err != nil {
		t.Fatal(err)
	}

	if _, err := appendEntry(app, first, "d", "mine"); err != nil {
		t.Fatal(err)
	}
	seq, err := appendEntry(app, other.Id, "d", "theirs")
	if err != nil {
		t.Fatal(err)
	}
	// Per-account counters: the second account starts at 1, not 2.
	if seq != 1 {
		t.Fatalf("second account's first seq = %d, want 1", seq)
	}

	entries, err := readFrom(app, first, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Payload != "mine" {
		t.Fatalf("account leaked another account's entries: %v", entries)
	}
}

func TestBlobsAreContentAddressedAndIdempotent(t *testing.T) {
	app, account := newTestApp(t)

	if err := putBlob(app, account, "hmac-name", "ciphertext"); err != nil {
		t.Fatal(err)
	}
	// Re-uploading the same content-addressed blob must be a no-op, not a
	// duplicate — two of the user's devices will do exactly this.
	if err := putBlob(app, account, "hmac-name", "ciphertext"); err != nil {
		t.Fatal(err)
	}

	payload, err := getBlob(app, account, "hmac-name")
	if err != nil {
		t.Fatal(err)
	}
	if payload != "ciphertext" {
		t.Fatalf("blob = %q", payload)
	}
}

func TestBlobsDoNotCrossAccounts(t *testing.T) {
	app, first := newTestApp(t)
	accounts, _ := app.FindCollectionByNameOrId(collAccounts)
	other := core.NewRecord(accounts)
	other.Set("seq", 0)
	if err := app.Save(other); err != nil {
		t.Fatal(err)
	}

	if err := putBlob(app, first, "same-name", "mine"); err != nil {
		t.Fatal(err)
	}
	if _, err := getBlob(app, other.Id, "same-name"); err == nil {
		t.Fatal("one account could read another's blob")
	}
}

func TestRevokedDeviceResolvesToNothing(t *testing.T) {
	app, account := newTestApp(t)
	devices, _ := app.FindCollectionByNameOrId(collDevices)

	live := core.NewRecord(devices)
	live.Set("account", account)
	live.Set("token", "live-token")
	live.Set("revoked", false)
	if err := app.Save(live); err != nil {
		t.Fatal(err)
	}

	revoked := core.NewRecord(devices)
	revoked.Set("account", account)
	revoked.Set("token", "revoked-token")
	revoked.Set("revoked", true)
	if err := app.Save(revoked); err != nil {
		t.Fatal(err)
	}

	if _, _, err := accountForToken(app, "live-token"); err != nil {
		t.Fatalf("live device rejected: %v", err)
	}
	if _, _, err := accountForToken(app, "revoked-token"); err == nil {
		t.Fatal("revoked device still resolved — revocation does nothing")
	}
	if _, _, err := accountForToken(app, ""); err == nil {
		t.Fatal("empty token resolved")
	}
	if _, _, err := accountForToken(app, "made-up"); err == nil {
		t.Fatal("unknown token resolved")
	}
}

// A batch is one commit, and the sequence numbers it hands out have to be
// indistinguishable from the ones a run of single appends would have given —
// the reader's gap-free guarantee does not know which endpoint was used.
func TestBatchIsGapFreeAndContinuesTheCounter(t *testing.T) {
	app, account := newTestApp(t)

	if _, err := appendEntry(app, account, "device-1", "first"); err != nil {
		t.Fatal(err)
	}

	payloads := make([]string, 50)
	for i := range payloads {
		payloads[i] = fmt.Sprintf("batched %d", i)
	}
	last, err := appendBatch(app, account, "device-1", payloads)
	if err != nil {
		t.Fatal(err)
	}
	if last != 51 {
		t.Fatalf("last seq = %d, want 51", last)
	}

	// And a single append afterwards carries on from there.
	next, err := appendEntry(app, account, "device-1", "after")
	if err != nil {
		t.Fatal(err)
	}
	if next != 52 {
		t.Fatalf("seq after batch = %d, want 52", next)
	}

	entries, err := readFrom(app, account, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 52 {
		t.Fatalf("read back %d entries, want 52", len(entries))
	}
	for i, entry := range entries {
		if entry.Seq != int64(i+1) {
			t.Fatalf("entry %d has seq %d — the log has a gap", i, entry.Seq)
		}
	}
}

// All or nothing. The client marks records sent only after the call returns,
// so a batch that half-wrote would lose exactly the records it did not write.
func TestBatchOverTheLimitWritesNothing(t *testing.T) {
	app, account := newTestApp(t)

	payloads := make([]string, maxBatch+1)
	for i := range payloads {
		payloads[i] = "x"
	}
	if _, err := appendBatch(app, account, "device-1", payloads); !errors.Is(err, ErrBatchTooLarge) {
		t.Fatalf("err = %v, want ErrBatchTooLarge", err)
	}

	entries, err := readFrom(app, account, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("wrote %d entries after a refused batch, want 0", len(entries))
	}
}

// The head is what exists, not what the counter is up to.
//
// Deleting entries out of band — a superuser in the dashboard, a restore from
// before they were written — used to leave the head at the counter's value.
// Every client then concluded the log was intact and that everything it held
// had already been sent, and reported a successful sync that moved nothing.
func TestHeadFollowsTheEntriesAndNotTheCounter(t *testing.T) {
	app, account := newTestApp(t)

	for i := 0; i < 5; i++ {
		if _, err := appendEntry(app, account, "device-1", "x"); err != nil {
			t.Fatal(err)
		}
	}
	head, err := headSeq(app, account)
	if err != nil {
		t.Fatal(err)
	}
	if head != 5 {
		t.Fatalf("head = %d, want 5", head)
	}

	// Rows removed behind the server's back, exactly as the console does it.
	if _, err := app.DB().NewQuery("DELETE FROM " + collEntries).Execute(); err != nil {
		t.Fatal(err)
	}

	head, err = headSeq(app, account)
	if err != nil {
		t.Fatal(err)
	}
	if head != 0 {
		t.Fatalf("head after the log was emptied = %d, want 0", head)
	}

	// And the counter is untouched, so new entries still get fresh numbers
	// rather than reusing ones a client may have seen.
	seq, err := appendEntry(app, account, "device-1", "after")
	if err != nil {
		t.Fatal(err)
	}
	if seq != 6 {
		t.Fatalf("seq after the log was emptied = %d, want 6", seq)
	}
}
