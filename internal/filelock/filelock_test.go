//go:build unix || windows

package filelock

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTryLock_Success(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.lock")

	lock, err := TryLock(path, "worker-1")
	require.NoError(t, err, "TryLock failed: %v", err)
	defer lock.Unlock()

	// The file names its holder (tla/FileLock.tla, HolderIsNamed).
	st, err := ReadStamp(path)
	require.NoError(t, err)
	assert.Equal(t, "worker-1", st.Label, "the held file's label")
	assert.Equal(t, os.Getpid(), st.PID, "the held file's pid")

	// Verify file exists on disk and is non-empty while held
	fi, err := os.Stat(path)
	require.NoError(t, err, "Stat failed: %v", err)
	assert.Positive(t, fi.Size(), "lock file size is 0 while held, want > 0")
}

func TestTryLock_Held(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "held.lock")

	lock1, err := TryLock(path, "holder")
	require.NoError(t, err, "first TryLock failed: %v", err)
	defer lock1.Unlock()

	lock2, err := TryLock(path, "contender")
	assert.ErrorIs(t, err, ErrHeld, "second TryLock error = %v, want ErrHeld", err)
	lock2.Unlock() // nil-safe: releases a mutant lock, no-ops on nil
	require.Nil(t, lock2, "second TryLock succeeded, want refusal")

	heldErr, ok := AsHeldError(err)
	require.True(t, ok, "expected *HeldError, got %T: %v", err, err)
	assert.Equal(t, "holder", heldErr.Holder.Label, "heldErr.Holder.Label = %q, want holder", heldErr.Holder.Label)
	assert.Equal(t, os.Getpid(), heldErr.Holder.PID, "heldErr.Holder.PID = %d, want %d", heldErr.Holder.PID, os.Getpid())
}

func TestLock_Success(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "lock_success.lock")

	lock, err := Lock(path, "winner", time.Second)
	require.NoError(t, err, "Lock failed: %v", err)
	defer lock.Unlock()
}

func TestLock_TimeoutBound(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "timeout.lock")

	lock1, err := TryLock(path, "first")
	require.NoError(t, err, "TryLock failed: %v", err)
	defer lock1.Unlock()

	clk := newLockStepClock(time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC))
	timeout := 100 * time.Millisecond
	opts := options{
		clock:        clk,
		pollInterval: 10 * time.Millisecond,
	}

	lock2, err := lockWithOptions(path, "second", timeout, opts)
	lock2.Unlock() // nil-safe: releases a mutant lock, no-ops on nil
	require.Nil(t, lock2, "lockWithOptions succeeded unexpectedly while held")
	require.ErrorIs(t, err, ErrTimeout, "err = %v, want ErrTimeout", err)
	// H2: Lock timeout must also match ErrHeld and report Holder and Wait
	assert.ErrorIs(t, err, ErrHeld, "err = %v, want errors.Is(err, ErrHeld) to be true on timeout", err)
	heldErr, ok := AsHeldError(err)
	require.True(t, ok, "expected *HeldError, got %T: %v", err, err)
	assert.True(t, heldErr.TimedOut, "heldErr.TimedOut = false, want true")
	assert.Equal(t, "first", heldErr.Holder.Label, "heldErr.Holder.Label = %q, want first", heldErr.Holder.Label)
	assert.GreaterOrEqual(t, heldErr.Wait, timeout, "heldErr.Wait = %v, want >= %v", heldErr.Wait, timeout)

	waited := clk.Waited()
	assert.GreaterOrEqual(t, waited, timeout, "virtual clock waited %v, want >= %v", waited, timeout)
}

func TestH1_ContendedTakerVsSharedProbe_ReturnsErrBusy(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "probe_busy.lock")

	// Create the file first so a prober can open it
	initLock, err := TryLock(path, "init")
	require.NoError(t, err, "init TryLock failed: %v", err)
	require.NoError(t, initLock.Unlock(), "init Unlock failed")

	// Open file and take SHARED lock (an asker: another refused taker asking)
	f, err := openFileSafe(path, os.O_RDWR, 0)
	require.NoError(t, err, "openFileSafe failed: %v", err)
	defer f.Close()

	shOk, shErr := trySharedLock(f)
	require.True(t, shOk && shErr == nil, "trySharedLock failed: ok=%v, err=%v", shOk, shErr)
	defer unlockFile(f)

	// An exclusive taker tries to take the lock
	_, tryErr := TryLock(path, "taker")
	require.Error(t, tryErr, "TryLock succeeded unexpectedly while shared lock held")

	// H1: Must return ErrBusy, and must NOT wrap ErrHeld
	assert.ErrorIs(t, tryErr, ErrBusy, "tryErr = %v, want ErrBusy", tryErr)
	assert.NotErrorIs(t, tryErr, ErrHeld, "tryErr wraps ErrHeld, want only ErrBusy when an asker is in the way")

	// Unlock shared lock
	unlockFile(f)

	// Now TryLock succeeds
	takerLock, err := TryLock(path, "taker")
	require.NoError(t, err, "TryLock failed after shared lock released: %v", err)
	defer takerLock.Unlock()
}

