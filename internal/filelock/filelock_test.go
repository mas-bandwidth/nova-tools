//go:build unix || windows

package filelock

import (
	"errors"
	"fmt"
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
	if err != nil {
		require.NoError(t, err, "TryLock failed: %v", err)
	}
	defer lock.Unlock()

	// The file names its holder (tla/FileLock.tla, HolderIsNamed).
	st, err := ReadStamp(path)
	require.NoError(t, err)
	assert.Equal(t, "worker-1", st.Label, "the held file's label")
	assert.Equal(t, os.Getpid(), st.PID, "the held file's pid")

	// Verify file exists on disk and is non-empty while held
	fi, err := os.Stat(path)
	if err != nil {
		require.NoError(t, err, "Stat failed: %v", err)
	}
	if fi.Size() == 0 {
		assert.Fail(t, fmt.Sprintf("lock file size is 0 while held, want > 0"))
	}
}

func TestTryLock_Held(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "held.lock")

	lock1, err := TryLock(path, "holder")
	if err != nil {
		require.NoError(t, err, "first TryLock failed: %v", err)
	}
	defer lock1.Unlock()

	lock2, err := TryLock(path, "contender")
	if lock2 != nil {
		lock2.Unlock()
		require.Fail(t, fmt.Sprintf("second TryLock succeeded, want refusal"))
	}
	if !errors.Is(err, ErrHeld) {
		assert.ErrorIs(t, err, ErrHeld, "second TryLock error = %v, want ErrHeld", err)
	}

	heldErr, ok := AsHeldError(err)
	if !ok {
		require.True(t, ok, "expected *HeldError, got %T: %v", err, err)
	}
	if heldErr.Holder.Label != "holder" {
		assert.Equal(t, "holder", heldErr.Holder.Label, "heldErr.Holder.Label = %q, want holder", heldErr.Holder.Label)
	}
	if heldErr.Holder.PID != os.Getpid() {
		assert.Fail(t, fmt.Sprintf("heldErr.Holder.PID = %d, want %d", heldErr.Holder.PID, os.Getpid()))
	}
}

func TestLock_Success(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "lock_success.lock")

	lock, err := Lock(path, "winner", time.Second)
	if err != nil {
		require.NoError(t, err, "Lock failed: %v", err)
	}
	defer lock.Unlock()
}

func TestLock_TimeoutBound(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "timeout.lock")

	lock1, err := TryLock(path, "first")
	if err != nil {
		require.NoError(t, err, "TryLock failed: %v", err)
	}
	defer lock1.Unlock()

	clk := newLockStepClock(time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC))
	timeout := 100 * time.Millisecond
	opts := options{
		clock:        clk,
		pollInterval: 10 * time.Millisecond,
	}

	lock2, err := lockWithOptions(path, "second", timeout, opts)
	if lock2 != nil {
		lock2.Unlock()
		require.Fail(t, fmt.Sprintf("lockWithOptions succeeded unexpectedly while held"))
	}
	if !errors.Is(err, ErrTimeout) {
		require.ErrorIs(t, err, ErrTimeout, "err = %v, want ErrTimeout", err)
	}
	// H2: Lock timeout must also match ErrHeld and report Holder and Wait
	if !errors.Is(err, ErrHeld) {
		assert.ErrorIs(t, err, ErrHeld, "err = %v, want errors.Is(err, ErrHeld) to be true on timeout", err)
	}
	heldErr, ok := AsHeldError(err)
	if !ok {
		require.True(t, ok, "expected *HeldError, got %T: %v", err, err)
	}
	if !heldErr.TimedOut {
		assert.True(t, heldErr.TimedOut, "heldErr.TimedOut = false, want true")
	}
	if heldErr.Holder.Label != "first" {
		assert.Equal(t, "first", heldErr.Holder.Label, "heldErr.Holder.Label = %q, want first", heldErr.Holder.Label)
	}
	if heldErr.Wait < timeout {
		assert.GreaterOrEqual(t, heldErr.Wait, timeout, "heldErr.Wait = %v, want >= %v", heldErr.Wait, timeout)
	}

	waited := clk.Waited()
	if waited < timeout {
		assert.GreaterOrEqual(t, waited, timeout, "virtual clock waited %v, want >= %v", waited, timeout)
	}
}

