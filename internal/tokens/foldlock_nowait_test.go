package tokens

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFoldLockZeroWaitRefusesAHeldLockNamingItsHolder verifies that when a fold lock is
// already held, a second take with wait=0 refuses immediately without any real-time
// wait. The error message names both the lock file and the holder's pid.
func TestFoldLockZeroWaitRefusesAHeldLockNamingItsHolder(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	release, err := TakeFoldLock(dir, LockWait)
	require.NoError(t, err)
	defer release()

	// A second take with zero wait should refuse immediately.
	_, err = TakeFoldLock(dir, 0)
	require.Error(t, err, "a second fold took the lock")

	// The refusal must name the lock file.
	assert.True(t, strings.Contains(err.Error(), LockName), "the refusal does not name the lock: %v", err)

	// The refusal must name the holder's pid.
	pid := HolderPID(filepath.Join(dir, LockName))
	assert.NotEqual(t, Dash, pid, "the lock file holds no pid")
	assert.True(t, strings.Contains(err.Error(), pid), "the refusal does not name the holder's pid: %v", err)
}

// TestFoldLockZeroWaitTakesAFreeLock verifies that a fold lock can be taken when
// the directory is free, and that the release is safe to call multiple times.
func TestFoldLockZeroWaitTakesAFreeLock(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	release, err := TakeFoldLock(dir, 0)
	require.NoError(t, err)
	defer release()

	// Release should be safe to call more than once.
	release()
	release()

	// The directory should be available again.
	again, err := TakeFoldLock(dir, 0)
	require.NoError(t, err, "the lock was not released: %v", err)
	again()
}
