package bus

import (
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
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
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(lock, nil, 0o644))
	fi, err := os.Lstat(lock)
	require.NoError(t, err)
	scans := 0
	noGit := func() ([]gitProc, error) {
		scans++
		return nil, nil
	}
	for _, young := range []time.Duration{0, staleIndexLockAge - time.Nanosecond, staleIndexLockAge} {
		cleared, err := clearStaleIndexLock(dir, fi.ModTime().Add(young), noGit)
		require.False(t, err != nil || cleared, "a lock aged %v: cleared=%v err=%v, want left alone", young, cleared, err)
		{
			_, err := os.Lstat(lock)
			require.NoError(t, err, "the lock was removed at %v, not older than %v: %v", young, staleIndexLockAge, err)
		}
	}
	require.Equal(t, 0, scans, "a lock not older than %v was put to the process scan %d times; age must decide first", staleIndexLockAge, scans)
	cleared, err := clearStaleIndexLock(dir, fi.ModTime().Add(staleIndexLockAge+time.Nanosecond), noGit)
	require.False(t, err != nil || !cleared, "a lock older than 60s: cleared=%v err=%v, want removed", cleared, err)
	{
		_, err := os.Lstat(lock)
		require.False(t, !os.IsNotExist(err), "the stale lock is still there: %v", err)
	}
	require.Equal(t, 1, scans, "a stale lock was removed after %d process scans, want exactly 1", scans)
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

// oldIndexLock is a checkout whose index.lock is already past the 60s bound.
// Age alone must not be why a later assertion keeps or removes it.
func oldIndexLock(t *testing.T) (dir, lock string) {
	t.Helper()
	dir = t.TempDir()
	if _, err := git(dir, "init", "-q"); err != nil {
		require.NoError(t, err)
	}
	var err error
	lock, err = indexLockPath(dir)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(lock, nil, 0o644))
	when := time.Now().Add(-2 * time.Minute)
	require.NoError(t, os.Chtimes(lock, when, when))
	return dir, lock
}

