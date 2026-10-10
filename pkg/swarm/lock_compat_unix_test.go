//go:build unix

package swarm

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// The compatibility witnesses for the unix slot store lock across an upgrade. Before
// pkg/filelock, nova-swarm took a bare flock(LOCK_EX|LOCK_NB) on the lock file it
// opened O_RDWR|O_CREATE 0644, and wrote nothing into it. An old binary is staged here as
// exactly that, on a second descriptor of the same file.

// An old nova-swarm holding the lock keeps the new take out, and the new take succeeds
// once the old one lets go.
func TestAnOldSwarmsLockKeepsTheNewOneOut(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), SlotStoreLockName)
	old, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	require.NoError(t, err)
	defer old.Close()
	require.NoError(t, syscall.Flock(int(old.Fd()), syscall.LOCK_EX|syscall.LOCK_NB), "stage the old binary's flock")

	_, err = takeFileLock(path, 0)
	require.Error(t, err, "the new lock was taken while an old nova-swarm held the same file")
	require.Contains(t, err.Error(), "another nova-swarm holds")

	require.NoError(t, syscall.Flock(int(old.Fd()), syscall.LOCK_UN))
	release, err := takeFileLock(path, 0)
	require.NoError(t, err, "the new lock could not be taken once the old nova-swarm let go: %v", err)
	release()
}

// The new take holding the lock keeps an old nova-swarm out, and the old one gets it once
// the new one releases.
func TestTheNewSwarmLockKeepsAnOldOneOut(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), SlotStoreLockName)
	release, err := takeFileLock(path, 0)
	require.NoError(t, err)

	old, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	require.NoError(t, err)
	defer old.Close()
	require.Error(t, syscall.Flock(int(old.Fd()), syscall.LOCK_EX|syscall.LOCK_NB), "an old nova-swarm took the lock while the new one held it")

	release()
	require.NoError(t, syscall.Flock(int(old.Fd()), syscall.LOCK_EX|syscall.LOCK_NB), "an old nova-swarm could not take the lock after the new one released it")
	require.NoError(t, syscall.Flock(int(old.Fd()), syscall.LOCK_UN))
}

// A fresh lock file is made with the mode the old binary gave it (0644 less the umask),
// not filelock's 0666 less the umask; the umask is measured with probe files, not set.
// What an old reader sees in the new stamp: nothing reads the slot store's lock file, old
// or new; the take writes its stamp there and the release clears it.
func TestAFreshSwarmLockKeepsTheOldMode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	probe := filepath.Join(dir, "probe")
	require.NoError(t, os.WriteFile(probe, nil, 0o644))
	pinfo, err := os.Stat(probe)
	require.NoError(t, err)

	path := filepath.Join(dir, SlotStoreLockName)
	release, err := takeFileLock(path, 0)
	require.NoError(t, err)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, pinfo.Mode().Perm(), info.Mode().Perm(), "a fresh slot store lock is %v; the old binary made it %v", info.Mode().Perm(), pinfo.Mode().Perm())
	release()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Empty(t, raw, "the release left %q in the lock file", raw)
}
