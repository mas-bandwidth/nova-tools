package bus

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The lock on Windows is a sentinel file, and the kernel does not drop it when
// the process dies, so a killed run leaves it behind. These tests stage that
// state on every platform: a child is started and reaped to get a genuinely
// dead pid, the lock file is stamped with it, and the sentinel is created. The
// recovery loop is then driven with the sentinel algorithm rather than flock,
// because an flock holder cannot be left dead -- the kernel releases it -- and
// the case under test is the sentinel one.
//
// The bug this closes: before the pid was checked, the next run waited out its
// whole budget for a process that was already gone and refused. And a normal
// release whose os.Remove hit a Windows delete-pending state left the sentinel
// for the next run to trip over.

// sentinelTry is the sentinel algorithm the Windows and generic builds use.
// lockFile takes its try function as a parameter, so the recovery loop is not
// privileged on the platform that happens to run the test.
func sentinelTry(f *os.File) (bool, bool, error) {
	held, err := os.OpenFile(sentinelPath(f.Name()), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err == nil {
		return true, false, held.Close()
	}
	if errors.Is(err, os.ErrExist) {
		return false, true, nil
	}
	return false, false, err
}

func writeHolder(t *testing.T, lockPath, holder string) {
	t.Helper()
	if err := os.WriteFile(lockPath, []byte(holder+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeSentinel(t *testing.T, lockPath string) {
	t.Helper()
	if err := os.WriteFile(sentinelPath(lockPath), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// deadPID stages a holder that has really died: a child is started, its pid is
// recorded, and it is reaped, which is the state a kill leaves behind on
// Windows. The pid alone is not taken on faith -- the same processAlive the
// product uses is asked, and the test refuses to run against a live one.
func deadPID(t *testing.T) int {
	t.Helper()
	c := exec.Command("git", "--version")
	if err := c.Start(); err != nil {
		t.Skipf("cannot start a process to stage a dead holder: %v", err)
	}
	pid := c.Process.Pid
	_ = c.Wait()
	if processAlive(pid) {
		t.Fatalf("the staged holder %d is still alive; this test needs a dead one", pid)
	}
	return pid
}

func TestLockRecoversWhenTheSentinelHolderDied(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, LockName)
	writeHolder(t, lockPath, fmt.Sprintf("pid=%d at=2000-01-01T00:00:00Z", deadPID(t)))
	writeSentinel(t, lockPath)

	release, err := lockFile(lockPath, 500*time.Millisecond, sentinelTry, nil)
	if err != nil {
		t.Fatalf("a lock whose holder is dead was not recovered: %v", err)
	}
	defer release()

	if got := ReadLockHolder(lockPath); got != fmt.Sprint(os.Getpid()) {
		t.Fatalf("after recovery the lock names %q, want this process %d", got, os.Getpid())
	}
	if _, err := os.Stat(sentinelPath(lockPath)); err != nil {
		t.Fatalf("the recovered lock's sentinel is missing: %v", err)
	}
}

// removeLockFile is the release path on the sentinel platforms. A missing file
// is success rather than an error, because the lock it named is already gone.
func TestRemoveLockFileTreatsAMissingFileAsGone(t *testing.T) {
	path := filepath.Join(t.TempDir(), LockName+".held")
	if err := removeLockFile(path); err != nil {
		t.Fatalf("removing a file that is not there returned %v, want nil", err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := removeLockFile(path); err != nil {
		t.Fatalf("removing the file returned %v, want nil", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the file was not removed: %v", err)
	}
}

func TestLockLeavesALiveHoldersSentinelAlone(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, LockName)
	writeHolder(t, lockPath, fmt.Sprintf("pid=%d", os.Getpid()))
	writeSentinel(t, lockPath)

	if _, err := lockFile(lockPath, 100*time.Millisecond, sentinelTry, nil); err == nil {
		t.Fatal("a lock held by a live process was taken")
	} else if !errors.Is(err, ErrLockHeld) {
		t.Fatalf("err = %v, want ErrLockHeld", err)
	}
	if _, err := os.Stat(sentinelPath(lockPath)); err != nil {
		t.Fatalf("a live holder's sentinel was cleared: %v", err)
	}
}

// A holder that cannot be shown to be gone is not a dead one: an empty or
// unwritable pid is left for a person rather than cleared on a guess.
func TestLockLeavesAnUnknownHolderAlone(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, LockName)
	writeHolder(t, lockPath, "")
	writeSentinel(t, lockPath)

	if _, err := lockFile(lockPath, 100*time.Millisecond, sentinelTry, nil); err == nil {
		t.Fatal("a lock with no recorded holder was taken")
	} else if !errors.Is(err, ErrLockHeld) {
		t.Fatalf("err = %v, want ErrLockHeld", err)
	}
	if _, err := os.Stat(sentinelPath(lockPath)); err != nil {
		t.Fatalf("a lock with no recorded holder was cleared: %v", err)
	}
}