func TestMutant_LastSleepCappedAtRemaining(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "cap_mutant.lock")

	lock1, err := TryLock(path, "holder")
	require.NoError(t, err, "TryLock failed: %v", err)
	defer lock1.Unlock()

	clk := newLockStepClock(time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC))
	timeout := 50 * time.Millisecond
	opts := options{
		clock:        clk,
		pollInterval: 30 * time.Millisecond,
		jitter: func(d time.Duration) time.Duration {
			return 30 * time.Millisecond // constant 30ms sleep request
		},
	}

	// Step 1: remaining = 50ms, sleep = 30ms.
	// Step 2: remaining = 20ms. If uncapped, sleeps 30ms (waited = 60ms).
	// With cap, sleeps 20ms (waited = 50ms).
	_, err2 := lockWithOptions(path, "waiter", timeout, opts)
	require.ErrorIs(t, err2, ErrTimeout, "err2 = %v, want ErrTimeout", err2)

	waited := clk.Waited()
	assert.Equal(t, 50*time.Millisecond, waited, "clk.Waited() = %v, want exactly 50ms (last sleep must be capped at remaining)", waited)
}

func TestLock_AcquiresAfterRelease(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "sequential.lock")

	lock1, err := Lock(path, "first", time.Second)
	require.NoError(t, err, "first Lock failed: %v", err)
	require.NoError(t, lock1.Unlock(), "first Unlock failed")

	lock2, err := Lock(path, "second", time.Second)
	require.NoError(t, err, "second Lock failed: %v", err)
	defer lock2.Unlock()
}

func TestUnlock_IdempotentAndNeverDeletes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "keep_file.lock")

	lock, err := TryLock(path, "temp")
	require.NoError(t, err, "TryLock failed: %v", err)

	require.NoError(t, lock.Unlock(), "first Unlock failed")
	assert.NoError(t, lock.Unlock(), "second Unlock failed")

	var nilLock *FileLock
	assert.NoError(t, nilLock.Unlock(), "nilLock.Unlock() = %v, want nil")

	// File MUST NOT be deleted!
	fi, err := os.Stat(path)
	require.NoError(t, err, "lock file missing after Unlock: %v", err)
	// File must be truncated to zero bytes
	assert.Zero(t, fi.Size(), "lock file size after Unlock = %d, want 0", fi.Size())
}

// A holder that died holding leaves its note; the next taker writes its own over
// it, so the file names its holder (tla/FileLock.tla, HolderIsNamed).
func TestTryLock_OverwritesAnUnreleasedNote(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "crash.lock")

	unreleased := Stamp{
		PID:     9999,
		Host:    "crashed-host",
		Started: time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC),
		Label:   "crashed-worker",
	}
	if err := os.WriteFile(path, []byte(unreleased.Format()+"\n"), 0666); err != nil {
		require.NoError(t, err, "WriteFile failed: %v", err)
	}

	lock, err := TryLock(path, "recovery-worker")
	require.NoError(t, err, "TryLock failed: %v", err)
	defer lock.Unlock()

	st, err := ReadStamp(path)
	require.NoError(t, err)
	assert.Equal(t, "recovery-worker", st.Label, "the file still names the dead holder: %+v", st)
	assert.Equal(t, os.Getpid(), st.PID, "the file still names the dead holder: %+v", st)
}

func TestSymlink_NotPermitted(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("target"), 0666); err != nil {
		require.NoError(t, err, "WriteFile failed: %v", err)
	}
	link := filepath.Join(dir, "link.lock")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not supported in this environment: %v", err)
	}

	_, err := TryLock(link, "test")
	assert.Error(t, err, "TryLock on symlink succeeded, want error")
}

