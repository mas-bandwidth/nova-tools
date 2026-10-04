package testkit_test

import (
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
)

func TestClockNowStartsWhereNewClockPutIt(t *testing.T) {
	t.Parallel()
	start := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	c := testkit.NewClock(start)
	assert.Equal(t, start, c.Now(), "Now is the start value until Advance moves it")
}

func TestClockAdvanceMovesNowForwardAndAccumulates(t *testing.T) {
	t.Parallel()
	start := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	c := testkit.NewClock(start)
	c.Advance(2 * time.Hour)
	c.Advance(30 * time.Second)
	assert.Equal(t, start.Add(2*time.Hour+30*time.Second), c.Now(), "Advance adds to what Now returned before it")
}

func TestClockIsSafeForConcurrentAdvanceAndNow(t *testing.T) {
	t.Parallel()
	start := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	c := testkit.NewClock(start)
	const goroutines, each = 8, 500
	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range each {
				c.Advance(time.Millisecond)
				c.Now()
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, start.Add(goroutines*each*time.Millisecond), c.Now(), "no Advance may be lost")
}
