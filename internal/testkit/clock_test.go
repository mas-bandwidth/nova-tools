package testkit_test

import (
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
)

var start = time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)

// fired reports whether ch has a value ready, without waiting.
func fired(ch <-chan time.Time) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestClockStandsStillUntilAdvancedOrSlept(t *testing.T) {
	t.Parallel()
	c := testkit.NewClock(start)
	assert.Equal(t, start, c.Now())
	assert.Equal(t, start, c.Now(), "the clock moved on its own")
	c.Advance(time.Minute)
	c.Sleep(2 * time.Second)
	assert.Equal(t, start.Add(time.Minute+2*time.Second), c.Now())
	var now func() time.Time = c.Now // the func() time.Time seam
	assert.Equal(t, c.Now(), now())
}

func TestAfterFiresOnceTheClockReachesTheDeadline(t *testing.T) {
	t.Parallel()
	c := testkit.NewClock(start)
	soon, later, now := c.After(time.Second), c.After(time.Hour), c.After(0)
	assert.True(t, fired(now), "After(0) did not fire at once")
	assert.False(t, fired(soon), "After fired before the clock moved")
	c.Advance(999 * time.Millisecond)
	assert.False(t, fired(soon), "After fired before its deadline")
	c.Sleep(time.Millisecond)
	select {
	case at := <-soon:
		assert.Equal(t, start.Add(time.Second), at)
	default:
		assert.Fail(t, "After did not fire at its deadline")
	}
	assert.False(t, fired(later), "a later After fired early")
	c.Advance(time.Hour)
	assert.True(t, fired(later))
	assert.Equal(t, []time.Duration{time.Second, time.Hour, 0}, c.Afters())
}

func TestClockIsSafeAcrossGoroutines(t *testing.T) {
	t.Parallel()
	c := testkit.NewClock(start)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			<-c.After(0)
			c.Sleep(time.Second)
			c.Now()
		})
	}
	wg.Wait()
	assert.Equal(t, start.Add(8*time.Second), c.Now())
	assert.Len(t, c.Afters(), 8)
}