func assertLockKept(t *testing.T, lock string, cleared bool, err error) {
	t.Helper()
	require.False(t, cleared, "the old lock was removed")
	{
		_, statErr := os.Lstat(lock)
		require.Equal(t, nil, statErr, "the old lock is gone: %v", statErr)
	}
	require.False(t, err == nil || !strings.HasPrefix(err.Error(), ownershipUnknown), "diagnostic = %v, want a %q reason", err, ownershipUnknown)
	require.False(t, strings.Contains(err.Error(), "\n") || len(err.Error()) > ownershipDiagCap, "diagnostic is not bounded: %q", err)
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
	require.False(t, verr != nil || !skip, "a vanished process: skip=%v err=%v, want skipped and no error", skip, verr)
	_, _, perr := gitProcFromView(procView{comm: "git\n", cmdErr: os.ErrPermission})
	require.False(t, perr == nil, "permission denied on cmdline was treated as a vanished process")
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
	{
		dead, ok := procStatDead("12 (git) Z 1 1")
		require.False(t, !ok || !dead, "zombie stat: dead=%v ok=%v", dead, ok)
	}
	{
		dead, ok := procStatDead("12 (git defunct) X 1")
		require.False(t, !ok || !dead, "dead stat: dead=%v ok=%v", dead, ok)
	}
	{
		dead, ok := procStatDead("12 (git) S 1 1")
		require.False(t, !ok || dead, "sleeping stat was dead: dead=%v ok=%v", dead, ok)
	}

	_, skip, err := gitProcFromView(procView{comm: "git\n", dead: true})
	require.False(t, err != nil || !skip, "dead empty cmdline: skip=%v err=%v, want skipped and no error", skip, err)

	dir, lock := oldIndexLock(t)
	ownerCmd := []byte("git\x00-C\x00" + dir + "\x00status")
	procs, err := classifyViews([]procView{
		{comm: "git\n"},
		{comm: "git\n", cmdline: ownerCmd, cwd: dir},
	}, 501)
	require.Equal(t, 1, len(placed(procs)), "empty cmdline hid the later owner: procs=%+v err=%v", procs, err)
	found, oerr := gitOwnsCheckoutScan(dir, func() ([]gitProc, error) {
		return procs, err
	})
	require.False(t, oerr != nil || found.Owner != 1 || found.Unknown != 1, "known owner was not recognized behind an empty cmdline: found=%+v err=%v", found, oerr)
	kept, kerr := clearStaleIndexLock(dir, time.Now(), func() ([]gitProc, error) {
		return procs, err
	})
	require.False(t, kerr != nil || kept, "owner's lock: cleared=%v err=%v, want kept", kept, kerr)
	{
		_, statErr := os.Lstat(lock)
		require.Equal(t, nil, statErr, "owner's lock is gone: %v", statErr)
	}

	unused, unusedLock := oldIndexLock(t)
	deadProcs, deadErr := classifyViews([]procView{{comm: "git\n", dead: true}}, 501)
	require.False(t, deadErr != nil || len(deadProcs) != 0, "dead cmdline stayed in the scan: procs=%+v err=%v", deadProcs, deadErr)
	cleared, cerr := clearStaleIndexLock(unused, time.Now(), func() ([]gitProc, error) {
		return deadProcs, deadErr
	})
	require.False(t, cerr != nil || !cleared, "unused lock blocked by a dead cmdline: cleared=%v err=%v", cleared, cerr)
	{
		_, statErr := os.Lstat(unusedLock)
		require.False(t, !os.IsNotExist(statErr), "unused lock still present: %v", statErr)
	}

	live, liveLock := oldIndexLock(t)
	_, liveErr := classifyViews([]procView{{comm: "git\n"}}, 501)
	require.False(t, liveErr == nil || strings.Contains(liveErr.Error(), "\n") || len(liveErr.Error()) > ownershipDiagCap || !strings.HasPrefix(liveErr.Error(), ownershipUnknown), "live empty cmdline diagnostic is not bounded: %q", liveErr)
	cleared, cerr = clearStaleIndexLock(live, time.Now(), func() ([]gitProc, error) {
		return nil, liveErr
	})
	assertLockKept(t, liveLock, cleared, cerr)
}

func TestProcOwnsRequiresAPathBoundary(t *testing.T) {
	t.Parallel()

	names := []string{"/bus"}
	require.False(t, !procOwns(names, gitProc{command: "git -C /bus status"}), "git -C /bus should own /bus")
	require.False(t, procOwns(names, gitProc{command: "git -C /bus2 status"}), "/bus2 must not count as owning /bus")
	require.False(t, !procOwns(names, gitProc{command: "git -C /bus/lane status"}), "a git inside the checkout should count as owning it")
	require.False(t, !procOwns(names, gitProc{cwd: "/bus", command: "git status"}), "a git whose cwd is the checkout should own it")
	require.False(t, procOwns(names, gitProc{cwd: "/bus2", command: "git status"}), "a git in /bus2 must not own /bus")
}

