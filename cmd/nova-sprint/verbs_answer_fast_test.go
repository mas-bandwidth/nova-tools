package main

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
)

// slowLand is the address the test's land reads through: its first open of the store
// blocks inside the land's step, on the server's line of control, until the test frees it.
const slowLand = "slow-land"

// laneRig is a server on the test's in-memory store, with its lanes open as listen opens
// them, ready cards on m1, a friend amy, and a land that holds the line of control until
// released. after is the server's clock for a verb's bound (ServeWait): fire answers
// the next verb that waits; nil waits for as long as it takes.
type laneRig struct {
	ta      *testApp
	entered chan struct{}
	release chan struct{}
	landed  chan int
	mu      sync.Mutex
	fire    bool
	waited  []time.Duration
}

func newLaneRig(t *testing.T) *laneRig {
	t.Helper()
	ta, _ := friendApp(t, "amy")
	ta.ok("friend sync --root " + t.TempDir())
	ta.ok("add --stream s1 --count 2")
	ta.ok("fleet up m1")
	ta.ok("start")
	ta.ok("tick")
	r := &laneRig{ta: ta, entered: make(chan struct{}), release: make(chan struct{}), landed: make(chan int, 1)}
	up := ta.a.backend
	var once sync.Once
	ta.a.backend = func(ctx context.Context, addr string, names sprint.Names) (store.Backend, error) {
		if addr == slowLand {
			once.Do(func() {
				close(r.entered)
				<-r.release
			})
		}
		return up(ctx, addr, names)
	}
	ta.a.after = func(d time.Duration) <-chan time.Time {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.waited = append(r.waited, d)
		if !r.fire {
			return nil
		}
		fired := make(chan time.Time, 1)
		fired <- t0
		return fired
	}
	ta.a.serveAddr = "mem:0"
	ta.a.openLanes()
	require.NotNil(t, ta.a.beats, "a server on a store that is not a twin file opens its lanes")
	return r
}

// land starts a landing whose first step, the read of the merge queue, holds the line of
// control until the test releases it, and returns once that step holds it.
func (r *laneRig) land(t *testing.T) {
	t.Helper()
	go func() { r.landed <- r.ta.a.landRound(context.Background(), slowLand, nil, &lockedBuffer{}) }()
	<-r.entered
	require.False(t, r.ta.a.serial.TryLock(), "the land's step holds the server's line of control")
}

func (r *laneRig) firing(on bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fire = on
}

func (r *laneRig) send(ctx context.Context, local bool, verbs ...[]string) []sprintwire.Result {
	return r.ta.a.serveCtx(ctx, sprintwire.Request{Verbs: verbs}, local).Results
}

// lockedBuffer is a writer the land's goroutine writes and nothing reads.
type lockedBuffer struct {
	mu sync.Mutex
	b  []byte
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.b = append(l.b, p...)
	return len(p), nil
}

