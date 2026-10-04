//go:build functional

package bus

import (
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// These tests start a real git and wait for the host's process table to show it owning a
// checkout (or to stop showing it): what they wait for is the operating system, so they are
// functional tests. The decision they end in, a live owner keeps the lock and an unknown scan
// never removes it, is pinned in the unit tier through the injected scan
// (clearStaleIndexLock's scan argument, repair_test.go).

// testWaitBound is how long an event poll waits for an observable before it reports rather
// than waits forever. It is read from NOVA_TEST_WAIT (default 30s), the allowed shape the
// waits class test names: every use returns the MOMENT the observable appears, so a slower
// runner pays only when the event never comes.
func testWaitBound() time.Duration {
	if v := os.Getenv("NOVA_TEST_WAIT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 30 * time.Second
}

func TestStaleIndexLockWithALiveGitIsLeftAlone(t *testing.T) {
	t.Parallel()
	hermetic(t)
	dir := cloneBus(t, bareBus(t))
	lock, err := indexLockPath(dir)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(lock, nil, 0o644))
	old := time.Now().Add(-2 * time.Minute)
	require.NoError(t, os.Chtimes(lock, old, old))
	cmd := exec.Command("git", "-C", dir, "cat-file", "--batch")
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	})
	deadline := time.Now().Add(testWaitBound())
	for {
		owns, oerr := gitOwnsCheckout(dir)
		require.False(t, oerr != nil && !scanUnknownElsewhere(t, oerr, lock), oerr)
		if owns {
			break
		}
		if time.Now().After(deadline) {
			procs, _ := gitProcesses()
			require.FailNowf(t, "assertion failed", "the live git never showed as owning %s; last err=%v procs=%+v", dir, oerr, procs)
		}
		time.Sleep(20 * time.Millisecond)
	}
	rep, err := ClearStaleIndexLock(dir, time.Now())
	require.NoError(t, err)
	require.False(t, rep.Cleared, "removed a stale index.lock while a git process still owned the checkout")
	if _, err := os.Lstat(lock); err != nil {
		require.NoError(t, err, "the lock is gone while git is alive: %v", err)
	}
	_ = stdin.Close()
	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
	deadline = time.Now().Add(testWaitBound())
	for {
		owns, oerr := gitOwnsCheckout(dir)
		require.False(t, oerr != nil && !scanUnknownElsewhere(t, oerr, lock), oerr)
		if oerr == nil && !owns {
			break
		}
		require.False(t, time.Now().After(deadline), "git still looked like it owned the checkout after it was killed: owns=%v err=%v", owns, oerr)
		time.Sleep(20 * time.Millisecond)
	}
	// The first scan answers "cannot tell", as a stranger's exiting git makes the real one
	// answer on a busy bench; later scans are the real host scan. The lock must survive the
	// unknown answer and go on the next known one.
	unknownOnce := true
	scan := func() ([]gitProc, error) {
		if unknownOnce {
			unknownOnce = false
			return nil, ownershipUnknownErr("cmdline empty")
		}
		return gitProcesses()
	}
	deadline = time.Now().Add(testWaitBound())
	for {
		cleared, err := clearStaleIndexLock(dir, time.Now(), scan)
		require.False(t, err != nil && !scanUnknownElsewhere(t, err, lock), "stale lock with no git: cleared=%v err=%v", cleared, err)
		if err == nil {
			require.True(t, cleared, "stale lock with no git: cleared=%v err=%v", cleared, err)
			break
		}
		if time.Now().After(deadline) {
			require.False(t, time.Now().After(deadline), "stale lock with no git: the scan stayed unknown for %v: %v", testWaitBound(), err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	{
		_, err := os.Lstat(lock)
		require.False(t, !os.IsNotExist(err), "the lock is still there after the git exited")
	}
}

// scanUnknownElsewhere reports whether err is the host-wide process scan answering
// "cannot tell" (#2958). The scan reads every git on the machine; on a gate bench another
// lane's git caught mid-exit (empty cmdline, not yet a zombie) makes it unknown for a
// moment, and that is not this checkout's answer. An unknown scan must never remove the
// lock, so it is asserted here, and the caller polls again until testWaitBound instead of
// failing on a process it does not own. Any other error is the caller's to fail on.
func scanUnknownElsewhere(t *testing.T, err error, lock string) bool {
	t.Helper()
	if err == nil || !strings.HasPrefix(err.Error(), ownershipUnknown) {
		return false
	}
	{
		_, lerr := os.Lstat(lock)
		require.Equal(t, nil, lerr, "the lock is gone although the scan was unknown (%v): %v", err, lerr)
	}
	return true
}

// A git started inside the checkout, with no -C, is visible only by its cwd.
// The old lock stays. A scan that claims to have looked and did not see it is a failure.
func TestStaleLockStaysForCwdGitWithoutDashC(t *testing.T) {
	t.Parallel()
	hermetic(t)
	dir, lock := oldIndexLock(t)
	cmd := exec.Command("git", "cat-file", "--batch")
	cmd.Dir = dir
	if strings.Contains(strings.Join(cmd.Args, " "), "-C") {
		require.NotContains(t, strings.Join(cmd.Args, " "), "-C", "the fixture git carries -C: %q", cmd.Args)
	}
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	})
	deadline := time.Now().Add(testWaitBound())
	saw := false
	for {
		owns, oerr := gitOwnsCheckout(dir)
		require.Equal(t, nil, oerr, "a live cwd git with no -C was an incomplete scan, not an owner: %v", oerr)
		if owns {
			saw = true
			break
		}
		if time.Now().After(deadline) {
			procs, _ := gitProcesses()
			require.FailNowf(t, "assertion failed", "cwd git with no -C was invisible; procs=%+v", procs)
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.True(t, saw, "cwd git with no -C was not recorded as an owner")
	rep, err := ClearStaleIndexLock(dir, time.Now())
	if err != nil || rep.Cleared {
		require.False(t, err != nil || rep.Cleared, "cleared=%v err=%v, want the lock left because the cwd git owns the checkout", rep.Cleared, err)
	}
	{
		_, statErr := os.Lstat(lock)
		require.Equal(t, nil, statErr, "the old lock is gone: %v", statErr)
	}
}
