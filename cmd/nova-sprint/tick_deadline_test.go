package main

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// A tick whose plan does not end is given up at the deadline, on the loop's
// clock: the line says so and the stacks name where the tick is, the tick's
// context is cancelled, and the caller is handed the channel the tick's end
// comes on (nova-tools#5122: the level that did not end). The clock is the
// test's.
func TestTickPastItsDeadlineIsGivenUp(t *testing.T) {
	t.Parallel()
	a := newApp(func(string) string { return "" })
	a.now = func() time.Time { return time.Date(2026, 10, 2, 14, 26, 11, 0, time.Local) }
	var waited time.Duration
	a.after = func(d time.Duration) <-chan time.Time {
		waited = d
		fired := make(chan time.Time, 1)
		fired <- a.now()
		return fired
	}
	stuck := make(chan struct{})
	defer close(stuck)
	var out, errb bytes.Buffer
	began := time.Date(2026, 10, 2, 14, 26, 1, 0, time.Local)
	cancelled := make(chan struct{})
	_, err, over, ended := a.tickWithin(context.Background(), func(ctx context.Context) (store.TickResult, error) {
		<-ctx.Done() // a plan that does not end until it is given up
		close(cancelled)
		<-stuck
		return store.TickResult{}, nil
	}, TickDeadline, began, &out, &errb)
	assert.True(t, over)
	assert.NoError(t, err)
	assert.Equal(t, 10*time.Second, waited)
	assert.Equal(t, "14:26:11 TICK DEADLINE the tick begun at 14:26:01 did not end within 10s: its plan is given up and the loop goes on to the next tick once it has stopped; the stacks follow on stderr\n", out.String())
	assert.Contains(t, errb.String(), "TestTickPastItsDeadlineIsGivenUp", "the stacks name where the tick is")
	<-cancelled // the tick's context is cancelled
	select {
	case <-ended:
		t.Fatal("ended is closed before the tick's goroutine returned")
	default:
	}
}

// A tick that ends is returned as it ended, and a deadline of 0 waits for ever
// without a clock.
func TestTickWithinItsDeadlineIsReturned(t *testing.T) {
	t.Parallel()
	for _, d := range []time.Duration{TickDeadline, 0} {
		a := newApp(func(string) string { return "" })
		a.after = func(time.Duration) <-chan time.Time {
			assert.NotZero(t, d, "a deadline of 0 reads no clock")
			return make(chan time.Time) // never fires: the tick ends first
		}
		var out, errb bytes.Buffer
		res, err, over, ended := a.tickWithin(context.Background(), func(context.Context) (store.TickResult, error) {
			return store.TickResult{State: store.Running}, nil
		}, d, time.Time{}, &out, &errb)
		assert.False(t, over)
		assert.NoError(t, err)
		assert.Equal(t, store.Running, res.State)
		assert.Empty(t, out.String()+errb.String())
		<-ended
	}
}

// deadlineLoop is a test's run loop with its deadline on: a RUNNING sprint on the
// in-memory store, the loop's store, and the exits the loop asked for.
type deadlineLoop struct {
	ta    *testApp
	st    *store.Store
	mu    sync.Mutex
	exits []int
	calls int
}

func newDeadlineLoop(t *testing.T) *deadlineLoop {
	t.Helper()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 3")
	ta.ok("start")
	st, _, code := ta.a.machineVerb("run", nil, &bytes.Buffer{})
	require.NotNil(t, st, "run: %d", code)
	l := &deadlineLoop{ta: ta, st: st}
	ta.a.tickDeadline = TickDeadline
	ta.a.exit = func(code int) { l.mu.Lock(); l.exits = append(l.exits, code); l.mu.Unlock() }
	return l
}

// tick counts the loop's ticks and returns this one's number.
func (l *deadlineLoop) tick() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	return l.calls
}

// overrun is a tick past its deadline that stops when it is given up: it fires
// the loop's clock and waits for its context.
func overrun(fire chan time.Time, ctx context.Context) (store.TickResult, error) {
	fire <- time.Time{}
	<-ctx.Done()
	return store.TickResult{}, ctx.Err()
}

