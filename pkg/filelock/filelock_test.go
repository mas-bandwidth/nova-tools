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

// release unlocks the test's lock and reports a failed release (docs/STANDARD.md
// section 2: an error is surfaced, never dropped silently): the take the test
// holds must leave cleanly, and a nil lock, a refused take's, no-ops.
func release(t *testing.T, lock *FileLock) {
	t.Helper()
	require.NoError(t, lock.Unlock(), "release the test's lock")
}

func TestTryLock_Success(t *testing.T) {
	t.Parallel()

	path := newRig(t).path("test.lock")
	lock, err := TryLock(path, "worker-1")
	require.NoError(t, err, "TryLock failed: %v", err)
	defer release(t, lock)

	// The file names its holder (tla/FileLock.tla, HolderIsNamed).
	st, err := ReadStamp(path)
	require.NoError(t, err)
	assert.Equal(t, "worker-1", st.Label, "the held file's label")
	assert.Equal(t, os.Getpid(), st.PID, "the held file's pid")

	fi, err := os.Stat(path)
	require.NoError(t, err, "Stat failed: %v", err)
	assert.Positive(t, fi.Size(), "lock file size is 0 while held, want > 0")
}

func TestTryLock_Held(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	path := r.path("held.lock")
	r.holder(path, "holder")

	lock2, err := r.try(path, "contender")
	assert.ErrorIs(t, err, ErrHeld, "second TryLock error = %v, want ErrHeld", err)
	release(t, lock2) // nil-safe: releases a mutant lock, no-ops on nil
	require.Nil(t, lock2, "second TryLock succeeded, want refusal")

	heldErr, ok := AsHeldError(err)
	require.True(t, ok, "expected *HeldError, got %T: %v", err, err)
	assert.Equal(t, "holder", heldErr.Holder.Label, "heldErr.Holder.Label = %q, want holder", heldErr.Holder.Label)
	assert.Equal(t, os.Getpid(), heldErr.Holder.PID, "heldErr.Holder.PID = %d, want %d", heldErr.Holder.PID, os.Getpid())
}

func TestLock_Success(t *testing.T) {
	t.Parallel()

	lock, err := Lock(newRig(t).path("lock_success.lock"), "winner", time.Second)
	require.NoError(t, err, "Lock failed: %v", err)
	defer release(t, lock)
}

func TestLock_TimeoutBound(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	path := r.path("timeout.lock")
	r.holder(path, "first")

	timeout := 100 * time.Millisecond
	lock2, err := r.timed(path, "second", timeout, options{pollInterval: 10 * time.Millisecond})
	require.Nil(t, lock2, "lockWithOptions succeeded unexpectedly while held")
	require.ErrorIs(t, err, ErrTimeout, "err = %v, want ErrTimeout", err)
	// H2: Lock timeout must also match ErrHeld and report Holder and Wait.
	assert.ErrorIs(t, err, ErrHeld, "err = %v, want errors.Is(err, ErrHeld) to be true on timeout", err)
	heldErr, ok := AsHeldError(err)
	require.True(t, ok, "expected *HeldError, got %T: %v", err, err)
	assert.True(t, heldErr.TimedOut, "heldErr.TimedOut = false, want true")
	assert.Equal(t, "first", heldErr.Holder.Label, "heldErr.Holder.Label = %q, want first", heldErr.Holder.Label)
	assert.GreaterOrEqual(t, heldErr.Wait, timeout, "heldErr.Wait = %v, want >= %v", heldErr.Wait, timeout)
	assert.GreaterOrEqual(t, r.waited(), timeout, "virtual clock waited %v, want >= %v", r.waited(), timeout)
}

