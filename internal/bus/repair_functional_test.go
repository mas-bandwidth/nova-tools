//go:build functional

package bus

import (
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestStaleIndexLockWithALiveGitIsLeftAlone(t *testing.T) {
	t.Parallel()
	hermetic(t)
	dir := cloneBus(t, bareBus(t))
	lock, err := indexLockPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", dir, "cat-file", "--batch")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
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
		if oerr != nil && !scanUnknownElsewhere(t, oerr, lock) {
			t.Fatal(oerr)
		}
		if owns {
			break
		}
		if time.Now().After(deadline) {
			procs, _ := gitProcesses()
			t.Fatalf("the live git never showed as owning %s; last err=%v procs=%+v", dir, oerr, procs)
		}
		time.Sleep(20 * time.Millisecond)
	}
	rep, err := ClearStaleIndexLock(dir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Cleared {
		t.Fatal("removed a stale index.lock while a git process still owned the checkout")
	}
	if _, err := os.Lstat(lock); err != nil {
		t.Fatalf("the lock is gone while git is alive: %v", err)
	}
	_ = stdin.Close()
	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
	deadline = time.Now().Add(testWaitBound())
	for {
		owns, oerr := gitOwnsCheckout(dir)
		if oerr != nil && !scanUnknownElsewhere(t, oerr, lock) {
			t.Fatal(oerr)
		}
		if oerr == nil && !owns {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("git still looked like it owned the checkout after it was killed: owns=%v err=%v", owns, oerr)
		}
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
		if err != nil && !scanUnknownElsewhere(t, err, lock) {
			t.Fatalf("stale lock with no git: cleared=%v err=%v", cleared, err)
		}
		if err == nil {
			if !cleared {
				t.Fatalf("stale lock with no git: cleared=%v err=%v", cleared, err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stale lock with no git: the scan stayed unknown for %v: %v", testWaitBound(), err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Lstat(lock); !os.IsNotExist(err) {
		t.Fatal("the lock is still there after the git exited")
	}
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
		t.Fatalf("the fixture git carries -C: %q", cmd.Args)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
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
		if oerr != nil {
			t.Fatalf("a live cwd git with no -C was an incomplete scan, not an owner: %v", oerr)
		}
		if owns {
			saw = true
			break
		}
		if time.Now().After(deadline) {
			procs, _ := gitProcesses()
			t.Fatalf("cwd git with no -C was invisible; procs=%+v", procs)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !saw {
		t.Fatal("cwd git with no -C was not recorded as an owner")
	}
	rep, err := ClearStaleIndexLock(dir, time.Now())
	if err != nil || rep.Cleared {
		t.Fatalf("cleared=%v err=%v, want the lock left because the cwd git owns the checkout", rep.Cleared, err)
	}
	if _, statErr := os.Lstat(lock); statErr != nil {
		t.Fatalf("the old lock is gone: %v", statErr)
	}
}