func TestH1_ContendedTakerVsSharedProbe_ReturnsErrBusy(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "probe_busy.lock")

	// Create the file first so a prober can open it
	initLock, err := TryLock(path, "init")
	if err != nil {
		require.NoError(t, err, "init TryLock failed: %v", err)
	}
	if err := initLock.Unlock(); err != nil {
		require.NoError(t, err, "init Unlock failed: %v", err)
	}

	// Open file and take SHARED lock (an asker: another refused taker asking)
	f, err := openFileSafe(path, os.O_RDWR, 0)
	if err != nil {
		require.NoError(t, err, "openFileSafe failed: %v", err)
	}
	defer f.Close()

	shOk, shErr := trySharedLock(f)
	if !shOk || shErr != nil {
		require.Fail(t, fmt.Sprintf("trySharedLock failed: ok=%v, err=%v", shOk, shErr))
	}
	defer unlockFile(f)

	// An exclusive taker tries to take the lock
	_, tryErr := TryLock(path, "taker")
	if tryErr == nil {
		require.Error(t, tryErr, "TryLock succeeded unexpectedly while shared lock held")
	}

	// H1: Must return ErrBusy, and must NOT wrap ErrHeld
	if !errors.Is(tryErr, ErrBusy) {
		assert.ErrorIs(t, tryErr, ErrBusy, "tryErr = %v, want ErrBusy", tryErr)
	}
	if errors.Is(tryErr, ErrHeld) {
		assert.Fail(t, fmt.Sprintf("tryErr wraps ErrHeld, want only ErrBusy when an asker is in the way"))
	}

	// Unlock shared lock
	unlockFile(f)

	// Now TryLock succeeds
	takerLock, err := TryLock(path, "taker")
	if err != nil {
		require.NoError(t, err, "TryLock failed after shared lock released: %v", err)
	}
	defer takerLock.Unlock()
}

func TestMutant_LastSleepCappedAtRemaining(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "cap_mutant.lock")

	lock1, err := TryLock(path, "holder")
	if err != nil {
		require.NoError(t, err, "TryLock failed: %v", err)
	}
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
	if !errors.Is(err2, ErrTimeout) {
		require.ErrorIs(t, err2, ErrTimeout, "err2 = %v, want ErrTimeout", err2)
	}

	waited := clk.Waited()
	if waited != 50*time.Millisecond {
		assert.Equal(t, 50*time.Millisecond, waited, "clk.Waited() = %v, want exactly 50ms (last sleep must be capped at remaining)", waited)
	}
}

func TestLock_AcquiresAfterRelease(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "sequential.lock")

	lock1, err := Lock(path, "first", time.Second)
	if err != nil {
		require.NoError(t, err, "first Lock failed: %v", err)
	}
	if err := lock1.Unlock(); err != nil {
		require.NoError(t, err, "first Unlock failed: %v", err)
	}

	lock2, err := Lock(path, "second", time.Second)
	if err != nil {
		require.NoError(t, err, "second Lock failed: %v", err)
	}
	defer lock2.Unlock()
}

func TestUnlock_IdempotentAndNeverDeletes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "keep_file.lock")

	lock, err := TryLock(path, "temp")
	if err != nil {
		require.NoError(t, err, "TryLock failed: %v", err)
	}

	if err := lock.Unlock(); err != nil {
		require.NoError(t, err, "first Unlock failed: %v", err)
	}
	if err := lock.Unlock(); err != nil {
		assert.NoError(t, err, "second Unlock failed: %v", err)
	}

	var nilLock *FileLock
	if err := nilLock.Unlock(); err != nil {
		assert.NoError(t, err, "nilLock.Unlock() = %v, want nil", err)
	}

	// File MUST NOT be deleted!
	fi, err := os.Stat(path)
	if err != nil {
		require.NoError(t, err, "lock file missing after Unlock: %v", err)
	}
	// File must be truncated to zero bytes
	if fi.Size() != 0 {
		assert.Fail(t, fmt.Sprintf("lock file size after Unlock = %d, want 0", fi.Size()))
	}
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
	if err != nil {
		require.NoError(t, err, "TryLock failed: %v", err)
	}
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

	if _, err := TryLock(link, "test"); err == nil {
		assert.Error(t, err, "TryLock on symlink succeeded, want error")
	}
}

