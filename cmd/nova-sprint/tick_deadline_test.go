package main

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// A tick whose plan does not end is given up at the deadline, on the loop's
// clock: the line says so and the stacks name where the tick is, and the
// caller is told to end the process instead of holding serial for ever
// (nova-tools#5122: the level that did not end). The clock is the test's.
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
	_, err, over := a.tickWithin(func() (store.TickResult, error) {
		<-stuck // a plan that does not end
		return store.TickResult{}, nil
	}, TickDeadline, began, &out, &errb)
	assert.True(t, over)
	assert.NoError(t, err)
	assert.Equal(t, 10*time.Second, waited)
	assert.Equal(t, "14:26:11 TICK DEADLINE the tick begun at 14:26:01 did not end within 10s: its plan is given up and run exits 4 so its supervisor starts it again; the stacks follow on stderr\n", out.String())
	assert.Contains(t, errb.String(), "TestTickPastItsDeadlineIsGivenUp", "the stacks name where the tick is")
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
		res, err, over := a.tickWithin(func() (store.TickResult, error) {
			return store.TickResult{State: store.Running}, nil
		}, d, time.Time{}, &out, &errb)
		assert.False(t, over)
		assert.NoError(t, err)
		assert.Equal(t, store.Running, res.State)
		assert.Empty(t, out.String()+errb.String())
	}
}

// The loop's first tick reads the sprint whole into a cold twin and is bounded by
// FirstTickDeadline, not --tick-deadline; every tick after it by --tick-deadline
// (2026-10-04 4:28-4:31 PM ET: each restart's cold first read ran past 10 s, a crash loop).
// A bound of 0 stays 0. The clock is the test's: each tick's bound is what it waited on.
func TestTheFirstTickHasItsOwnLongerBound(t *testing.T) {
	t.Parallel()
	assert.Equal(t, FirstTickDeadline, tickBound(0, TickDeadline))
	assert.Equal(t, TickDeadline, tickBound(1, TickDeadline))
	assert.Equal(t, time.Duration(0), tickBound(0, 0))
	assert.Equal(t, 5*time.Minute, tickBound(0, 5*time.Minute), "a deadline longer than the first tick's is kept")

	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	ta.a.tickDeadline = TickDeadline
	var bounds []time.Duration
	ta.a.after = func(d time.Duration) <-chan time.Time {
		bounds = append(bounds, d)
		return make(chan time.Time) // never fires: each tick ends first
	}
	st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
	assert.NoError(t, err)
	var out, errb bytes.Buffer
	ta.a.runLoop(context.Background(), st, 0, 3, &out, &errb)
	assert.Equal(t, []time.Duration{FirstTickDeadline, TickDeadline, TickDeadline}, bounds, out.String()+errb.String())
}