func TestDiscardPathRemovesASymlinkedBeatWithoutFollowingIt(t *testing.T) {
	t.Parallel()
	hermetic(t)
	root := cloneBus(t, bareBus(t))
	v := victim(t, filepath.Dir(root))
	link := filepath.Join(root, "from-ada", BeatName)
	plant(t, v, link)
	discarded, err := DiscardPath(root, "from-ada/"+BeatName)
	require.False(t, err != nil || !discarded, "discarded=%v err=%v, want the symlink removed", discarded, err)
	unchanged(t, v)
	{
		_, err := os.Lstat(link)
		require.False(t, !os.IsNotExist(err), "the symlink is still there: %v", err)
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
		require.NoError(t, err)
	}
	if _, err := FetchAndFastForward(writer, "origin", "main"); err != nil {
		require.NoError(t, err)
	}
	write(t, writer, "from-ada/CURSOR", "UPSTREAM\n")
	if _, err := CommitAndPush(writer, testIdentity["Ada"], []string{"from-ada/CURSOR"}, "cursor moved", "origin", "main", 3); err != nil {
		require.NoError(t, err)
	}
	const sentinel = "SENTINEL-LOCAL do not touch\n"
	write(t, reader, "from-ada/CURSOR", sentinel)
	before, err := HeadCommit(reader)
	require.NoError(t, err)
	_, err = RecoverWaitFastForward(reader, "origin", "main", []string{"from-ada/BEAT"}, time.Now())
	require.False(t, err == nil || !strings.Contains(err.Error(), "from-ada/CURSOR") || !strings.Contains(err.Error(), "not this tool's to discard"), "want a plain refusal naming CURSOR, got %v", err)
	got, err := os.ReadFile(filepath.Join(reader, "from-ada", "CURSOR"))
	require.NoError(t, err)
	require.False(t, string(got) != sentinel, "CURSOR was touched: %q", got)
	after, err := HeadCommit(reader)
	require.NoError(t, err)
	require.Equal(t, before, after, "HEAD moved over a dirty CURSOR: %s -> %s", before, after)
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
		{
			_, skip, err := gitProcFromView(v)
			require.False(t, err != nil || !skip, "a process gone mid-read (%+v): skip=%v err=%v, want skipped and no error", v, skip, err)
		}
	}

	dir, lock := oldIndexLock(t)
	scan := func() ([]gitProc, error) {
		return classifyViews([]procView{{commErr: esrch("comm")}, {comm: "bash\n"}}, 501)
	}
	cleared, err := clearStaleIndexLock(dir, time.Now(), scan)
	require.False(t, err != nil || !cleared, "a stale lock with only a vanishing process in the scan: cleared=%v err=%v, want removed", cleared, err)
	{
		_, statErr := os.Lstat(lock)
		require.False(t, !os.IsNotExist(statErr), "stale index.lock still present: %v", statErr)
	}

	// ESRCH is not a licence for every errno: a live process that denies the read
	// still keeps the lock.
	kept, keptLock := oldIndexLock(t)
	cleared, err = clearStaleIndexLock(kept, time.Now(), func() ([]gitProc, error) {
		return classifyViews([]procView{{comm: "git\n", cmdErr: &fs.PathError{Op: "read", Path: "/proc/1/cmdline", Err: syscall.EACCES}}}, 501)
	})
	assertLockKept(t, keptLock, cleared, err)
}

// The linux twin of TestGitScanSkipsAnotherAccountsGit: /proc/<pid>/cwd of another
// account answers EACCES, which read as a live git of unknown place and refused the
// wait. A view whose status proves another uid is skipped, flagged foreign; our own git
// beside it is placed; the same unreadable view of our own account is still unknown, and
// a view whose account could not be read is unknown too, not skipped.
func TestGitScanSkipsAnotherAccountsGitLinux(t *testing.T) {
	t.Parallel()
	denied := &fs.PathError{Op: "readlink", Path: "/proc/18772/cwd", Err: syscall.EACCES}
	foreign := procView{comm: "git\n", cmdline: []byte("git\x00status"), cwdErr: denied, owner: 502, ownerKnown: true, account: 502, accountKnown: true}
	own := procView{comm: "git\n", cmdline: []byte("git\x00fetch"), cwd: "/home/nova/bus", owner: 501, ownerKnown: true, account: 501, accountKnown: true}
	procs, err := classifyViews([]procView{foreign, own}, 501)
	require.False(t, err != nil || len(placed(procs)) != 1 || placed(procs)[0].cwd != "/home/nova/bus" || len(procs) != 2 || !procs[0].foreign, "another account's git beside our own: procs=%+v err=%v, want our one placed, theirs flagged foreign, and no error", procs, err)
	{
		_, err := classifyViews([]procView{foreign}, 502)
		require.False(t, err == nil || !strings.HasPrefix(err.Error(), ownershipUnknown), "our own git with an unreadable cwd: err=%v, want %q", err, ownershipUnknown)
	}
	unowned := foreign
	unowned.ownerKnown, unowned.accountKnown = false, false
	{
		procs, err := classifyViews([]procView{unowned}, 501)
		require.False(t, err == nil || !strings.HasPrefix(err.Error(), ownershipUnknown) || len(procs) != 1 || !procs[0].unknown, "a git whose account could not be read: procs=%+v err=%v, want one unknown and %q", procs, err, ownershipUnknown)
	}
}

