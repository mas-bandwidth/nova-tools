package testkit

import (
	"sync"
	"time"
)

// Clock is a fake clock over a time.Time value, for tests of code that takes
// its time as a `now func() time.Time` (docs/STANDARD.md, section 8: the
// environment is injected through the code's config, never set on the test).
// Pass c.Now where the code asks for now; Advance moves the value between a
// call and the next. It is safe for concurrent use.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

// NewClock returns a Clock whose Now is start.
func NewClock(start time.Time) *Clock { return &Clock{now: start} }

// Now is the value to hand the code as its now func() time.Time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward by d: what Now returns next is what it
// returned last, plus d.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
