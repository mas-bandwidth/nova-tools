package friend

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// followRig drives a Follower with fakes: no process is fetched, verified or replaced.
type followRig struct {
	mu       sync.Mutex
	now      time.Time
	f        *Follower
	fetches  []string
	fetchErr error
	version  string // what the staged binary's version verb answers
	swapped  []string
	swapErr  error
	execs    int
	execErr  error
	removed  []string
	records  []string
}

func newFollowRig(t *testing.T, build string) *followRig {
	t.Helper()
	r := &followRig{now: t0, version: "v2.0.0"}
	r.f = &Follower{
		Build: build,
		Now:   func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now },
		Fetch: func(_ context.Context, release string) (string, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.fetches = append(r.fetches, release)
			if r.fetchErr != nil {
				return "", r.fetchErr
			}
			return "/bin/.nova-friend." + release + ".new", nil
		},
		Verify: func(_ context.Context, staged string) (string, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			return r.version, nil
		},
		Swap: func(staged string) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.swapped = append(r.swapped, staged)
			return r.swapErr
		},
		Exec: func() error {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.execs++
			return r.execErr
		},
		Remove: func(staged string) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.removed = append(r.removed, staged)
			return nil
		},
		Record: func(line string) { r.mu.Lock(); defer r.mu.Unlock(); r.records = append(r.records, line) },
	}
	return r
}

// step is one daemon step: the follower stepped, and the fetch it may have started run to
// its end (the bubble waits for it), so the next step reads its result.
func (r *followRig) step(idle bool) bool {
	r.mu.Lock()
	r.now = r.now.Add(BeatEvery)
	now := r.now
	r.mu.Unlock()
	hold := r.f.Step(context.Background(), now, idle)
	synctest.Wait()
	return hold
}

func (r *followRig) advance(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.now = r.now.Add(d)
}

func (r *followRig) said(word string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, l := range r.records {
		if strings.Contains(l, word) {
			n++
		}
	}
	return n
}

// A row that names the build this daemon runs, or none, is nothing to do: no hold, no fetch.
func TestAFollowOfTheBuildAlreadyRunningIsANoOp(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newFollowRig(t, "v2.0.0")
		assert.False(t, r.step(true), "no release named: no hold")
		r.f.Want("v2.0.0")
		assert.False(t, r.f.Following())
		for range 3 {
			assert.False(t, r.step(true))
		}
		assert.Empty(t, r.fetches, "the build the row names is the one running: nothing fetched")
		assert.Equal(t, 0, r.execs)
		assert.Equal(t, 1, r.said("CONFIG release=v2.0.0 (was -)"), "the row's release is said once")
	})
}

// The row names another release: the daemon holds its lanes, fetches and verifies the
// release, waits for the lanes to be idle, then swaps it in and restarts under it.
func TestAFollowFetchesVerifiesWaitsForIdleThenRestarts(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newFollowRig(t, "v1.0.0")
		r.f.Want("v2.0.0")
		assert.True(t, r.f.Following())
		assert.True(t, r.step(false), "a lane runs: held, and the fetch starts")
		assert.Equal(t, []string{"v2.0.0"}, r.fetches)
		assert.True(t, r.step(false), "fetched and verified; the lanes still run: held, not restarted")
		assert.Equal(t, 0, r.execs)
		assert.Empty(t, r.swapped, "nothing is swapped into place while a lane runs")
		assert.True(t, r.step(true), "idle: swapped and restarted")
		assert.Equal(t, []string{"/bin/.nova-friend.v2.0.0.new"}, r.swapped)
		assert.Equal(t, 1, r.execs)
		assert.Equal(t, 1, r.said("taking no new lanes; fetching it"))
		assert.Equal(t, 1, r.said("fetched and verified"))
		assert.Equal(t, 1, r.said("v2.0.0 is in place; restarting under it"))
		assert.Len(t, r.fetches, 1, "one fetch serves the follow")
	})
}

