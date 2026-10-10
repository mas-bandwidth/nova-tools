package testkit

import (
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClockAdvancesSerializeUnderTheLock pins that Advance holds the clock's
// lock across its read-modify-write, so a concurrent Advance cannot interleave
// and lose one (docs/STANDARD.md section 8). The test holds the lock and hands
// the one processor to a goroutine whose Advance must then block on it; an
// Advance that skips the lock runs to completion at once and is caught.
func TestClockAdvancesSerializeUnderTheLock(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	c := NewClock(start)

	// One processor: once the goroutine below announces itself it runs to its
	// next block, the test-held lock, before the test resumes. The interleaving
	// is therefore the test's, not the scheduler's.
	prev := runtime.GOMAXPROCS(1)
	t.Cleanup(func() { runtime.GOMAXPROCS(prev) })

	c.mu.Lock()
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		close(started)
		c.Advance(time.Second)
		close(done)
	}()
	<-started
	runtime.Gosched()
	select {
	case <-done:
		c.mu.Unlock()
		require.Fail(t, "Advance did not take the lock: it moved the clock while the test held it")
	default:
	}
	c.mu.Unlock()
	<-done
	assert.Equal(t, start.Add(time.Second), c.Now(), "the blocked Advance did not run after the lock was released")
}