// A tick past its deadline gives up its plan, never the process (the live
// server, 2026-10-06: "TICK DEADLINE ... run exits 4", 21 exits that day, each
// leaving every worker's verb refused while launchd started it again): the loop
// says TICK DEADLINE with the stacks, waits for the tick given up to stop, counts
// tick_overrun on the heartbeat, gives the line back and begins the next tick,
// which deals. No exit is asked for.
func TestATickPastItsDeadlineIsAbandonedNotTheProcess(t *testing.T) {
	t.Parallel()
	l := newDeadlineLoop(t)
	fire := make(chan time.Time, 1) // the deadline's clock: fired only by the tick that overruns
	var asked []time.Duration
	l.ta.a.after = func(d time.Duration) <-chan time.Time { asked = append(asked, d); return fire }
	l.ta.a.tickFn = func(ctx context.Context, st *store.Store) (store.TickResult, error) {
		if l.tick() == 1 {
			return overrun(fire, ctx)
		}
		return st.Tick(ctx)
	}
	var out, errb bytes.Buffer
	assert.False(t, l.ta.a.runLoop(context.Background(), l.st, 20, 3, &out, &errb))
	assert.Empty(t, l.exits, "a tick past its deadline never ends the process:\n%s", out.String())
	assert.Equal(t, 3, l.calls, "the loop went on to the next ticks")
	assert.Contains(t, out.String(), " TICK DEADLINE the tick begun at ")
	assert.Contains(t, out.String(), " TICK OVERRUN the tick begun at ")
	assert.Contains(t, out.String(), "tick_overrun=1, 1 in a row; the loop goes on\n")
	assert.Contains(t, out.String(), "MOVED deal: s1-1 work ready -> working", "the tick after deals")
	assert.Contains(t, errb.String(), "goroutine ", "the stacks are written")
	assert.Equal(t, TickDeadline, asked[0])
	_, hb, err := l.st.Machine(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(1), hb.TickOverrun, "the heartbeat counts the overrun, and the ticks after keep it")
	assert.Zero(t, hb.Failures)
	require.True(t, l.ta.a.serial.TryLock(), "the line is given back")
	l.ta.a.serial.Unlock()
}

// Three ticks in a row past their deadline are a wedged process, and only then
// does run exit 4: three ticks each given up, or one tick given up that never
// stops (each further deadline it has not stopped in counts one more). A tick
// that ends in time between two overruns starts the count again.
func TestThreeOverrunsInARowExitFour(t *testing.T) {
	t.Parallel()
	t.Run("three ticks given up", func(t *testing.T) {
		t.Parallel()
		l := newDeadlineLoop(t)
		fire := make(chan time.Time, 1)
		l.ta.a.after = func(time.Duration) <-chan time.Time { return fire }
		l.ta.a.tickFn = func(ctx context.Context, _ *store.Store) (store.TickResult, error) {
			l.tick()
			return overrun(fire, ctx)
		}
		var out, errb bytes.Buffer
		assert.False(t, l.ta.a.runLoop(context.Background(), l.st, 20, 10, &out, &errb))
		assert.Equal(t, []int{exitTickDeadline}, l.exits)
		assert.Equal(t, 3, l.calls, "no tick after the third overrun")
		assert.Contains(t, out.String(), "tick_overrun=1, 1 in a row")
		assert.Contains(t, out.String(), "tick_overrun=2, 2 in a row")
		// a tick given up counts its deadline as its wall: 10 s, then 3 x 10 s, then the cap
		assert.Contains(t, out.String(), "did not end within 10s")
		assert.Contains(t, out.String(), "did not end within 30s")
		assert.Contains(t, out.String(), " TICK WEDGED 3 ticks in a row ran past their deadline (1m0s)")
		assert.Contains(t, out.String(), "the process is wedged and run exits 4 so its supervisor starts it again")
	})
	t.Run("one tick that never stops", func(t *testing.T) {
		t.Parallel()
		l := newDeadlineLoop(t)
		l.ta.a.after = func(time.Duration) <-chan time.Time {
			fired := make(chan time.Time, 1)
			fired <- time.Time{}
			return fired
		}
		never := make(chan struct{})
		defer close(never)
		l.ta.a.tickFn = func(context.Context, *store.Store) (store.TickResult, error) {
			l.tick()
			<-never // deaf to its context: a plan that never ends
			return store.TickResult{}, nil
		}
		var out, errb bytes.Buffer
		assert.False(t, l.ta.a.runLoop(context.Background(), l.st, 20, 10, &out, &errb))
		assert.Equal(t, []int{exitTickDeadline}, l.exits)
		assert.Equal(t, 1, l.calls, "no tick begins beside the one given up")
		assert.Equal(t, 2, strings.Count(out.String(), "given up, has not stopped within a further 10s"), out.String())
		assert.Contains(t, out.String(), " TICK WEDGED 3 ticks in a row ran past their deadline")
		assert.NotContains(t, out.String(), "TICK OVERRUN")
	})
	t.Run("an in-time tick between overruns", func(t *testing.T) {
		t.Parallel()
		l := newDeadlineLoop(t)
		fire := make(chan time.Time, 1)
		l.ta.a.after = func(time.Duration) <-chan time.Time { return fire }
		l.ta.a.tickFn = func(ctx context.Context, st *store.Store) (store.TickResult, error) {
			if l.tick()%2 == 1 {
				return overrun(fire, ctx)
			}
			return st.Tick(ctx)
		}
		var out, errb bytes.Buffer
		assert.False(t, l.ta.a.runLoop(context.Background(), l.st, 20, 7, &out, &errb))
		assert.Empty(t, l.exits, "four overruns, never two in a row:\n%s", out.String())
		assert.Equal(t, 7, l.calls)
		assert.Equal(t, 4, strings.Count(out.String(), ", 1 in a row; the loop goes on"), out.String())
	})
}

