//go:build unix

package update

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// The compatibility witnesses for the unix snapshot lock across an upgrade. Before
// internal/filelock, nova-update took a bare flock(LOCK_EX|LOCK_NB) on <snapshot>.lock,
// which it opened O_CREATE|O_RDWR 0600 and never wrote. An old binary is staged here as
// exactly that, on a second descriptor of the same file. The waits use a context that is
// already done, so nothing waits on the wall clock.

// An old nova-update holding the snapshot keeps the new one out (the budget ends with
// "snapshot is busy"), and the new one takes it once the old one lets go.
func TestAnOldReportsSnapshotLockKeepsTheNewOneOut(t *testing.T) {
	t.Parallel()
	snap := filepath.Join(t.TempDir(), "snapshot.json")
	old, err := os.OpenFile(snap+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	require.NoError(t, err)
	defer old.Close()
	require.NoError(t, syscall.Flock(int(old.Fd()), syscall.LOCK_EX|syscall.LOCK_NB), "stage the old binary's flock")

	done, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = lockSnapshot(done, snap)
	require.Error(t, err, "the new report took the snapshot while an old one held the same lock file")
	require.Contains(t, err.Error(), "snapshot is busy")

	require.NoError(t, syscall.Flock(int(old.Fd()), syscall.LOCK_UN))
	release, err := lockSnapshot(context.Background(), snap)
	require.NoError(t, err, "the new report could not take the snapshot once the old one let go: %v", err)
	release()
}

// The new report holding the snapshot keeps an old one out, and the old one gets it once
// the new one releases. What an old reader sees in the new stamp: no version of
// nova-update reads the lock file; the release leaves it empty, as the old one kept it.
func TestTheNewSnapshotLockKeepsAnOldReportOut(t *testing.T) {
	t.Parallel()
	snap := filepath.Join(t.TempDir(), "snapshot.json")
	release, err := lockSnapshot(context.Background(), snap)
	require.NoError(t, err)

	old, err := os.OpenFile(snap+".lock", os.O_RDWR, 0)
	require.NoError(t, err)
	defer old.Close()
	require.Error(t, syscall.Flock(int(old.Fd()), syscall.LOCK_EX|syscall.LOCK_NB), "an old report took the snapshot while the new one held it")

	release()
	require.NoError(t, syscall.Flock(int(old.Fd()), syscall.LOCK_EX|syscall.LOCK_NB), "an old report could not take the snapshot after the new one released it")
	require.NoError(t, syscall.Flock(int(old.Fd()), syscall.LOCK_UN))
	raw, err := os.ReadFile(snap + ".lock")
	require.NoError(t, err)
	require.Empty(t, raw, "the release left %q in the lock file", raw)
}

// A fresh <snapshot>.lock is made 0600, as the old report made it, under a umask that
// would make a 0666 create something else (measured with a probe file, not set).
func TestAFreshSnapshotLockIsOwnerOnly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	probe := filepath.Join(dir, "probe")
	require.NoError(t, os.WriteFile(probe, nil, 0o666))
	pinfo, err := os.Stat(probe)
	require.NoError(t, err)
	if pinfo.Mode().Perm() == 0o600 {
		t.Skipf("this process's umask already makes a 0666 create 0600, so the witness cannot tell the modes apart")
	}
	snap := filepath.Join(dir, "snapshot.json")
	release, err := lockSnapshot(context.Background(), snap)
	require.NoError(t, err)
	defer release()
	info, err := os.Stat(snap + ".lock")
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "a fresh snapshot lock is %v; nova-update has always made it 0600 (a 0666 create here gives %v)", info.Mode().Perm(), pinfo.Mode().Perm())
}