// placed is the gits a scan could place or must still place: the ones that are neither
// flagged foreign nor flagged unknown.
func placed(procs []gitProc) []gitProc {
	var out []gitProc
	for _, p := range procs {
		if !p.foreign && !p.unknown {
			out = append(out, p)
		}
	}
	return out
}

// readProcView through a fake /proc records the owner of every entry, the account its
// status states, and reads the rest the same way whoever owns it. Another account's git
// with an unreadable cwd and no absolute location is skipped, flagged foreign; the same
// git naming a checkout with -C is placed; our own is read in full; an owner stat that
// says the pid is gone is the vanished case; an owner stat with no uid and a status that
// cannot be read leave the account unknown, and the unplaced git is then unknown.
func TestReadProcViewRecordsTheOwner(t *testing.T) {
	t.Parallel()
	denied := &fs.PathError{Op: "readlink", Path: "/proc/18772/cwd", Err: syscall.EACCES}
	fake := func(owner uint32, ok bool, ownerErr error, cmdline string, cwdErr error) procReader {
		return procReader{
			owner: func(string) (uint32, bool, error) { return owner, ok, ownerErr },
			readFile: func(name string) ([]byte, error) {
				if strings.HasSuffix(name, "/comm") {
					return []byte("git\n"), nil
				}
				if strings.HasSuffix(name, "/status") {
					if !ok {
						return nil, &fs.PathError{Op: "open", Path: name, Err: syscall.EACCES}
					}
					return []byte(fmt.Sprintf("Name:\tgit\nUid:\t%d\t%d\t%d\t%d\n", owner, owner, owner, owner)), nil
				}
				return []byte(cmdline), nil
			},
			readlink: func(string) (string, error) {
				if cwdErr != nil {
					return "", cwdErr
				}
				return "/home/nova/bus", nil
			},
		}
	}

	v := readProcView("18772", fake(502, true, nil, "git\x00status", denied))
	require.False(t, !v.ownerKnown || v.owner != 502 || !v.accountKnown || v.account != 502 || strings.TrimSpace(v.comm) != "git" || v.cwdErr == nil, "another account's entry: %+v, want owner and account 502 and comm, cmdline and cwd read", v)
	{
		procs, err := classifyViews([]procView{v}, 501)
		require.False(t, err != nil || len(procs) != 1 || !procs[0].foreign, "another account's unplaced git: procs=%+v err=%v, want one flagged foreign and no error", procs, err)
	}

	v = readProcView("18773", fake(502, true, nil, "git\x00-C\x00/home/glenn/bus\x00commit", denied))
	{
		procs, err := classifyViews([]procView{v}, 501)
		require.False(t, err != nil || len(procs) != 1 || procs[0].command != "git -C /home/glenn/bus commit" || procs[0].foreign, "another account's git naming a checkout with -C: procs=%+v err=%v, want it placed", procs, err)
	}

	v = readProcView("77", fake(501, true, nil, "git\x00status", nil))
	require.False(t, !v.ownerKnown || v.owner != 501 || !v.accountKnown || v.account != 501 || v.cwd != "/home/nova/bus", "our own entry: %+v, want owner and account 501 and cwd read", v)

	gone := &fs.PathError{Op: "stat", Path: "/proc/19050", Err: syscall.ENOENT}
	v = readProcView("19050", fake(0, false, gone, "git\x00status", nil))
	{
		_, skip, err := gitProcFromView(v)
		require.False(t, err != nil || !skip, "a pid gone at the owner stat: skip=%v err=%v, want skipped", skip, err)
	}

	v = readProcView("19051", fake(0, false, nil, "git\x00status", denied))
	{
		procs, err := classifyViews([]procView{v}, 501)
		require.False(t, v.ownerKnown || v.accountKnown || v.accountErr == nil || err == nil || !strings.HasPrefix(err.Error(), ownershipUnknown) || len(procs) != 1 || !procs[0].unknown, "an owner stat with no uid and an unreadable status: view=%+v procs=%+v err=%v, want account unknown and the scan unknown", v, procs, err)
	}
}