// The deadline is not a fixed 10 s: it is three times the median wall of the
// last 20 ticks, never under --tick-deadline and never over a minute (unless
// --tick-deadline asks for more), so a slow store stretches it instead of
// killing the server; the TIMES line says the deadline the tick had.
func TestTheTickDeadlineStretchesWithTheMedianWall(t *testing.T) {
	t.Parallel()
	walls := func(d time.Duration, n int) []time.Duration {
		var w []time.Duration
		for range n {
			w = keepWall(w, d)
		}
		return w
	}
	assert.Equal(t, 10*time.Second, tickDeadlineOf(TickDeadline, nil), "no wall yet: the least")
	assert.Equal(t, 10*time.Second, tickDeadlineOf(TickDeadline, walls(time.Second, 20)), "fast ticks: the least")
	assert.Equal(t, 24*time.Second, tickDeadlineOf(TickDeadline, walls(8*time.Second, 20)), "the live store's 8 s ticks")
	assert.Equal(t, TickDeadlineCap, tickDeadlineOf(TickDeadline, walls(30*time.Second, 20)), "capped at a minute")
	assert.Equal(t, 90*time.Second, tickDeadlineOf(90*time.Second, walls(40*time.Second, 20)), "a least over the cap is kept")
	assert.Zero(t, tickDeadlineOf(0, walls(8*time.Second, 20)), "0 waits for ever")
	assert.Len(t, walls(time.Second, 25), tickWalls, "the last 20 walls are kept")
	// the median, not the mean: one slow tick among fast ones stretches nothing
	w := append(walls(2*time.Second, 19), 50*time.Second)
	assert.Equal(t, 10*time.Second, tickDeadlineOf(TickDeadline, w))
	w = append(walls(time.Second, 10), walls(8*time.Second, 10)...)
	assert.Equal(t, 24*time.Second, tickDeadlineOf(TickDeadline, w), "the upper of the two middle walls")

	// the loop: each tick takes 8 s on the loop's clock
	l := newDeadlineLoop(t)
	var asked []time.Duration
	l.ta.a.after = func(d time.Duration) <-chan time.Time { asked = append(asked, d); return make(chan time.Time) }
	l.ta.a.tickFn = func(ctx context.Context, st *store.Store) (store.TickResult, error) {
		res, err := st.Tick(ctx)
		l.ta.mu.Lock()
		l.ta.now = l.ta.now.Add(8 * time.Second)
		l.ta.mu.Unlock()
		return res, err
	}
	var out, errb bytes.Buffer
	l.ta.a.runLoop(context.Background(), l.st, 20, 3, &out, &errb)
	assert.Equal(t, []time.Duration{10 * time.Second, 24 * time.Second, 24 * time.Second}, asked)
	assert.Empty(t, l.exits)
	var deadlines []string
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "TIMES ") {
			deadlines = append(deadlines, line[strings.LastIndex(line, " ")+1:])
		}
	}
	assert.Equal(t, []string{"deadline=10s", "deadline=24s", "deadline=24s"}, deadlines, out.String())
}
