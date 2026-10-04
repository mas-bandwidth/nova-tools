package filelock

import (
	"sync"
	"time"
)

// The test's clock: production takes its time through options.clock (realClock by
// default), so a test passes this one and a bounded wait costs no wall time.

// lockStepClock is an in-memory virtual clock for testing bounded waits without sleeping.
type lockStepClock struct {
	mu    sync.Mutex
	start time.Time
	now   time.Time
}

// newLockStepClock returns a lockStepClock initialized to start (or a default fixed time if zero).
func newLockStepClock(start time.Time) *lockStepClock {
	if start.IsZero() {
		start = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	}
	return &lockStepClock{start: start, now: start}
}

func (c *lockStepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *lockStepClock) Sleep(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Waited returns the elapsed virtual duration since clock creation.
func (c *lockStepClock) Waited() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now.Sub(c.start)
}
