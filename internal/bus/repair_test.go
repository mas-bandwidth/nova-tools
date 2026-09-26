package bus

import (
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestClearStaleIndexLockAgeBoundary pins the age rule and nothing else. The clock is
// the lock's own mtime plus a fixed offset, and the process scan is injected: the real
// scan reads every git on the host, and on a gate bench another lane's git caught
// mid-exit (comm git, empty cmdline, not yet a zombie) made the scan unknown and failed
// this test for a reason that has nothing to do with age (#2958). Ownership has its own
// tests below; here no git owns the checkout, so age alone decides.
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
	scans := 0
	noGit := func() ([]gitProc, error) {
		scans++
		return nil, nil
	}
	for _, young := range []time.Duration{0, staleIndexLockAge - time.Nanosecond, staleIndexLockAge} {
		cleared, err := clearStaleIndexLock(dir, fi.ModTime().Add(young), noGit)
		if err != nil || cleared {
			t.Fatalf("a lock aged %v: cleared=%v err=%v, want left alone", young, cleared, err)
		}
		if _, err := os.Lstat(lock); err != nil {
			t.Fatalf("the lock was removed at %v, not older than %v: %v", young, staleIndexLockAge, err)
		}
	}
	if scans != 0 {
		t.Fatalf("a lock not older than %v was put to the process scan %d times; age must decide first", staleIndexLockAge, scans)
	}
	cleared, err := clearStaleIndexLock(dir, fi.ModTime().Add(staleIndexLockAge+time.Nanosecond), noGit)
	if err != nil || !cleared {
		t.Fatalf("a lock older than 60s: cleared=%v err=%v, want removed", cleared, err)
	}
	if _, err := os.Lstat(lock); !os.IsNotExist(err) {
		t.Fatalf("the stale lock is still there: %v", err)
	}
	if scans != 1 {
		t.Fatalf("a stale lock was removed after %d process scans, want exactly 1", scans)
	}
}