func TestDirectory_NotPermitted(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	if _, err := TryLock(dir, "test"); err == nil {
		assert.Error(t, err, "TryLock on directory succeeded, want error")
	}
}

func TestPathEscapingInErrors(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "path with spaces.lock")

	lock1, err := TryLock(path, "holder label")
	if err != nil {
		require.NoError(t, err, "TryLock failed: %v", err)
	}
	defer lock1.Unlock()

	_, err = TryLock(path, "second")
	if err == nil {
		require.Error(t, err, "expected error on held lock")
	}

	errStr := err.Error()
	if !strings.Contains(errStr, `"`) {
		assert.Contains(t, errStr, `"`, "error string %q does not quote path with %%q", errStr)
	}
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
	if err != nil {
		require.NoError(t, err, "ParseStamp failed: %v", err)
	}

	if parsed.PID != s.PID {
		assert.Equal(t, s.PID, parsed.PID, "parsed.PID = %d, want %d", parsed.PID, s.PID)
	}
	if parsed.Host != s.Host {
		assert.Equal(t, s.Host, parsed.Host, "parsed.Host = %q, want %q", parsed.Host, s.Host)
	}
	if !parsed.Started.Equal(s.Started) {
		assert.Fail(t, fmt.Sprintf("parsed.Started = %v, want %v", parsed.Started, s.Started))
	}
	if parsed.Label != s.Label {
		assert.Equal(t, s.Label, parsed.Label, "parsed.Label = %q, want %q", parsed.Label, s.Label)
	}

	// Single line string format
	str := s.String()
	parsedSingle, err := ParseStamp(str)
	if err != nil {
		require.NoError(t, err, "ParseStamp(s.String()) failed: %v", err)
	}
	if parsedSingle.PID != s.PID || parsedSingle.Host != s.Host || parsedSingle.Label != s.Label {
		assert.Fail(t, fmt.Sprintf("parsedSingle = %+v, unexpected", parsedSingle))
	}
}

func TestReadStamp(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "read_stamp.lock")

	lock, err := TryLock(path, "read-test")
	if err != nil {
		require.NoError(t, err, "TryLock failed: %v", err)
	}
	defer lock.Unlock()

	stamp, err := ReadStamp(path)
	if err != nil {
		require.NoError(t, err, "ReadStamp failed: %v", err)
	}
	if stamp.Label != "read-test" {
		assert.Equal(t, "read-test", stamp.Label, "stamp.Label = %q, want read-test", stamp.Label)
	}
	if stamp.PID != os.Getpid() {
		assert.Fail(t, fmt.Sprintf("stamp.PID = %d, want %d", stamp.PID, os.Getpid()))
	}
}

func TestOptionsDefaults(t *testing.T) {
	t.Parallel()

	opts := options{}
	if opts.getPollInterval() != defaultPollInterval {
		assert.Fail(t, fmt.Sprintf("pollInterval = %v, want %v", opts.getPollInterval(), defaultPollInterval))
	}
	if opts.getPID() != os.Getpid() {
		assert.Fail(t, fmt.Sprintf("pid = %d, want %d", opts.getPID(), os.Getpid()))
	}
	if opts.getJitter(100*time.Millisecond) < 100*time.Millisecond {
		assert.Fail(t, fmt.Sprintf("jitter < base"))
	}
	if opts.getJitter(0) != 0 {
		assert.Fail(t, fmt.Sprintf("jitter(0) != 0"))
	}
	if opts.getClock() == nil {
		assert.Fail(t, fmt.Sprintf("clock is nil"))
	}

	customOpts := options{
		host: "custom-host",
		pid:  777,
		jitter: func(d time.Duration) time.Duration {
			return d * 2
		},
	}
	if customOpts.getHost() != "custom-host" {
		assert.Fail(t, fmt.Sprintf("host() = %q, want custom-host", customOpts.getHost()))
	}
	if customOpts.getPID() != 777 {
		assert.Fail(t, fmt.Sprintf("pid() = %d, want 777", customOpts.getPID()))
	}
	if customOpts.getJitter(10*time.Millisecond) != 20*time.Millisecond {
		assert.Fail(t, fmt.Sprintf("custom jitter failed"))
	}
}