func TestH1_ContendedTakerVsSharedProbe_ReturnsErrBusy(t *testing.T) {
	t.Parallel()

	path := newRig(t).path("probe_busy.lock")
	require.NoError(t, os.WriteFile(path, nil, 0666))

	f, err := openFileSafe(path, os.O_RDWR, 0)
	require.NoError(t, err, "openFileSafe failed: %v", err)
	defer func() { _ = f.Close() }() // ignored: the probe writes nothing, a close error loses nothing

	shOk, shErr := trySharedLock(f)
	require.True(t, shOk && shErr == nil, "trySharedLock failed: ok=%v, err=%v", shOk, shErr)
	defer unlockFile(f)

	// H1: with an asker holding the shared lock, the taker must answer
	// ErrBusy and must NOT wrap ErrHeld.
	_, tryErr := TryLock(path, "taker")
	require.Error(t, tryErr, "TryLock succeeded unexpectedly while shared lock held")
	assert.ErrorIs(t, tryErr, ErrBusy, "tryErr = %v, want ErrBusy", tryErr)
	assert.NotErrorIs(t, tryErr, ErrHeld, "tryErr wraps ErrHeld, want only ErrBusy when an asker is in the way")

	unlockFile(f)

	takerLock, err := TryLock(path, "taker")
	require.NoError(t, err, "TryLock failed after shared lock released: %v", err)
	defer release(t, takerLock)
}

func TestMutant_LastSleepCappedAtRemaining(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	path := r.path("cap_mutant.lock")
	r.holder(path, "holder")

	// A constant 30ms sleep request against a 50ms bound steps 30+20;
	// uncapped, the last step would wait 30 and total 60ms.
	timeout := 50 * time.Millisecond
	_, err2 := r.timed(path, "waiter", timeout, options{
		pollInterval: 30 * time.Millisecond,
		jitter: func(time.Duration) time.Duration {
			return 30 * time.Millisecond
		},
	})
	require.ErrorIs(t, err2, ErrTimeout, "err2 = %v, want ErrTimeout", err2)
	assert.Equal(t, 50*time.Millisecond, r.waited(), "clk.Waited() = %v, want exactly 50ms (last sleep must be capped at remaining)", r.waited())
}

func TestLock_AcquiresAfterRelease(t *testing.T) {
	t.Parallel()

	path := newRig(t).path("sequential.lock")
	lock1, err := Lock(path, "first", time.Second)
	require.NoError(t, err, "first Lock failed: %v", err)
	require.NoError(t, lock1.Unlock(), "first Unlock failed")

	lock2, err := Lock(path, "second", time.Second)
	require.NoError(t, err, "second Lock failed: %v", err)
	defer release(t, lock2)
}

func TestUnlock_IdempotentAndNeverDeletes(t *testing.T) {
	t.Parallel()

	path := newRig(t).path("keep_file.lock")
	lock, err := TryLock(path, "temp")
	require.NoError(t, err, "TryLock failed: %v", err)

	require.NoError(t, lock.Unlock(), "first Unlock failed")
	assert.NoError(t, lock.Unlock(), "second Unlock failed")

	var nilLock *FileLock
	assert.NoError(t, nilLock.Unlock(), "nilLock.Unlock() = %v, want nil")

	fi, err := os.Stat(path)
	require.NoError(t, err, "lock file missing after Unlock: %v", err)
	assert.Zero(t, fi.Size(), "lock file size after Unlock = %d, want 0", fi.Size())
}

// A holder that died holding leaves its note; the next taker writes its own over
// it, so the file names its holder (tla/FileLock.tla, HolderIsNamed).
func TestTryLock_OverwritesAnUnreleasedNote(t *testing.T) {
	t.Parallel()

	path := newRig(t).path("crash.lock")
	unreleased := Stamp{
		PID:     9999,
		Host:    "crashed-host",
		Started: time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC),
		Label:   "crashed-worker",
	}
	require.NoError(t, os.WriteFile(path, []byte(unreleased.Format()+"\n"), 0666))

	lock, err := TryLock(path, "recovery-worker")
	require.NoError(t, err, "TryLock failed: %v", err)
	defer release(t, lock)

	st, err := ReadStamp(path)
	require.NoError(t, err)
	assert.Equal(t, "recovery-worker", st.Label, "the file still names the dead holder: %+v", st)
	assert.Equal(t, os.Getpid(), st.PID, "the file still names the dead holder: %+v", st)
}

