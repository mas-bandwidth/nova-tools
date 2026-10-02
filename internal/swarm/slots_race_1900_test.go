package swarm

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ISSUE #1900. The store had no lock. TakeSlotLeases reaped, counted the lease
// directories, tested the count against the share and the capacity, and THEN made a
// new directory per seat. os.Mkdir is atomic per ID -- which is what the comment at
// the top of slots.go claims -- and it is not atomic per CAPACITY: two takers who both
// read total=0 against a capacity-1 store both passed the test and both won the last
// seat. Johnny measured three of four 50-way trials over-granting a capacity-1 store,
// which is the accounting failing OPEN, the opposite of DRIFT.
//
// The bound this test crosses is the spec's own sentence: a bench's seats granted can
// never exceed its capacity. It is crossed from inside one process rather than with 50
// spawns because the window is the same one -- count, then mkdir -- and a test that
// needs a scheduler to cooperate is a test that goes green on a slow day.
func TestConcurrentTakesNeverExceedCapacity(t *testing.T) {
	t.Parallel()

	for trial := 0; trial < 8; trial++ {
		store := t.TempDir()
		writeShares(t, store, "capacity\t1\nreserve\t0\nalice\t1\n")

		const takers = 16
		var wg sync.WaitGroup
		var mu sync.Mutex
		granted := 0
		start := make(chan struct{})
		now := time.Now().UTC()
		for i := 0; i < takers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				ids, _, _, _, _, ok, err := TakeSlotLeases(store, "alice", 1, time.Hour, "u", now, os.Getpid())
				if err != nil {
					t.Errorf("take: %v", err)
					return
				}
				if ok {
					mu.Lock()
					granted += len(ids)
					mu.Unlock()
				}
			}(i)
		}
		close(start)
		wg.Wait()

		require.LessOrEqual(t, granted, 1, "trial %d: a capacity-1 store granted %d seats", trial, granted)
		entries, err := os.ReadDir(filepath.Join(store, "slots"))
		require.NoError(t, err)
		live := 0
		for _, e := range entries {
			if e.IsDir() {
				live++
			}
		}
		require.LessOrEqual(t, live, 1, "trial %d: a capacity-1 store holds %d lease directories", trial, live)
		require.Equal(t, 1, granted, "trial %d: the one seat was not granted at all (granted=%d live=%d); a lock that refuses everybody is not the fix", trial, granted, live)
		require.Equal(t, 1, live, "trial %d: the one seat was not granted at all (granted=%d live=%d); a lock that refuses everybody is not the fix", trial, granted, live)
	}
}

// The store's format is what the Studio's live store already holds: shares.tsv keeps
// every byte of its shape and the lock is a file beside it, so a store made by an
// older `slots init` still takes and still lists.
func TestTheStoreLockDoesNotChangeTheSharesFormat(t *testing.T) {
	t.Parallel()

	store := t.TempDir()
	body := "capacity\t4\nreserve\t1\nalice\t2\nbob\t2\n"
	writeShares(t, store, body)

	_, _, _, _, _, ok, err := TakeSlotLeases(store, "alice", 1, time.Hour, "card", time.Now().UTC(), os.Getpid())
	require.NoError(t, err, "a store made before the lock existed no longer takes: ok=%v err=%v", ok, err)
	require.True(t, ok, "a store made before the lock existed no longer takes: ok=%v err=%v", ok, err)
	raw, err := os.ReadFile(filepath.Join(store, "shares.tsv"))
	require.NoError(t, err)
	require.Equal(t, body, string(raw), "shares.tsv was rewritten:\n got: %q\nwant: %q", raw, body)
	_, err = os.Stat(filepath.Join(store, SlotStoreLockName))
	require.NoError(t, err, "the lock file was not made beside shares.tsv: %v", err)
}

// A take against a store that was never `slots init`ed still says shares.tsv, and does
// not make a directory on the way past.
func TestTakeAgainstAStoreThatWasNeverInitedStillSaysSharesTSV(t *testing.T) {
	t.Parallel()

	store := filepath.Join(t.TempDir(), "never-made")
	_, _, _, _, _, ok, err := TakeSlotLeases(store, "alice", 1, time.Hour, "card", time.Now().UTC(), os.Getpid())
	require.False(t, ok, "a take against a store with no shares.tsv granted: ok=%v err=%v", ok, err)
	require.Error(t, err, "a take against a store with no shares.tsv granted: ok=%v err=%v", ok, err)
	_, serr := os.Stat(store)
	require.Error(t, serr, "the refused take made the store directory at %s", store)
}

func writeShares(t *testing.T, store, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(store, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(store, "shares.tsv"), []byte(body), 0o644))
}