func TestHeldErrorFormatting(t *testing.T) {
	t.Parallel()

	e1 := &HeldError{Path: "lockfile.lock"}
	if !strings.Contains(e1.Error(), "is held") {
		assert.Fail(t, fmt.Sprintf("e1.Error() = %q", e1.Error()))
	}

	e2 := &HeldError{Path: "lockfile.lock", Wait: 2 * time.Second}
	if !strings.Contains(e2.Error(), "waited 2s") {
		assert.Fail(t, fmt.Sprintf("e2.Error() = %q", e2.Error()))
	}

	holder := Stamp{PID: 1234, Label: "my-holder"}
	e3 := &HeldError{Path: "lockfile.lock", Holder: holder, Wait: 5 * time.Second}
	if !strings.Contains(e3.Error(), "waited 5s") || !strings.Contains(e3.Error(), "my-holder") {
		assert.Fail(t, fmt.Sprintf("e3.Error() = %q", e3.Error()))
	}
}

func TestParseStamp_SpecialCases(t *testing.T) {
	t.Parallel()

	empty, err := ParseStamp("")
	if err != nil || !empty.IsZero() {
		assert.Fail(t, fmt.Sprintf("ParseStamp empty = %+v, %v", empty, err))
	}

	dash, err := ParseStamp("-")
	if err != nil || !dash.IsZero() {
		assert.Fail(t, fmt.Sprintf("ParseStamp dash = %+v, %v", dash, err))
	}

	barePID, err := ParseStamp("9876\n")
	if err != nil || barePID.PID != 9876 {
		assert.Fail(t, fmt.Sprintf("ParseStamp barePID = %+v, %v", barePID, err))
	}

	multiWithEmptyLines := "pid=456\n\nhost=box\nstarted=2026-09-27T12:00:00Z\nlabel=foo\n"
	st, err := ParseStamp(multiWithEmptyLines)
	if err != nil || st.PID != 456 || st.Host != "box" || st.Label != "foo" {
		assert.Fail(t, fmt.Sprintf("ParseStamp multi = %+v, %v", st, err))
	}

	singleLine := "pid=789 host=node1 started=2026-09-27T12:00:00Z label=bar"
	stSingle, err := ParseStamp(singleLine)
	if err != nil || stSingle.PID != 789 || stSingle.Host != "node1" || stSingle.Label != "bar" {
		assert.Fail(t, fmt.Sprintf("ParseStamp single = %+v, %v", stSingle, err))
	}
}

func TestHelpers_NilAndErrors(t *testing.T) {
	t.Parallel()

	if ok, err := tryLockFile(nil); ok || err == nil {
		assert.Fail(t, fmt.Sprintf("tryLockFile(nil) = %v, %v", ok, err))
	}
	if ok, err := trySharedLock(nil); ok || err == nil {
		assert.Fail(t, fmt.Sprintf("trySharedLock(nil) = %v, %v", ok, err))
	}
	unlockFile(nil) // should not panic

	empty := readExistingStamp(nil)
	if !empty.IsZero() {
		assert.Fail(t, fmt.Sprintf("readExistingStamp(nil) = %+v, want zero", empty))
	}

	_, err := ReadStamp(filepath.Join(t.TempDir(), "nonexistent"))
	if err == nil {
		assert.Error(t, err, "ReadStamp nonexistent should error")
	}

	clk := newLockStepClock(time.Time{})
	if clk.Now().IsZero() {
		assert.Fail(t, fmt.Sprintf("newLockStepClock(zero) returned zero time"))
	}
}

func TestRealClock(t *testing.T) {
	t.Parallel()

	rc := realClock{}
	now := rc.Now()
	if now.IsZero() {
		require.Fail(t, "realClock.Now() is zero")
	}
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

	if _, err := TryLock(badPath, "bad"); err == nil {
		assert.Error(t, err, "TryLock with non-directory parent should error")
	}
}

