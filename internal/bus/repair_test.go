package bus

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClearStaleIndexLockAgeBoundary(t *testing.T) {
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
	fi, err := os.Lstat(lock)
	if err != nil {
		t.Fatal(err)
	}
	cleared, err := ClearStaleIndexLock(dir, fi.ModTime().Add(staleIndexLockAge))
	if err != nil || cleared {
		t.Fatalf("a lock aged exactly 60s: cleared=%v err=%v, want left alone", cleared, err)
	}
	if _, err := os.Lstat(lock); err != nil {
		t.Fatalf("the lock was removed at exactly 60s: %v", err)
	}
	cleared, err = ClearStaleIndexLock(dir, fi.ModTime().Add(staleIndexLockAge+time.Nanosecond))
	if err != nil || !cleared {
		t.Fatalf("a lock older than 60s: cleared=%v err=%v, want removed", cleared, err)
	}
	if _, err := os.Lstat(lock); !os.IsNotExist(err) {
		t.Fatalf("the stale lock is still there: %v", err)
	}
}

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
		if oerr != nil {
			t.Fatal(oerr)
		}
		if owns {
			break
		}
		if time.Now().After(deadline) {
			procs, _ := gitProcesses()
			t.Fatalf("the live git never showed as owning %s; procs=%+v", dir, procs)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cleared, err := ClearStaleIndexLock(dir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if cleared {
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
		if oerr != nil {
			t.Fatal(oerr)
		}
		if !owns {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("git still looked like it owned the checkout after it was killed")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cleared, err = ClearStaleIndexLock(dir, time.Now())
	if err != nil || !cleared {
		t.Fatalf("stale lock with no git: cleared=%v err=%v", cleared, err)
	}
	if _, err := os.Lstat(lock); !os.IsNotExist(err) {
		t.Fatal("the lock is still there after the git exited")
	}
}

// oldIndexLock is a checkout whose index.lock is already past the 60s bound.
// Age alone must not be why a later assertion keeps or removes it.
func oldIndexLock(t *testing.T) (dir, lock string) {
	t.Helper()
	dir = cloneBus(t, bareBus(t))
	var err error
	lock, err = indexLockPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(lock, when, when); err != nil {
		t.Fatal(err)
	}
	return dir, lock
}

func assertLockKept(t *testing.T, lock string, cleared bool, err error) {
	t.Helper()
	if cleared {
		t.Fatal("the old lock was removed")
	}
	if _, statErr := os.Lstat(lock); statErr != nil {
		t.Fatalf("the old lock is gone: %v", statErr)
	}
	if err == nil || !strings.HasPrefix(err.Error(), ownershipUnknown) {
		t.Fatalf("diagnostic = %v, want a %q reason", err, ownershipUnknown)
	}
	if strings.Contains(err.Error(), "\n") || len(err.Error()) > ownershipDiagCap {
		t.Fatalf("diagnostic is not bounded: %q", err)
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
	cleared, err := ClearStaleIndexLock(dir, time.Now())
	if err != nil || cleared {
		t.Fatalf("cleared=%v err=%v, want the lock left because the cwd git owns the checkout", cleared, err)
	}
	if _, statErr := os.Lstat(lock); statErr != nil {
		t.Fatalf("the old lock is gone: %v", statErr)
	}
}

// The scan saw a live git with no -C and could not read its cwd. That is not "no owner".
func TestStaleLockStaysWhenCwdInspectionFails(t *testing.T) {
	t.Parallel()
	hermetic(t)
	dir, lock := oldIndexLock(t)
	cleared, err := clearStaleIndexLock(dir, time.Now(), func() ([]gitProc, error) {
		return []gitProc{{command: "git cat-file --batch", cwdKnown: false}}, nil
	})
	assertLockKept(t, lock, cleared, err)
}

// A still-present process whose metadata cannot be read is not a process that
// vanished. Permission denied keeps the lock; ENOENT is skipped and is not that error.
func TestStaleLockStaysWhenProcessInspectionIsDenied(t *testing.T) {
	t.Parallel()
	hermetic(t)
	dir, lock := oldIndexLock(t)
	_, skip, verr := gitProcFromView(procView{commErr: os.ErrNotExist})
	if verr != nil || !skip {
		t.Fatalf("a vanished process: skip=%v err=%v, want skipped and no error", skip, verr)
	}
	_, _, perr := gitProcFromView(procView{comm: "git\n", cmdErr: os.ErrPermission})
	if perr == nil {
		t.Fatal("permission denied on cmdline was treated as a vanished process")
	}
	cleared, err := clearStaleIndexLock(dir, time.Now(), func() ([]gitProc, error) {
		return nil, perr
	})
	assertLockKept(t, lock, cleared, err)
}

func TestProcOwnsRequiresAPathBoundary(t *testing.T) {
	names := []string{"/bus"}
	if !procOwns(names, gitProc{command: "git -C /bus status"}) {
		t.Fatal("git -C /bus should own /bus")
	}
	if procOwns(names, gitProc{command: "git -C /bus2 status"}) {
		t.Fatal("/bus2 must not count as owning /bus")
	}
	if !procOwns(names, gitProc{command: "git -C /bus/lane status"}) {
		t.Fatal("a git inside the checkout should count as owning it")
	}
	if !procOwns(names, gitProc{cwd: "/bus", command: "git status"}) {
		t.Fatal("a git whose cwd is the checkout should own it")
	}
	if procOwns(names, gitProc{cwd: "/bus2", command: "git status"}) {
		t.Fatal("a git in /bus2 must not own /bus")
	}
}

func TestDiscardPathRemovesASymlinkedBeatWithoutFollowingIt(t *testing.T) {
	t.Parallel()
	hermetic(t)
	root := cloneBus(t, bareBus(t))
	v := victim(t, filepath.Dir(root))
	link := filepath.Join(root, "from-ada", BeatName)
	plant(t, v, link)
	discarded, err := DiscardPath(root, "from-ada/"+BeatName)
	if err != nil || !discarded {
		t.Fatalf("discarded=%v err=%v, want the symlink removed", discarded, err)
	}
	unchanged(t, v)
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("the symlink is still there: %v", err)
	}
}

func TestRecoverWaitFastForwardLeavesADirtyCursor(t *testing.T) {
	t.Parallel()
	hermetic(t)
	bare := bareBus(t)
	reader := cloneBus(t, bare)
	writer := cloneBus(t, bare)
	write(t, reader, "from-ada/CURSOR", "BASE\n")
	if _, err := CommitAndPush(reader, testIdentity["Ada"], []string{"from-ada/CURSOR"}, "cursor", "origin", "main", 3); err != nil {
		t.Fatal(err)
	}
	if _, err := FetchAndFastForward(writer, "origin", "main"); err != nil {
		t.Fatal(err)
	}
	write(t, writer, "from-ada/CURSOR", "UPSTREAM\n")
	if _, err := CommitAndPush(writer, testIdentity["Ada"], []string{"from-ada/CURSOR"}, "cursor moved", "origin", "main", 3); err != nil {
		t.Fatal(err)
	}
	const sentinel = "SENTINEL-LOCAL do not touch\n"
	write(t, reader, "from-ada/CURSOR", sentinel)
	before, err := HeadCommit(reader)
	if err != nil {
		t.Fatal(err)
	}
	_, err = RecoverWaitFastForward(reader, "origin", "main", []string{"from-ada/BEAT"}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "from-ada/CURSOR") || !strings.Contains(err.Error(), "not this tool's to discard") {
		t.Fatalf("want a plain refusal naming CURSOR, got %v", err)
	}
	got, err := os.ReadFile(filepath.Join(reader, "from-ada", "CURSOR"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != sentinel {
		t.Fatalf("CURSOR was touched: %q", got)
	}
	after, err := HeadCommit(reader)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("HEAD moved over a dirty CURSOR: %s -> %s", before, after)
	}
}