// A lock path must be a plain file: a symlink, a directory and a path whose
// parent is a regular file are all refused.
func TestTryLock_RefusesUnfitPaths(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		path func(t *testing.T) string
	}{
		{"TestSymlink_NotPermitted", func(t *testing.T) string {
			dir := t.TempDir()
			target := filepath.Join(dir, "target.txt")
			require.NoError(t, os.WriteFile(target, []byte("target"), 0666))
			link := filepath.Join(dir, "link.lock")
			if err := os.Symlink(target, link); err != nil {
				t.Skipf("symlink not supported in this environment: %v", err)
			}
			return link
		}},
		{"TestDirectory_NotPermitted", func(t *testing.T) string { return t.TempDir() }},
		{"TestTryLock_NotDirParent", func(t *testing.T) string {
			regular := filepath.Join(t.TempDir(), "regular.txt")
			require.NoError(t, os.WriteFile(regular, []byte("file"), 0666))
			return filepath.Join(regular, "sub.lock")
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := TryLock(tt.path(t), "test")
			assert.Error(t, err, "%s: TryLock succeeded, want error", tt.name)
		})
	}
}

func TestPathEscapingInErrors(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	path := r.path("path with spaces.lock")
	r.holder(path, "holder label")

	_, err := TryLock(path, "second")
	require.Error(t, err, "expected error on held lock")
	assert.Contains(t, err.Error(), `"`, "error string %q does not quote path with %%q", err.Error())
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

	// The single-line rendering parses back too.
	parsedSingle, err := ParseStamp(s.String())
	require.NoError(t, err, "ParseStamp(s.String()) failed: %v", err)
	assert.True(t, parsedSingle.PID == s.PID && parsedSingle.Host == s.Host && parsedSingle.Label == s.Label, "parsedSingle = %+v, unexpected", parsedSingle)
}

func TestReadStamp(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	path := r.path("read_stamp.lock")
	r.holder(path, "read-test")

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

	e3 := &HeldError{Path: "lockfile.lock", Holder: Stamp{PID: 1234, Label: "my-holder"}, Wait: 5 * time.Second}
	assert.Contains(t, e3.Error(), "waited 5s", "e3.Error() = %q", e3.Error())
	assert.Contains(t, e3.Error(), "my-holder", "e3.Error() = %q", e3.Error())
}

func TestParseStamp_SpecialCases(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, text  string
		zero        bool
		pid         int
		host, label string
	}{
		{"empty", "", true, 0, "", ""},
		{"dash", "-", true, 0, "", ""},
		{"barePID", "9876\n", false, 9876, "", ""},
		{"multiWithEmptyLines", "pid=456\n\nhost=box\nstarted=2026-09-27T12:00:00Z\nlabel=foo\n", false, 456, "box", "foo"},
		{"singleLine", "pid=789 host=node1 started=2026-09-27T12:00:00Z label=bar", false, 789, "node1", "bar"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			st, err := ParseStamp(tt.text)
			assert.NoError(t, err, "ParseStamp(%q) errored", tt.text)
			if tt.zero {
				assert.True(t, st.IsZero(), "ParseStamp(%q) = %+v, want zero", tt.text, st)
				return
			}
			assert.Equal(t, tt.pid, st.PID, "ParseStamp(%q).PID", tt.text)
			assert.Equal(t, tt.host, st.Host, "ParseStamp(%q).Host", tt.text)
			assert.Equal(t, tt.label, st.Label, "ParseStamp(%q).Label", tt.text)
		})
	}
}

func TestHelpers_NilAndErrors(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		fn   func(*os.File) (bool, error)
	}{
		{"tryLockFile", tryLockFile},
		{"trySharedLock", trySharedLock},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ok, err := tt.fn(nil)
			assert.False(t, ok, "%s(nil) = %v, %v, want false", tt.name, ok, err)
			assert.Error(t, err, "%s(nil) = %v, %v, want an error", tt.name, ok, err)
		})
	}
	unlockFile(nil) // should not panic

	empty := readExistingStamp(nil)
	assert.True(t, empty.IsZero(), "readExistingStamp(nil) = %+v, want zero", empty)

	_, err := ReadStamp(filepath.Join(t.TempDir(), "nonexistent"))
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

