//go:build unix

package bus

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The compatibility witnesses for the checkout lock across an upgrade. Before
// internal/filelock, nova-bus took a bare flock(LOCK_EX|LOCK_NB) on the lock file it
// opened O_RDWR|O_CREATE 0644, and stamped "pid=<n> at=<stamp>" into it. An old binary
// is staged here as exactly that, on a second descriptor of the same file.

// An old nova-bus holding the lock keeps the new take out, and the new take succeeds
// once the old one lets go.
func TestAnOldBusLockKeepsTheNewOneOut(t *testing.T) {
	t.Parallel()
	hermetic(t)
	clone := cloneBus(t, bareBus(t))
	gd, err := GitDir(clone)
	require.NoError(t, err)
	path := filepath.Join(gd, LockName)

	old, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	require.NoError(t, err)
	defer old.Close()
	require.NoError(t, syscall.Flock(int(old.Fd()), syscall.LOCK_EX|syscall.LOCK_NB), "stage the old binary's flock")

	_, err = LockCheckout(clone, 0)
	require.Error(t, err, "the new lock was taken while an old nova-bus held the same file")
	require.Contains(t, err.Error(), "another nova-bus is already running on this checkout")

	require.NoError(t, syscall.Flock(int(old.Fd()), syscall.LOCK_UN))
	release, err := LockCheckout(clone, time.Second)
	require.NoError(t, err, "the new lock could not be taken once the old nova-bus let go: %v", err)
	release()
}

// The new take holding the lock keeps an old nova-bus out, and the old one gets it once
// the new one releases.
func TestTheNewBusLockKeepsAnOldOneOut(t *testing.T) {
	t.Parallel()
	hermetic(t)
	clone := cloneBus(t, bareBus(t))
	gd, err := GitDir(clone)
	require.NoError(t, err)
	path := filepath.Join(gd, LockName)

	release, err := LockCheckout(clone, 0)
	require.NoError(t, err)

	old, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	require.NoError(t, err)
	defer old.Close()
	require.Error(t, syscall.Flock(int(old.Fd()), syscall.LOCK_EX|syscall.LOCK_NB), "an old nova-bus took the lock while the new one held it")

	release()
	require.NoError(t, syscall.Flock(int(old.Fd()), syscall.LOCK_EX|syscall.LOCK_NB), "an old nova-bus could not take the lock after the new one released it")
	require.NoError(t, syscall.Flock(int(old.Fd()), syscall.LOCK_UN))
}

// A fresh lock file is made with the mode the old binary gave it (0644 less the umask),
// not filelock's 0666 less the umask; the umask is measured with a probe file, not set.
// The release clears what the holder wrote, so a free lock names nobody.
func TestAFreshBusLockKeepsTheOldMode(t *testing.T) {
	t.Parallel()
	hermetic(t)
	clone := cloneBus(t, bareBus(t))
	gd, err := GitDir(clone)
	require.NoError(t, err)
	path := filepath.Join(gd, LockName)

	probe := filepath.Join(gd, "probe")
	require.NoError(t, os.WriteFile(probe, nil, 0o644))
	pinfo, err := os.Stat(probe)
	require.NoError(t, err)

	release, err := LockCheckout(clone, 0)
	require.NoError(t, err)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, pinfo.Mode().Perm(), info.Mode().Perm(), "a fresh checkout lock is %v; the old binary made it %v", info.Mode().Perm(), pinfo.Mode().Perm())
	release()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Empty(t, raw, "the release left %q in the lock file", raw)
}
