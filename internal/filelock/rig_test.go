package filelock

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/require"
)

// lockStepClock is testkit.Clock behind filelock's clock interface. The
// interface needs Sleep; Sleep advances the clock and does not block.
type lockStepClock struct {
	c     *testkit.Clock
	start time.Time
}

// newLockStepClock returns a clock for tests, set to start, or testkit's fixed
// test time when start is zero.
func newLockStepClock(start time.Time) *lockStepClock {
	c := testkit.NewClock(start)
	return &lockStepClock{c: c, start: c.Now()}
}

func (c *lockStepClock) Now() time.Time        { return c.c.Now() }
func (c *lockStepClock) Sleep(d time.Duration) { c.c.Advance(d) }
func (c *lockStepClock) Waited() time.Duration { return c.c.Now().Sub(c.start) }

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