// A git of another account that names this checkout's git dir absolutely is an owner
// through the shared view path: the stale lock stays. An unrelated process of another
// account beside the same kind of checkout does not keep it.
func TestForeignGitDirKeepsItsLock(t *testing.T) {
	t.Parallel()
	hermetic(t)
	denied := &fs.PathError{Op: "readlink", Path: "/proc/18772/cwd", Err: syscall.EACCES}
	for _, form := range []string{"split", "joined"} {
		dir, lock := oldIndexLock(t)
		gd, err := GitDir(dir)
		require.NoError(t, err)
		args := []string{"git", "--git-dir", gd, "fetch"}
		if form == "joined" {
			args = []string{"git", "--git-dir=" + gd, "fetch"}
		}
		cleared, cerr := clearStaleIndexLock(dir, time.Now(), func() ([]gitProc, error) {
			return classifyViews([]procView{{owner: 502, ownerKnown: true, comm: "git\n", cmdline: []byte(strings.Join(args, "\x00")), cwdErr: denied}}, 501)
		})
		require.False(t, cleared || cerr != nil, "%s --git-dir of another account: cleared=%v err=%v, want the lock kept as owned", form, cleared, cerr)
		{
			_, statErr := os.Lstat(lock)
			require.Equal(t, nil, statErr, "%s --git-dir: lock lost: %v", form, statErr)
		}
	}

	private, privateLock := oldIndexLock(t)
	cleared, cerr := clearStaleIndexLock(private, time.Now(), func() ([]gitProc, error) {
		return classifyViews([]procView{
			{owner: 502, ownerKnown: true, account: 502, accountKnown: true, comm: "git\n", cmdline: []byte("git\x00status"), cwdErr: denied},
			{owner: 502, ownerKnown: true, account: 502, accountKnown: true, comm: "bash\n"},
		}, 501)
	})
	require.False(t, cerr != nil || !cleared, "unrelated processes of another account beside a private checkout: cleared=%v err=%v, want removed", cleared, cerr)
	{
		_, statErr := os.Lstat(privateLock)
		require.False(t, !os.IsNotExist(statErr), "stale index.lock still present: %v", statErr)
	}
}