// A verb is answered within a second whatever the lander is doing (docs/SPEC-SPRINT.md
// section 14, The server, "A verb is answered within a second"; tla/ServerLine.tla).
// While a landing's step holds the server's line of control, the beats and the reads are
// answered on their lanes as when it is free, waiting for nothing; a worker's write waits
// for the line at most ServeWait and is answered busy, with the rest of its batch, having
// changed nothing; once the landing frees the line the same write is answered whole.
func TestVerbAnsweredWhileLandInProgress(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	r.land(t)

	res := r.send(context.Background(), false,
		[]string{"fleet", "beat", "m1", "--load", "12"},
		[]string{"friend", "beat", "amy"},
		[]string{"queue", "--as", "m1", "--json"},
	)
	require.Len(t, res, 3)
	for i, x := range res {
		require.Equal(t, 0, x.Code, "verb %d is answered while the land holds the line: %s%s", i, x.Stdout, x.Stderr)
	}
	assert.Contains(t, res[0].Stdout, "m1")
	assert.Contains(t, res[1].Stdout, "FRIEND-BEAT OK amy")
	var q struct {
		Cards []struct{ ID, Col string }
	}
	require.NoError(t, json.Unmarshal([]byte(res[2].Stdout), &q), res[2].Stdout)
	require.NotEmpty(t, q.Cards, "m1's queue is read while the land holds the line")
	for _, c := range q.Cards {
		assert.Equal(t, sprint.Ready, c.Col)
	}
	for _, read := range [][]string{{"where"}, {"card", "s1-1"}, {"log"}} {
		got := r.send(context.Background(), true, read)
		require.Equal(t, 0, got[0].Code, "%v is answered while the land holds the line: %s%s", read, got[0].Stdout, got[0].Stderr)
	}
	assert.Empty(t, r.waited, "no beat or read waited for anything")

	// a write waits for the line at most ServeWait: busy, and the rest of its batch with it
	r.firing(true)
	res = r.send(context.Background(), false,
		[]string{"take", "--as", "m1", "--limit", "1", "--epoch", "0", "--json"},
		[]string{"fleet", "beat", "m1", "--load", "12"},
	)
	require.Len(t, res, 2)
	for i, x := range res {
		assert.Equal(t, 2, x.Code, "verb %d", i)
		assert.Contains(t, x.Stderr, "busy", "verb %d", i)
		assert.Contains(t, x.Stderr, "nothing was run or changed; send it again", "verb %d", i)
	}
	assert.Contains(t, res[0].Stderr, "busy: the server's line of control")
	assert.Contains(t, res[0].Stderr, "was held past 1s")
	assert.Equal(t, []time.Duration{ServeWait}, r.waited, "the write waited ServeWait, on the server's clock")

	// the landing frees the line: the same write is answered whole
	r.firing(false)
	close(r.release)
	require.Equal(t, 0, <-r.landed)
	res = r.send(context.Background(), false, []string{"take", "--as", "m1", "--limit", "1", "--epoch", "0", "--json"})
	require.Equal(t, 0, res[0].Code, res[0].Stderr)
	assert.Len(t, taken(t, res[0]), 1, "the busy take changed nothing: the card it asked for is taken now")
}

// A batch whose sender has gone runs nothing more: gone before the server reads it, or
// while a verb waits for the line. Nothing of it changes the store.
func TestABatchWhoseSenderHasGoneIsNotRun(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	take := []string{"take", "--as", "m1", "--limit", "1", "--epoch", "0", "--json"}

	gone, cancel := context.WithCancel(context.Background())
	cancel()
	for _, x := range r.send(gone, false, take, []string{"fleet", "beat", "m1", "--load", "1"}) {
		assert.Equal(t, 2, x.Code)
		assert.Contains(t, x.Stderr, "not run: its sender has gone")
	}

	// the line is held; the sender goes while the take waits for it
	r.ta.a.serial.Lock()
	waiting, leave := context.WithCancel(context.Background())
	r.ta.a.after = func(time.Duration) <-chan time.Time {
		leave() // the sender goes once the take is waiting
		return nil
	}
	res := r.send(waiting, false, take, take)
	r.ta.a.serial.Unlock()
	for _, x := range res {
		assert.Equal(t, 2, x.Code)
		assert.Contains(t, x.Stderr, "not run: its sender has gone")
	}

	// nothing was taken: every card of m1 is still ready
	r.ta.a.after = func(time.Duration) <-chan time.Time { return nil }
	q := r.send(context.Background(), false, []string{"queue", "--as", "m1", "--json"})
	require.Equal(t, 0, q[0].Code, q[0].Stderr)
	var got struct {
		Cards []struct{ ID, Col string }
	}
	require.NoError(t, json.Unmarshal([]byte(q[0].Stdout), &got))
	require.NotEmpty(t, got.Cards)
	for _, c := range got.Cards {
		assert.Equal(t, sprint.Ready, c.Col, "a batch whose sender went took nothing")
	}
}

// lockWithin takes a free lock at once on no clock; a held one it waits for at most the
// bound, and its abandoned take, when it comes, frees the lock having run nothing.
func TestLockWithinIsBoundedAndLeavesNothingHeld(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	never := func() <-chan time.Time { t.Fatal("a free lock waits on no clock"); return nil }
	require.NoError(t, lockWithin(context.Background(), &mu, never))
	fired := func() <-chan time.Time {
		c := make(chan time.Time, 1)
		c <- t0
		return c
	}
	assert.ErrorIs(t, lockWithin(context.Background(), &mu, fired), errBusy, "held past the bound: busy")
	mu.Unlock()
	// the abandoned take comes, frees the lock, and the next take finds it
	mu.Lock()
	mu.Unlock()
	require.NoError(t, lockWithin(context.Background(), &mu, func() <-chan time.Time { return nil }))
	mu.Unlock()
}