// H1 witness. An asker (another refused taker asking) holds the shared
// lock for an instant, and the kernel refuses the exclusive lock while it does.
// The asker here never leaves, so every take lands in that instant. Nobody
// holds, so the taker must answer busy (ErrBusy) and never held (ErrHeld):
// tla/FileLock.tla, HeldIsTrue, kept by the Blocked action's shared re-ask.
func TestTryLock_AskerIsNotAHolder(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "asker.lock")
	if err := os.WriteFile(path, nil, 0666); err != nil {
		require.NoError(t, err, err)
	}
	asker, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		require.NoError(t, err, err)
	}
	defer asker.Close()
	if ok, err := trySharedLock(asker); err != nil || !ok {
		require.Fail(t, fmt.Sprintf("asker's shared lock = %v, %v, want granted", ok, err))
	}

	lock, err := TryLock(path, "taker")
	if lock != nil {
		lock.Unlock()
		require.Fail(t, "TryLock succeeded while a shared lock was held")
	}
	if errors.Is(err, ErrHeld) {
		assert.Fail(t, fmt.Sprintf("told held with nobody holding: %v", err))
	}
	if _, ok := AsHeldError(err); ok {
		assert.False(t, ok, "a *HeldError with nobody holding: %v", err)
	}
	if !errors.Is(err, ErrBusy) {
		assert.ErrorIs(t, err, ErrBusy, "err = %v, want ErrBusy", err)
	}

	// A bounded Lock kept out by the asker alone runs out as busy, not held.
	clk := newLockStepClock(time.Time{})
	lock, err = lockWithOptions(path, "waiter", 50*time.Millisecond, options{clock: clk})
	if lock != nil {
		lock.Unlock()
		require.Fail(t, "Lock succeeded while a shared lock was held")
	}
	if errors.Is(err, ErrHeld) || !errors.Is(err, ErrBusy) || !errors.Is(err, ErrTimeout) {
		assert.Fail(t, fmt.Sprintf("bounded Lock against an asker: err = %v, want ErrTimeout and ErrBusy, never ErrHeld", err))
	}

	// The asker leaves; the next take is granted.
	unlockFile(asker)
	lock, err = TryLock(path, "taker")
	if err != nil {
		require.NoError(t, err, "TryLock after the asker left: %v", err)
	}
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
	if err != nil {
		require.NoError(t, err, err)
	}
	defer holder.Unlock()

	for _, bound := range []time.Duration{0, 1, time.Millisecond, 10 * time.Millisecond, 200 * time.Millisecond, time.Second} {
		clk := newLockStepClock(time.Time{})
		lock, err := lockWithOptions(path, "waiter", bound, options{clock: clk})
		if lock != nil {
			lock.Unlock()
			require.Fail(t, fmt.Sprintf("bound %s: acquired a held lock", bound))
		}
		if w := clk.Waited(); w > bound {
			assert.LessOrEqual(t, w, bound, "bound %s exceeded: waited %s", bound, w)
		}
		he, ok := AsHeldError(err)
		if !ok {
			assert.True(t, ok, "bound %s: the run-out does not name the holder: %v", bound, err)
			continue
		}
		if he.Holder.Label != "holder" || he.Holder.PID != os.Getpid() {
			assert.Fail(t, fmt.Sprintf("bound %s: holder = %s, want pid=%d label=\"holder\"", bound, he.Holder, os.Getpid()))
		}
		if he.Wait != bound {
			assert.Equal(t, bound, he.Wait, "bound %s: Wait = %s, want the bound", bound, he.Wait)
		}
		if !errors.Is(err, ErrHeld) || !errors.Is(err, ErrTimeout) {
			assert.Fail(t, fmt.Sprintf("bound %s: errors.Is held=%v timeout=%v, want both: %v", bound, errors.Is(err, ErrHeld), errors.Is(err, ErrTimeout), err))
		}
	}
}

// H2 witness: HeldError.Wait is set by the package on a run-out, and printed.
func TestLock_RunOutSetsWait(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "wait.lock")
	holder, err := TryLock(path, "holder")
	if err != nil {
		require.NoError(t, err, err)
	}
	defer holder.Unlock()

	_, err = lockWithOptions(path, "waiter", 50*time.Millisecond, options{clock: newLockStepClock(time.Time{})})
	he, ok := AsHeldError(err)
	if !ok {
		require.True(t, ok, "a bounded Lock that ran out against a holder is not a *HeldError: %v", err)
	}
	if he.Wait != 50*time.Millisecond {
		assert.Equal(t, 50*time.Millisecond, he.Wait, "HeldError.Wait = %s after a 50ms bound ran out", he.Wait)
	}
	if !strings.Contains(err.Error(), "waited 50ms") {
		assert.Fail(t, fmt.Sprintf("run-out text does not say the wait: %q", err.Error()))
	}
}

