package testkit

import (
	"sync"
	"time"
)

// Clock is a fake for the clock seams the code takes (an interface of Now,
// Sleep and After, or a func() time.Time, which is c.Now as a method value).
// It stands still until it is moved: Advance moves it, and so does Sleep, which
// never blocks, so a wait the code makes is a step of the clock and never a
// wait on the machine. After fires once the clock reaches the deadline. It is
// safe for concurrent use.
type Clock struct {
	mu     sync.Mutex
	now    time.Time
	timers []timer
	afters []time.Duration
}

// timer is one After not yet fired.
type timer struct {
	at time.Time
	ch chan time.Time
}

// NewClock is a clock standing at start.
func NewClock(start time.Time) *Clock { return &Clock{now: start} }

// Now is the clock's time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock on by d and fires every After that falls due.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	pending := c.timers[:0]
	for _, tm := range c.timers {
		if tm.at.After(c.now) {
			pending = append(pending, tm)
			continue
		}
		tm.ch <- c.now
	}
	c.timers = pending
}

// Sleep is Advance: the code's wait moves the clock and returns at once.
func (c *Clock) Sleep(d time.Duration) { c.Advance(d) }

// After returns a channel that receives the clock's time once the clock has
// moved d on from now; at once when d is not positive. Every d asked for is
// kept, in order, for Afters.
func (c *Clock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.afters = append(c.afters, d)
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- c.now
		return ch
	}
	c.timers = append(c.timers, timer{at: c.now.Add(d), ch: ch})
	return ch
}

// Afters is every duration After was asked for, in the order asked: the
// deadlines the code set, which a test asserts without waiting for any.
func (c *Clock) Afters() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.afters...)
}
