package main

import (
	"bytes"
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
