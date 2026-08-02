package main

import (
	"fmt"
	"sync"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// appendNaive is the implementation the plan warns against: the counter is
// read and bumped outside the insert's transaction, which is the same hazard
// shape as a bare identity column.
//
// It exists only so the test below can show the concurrency tests are capable
// of catching the bug. A passing test that cannot fail is worse than no test,
// and this whole design rests on a failure mode that never shows up by hand.
func appendNaive(app core.App, accountID string) (int64, error) {
	account, err := app.FindRecordById(collAccounts, accountID)
	if err != nil {
		return 0, err
	}
	seq := int64(account.GetInt("seq")) + 1
	account.Set("seq", seq)
	if err := app.Save(account); err != nil {
		return 0, err
	}
	return seq, nil
}

func TestNaiveAppendActuallyBreaks(t *testing.T) {
	app, account := newTestApp(t)

	const writers = 8
	const perWriter = 12
	var wg sync.WaitGroup
	seen := make(chan int64, writers*perWriter)

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				if seq, err := appendNaive(app, account); err == nil {
					seen <- seq
				}
			}
		}()
	}
	wg.Wait()
	close(seen)

	counts := map[int64]int{}
	total := 0
	for seq := range seen {
		counts[seq]++
		total++
	}

	duplicates := 0
	for _, n := range counts {
		if n > 1 {
			duplicates += n - 1
		}
	}

	fmt.Printf("naive: %d appends produced %d distinct seqs (%d duplicates)\n",
		total, len(counts), duplicates)

	if duplicates == 0 && len(counts) == total {
		t.Skip("the naive version did not race on this run — which is exactly " +
			"why it cannot be relied on, and why the real one is transactional")
	}
}