func TestDirectory_NotPermitted(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	_, err := TryLock(dir, "test")
	assert.Error(t, err, "TryLock on directory succeeded, want error")
}

func TestPathEscapingInErrors(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "path with spaces.lock")

	lock1, err := TryLock(path, "holder label")
	require.NoError(t, err, "TryLock failed: %v", err)
	defer lock1.Unlock()

	_, err = TryLock(path, "second")
	require.Error(t, err, "expected error on held lock")

	errStr := err.Error()
	assert.Contains(t, errStr, `"`, "error string %q does not quote path with %%q", errStr)
}

func TestStamp_FormatsAndParses(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 27, 18, 30, 0, 123456789, time.UTC)
	s := Stamp{
		PID:     12345,
		Host:    "mac-studio",
		Started: now,
		Label:   "worker-alpha",
	}

	formatted := s.Format()
	parsed, err := ParseStamp(formatted)
	require.NoError(t, err, "ParseStamp failed: %v", err)
	assert.Equal(t, s.PID, parsed.PID, "parsed.PID = %d, want %d", parsed.PID, s.PID)
	assert.Equal(t, s.Host, parsed.Host, "parsed.Host = %q, want %q", parsed.Host, s.Host)
	assert.True(t, parsed.Started.Equal(s.Started), "parsed.Started = %v, want %v", parsed.Started, s.Started)
	assert.Equal(t, s.Label, parsed.Label, "parsed.Label = %q, want %q", parsed.Label, s.Label)

	// Single line string format
	str := s.String()
	parsedSingle, err := ParseStamp(str)
	require.NoError(t, err, "ParseStamp(s.String()) failed: %v", err)
	assert.True(t, parsedSingle.PID == s.PID && parsedSingle.Host == s.Host && parsedSingle.Label == s.Label, "parsedSingle = %+v, unexpected", parsedSingle)
}

func TestReadStamp(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "read_stamp.lock")

	lock, err := TryLock(path, "read-test")
	require.NoError(t, err, "TryLock failed: %v", err)
	defer lock.Unlock()

	stamp, err := ReadStamp(path)
	require.NoError(t, err, "ReadStamp failed: %v", err)
	assert.Equal(t, "read-test", stamp.Label, "stamp.Label = %q, want read-test", stamp.Label)
	assert.Equal(t, os.Getpid(), stamp.PID, "stamp.PID = %d, want %d", stamp.PID, os.Getpid())
}

func TestOptionsDefaults(t *testing.T) {
	t.Parallel()

	opts := options{}
	assert.Equal(t, defaultPollInterval, opts.getPollInterval(), "pollInterval = %v, want %v", opts.getPollInterval(), defaultPollInterval)
	assert.Equal(t, os.Getpid(), opts.getPID(), "pid = %d, want %d", opts.getPID(), os.Getpid())
	assert.GreaterOrEqual(t, opts.getJitter(100*time.Millisecond), 100*time.Millisecond, "jitter < base")
	assert.Zero(t, opts.getJitter(0), "jitter(0) != 0")
	assert.NotNil(t, opts.getClock(), "clock is nil")

	customOpts := options{
		host: "custom-host",
		pid:  777,
		jitter: func(d time.Duration) time.Duration {
			return d * 2
		},
	}
	assert.Equal(t, "custom-host", customOpts.getHost(), "host() = %q, want custom-host", customOpts.getHost())
	assert.Equal(t, 777, customOpts.getPID(), "pid() = %d, want 777", customOpts.getPID())
	assert.Equal(t, 20*time.Millisecond, customOpts.getJitter(10*time.Millisecond), "custom jitter failed")
}

func TestHeldErrorFormatting(t *testing.T) {
	t.Parallel()

	e1 := &HeldError{Path: "lockfile.lock"}
	assert.Contains(t, e1.Error(), "is held", "e1.Error() = %q", e1.Error())

	e2 := &HeldError{Path: "lockfile.lock", Wait: 2 * time.Second}
	assert.Contains(t, e2.Error(), "waited 2s", "e2.Error() = %q", e2.Error())

	holder := Stamp{PID: 1234, Label: "my-holder"}
	e3 := &HeldError{Path: "lockfile.lock", Holder: holder, Wait: 5 * time.Second}
	assert.True(t, strings.Contains(e3.Error(), "waited 5s") && strings.Contains(e3.Error(), "my-holder"), "e3.Error() = %q", e3.Error())
}

