package swarm

import (
	"time"
)

// stepClock is the pull's clock in the tests: Now stands still until Sleep moves it, so a
// bounded wait reaches its deadline in as many polls as it would in real time and not one
// wall-clock millisecond. The production clock is realClock.
type stepClock struct{ now time.Time }

func (c *stepClock) Now() time.Time { return c.now }

func (c *stepClock) Sleep(d time.Duration) { c.now = c.now.Add(d) }