func TestStaleIndexLockWithALiveGitIsLeftAlone(t *testing.T) {
	t.Parallel()
	// SLEEPS: this test waits on the wall clock (calls time.Sleep). Skipped 2026-09-25
	// by Glenn's rule ("unit tests must not have real sleeps or waits"): it becomes a
	// mocked-clock unit test or a functional program (nova-tools #4221).
	t.Skip("SLEEPS: needs a mocked clock or a functional test (nova-tools #4221)")
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
		cleared, err = clearStaleIndexLock(dir, time.Now(), scan)
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
	if _, lerr := os.Lstat(lock); lerr != nil {
		t.Fatalf("the lock is gone although the scan was unknown (%v): %v", err, lerr)
	}
	return true
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
	// SLEEPS: this test waits on the wall clock (calls time.Sleep). Skipped 2026-09-25
	// by Glenn's rule ("unit tests must not have real sleeps or waits"): it becomes a
	// mocked-clock unit test or a functional program (nova-tools #4221).
	t.Skip("SLEEPS: needs a mocked clock or a functional test (nova-tools #4221)")
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

// An empty cmdline is a dead git (a zombie) until the state says the process is
// still live. A dead one must not block cleanup of an unused lock, and must not
// hide a known owner later in the same scan. A live one that still cannot be
// placed stays unknown, and that diagnostic stays one short line.
func TestEmptyCmdlineDeadProcessDoesNotBlockLockCleanup(t *testing.T) {
	t.Parallel()
	hermetic(t)
	if dead, ok := procStatDead("12 (git) Z 1 1"); !ok || !dead {
		t.Fatalf("zombie stat: dead=%v ok=%v", dead, ok)
	}
	if dead, ok := procStatDead("12 (git defunct) X 1"); !ok || !dead {
		t.Fatalf("dead stat: dead=%v ok=%v", dead, ok)
	}
	if dead, ok := procStatDead("12 (git) S 1 1"); !ok || dead {
		t.Fatalf("sleeping stat was dead: dead=%v ok=%v", dead, ok)
	}

	_, skip, err := gitProcFromView(procView{comm: "git\n", dead: true})
	if err != nil || !skip {
		t.Fatalf("dead empty cmdline: skip=%v err=%v, want skipped and no error", skip, err)
	}

	dir, lock := oldIndexLock(t)
	ownerCmd := []byte("git\x00-C\x00" + dir + "\x00status")
	procs, err := classifyViews([]procView{
		{comm: "git\n"},
		{comm: "git\n", cmdline: ownerCmd, cwd: dir},
	})
	if len(procs) != 1 {
		t.Fatalf("empty cmdline hid the later owner: procs=%+v err=%v", procs, err)
	}
	owns, oerr := gitOwnsCheckoutScan(dir, func() ([]gitProc, error) {
		return procs, err
	})
	if oerr != nil || !owns {
		t.Fatalf("known owner was not recognized behind an empty cmdline: owns=%v err=%v", owns, oerr)
	}
	kept, kerr := clearStaleIndexLock(dir, time.Now(), func() ([]gitProc, error) {
		return procs, err
	})
	if kerr != nil || kept {
		t.Fatalf("owner's lock: cleared=%v err=%v, want kept", kept, kerr)
	}
	if _, statErr := os.Lstat(lock); statErr != nil {
		t.Fatalf("owner's lock is gone: %v", statErr)
	}

	unused, unusedLock := oldIndexLock(t)
	deadProcs, deadErr := classifyViews([]procView{{comm: "git\n", dead: true}})
	if deadErr != nil || len(deadProcs) != 0 {
		t.Fatalf("dead cmdline stayed in the scan: procs=%+v err=%v", deadProcs, deadErr)
	}
	cleared, cerr := clearStaleIndexLock(unused, time.Now(), func() ([]gitProc, error) {
		return deadProcs, deadErr
	})
	if cerr != nil || !cleared {
		t.Fatalf("unused lock blocked by a dead cmdline: cleared=%v err=%v", cleared, cerr)
	}
	if _, statErr := os.Lstat(unusedLock); !os.IsNotExist(statErr) {
		t.Fatalf("unused lock still present: %v", statErr)
	}

	live, liveLock := oldIndexLock(t)
	_, liveErr := classifyViews([]procView{{comm: "git\n"}})
	if liveErr == nil || strings.Contains(liveErr.Error(), "\n") || len(liveErr.Error()) > ownershipDiagCap || !strings.HasPrefix(liveErr.Error(), ownershipUnknown) {
		t.Fatalf("live empty cmdline diagnostic is not bounded: %q", liveErr)
	}
	cleared, cerr = clearStaleIndexLock(live, time.Now(), func() ([]gitProc, error) {
		return nil, liveErr
	})
	assertLockKept(t, liveLock, cleared, cerr)
}

func TestProcOwnsRequiresAPathBoundary(t *testing.T) {
	t.Parallel()

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

// #3029: a gate batch went red on TestWaitRepairStaleLockAndDirtyBeat with
//
//	WAIT REFUSED: cannot tell whether a git process owns this checkout: read /proc/637768/comm: no such process
//
// while every member passed alone. Linux answers a read of /proc/<pid>/comm, cmdline,
// stat or cwd with ESRCH, not ENOENT, while a process that has just exited is being torn
// down. On a gate host running a whole package in parallel some process is always in that
// window, and it need not even be a git. A pid that is gone is gone, whichever errno says
// so: it is not an owner, and it is not an unreadable live process. The scan is supplied
// here, so the answer does not depend on which process happens to be exiting when it runs.
func TestVanishingProcessESRCHDoesNotBlockLockCleanup(t *testing.T) {
	t.Parallel()
	hermetic(t)
	esrch := func(file string) error {
		return &fs.PathError{Op: "read", Path: "/proc/637768/" + file, Err: syscall.ESRCH}
	}
	for _, v := range []procView{
		{commErr: esrch("comm")},
		{comm: "git\n", cmdErr: esrch("cmdline")},
		{comm: "git\n", cmdline: []byte("git\x00status"), cwdErr: esrch("cwd")},
	} {
		if _, skip, err := gitProcFromView(v); err != nil || !skip {
			t.Fatalf("a process gone mid-read (%+v): skip=%v err=%v, want skipped and no error", v, skip, err)
		}
	}

	dir, lock := oldIndexLock(t)
	scan := func() ([]gitProc, error) {
		return classifyViews([]procView{{commErr: esrch("comm")}, {comm: "bash\n"}})
	}
	cleared, err := clearStaleIndexLock(dir, time.Now(), scan)
	if err != nil || !cleared {
		t.Fatalf("a stale lock with only a vanishing process in the scan: cleared=%v err=%v, want removed", cleared, err)
	}
	if _, statErr := os.Lstat(lock); !os.IsNotExist(statErr) {
		t.Fatalf("stale index.lock still present: %v", statErr)
	}

	// ESRCH is not a licence for every errno: a live process that denies the read
	// still keeps the lock.
	kept, keptLock := oldIndexLock(t)
	cleared, err = clearStaleIndexLock(kept, time.Now(), func() ([]gitProc, error) {
		return classifyViews([]procView{{comm: "git\n", cmdErr: &fs.PathError{Op: "read", Path: "/proc/1/cmdline", Err: syscall.EACCES}}})
	})
	assertLockKept(t, keptLock, cleared, err)
}