func TestParseStamp_SpecialCases(t *testing.T) {
	t.Parallel()

	empty, err := ParseStamp("")
	assert.True(t, err == nil && empty.IsZero(), "ParseStamp empty = %+v, %v", empty, err)

	dash, err := ParseStamp("-")
	assert.True(t, err == nil && dash.IsZero(), "ParseStamp dash = %+v, %v", dash, err)

	barePID, err := ParseStamp("9876\n")
	assert.True(t, err == nil && barePID.PID == 9876, "ParseStamp barePID = %+v, %v", barePID, err)

	multiWithEmptyLines := "pid=456\n\nhost=box\nstarted=2026-09-27T12:00:00Z\nlabel=foo\n"
	st, err := ParseStamp(multiWithEmptyLines)
	assert.True(t, err == nil && st.PID == 456 && st.Host == "box" && st.Label == "foo", "ParseStamp multi = %+v, %v", st, err)

	singleLine := "pid=789 host=node1 started=2026-09-27T12:00:00Z label=bar"
	stSingle, err := ParseStamp(singleLine)
	assert.True(t, err == nil && stSingle.PID == 789 && stSingle.Host == "node1" && stSingle.Label == "bar", "ParseStamp single = %+v, %v", stSingle, err)
}

func TestHelpers_NilAndErrors(t *testing.T) {
	t.Parallel()

	ok, err := tryLockFile(nil)
	assert.False(t, ok, "tryLockFile(nil) = %v, %v", ok, err)
	assert.Error(t, err, "tryLockFile(nil) = %v, %v", ok, err)

	ok, err = trySharedLock(nil)
	assert.False(t, ok, "trySharedLock(nil) = %v, %v", ok, err)
	assert.Error(t, err, "trySharedLock(nil) = %v, %v", ok, err)
	unlockFile(nil) // should not panic

	empty := readExistingStamp(nil)
	assert.True(t, empty.IsZero(), "readExistingStamp(nil) = %+v, want zero", empty)

	_, err = ReadStamp(filepath.Join(t.TempDir(), "nonexistent"))
	assert.Error(t, err, "ReadStamp nonexistent should error")

	clk := newLockStepClock(time.Time{})
	assert.False(t, clk.Now().IsZero(), "newLockStepClock(zero) returned zero time")
}

func TestRealClock(t *testing.T) {
	t.Parallel()

	rc := realClock{}
	require.False(t, rc.Now().IsZero(), "realClock.Now() is zero")
	rc.Sleep(0)
}

func TestTryLock_NotDirParent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	regularFile := filepath.Join(dir, "regular.txt")
	if err := os.WriteFile(regularFile, []byte("file"), 0666); err != nil {
		require.NoError(t, err, err)
	}
	badPath := filepath.Join(regularFile, "sub.lock")

	_, err := TryLock(badPath, "bad")
	assert.Error(t, err, "TryLock with non-directory parent should error")
}

// H1 witness. An asker (another refused taker asking) holds the shared
// lock for an instant, and the kernel refuses the exclusive lock while it does.
// The asker here never leaves, so every take lands in that instant. Nobody
// holds, so the taker must answer busy (ErrBusy) and never held (ErrHeld):
// tla/FileLock.tla, HeldIsTrue, kept by the Blocked action's shared re-ask.
func TestTryLock_AskerIsNotAHolder(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "asker.lock")
	require.NoError(t, os.WriteFile(path, nil, 0666))
	asker, err := os.OpenFile(path, os.O_RDWR, 0)
	require.NoError(t, err, err)
	defer asker.Close()
	ok, err := trySharedLock(asker)
	require.True(t, ok && err == nil, "asker's shared lock = %v, %v, want granted", ok, err)

	lock, err := TryLock(path, "taker")
	lock.Unlock() // nil-safe: releases a mutant lock, no-ops on nil
	require.Nil(t, lock, "TryLock succeeded while a shared lock was held")
	assert.NotErrorIs(t, err, ErrHeld, "told held with nobody holding: %v", err)
	_, isHeld := AsHeldError(err)
	assert.False(t, isHeld, "a *HeldError with nobody holding: %v", err)
	assert.ErrorIs(t, err, ErrBusy, "err = %v, want ErrBusy", err)

	// A bounded Lock kept out by the asker alone runs out as busy, not held.
	clk := newLockStepClock(time.Time{})
	lock, err = lockWithOptions(path, "waiter", 50*time.Millisecond, options{clock: clk})
	lock.Unlock() // nil-safe: releases a mutant lock, no-ops on nil
	require.Nil(t, lock, "Lock succeeded while a shared lock was held")
	assert.True(t, !errors.Is(err, ErrHeld) && errors.Is(err, ErrBusy) && errors.Is(err, ErrTimeout), "bounded Lock against an asker: err = %v, want ErrTimeout and ErrBusy, never ErrHeld", err)

	// The asker leaves; the next take is granted.
	unlockFile(asker)
	lock, err = TryLock(path, "taker")
	require.NoError(t, err, "TryLock after the asker left: %v", err)
	lock.Unlock()
}

