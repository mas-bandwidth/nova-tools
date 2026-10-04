//go:build unix

package tokens

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// The compatibility witnesses for the unix fold lock across an upgrade. Before
// internal/filelock, a fold took a bare flock(LOCK_EX|LOCK_NB) on <out>/fold.lock (created
// O_EXCL 0644, or opened when it existed), wrote its bare pid into it and never cleared
// it. An old binary is staged here as exactly that, on a second descriptor of the same
// file.

func stageOldFold(t *testing.T, path string) *os.File {
	t.Helper()
	old, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	require.NoError(t, err)
	t.Cleanup(func() { old.Close() })
	require.NoError(t, syscall.Flock(int(old.Fd()), syscall.LOCK_EX|syscall.LOCK_NB), "stage the old fold's flock")
	require.NoError(t, old.Truncate(0))
	_, err = old.WriteAt([]byte("424242\n"), 0)
	require.NoError(t, err)
	return old
}

// An old fold holding the lock keeps the new one out, and the refusal names the old
// holder's bare pid; once the old fold lets go the new one takes the lock.
func TestAnOldFoldKeepsTheNewOneOut(t *testing.T) {
	t.Parallel()
	out := t.TempDir()
	old := stageOldFold(t, filepath.Join(out, LockName))

	_, err := TakeFoldLock(out, 0)
	require.Error(t, err, "the new fold took the lock while an old fold held the same file")
	require.Contains(t, err.Error(), "another nova-tokens fold holds")
	require.Contains(t, err.Error(), "(pid 424242)", "the new fold does not name the old holder's bare pid: %v", err)

	require.NoError(t, syscall.Flock(int(old.Fd()), syscall.LOCK_UN))
	release, err := TakeFoldLock(out, 0)
	require.NoError(t, err, "the new fold could not take the lock once the old one let go: %v", err)
	release()
}

// The new fold holding the lock keeps an old fold out. What the old fold would print:
// its HolderPID took the whole file as one integer, and the new stamp is not one, so an
// old fold's refusal names the holder as "-" while the new one names the pid. The
// difference is shown, never decided on: the pid is only printed in the refusal.
func TestTheNewFoldKeepsAnOldOneOut(t *testing.T) {
	t.Parallel()
	out := t.TempDir()
	path := filepath.Join(out, LockName)
	release, err := TakeFoldLock(out, 0)
	require.NoError(t, err)

	old, err := os.OpenFile(path, os.O_RDWR, 0)
	require.NoError(t, err)
	defer old.Close()
	require.Error(t, syscall.Flock(int(old.Fd()), syscall.LOCK_EX|syscall.LOCK_NB), "an old fold took the lock while the new one held it")

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	_, atoiErr := strconv.Atoi(strings.TrimSpace(string(raw)))
	require.Error(t, atoiErr, "the new stamp parses as a bare pid, so this witness no longer describes what an old fold prints")
	require.Equal(t, strconv.Itoa(os.Getpid()), HolderPID(path), "the new fold does not read its own stamp's pid")

	release()
	require.NoError(t, syscall.Flock(int(old.Fd()), syscall.LOCK_EX|syscall.LOCK_NB), "an old fold could not take the lock after the new one released it")
	require.NoError(t, syscall.Flock(int(old.Fd()), syscall.LOCK_UN))
}

// A fresh fold.lock is made with the mode the old fold gave it (0644 less the umask), not
// filelock's 0666 less the umask; the umask is measured with a probe file, not set.
func TestAFreshFoldLockKeepsTheOldMode(t *testing.T) {
	t.Parallel()
	out := t.TempDir()
	probe := filepath.Join(out, "probe")
	require.NoError(t, os.WriteFile(probe, nil, 0o644))
	pinfo, err := os.Stat(probe)
	require.NoError(t, err)
	require.NoError(t, os.Remove(probe))

	release, err := TakeFoldLock(out, 0)
	require.NoError(t, err)
	defer release()
	info, err := os.Stat(filepath.Join(out, LockName))
	require.NoError(t, err)
	require.Equal(t, pinfo.Mode().Perm(), info.Mode().Perm(), "a fresh fold.lock is %v; the old fold made it %v", info.Mode().Perm(), pinfo.Mode().Perm())
}
