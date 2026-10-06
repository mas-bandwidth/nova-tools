package filelock

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/require"
)

// clockAdapter wraps testkit.Clock to provide the clock interface expected by filelock tests.
type clockAdapter struct {
	c     *testkit.Clock
	start time.Time
}

func newClockAdapter() *clockAdapter {
	start := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	return &clockAdapter{c: testkit.NewClock(start), start: start}
}

// newClockForTest creates a clock for filelock_test.go tests that need a standalone clock.
func newClockForTest(start time.Time) *clockAdapter {
	if start.IsZero() {
		start = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	}
	return &clockAdapter{c: testkit.NewClock(start), start: start}
}

func (a *clockAdapter) Now() time.Time       { return a.c.Now() }
func (a *clockAdapter) Sleep(d time.Duration) { a.c.Advance(d) }
func (a *clockAdapter) Waited() time.Duration { return a.c.Now().Sub(a.start) }

// rig is one filelock test's fixture: a temp dir whose lock paths are named
// from it, and a virtual clock a bounded wait advances without sleeping.
type rig struct {
	t     *testing.T
	dir   string
	clock *clockAdapter
}

// newRig makes a rig over a fresh temp dir and a virtual clock at its origin.
func newRig(t *testing.T) *rig {
	t.Helper()
	return &rig{t: t, dir: t.TempDir(), clock: newClockAdapter()}
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