// H2 witness, the bound on the virtual clock. A bounded Lock against a holder
// never waits past its bound, and when it runs out it names the holder: a
// *HeldError carrying the note read on the last refusal and the bound as Wait,
// answering both ErrHeld and ErrTimeout. internal/merge (AsHeldError, exit 2),
// internal/bus (the pid in the refusal) and internal/tokens (HolderPID) all
// name the holder on a run-out.
func TestLock_RunOutNamesTheHolder(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "runout.lock")
	holder, err := TryLock(path, "holder")
	require.NoError(t, err, err)
	defer holder.Unlock()

	for _, bound := range []time.Duration{0, 1, time.Millisecond, 10 * time.Millisecond, 200 * time.Millisecond, time.Second} {
		clk := newLockStepClock(time.Time{})
		lock, err := lockWithOptions(path, "waiter", bound, options{clock: clk})
		lock.Unlock() // nil-safe: releases a mutant lock, no-ops on nil
		require.Nil(t, lock, "bound %s: acquired a held lock", bound)
		w := clk.Waited()
		assert.LessOrEqual(t, w, bound, "bound %s exceeded: waited %s", bound, w)
		he, ok := AsHeldError(err)
		if !assert.True(t, ok, "bound %s: the run-out does not name the holder: %v", bound, err) {
			continue
		}
		assert.True(t, he.Holder.Label == "holder" && he.Holder.PID == os.Getpid(), "bound %s: holder = %s, want pid=%d label=\"holder\"", bound, he.Holder, os.Getpid())
		assert.Equal(t, bound, he.Wait, "bound %s: Wait = %s, want the bound", bound, he.Wait)
		assert.True(t, errors.Is(err, ErrHeld) && errors.Is(err, ErrTimeout), "bound %s: errors.Is held=%v timeout=%v, want both: %v", bound, errors.Is(err, ErrHeld), errors.Is(err, ErrTimeout), err)
	}
}

// H2 witness: HeldError.Wait is set by the package on a run-out, and printed.
func TestLock_RunOutSetsWait(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "wait.lock")
	holder, err := TryLock(path, "holder")
	require.NoError(t, err, err)
	defer holder.Unlock()

	_, err = lockWithOptions(path, "waiter", 50*time.Millisecond, options{clock: newLockStepClock(time.Time{})})
	he, ok := AsHeldError(err)
	require.True(t, ok, "a bounded Lock that ran out against a holder is not a *HeldError: %v", err)
	assert.Equal(t, 50*time.Millisecond, he.Wait, "HeldError.Wait = %s after a 50ms bound ran out", he.Wait)
	assert.Contains(t, err.Error(), "waited 50ms", "run-out text does not say the wait: %q", err.Error())
}

func TestMutant_Fsync(t *testing.T) {
	t.Parallel()

	// 1. defaultSync on a closed file descriptor MUST return an error.
	// If defaultSync was mutated to a dummy `return nil`, this fails!
	f, err := os.CreateTemp(t.TempDir(), "fsync-test")
	require.NoError(t, err, err)
	_ = f.Close()
	require.Error(t, defaultSync(f), "defaultSync on closed file descriptor returned nil, want error")

	// 2. Injected sync error in tryLockWithOptions must abort, cleanup, and return error.
	dir := t.TempDir()
	path := filepath.Join(dir, "sync_err.lock")
	syncErr := errors.New("simulated disk sync error")
	opts := options{
		sync: func(f *os.File) error {
			return syncErr
		},
	}
	lock, err := tryLockWithOptions(path, "fsync-fail", opts)
	lock.Unlock() // nil-safe: releases a mutant lock, no-ops on nil
	require.Nil(t, lock, "tryLockWithOptions succeeded despite sync error")
	require.ErrorIs(t, err, syncErr, "err = %v, want syncErr", err)

	// 3. Verify sync is called on successful lock. The take below also shows the
	// failed one let go of the kernel lock: a lock still held would refuse it.
	syncCalled := false
	optsSuccess := options{
		sync: func(f *os.File) error {
			syncCalled = true
			return f.Sync()
		},
	}
	successLock, err := tryLockWithOptions(path, "fsync-ok", optsSuccess)
	require.NoError(t, err, "tryLockWithOptions failed: %v", err)
	require.True(t, syncCalled, "sync was not called during successful tryLockWithOptions")
	_ = successLock.Unlock()
}