// H1 witness. An asker (another refused taker asking) holds the shared
// lock for an instant, and the kernel refuses the exclusive lock while it does.
// The asker here never leaves, so every take lands in that instant. Nobody
// holds, so the taker must answer busy (ErrBusy) and never held (ErrHeld):
// tla/FileLock.tla, HeldIsTrue, kept by the Blocked action's shared re-ask.
func TestTryLock_AskerIsNotAHolder(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	path := r.path("asker.lock")
	require.NoError(t, os.WriteFile(path, nil, 0666))
	asker, err := os.OpenFile(path, os.O_RDWR, 0)
	require.NoError(t, err)
	defer func() { _ = asker.Close() }() // ignored: the asker writes nothing, a close error loses nothing
	ok, err := trySharedLock(asker)
	require.True(t, ok && err == nil, "asker's shared lock = %v, %v, want granted", ok, err)

	lock, err := TryLock(path, "taker")
	release(t, lock) // nil-safe: releases a mutant lock, no-ops on nil
	require.Nil(t, lock, "TryLock succeeded while a shared lock was held")
	assert.NotErrorIs(t, err, ErrHeld, "told held with nobody holding: %v", err)
	_, isHeld := AsHeldError(err)
	assert.False(t, isHeld, "a *HeldError with nobody holding: %v", err)
	assert.ErrorIs(t, err, ErrBusy, "err = %v, want ErrBusy", err)

	// A bounded Lock kept out by the asker alone runs out as busy, not held.
	lock, err = r.timed(path, "waiter", 50*time.Millisecond, options{})
	require.Nil(t, lock, "Lock succeeded while a shared lock was held")
	assert.True(t, !errors.Is(err, ErrHeld) && errors.Is(err, ErrBusy) && errors.Is(err, ErrTimeout), "bounded Lock against an asker: err = %v, want ErrTimeout and ErrBusy, never ErrHeld", err)

	// The asker leaves; the next take is granted.
	unlockFile(asker)
	lock, err = TryLock(path, "taker")
	require.NoError(t, err, "TryLock after the asker left: %v", err)
	release(t, lock)
}