// A fetch that fails leaves the running build in place, is said once for the release, and is
// tried again after a minute, then every five minutes: never a tight loop.
func TestAFailedFollowBacksOffAndIsSaidOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newFollowRig(t, "v1.0.0")
		r.fetchErr = errors.New("GET .../SHA256SUMS: 404 Not Found")
		r.f.Want("v2.0.0")
		assert.True(t, r.step(true))
		assert.True(t, r.step(true), "the failure read; still held")
		assert.Len(t, r.fetches, 1)
		assert.Equal(t, 0, r.execs)
		assert.Equal(t, 1, r.said("v2.0.0 not followed: GET .../SHA256SUMS: 404 Not Found; the running build v1.0.0 stays in place; tried again in 1m0s, then every 5m0s"))
		for range 10 {
			r.step(true)
		}
		assert.Len(t, r.fetches, 1, "no fetch within the minute")
		r.advance(time.Minute)
		r.step(true)
		assert.Len(t, r.fetches, 2, "tried again after a minute")
		r.step(true)
		r.advance(time.Minute)
		r.step(true)
		assert.Len(t, r.fetches, 2, "the second wait is five minutes")
		r.advance(4 * time.Minute)
		r.step(true)
		assert.Len(t, r.fetches, 3)
		assert.Equal(t, 1, r.said("not followed"), "said once per release value")
		// the fetch answers: followed on the next try
		r.fetchErr = nil
		r.step(true)
		r.advance(5 * time.Minute)
		r.step(true)
		r.step(true)
		assert.Equal(t, 1, r.execs)
	})
}

// A fetched binary that does not answer with the release is never put in place: it is
// removed, the failure counted, and the running build stays.
func TestAFetchedBinaryThatIsNotTheReleaseIsNeverRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newFollowRig(t, "v1.0.0")
		r.version = "v1.9.9"
		r.f.Want("v2.0.0")
		r.step(true)
		r.step(true)
		assert.Empty(t, r.swapped)
		assert.Equal(t, 0, r.execs)
		assert.Equal(t, []string{"/bin/.nova-friend.v2.0.0.new"}, r.removed, "the staged binary that failed verification is dropped")
		assert.Equal(t, 1, r.said("answers version v1.9.9, not v2.0.0"))
	})
}

// Past the drain bound the daemon restarts over running lanes, saying so: the daemon that
// comes up finishes their cards as every started card whose run is gone.
func TestAFollowRestartsAtTheDrainBoundOverRunningLanes(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newFollowRig(t, "v1.0.0")
		r.f.Want("v2.0.0")
		r.step(false)
		r.step(false)
		assert.Equal(t, 0, r.execs)
		r.advance(FollowDrain)
		assert.True(t, r.step(false))
		assert.Equal(t, 1, r.execs)
		assert.Equal(t, 1, r.said("lanes still run 3h0m0s after the row named v2.0.0; restarting under it anyway"))
	})
}

// A restart that fails is a failure like a fetch's: said once, tried again on the backoff,
// and the row changing back drops what was staged.
func TestAFailedRestartIsTriedAgainAndARowChangeDropsTheStagedBinary(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newFollowRig(t, "v1.0.0")
		r.execErr = errors.New("exec: permission denied")
		r.f.Want("v2.0.0")
		r.step(true)
		r.step(true)
		assert.Equal(t, 1, r.execs)
		assert.Equal(t, 1, r.said("the restart under v2.0.0 failed: exec: permission denied"))
		r.advance(time.Minute)
		r.step(true)
		r.step(true)
		assert.Len(t, r.fetches, 2, "fetched again after the wait")
		r.f.Want("v1.0.0")
		assert.False(t, r.step(true), "the row names the running build again: no hold")
		assert.Equal(t, []string{"/bin/.nova-friend.v2.0.0.new"}, r.removed, "the staged binary the row no longer wants is dropped")
		assert.Equal(t, 1, r.said("CONFIG release=v1.0.0 (was v2.0.0)"))
	})
}

// An unstamped build (a hand build, no version) follows nothing, and says so once per
// release value.
func TestAnUnstampedBuildFollowsNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newFollowRig(t, "")
		r.f.Want("v2.0.0")
		assert.False(t, r.f.Following())
		for range 3 {
			assert.False(t, r.step(true))
		}
		assert.Empty(t, r.fetches)
		assert.Equal(t, 1, r.said("this build is unstamped (a hand build); it follows nothing"))
	})
}

func TestParseRelease(t *testing.T) {
	t.Parallel()
	rel, ok := ParseRelease("FRIEND-BEAT OK bob at=x row_mode=one-shot row_width=8 row_release=v1.2.0 row_tiers=pro")
	require.True(t, ok)
	assert.Equal(t, "v1.2.0", rel)
	_, ok = ParseRelease("FRIEND-BEAT OK bob at=x row_mode=one-shot row_width=8")
	assert.False(t, ok, "a row that names none")
}