func TestMutant_Jitter(t *testing.T) {
	t.Parallel()

	// 1. defaultJitter must have spread and produce values > base duration
	d := 100 * time.Millisecond
	seen := make(map[time.Duration]bool)
	hasGreater := false
	for i := 0; i < 200; i++ {
		j := defaultJitter(d)
		seen[j] = true
		if j > d {
			hasGreater = true
		}
	}
	require.True(t, hasGreater, "defaultJitter never produced a duration > d (mutant: return d)")
	require.GreaterOrEqual(t, len(seen), 5, "defaultJitter produced only %d distinct values across 200 iterations (want >= 5)", len(seen))

	// 2. lockLoop backoff must invoke opts.jitter
	dir := t.TempDir()
	path := filepath.Join(dir, "jitter_hook.lock")
	held, err := TryLock(path, "holder")
	require.NoError(t, err, err)
	defer held.Unlock()

	jitterInvoked := false
	clk := newLockStepClock(time.Time{})
	opts := options{
		clock: clk,
		jitter: func(base time.Duration) time.Duration {
			jitterInvoked = true
			return base
		},
	}
	_, _ = lockWithOptions(path, "waiter", 50*time.Millisecond, opts)
	require.True(t, jitterInvoked, "opts.jitter was never invoked during lockLoop backoff (mutant: jitter removed)")
}

func TestTryLock_DedicatedPathTruncates(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "data_file.lock")

	precious := []byte("critical initial data that should be truncated when taking lock\n")
	if err := os.WriteFile(path, precious, 0666); err != nil {
		require.NoError(t, err, err)
	}

	lock, err := TryLock(path, "dedicated-taker")
	require.NoError(t, err, "TryLock on existing file failed: %v", err)
	defer lock.Unlock()

	data, err := os.ReadFile(path)
	require.NoError(t, err, "ReadFile failed: %v", err)
	require.NotEqual(t, string(precious), string(data), "TryLock did not truncate existing file contents")
	require.Contains(t, string(data), "label=dedicated-taker", "file content does not contain new stamp: %s", string(data))
}

func TestCappedHostileLabelAndPathSanitization(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "huge_label.lock")

	bigLabel := strings.Repeat("X", 1024*1024) // 1 MB label
	lock, err := TryLock(path, bigLabel)
	require.NoError(t, err, "TryLock with 1MB label failed: %v", err)
	defer lock.Unlock()

	st, err := ReadStamp(path)
	require.NoError(t, err)
	require.LessOrEqual(t, len(st.Label), 1024, "the stamp's label length = %d, want <= 1024", len(st.Label))

	// Second taker fails with HeldError
	_, herr := TryLock(path, "second")
	require.Error(t, herr, "second TryLock succeeded, want HeldError")
	require.LessOrEqual(t, len(herr.Error()), 4096, "HeldError.Error() length = %d, want <= 4096", len(herr.Error()))
	// Error string must not contain control characters
	for i := 0; i < len(herr.Error()); i++ {
		c := herr.Error()[i]
		require.False(t, c < 0x20 || c == 0x7f, "HeldError.Error() contains control char 0x%02x: %q", c, herr.Error())
	}

	// Safe path error wrapping check
	badDir := filepath.Join(dir, "newline\nin\npath")
	if err := os.Mkdir(badDir, 0755); err == nil {
		badLock := filepath.Join(badDir, "l.lock")
		l, err := TryLock(badLock, "test")
		if err == nil {
			defer l.Unlock()
			_, err2 := TryLock(badLock, "test2")
			if err2 != nil {
				for i := 0; i < len(err2.Error()); i++ {
					c := err2.Error()[i]
					require.False(t, c < 0x20 || c == 0x7f, "error text contains unescaped control char 0x%02x: %q", c, err2.Error())
				}
			}
		}
	}
}