// H2 witness, the bound on the virtual clock. A bounded Lock against a holder
// never waits past its bound, and when it runs out it names the holder: a
// *HeldError carrying the note read on the last refusal and the bound as Wait,
// answering both ErrHeld and ErrTimeout. internal/merge (AsHeldError, exit 2),
// pkg/bus (the pid in the refusal) and internal/tokens (HolderPID) all
// name the holder on a run-out.
func TestLock_RunOutNamesTheHolder(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	path := r.path("runout.lock")
	r.holder(path, "holder")

	for _, bound := range []time.Duration{0, 1, time.Millisecond, 10 * time.Millisecond, 200 * time.Millisecond, time.Second} {
		clk := newLockStepClock(time.Time{})
		lock, err := lockWithOptions(path, "waiter", bound, options{clock: clk})
		release(t, lock) // nil-safe: releases a mutant lock, no-ops on nil
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

	r := newRig(t)
	path := r.path("wait.lock")
	r.holder(path, "holder")

	_, err := r.timed(path, "waiter", 50*time.Millisecond, options{})
	he, ok := AsHeldError(err)
	require.True(t, ok, "a bounded Lock that ran out against a holder is not a *HeldError: %v", err)
	assert.Equal(t, 50*time.Millisecond, he.Wait, "HeldError.Wait = %s after a 50ms bound ran out", he.Wait)
	assert.Contains(t, err.Error(), "waited 50ms", "run-out text does not say the wait: %q", err.Error())
}

func TestMutant_Fsync(t *testing.T) {
	t.Parallel()

	r := newRig(t)

	// defaultSync on a closed file descriptor must error: a mutant
	// `return nil` fails here.
	f, err := os.CreateTemp(t.TempDir(), "fsync-test")
	require.NoError(t, err)
	_ = f.Close()
	require.Error(t, defaultSync(f), "defaultSync on closed file descriptor returned nil, want error")

	// An injected sync error in tryLockWithOptions aborts, cleans up, and
	// returns the error.
	path := r.path("sync_err.lock")
	syncErr := errors.New("simulated disk sync error")
	lock, err := tryLockWithOptions(path, "fsync-fail", options{
		sync: func(*os.File) error { return syncErr },
	})
	release(t, lock) // nil-safe: releases a mutant lock, no-ops on nil
	require.Nil(t, lock, "tryLockWithOptions succeeded despite sync error")
	require.ErrorIs(t, err, syncErr, "err = %v, want syncErr", err)

	// sync is called on a successful lock. The take below also shows the
	// failed one let go of the kernel lock: a lock still held would refuse it.
	syncCalled := false
	successLock, err := tryLockWithOptions(path, "fsync-ok", options{
		sync: func(f *os.File) error {
			syncCalled = true
			return f.Sync()
		},
	})
	require.NoError(t, err, "tryLockWithOptions failed: %v", err)
	require.True(t, syncCalled, "sync was not called during successful tryLockWithOptions")
	_ = successLock.Unlock()
}

func TestMutant_Jitter(t *testing.T) {
	t.Parallel()

	// defaultJitter must spread: values above the base, never one fixed value.
	d := 100 * time.Millisecond
	seen := make(map[time.Duration]bool)
	hasGreater := false
	for range 200 {
		j := defaultJitter(d)
		seen[j] = true
		if j > d {
			hasGreater = true
		}
	}
	require.True(t, hasGreater, "defaultJitter never produced a duration > d (mutant: return d)")
	require.GreaterOrEqual(t, len(seen), 5, "defaultJitter produced only %d distinct values across 200 iterations (want >= 5)", len(seen))

	// lockLoop backoff must invoke opts.jitter.
	r := newRig(t)
	path := r.path("jitter_hook.lock")
	r.holder(path, "holder")

	jitterInvoked := false
	_, _ = r.timed(path, "waiter", 50*time.Millisecond, options{
		jitter: func(base time.Duration) time.Duration {
			jitterInvoked = true
			return base
		},
	})
	require.True(t, jitterInvoked, "opts.jitter was never invoked during lockLoop backoff (mutant: jitter removed)")
}

func TestTryLock_DedicatedPathTruncates(t *testing.T) {
	t.Parallel()

	path := newRig(t).path("data_file.lock")
	precious := []byte("critical initial data that should be truncated when taking lock\n")
	require.NoError(t, os.WriteFile(path, precious, 0666))

	lock, err := TryLock(path, "dedicated-taker")
	require.NoError(t, err, "TryLock on existing file failed: %v", err)
	defer release(t, lock)

	data, err := os.ReadFile(path)
	require.NoError(t, err, "ReadFile failed: %v", err)
	require.NotEqual(t, string(precious), string(data), "TryLock did not truncate existing file contents")
	require.Contains(t, string(data), "label=dedicated-taker", "file content does not contain new stamp: %s", string(data))
}

// requireNoControlChars pins that error text carries no raw control characters:
// hostile bytes reach the reader quoted, never raw.
func requireNoControlChars(t *testing.T, text string) {
	t.Helper()
	for _, c := range text {
		require.False(t, c < 0x20 || c == 0x7f, "error text contains control char 0x%02x: %q", c, text)
	}
}

func TestCappedHostileLabelAndPathSanitization(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	path := r.path("huge_label.lock")

	bigLabel := strings.Repeat("X", 1024*1024) // 1 MB label
	lock, err := TryLock(path, bigLabel)
	require.NoError(t, err, "TryLock with 1MB label failed: %v", err)
	defer release(t, lock)

	st, err := ReadStamp(path)
	require.NoError(t, err)
	require.LessOrEqual(t, len(st.Label), 1024, "the stamp's label length = %d, want <= 1024", len(st.Label))

	_, herr := TryLock(path, "second")
	require.Error(t, herr, "second TryLock succeeded, want HeldError")
	require.LessOrEqual(t, len(herr.Error()), 4096, "HeldError.Error() length = %d, want <= 4096", len(herr.Error()))
	requireNoControlChars(t, herr.Error())

	// A newline in the path must reach the error text quoted, never raw.
	badDir := filepath.Join(r.dir, "newline\nin\npath")
	if err := os.Mkdir(badDir, 0755); err == nil {
		badLock := filepath.Join(badDir, "l.lock")
		if l, err := TryLock(badLock, "test"); err == nil {
			defer release(t, l)
			if _, err2 := TryLock(badLock, "test2"); err2 != nil {
				requireNoControlChars(t, err2.Error())
			}
		}
	}
}
