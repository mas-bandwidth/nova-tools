package filelock

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The test's clock: production takes its time through options.clock (realClock
// by default), so a test passes this one and a bounded wait costs no wall time.

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

// rig is one filelock test's fixture: a temp dir whose lock paths are named
// from it, and a virtual clock a bounded wait advances without sleeping.
type rig struct {
	t     *testing.T
	dir   string
	clock *lockStepClock
}

// newRig makes a rig over a fresh temp dir and a virtual clock at its origin.
func newRig(t *testing.T) *rig {
	t.Helper()
	return &rig{t: t, dir: t.TempDir(), clock: newLockStepClock(time.Time{})}
}

// path is the lock file named base under the rig's temp dir.
func (r *rig) path(base string) string { return filepath.Join(r.dir, base) }

// opts is lock options on the rig's virtual clock, carrying the given seams.
func (r *rig) opts(base options) options { base.clock = r.clock; return base }

// holder takes the lock on path under label and releases it when the test ends.
func (r *rig) holder(path, label string) *FileLock {
	r.t.Helper()
	lock, err := TryLock(path, label)
	require.NoError(r.t, err, "take %s as %q", path, label)
	r.t.Cleanup(func() { _ = lock.Unlock() })
	return lock
}

// try is TryLock on path under label, the caller owning the lock on success.
func (r *rig) try(path, label string) (*FileLock, error) {
	r.t.Helper()
	return TryLock(path, label)
}

// timed is lockWithOptions on path with the bound and the rig's clock,
// releasing the lock a mutant left behind.
func (r *rig) timed(path, label string, bound time.Duration, base options) (*FileLock, error) {
	r.t.Helper()
	lock, err := lockWithOptions(path, label, bound, r.opts(base))
	_ = lock.Unlock() // nil-safe: releases a mutant lock, no-ops on nil
	return lock, err
}

// waited is the virtual duration the rig's clock has advanced.
func (r *rig) waited() time.Duration { return r.clock.Waited() }
