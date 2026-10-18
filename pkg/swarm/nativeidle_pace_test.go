package swarm

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countingSnap is a fakeSnap that counts the snapshots the watch takes.
type countingSnap struct {
	mu    sync.Mutex
	cpu   uint64
	taken int
}

func (c *countingSnap) TreeCPU(int) (uint64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cpu, true
}

// TestTheIdleWatchPollsEachSecondAndSnapshotsEveryFifteen (2026-10-17): the watch stat'd the
// card's log every 100 ms (3,000 stats a 300 s window per running card), read the whole
// process table every 5 s, and past the window listed the job directory every 100 ms. Now
// the poll is a second and, in the default window, the table is read every 15 s; a tree
// pinning a core still clears the busy share over that span, and one spending only its
// event loop is still ended at the window. The clock starts at 1000 s and the test drives
// every tick.
func TestTheIdleWatchPollsEachSecondAndSnapshotsEveryFifteen(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 15*time.Second, nativeSnapshotEvery(DefaultNativeIdle))
	assert.Equal(t, 2500*time.Millisecond, nativeSnapshotEvery(10*time.Second), "a short window keeps batch's poll")
	assert.Equal(t, 500*time.Millisecond, nativeSnapshotEvery(time.Second))

	run := func(t *testing.T, perSecond func(s int) uint64, seconds int) (poll time.Duration, snaps int, ended bool) {
		t.Helper()
		job := t.TempDir()
		log := filepath.Join(job, "harness-output.log")
		require.NoError(t, os.WriteFile(log, []byte("start\n"), 0o644))
		start := time.Unix(1000, 0)
		ticks := make(chan time.Time)
		snap := &countingSnap{}
		stop := make(chan struct{})
		defer close(stop)
		var mu sync.Mutex
		w := IdleWatch{Log: log, Job: job, Pid: 4242, Idle: DefaultNativeIdle, Reader: NewWallReader("c", nil)}
		w.now = func() time.Time { return start }
		w.ticker = func(d time.Duration) (<-chan time.Time, func()) {
			mu.Lock()
			poll = d
			mu.Unlock()
			return ticks, func() {}
		}
		w.snapshot = func() activitySnapshot {
			snap.mu.Lock()
			snap.taken++
			snap.mu.Unlock()
			return snap
		}
		out := WatchIdle(w, stop)
		for s := 1; s <= seconds; s++ {
			snap.mu.Lock()
			snap.cpu += perSecond(s)
			snap.mu.Unlock()
			select {
			case ticks <- start.Add(time.Duration(s) * time.Second):
			case <-out:
				ended = true
			}
			if ended {
				break
			}
		}
		if !ended {
			// one more tick at the last instant: the watch has finished with the one before
			select {
			case ticks <- start.Add(time.Duration(seconds) * time.Second):
			case <-out:
				ended = true
			}
		}
		mu.Lock()
		defer mu.Unlock()
		snap.mu.Lock()
		defer snap.mu.Unlock()
		return poll, snap.taken, ended
	}

	poll, snaps, ended := run(t, func(int) uint64 { return uint64(time.Second) }, 400)
	assert.Equal(t, time.Second, poll, "the log is read once a second")
	assert.False(t, ended, "a tree pinning a core is working over a 15 s span")
	assert.LessOrEqual(t, snaps, 400/15+2, "one snapshot every 15 s, not every 5 s")
	assert.GreaterOrEqual(t, snaps, 400/15-1)

	eventLoop := func(int) uint64 { return uint64(time.Second) * 103 / 10000 }
	_, _, ended = run(t, eventLoop, 320)
	assert.True(t, ended, "a harness spending only its event loop (1.03%% of a core) is ended at the window")

	// THE WINDOW'S END (the cold read of PR 5953): still for 290 s, then a core pinned from
	// 291 s. The last periodic reading is at 286 s, so without one more before the end the
	// card would be ended at 300 s while it works; the fresh reading sees the work.
	late := func(s int) uint64 {
		if s > 290 {
			return uint64(time.Second)
		}
		return eventLoop(s)
	}
	_, _, ended = run(t, late, 400)
	assert.False(t, ended, "a tree that began working inside the last snapshot span is not idle")

	// BURSTY WORK BEHIND THE SHELL CAP (the second cold read of PR 5953): one tool call
	// running `gh pr checks` every 10 s at ~0.6 s of CPU each, nothing printed. Every 15 s
	// span holds 0.6 to 1.2 s, never the 1.5 s a tenth of the span would ask; the bar is a
	// tenth of batch's 5 s poll, and the card is kept.
	bursty := func(s int) uint64 {
		if s%10 == 0 {
			return uint64(600 * time.Millisecond)
		}
		return eventLoop(s)
	}
	_, _, ended = run(t, bursty, 400)
	assert.False(t, ended, "a tree spending 0.6 s every 10 s is working")
}