// Age first, then the lock file's own owner, then the scan. The owner is supplied
// (lockOwner seam) and the process view is supplied; the lock is a real index.lock in a
// fixture checkout, and the clock is its own mtime plus an offset. (a0) A fresh lock of
// another account, or of unreadable owner: left alone with no error, so the wait goes
// on, and neither the owner nor the scan is asked. (a) A stale lock of another account
// beside another account's unplaced git: refused, naming the uid, and the scan is never
// asked. (b) Our own lock
// beside the same git: cleared, the skip. (c) Our own lock beside another account's git
// that names this checkout with -C: kept as owned. (d) An owner that cannot be read:
// refused.
func TestIndexLockAgeThenOwnerThenScan(t *testing.T) {
	t.Parallel()
	hermetic(t)
	denied := &fs.PathError{Op: "readlink", Path: "/proc/18772/cwd", Err: syscall.EACCES}
	unplaced := procView{owner: 502, ownerKnown: true, account: 502, accountKnown: true, comm: "git\n", cmdline: []byte("git\x00commit"), cwdErr: denied}
	owner := func(uid uint32, ok bool) func(os.FileInfo) (uint32, bool) {
		return func(os.FileInfo) (uint32, bool) { return uid, ok }
	}

	dir, lock := oldIndexLock(t)
	fi, err := os.Lstat(lock)
	require.NoError(t, err)
	fresh := fi.ModTime().Add(staleIndexLockAge - time.Second)
	for _, o := range []struct {
		uid uint32
		ok  bool
	}{{502, true}, {0, false}} {
		cleared, err := clearStaleIndexLockAs(dir, fresh, func() ([]gitProc, error) {
			require.FailNow(t, "(a0) the scan ran for a fresh lock")
			return nil, nil
		}, func(os.FileInfo) (uint32, bool) {
			require.FailNow(t, "(a0) the owner was read for a fresh lock")
			return o.uid, o.ok
		}, 501)
		if cleared || err != nil {
			require.False(t, cleared || err != nil, "(a0) fresh lock, owner %d ok=%v: cleared=%v err=%v, want left alone and no error", o.uid, o.ok, cleared, err)
		}
		{
			_, statErr := os.Lstat(lock)
			require.Equal(t, nil, statErr, "(a0) lock lost: %v", statErr)
		}
	}

	asked := false
	cleared, err := clearStaleIndexLockAs(dir, time.Now(), func() ([]gitProc, error) {
		asked = true
		return classifyViews([]procView{unplaced}, 501)
	}, owner(502, true), 501)
	const foreign = "index.lock is owned by uid 502, not this account; ask its owner or the bench admin"
	require.False(t, cleared || err == nil || err.Error() != foreign || asked, "(a) another account's stale lock: cleared=%v err=%v scanned=%v, want refused with %q and no scan", cleared, err, asked, foreign)
	{
		_, statErr := os.Lstat(lock)
		require.Equal(t, nil, statErr, "(a) lock lost: %v", statErr)
	}

	cleared, err = clearStaleIndexLockAs(dir, time.Now(), func() ([]gitProc, error) {
		return classifyViews([]procView{unplaced}, 501)
	}, owner(501, true), 501)
	require.False(t, err != nil || !cleared, "(b) our own lock beside another account's unplaced git: cleared=%v err=%v, want removed", cleared, err)
	{
		_, statErr := os.Lstat(lock)
		require.False(t, !os.IsNotExist(statErr), "(b) stale index.lock still present: %v", statErr)
	}

	named, namedLock := oldIndexLock(t)
	withC := unplaced
	withC.cmdline = []byte("git\x00-C\x00" + named + "\x00commit")
	cleared, err = clearStaleIndexLockAs(named, time.Now(), func() ([]gitProc, error) {
		return classifyViews([]procView{withC}, 501)
	}, owner(501, true), 501)
	require.False(t, cleared || err != nil, "(c) our own lock, another account's git naming it with -C: cleared=%v err=%v, want kept as owned", cleared, err)
	{
		_, statErr := os.Lstat(namedLock)
		require.Equal(t, nil, statErr, "(c) lock lost: %v", statErr)
	}

	cleared, err = clearStaleIndexLockAs(named, time.Now(), func() ([]gitProc, error) {
		require.FailNow(t, "(d) the scan ran for a lock whose owner could not be read")
		return nil, nil
	}, owner(0, false), 501)
	require.False(t, cleared || err == nil || !strings.HasPrefix(err.Error(), "index.lock owner cannot be read") || !strings.HasSuffix(err.Error(), "ask its owner or the bench admin"), "(d) stale lock, owner unreadable: cleared=%v err=%v, want refused", cleared, err)
	{
		_, statErr := os.Lstat(namedLock)
		require.Equal(t, nil, statErr, "(d) lock lost: %v", statErr)
	}
}