func TestMutant_Fsync(t *testing.T) {
	t.Parallel()

	// 1. defaultSync on a closed file descriptor MUST return an error.
	// If defaultSync was mutated to a dummy `return nil`, this fails!
	f, err := os.CreateTemp(t.TempDir(), "fsync-test")
	if err != nil {
		require.NoError(t, err, err)
	}
	_ = f.Close()
	if err := defaultSync(f); err == nil {
		require.Error(t, err, "defaultSync on closed file descriptor returned nil, want error")
	}

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
	if lock != nil {
		lock.Unlock()
		require.Fail(t, "tryLockWithOptions succeeded despite sync error")
	}
	if !errors.Is(err, syncErr) {
		require.ErrorIs(t, err, syncErr, "err = %v, want syncErr", err)
	}

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
	if err != nil {
		require.NoError(t, err, "tryLockWithOptions failed: %v", err)
	}
	if !syncCalled {
		require.True(t, syncCalled, "sync was not called during successful tryLockWithOptions")
	}
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
	if !hasGreater {
		require.True(t, hasGreater, "defaultJitter never produced a duration > d (mutant: return d)")
	}
	if len(seen) < 5 {
		require.Fail(t, fmt.Sprintf("defaultJitter produced only %d distinct values across 200 iterations (want >= 5)", len(seen)))
	}

	// 2. lockLoop backoff must invoke opts.jitter
	dir := t.TempDir()
	path := filepath.Join(dir, "jitter_hook.lock")
	held, err := TryLock(path, "holder")
	if err != nil {
		require.NoError(t, err, err)
	}
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
	if !jitterInvoked {
		require.True(t, jitterInvoked, "opts.jitter was never invoked during lockLoop backoff (mutant: jitter removed)")
	}
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
	if err != nil {
		require.NoError(t, err, "TryLock on existing file failed: %v", err)
	}
	defer lock.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		require.NoError(t, err, "ReadFile failed: %v", err)
	}
	if string(data) == string(precious) {
		require.Fail(t, "TryLock did not truncate existing file contents")
	}
	if !strings.Contains(string(data), "label=dedicated-taker") {
		require.Contains(t, string(data), "label=dedicated-taker", "file content does not contain new stamp: %s", string(data))
	}
}

func TestCappedHostileLabelAndPathSanitization(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "huge_label.lock")

	bigLabel := strings.Repeat("X", 1024*1024) // 1 MB label
	lock, err := TryLock(path, bigLabel)
	if err != nil {
		require.NoError(t, err, "TryLock with 1MB label failed: %v", err)
	}
	defer lock.Unlock()

	st, err := ReadStamp(path)
	require.NoError(t, err)
	if len(st.Label) > 1024 {
		require.Fail(t, fmt.Sprintf("the stamp's label length = %d, want <= 1024", len(st.Label)))
	}

	// Second taker fails with HeldError
	_, herr := TryLock(path, "second")
	if herr == nil {
		require.Error(t, herr, "second TryLock succeeded, want HeldError")
	}
	if len(herr.Error()) > 4096 {
		require.Fail(t, fmt.Sprintf("HeldError.Error() length = %d, want <= 4096", len(herr.Error())))
	}
	// Error string must not contain control characters
	for i := 0; i < len(herr.Error()); i++ {
		if c := herr.Error()[i]; c < 0x20 || c == 0x7f {
			require.Fail(t, fmt.Sprintf("HeldError.Error() contains control char 0x%02x: %q", c, herr.Error()))
		}
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
					if c := err2.Error()[i]; c < 0x20 || c == 0x7f {
						require.Fail(t, fmt.Sprintf("error text contains unescaped control char 0x%02x: %q", c, err2.Error()))
					}
				}
			}
		}
	}
}