// The real owner reader on the fixture lock: a lock this process wrote is owned by its
// effective uid, so the wait path goes on to the age rule.
func TestIndexLockOwnerIsTheWriter(t *testing.T) {
	t.Parallel()
	hermetic(t)
	_, lock := oldIndexLock(t)
	fi, err := os.Lstat(lock)
	require.NoError(t, err)
	uid, ok := lockFileOwner(fi)
	if !ok || uid != effectiveUID() {
		require.False(t, !ok || uid != effectiveUID(), "lock owner: uid=%d ok=%v, want %d", uid, ok, effectiveUID())
	}
}

// Only the inspected lock is removed. Each case changes the lock path during the supplied
// scan and expects ErrIndexLockChanged with the lock left: (1) a new inode at the path,
// the old one kept aside so the inode cannot be reused; (2) the same inode touched to a
// newer mtime; (3) the same inode read as another owner after the scan. (4) A lock gone
// during the scan is not cleared and is not an error. (5) Unchanged: cleared.
func TestIndexLockRevalidatedBeforeRemove(t *testing.T) {
	t.Parallel()
	hermetic(t)
	self := effectiveUID()
	owners := func(first, later uint32) func(os.FileInfo) (uint32, bool) {
		n := 0
		return func(os.FileInfo) (uint32, bool) {
			n++
			if n == 1 {
				return first, true
			}
			return later, true
		}
	}
	const want = "index.lock changed during the scan; waiting"
	check := func(name string, dir, lock string, scan func() ([]gitProc, error), lockOwner func(os.FileInfo) (uint32, bool), keep []byte) {
		t.Helper()
		cleared, err := clearStaleIndexLockAs(dir, time.Now(), scan, lockOwner, self)
		require.False(t, cleared || !errors.Is(err, ErrIndexLockChanged) || err.Error() != want, "%s: cleared=%v err=%v, want %q", name, cleared, err, want)
		got, readErr := os.ReadFile(lock)
		require.False(t, readErr != nil || string(got) != string(keep), "%s: lock at the path lost: %v %q", name, readErr, got)
	}

	dir, lock := oldIndexLock(t)
	check("(1) replaced", dir, lock, func() ([]gitProc, error) {
		require.NoError(t, os.Rename(lock, lock+".old"))
		return nil, os.WriteFile(lock, []byte("new"), 0o600)
	}, lockFileOwner, []byte("new"))

	dir, lock = oldIndexLock(t)
	check("(2) touched", dir, lock, func() ([]gitProc, error) {
		fi, err := os.Lstat(lock)
		require.NoError(t, err)
		later := fi.ModTime().Add(time.Second)
		return nil, os.Chtimes(lock, later, later)
	}, lockFileOwner, nil)

	dir, lock = oldIndexLock(t)
	check("(3) owner changed", dir, lock, func() ([]gitProc, error) { return nil, nil }, owners(self, self+1), nil)

	dir, lock = oldIndexLock(t)
	cleared, err := clearStaleIndexLockAs(dir, time.Now(), func() ([]gitProc, error) {
		return nil, os.Remove(lock)
	}, lockFileOwner, self)
	require.False(t, cleared || err != nil, "(4) gone during the scan: cleared=%v err=%v, want not cleared and no error", cleared, err)

	dir, lock = oldIndexLock(t)
	cleared, err = clearStaleIndexLockAs(dir, time.Now(), func() ([]gitProc, error) { return nil, nil }, lockFileOwner, self)
	require.False(t, err != nil || !cleared, "(5) unchanged: cleared=%v err=%v, want removed", cleared, err)
	{
		_, statErr := os.Lstat(lock)
		require.False(t, !os.IsNotExist(statErr), "(5) lock still present: %v", statErr)
	}
}
