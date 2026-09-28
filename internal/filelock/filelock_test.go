//go:build unix || windows

package filelock

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTryLock_Success(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.lock")

	lock, err := TryLock(path, "worker-1")
	if err != nil {
		t.Fatalf("TryLock failed: %v", err)
	}
	defer lock.Unlock()

	if lock.Path() != path {
		t.Errorf("lock.Path() = %q, want %q", lock.Path(), path)
	}
	if lock.Stamp().Label != "worker-1" {
		t.Errorf("lock.Stamp().Label = %q, want worker-1", lock.Stamp().Label)
	}
	if lock.Stamp().PID <= 0 {
		t.Errorf("lock.Stamp().PID = %d, want > 0", lock.Stamp().PID)
	}
	if lock.Previous() != nil {
		t.Errorf("lock.Previous() = %+v, want nil for fresh lock", lock.Previous())
	}

	// Verify file exists on disk and is non-empty while held
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if fi.Size() == 0 {
		t.Errorf("lock file size is 0 while held, want > 0")
	}
}

func TestTryLock_Held(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "held.lock")

	lock1, err := TryLock(path, "holder")
	if err != nil {
		t.Fatalf("first TryLock failed: %v", err)
	}
	defer lock1.Unlock()

	lock2, err := TryLock(path, "contender")
	if lock2 != nil {
		lock2.Unlock()
		t.Fatalf("second TryLock succeeded, want refusal")
	}
	if !errors.Is(err, ErrHeld) {
		t.Errorf("second TryLock error = %v, want ErrHeld", err)
	}

	heldErr, ok := AsHeldError(err)
	if !ok {
		t.Fatalf("expected *HeldError, got %T: %v", err, err)
	}
	if heldErr.Holder.Label != "holder" {
		t.Errorf("heldErr.Holder.Label = %q, want holder", heldErr.Holder.Label)
	}
	if heldErr.Holder.PID != os.Getpid() {
		t.Errorf("heldErr.Holder.PID = %d, want %d", heldErr.Holder.PID, os.Getpid())
	}
}

func TestLock_Success(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "lock_success.lock")

	lock, err := Lock(path, "winner", time.Second)
	if err != nil {
		t.Fatalf("Lock failed: %v", err)
	}
	defer lock.Unlock()

	if lock.Previous() != nil {
		t.Errorf("lock.Previous() = %+v, want nil", lock.Previous())
	}
}

func TestLock_TimeoutBound(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "timeout.lock")

	lock1, err := TryLock(path, "first")
	if err != nil {
		t.Fatalf("TryLock failed: %v", err)
	}
	defer lock1.Unlock()

	clk := NewLockStepClock(time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC))
	timeout := 100 * time.Millisecond
	opts := Options{
		Clock:        clk,
		PollInterval: 10 * time.Millisecond,
	}

	lock2, err := LockWithOptions(path, "second", timeout, opts)
	if lock2 != nil {
		lock2.Unlock()
		t.Fatalf("LockWithOptions succeeded unexpectedly while held")
	}
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	// H2: Lock timeout must also match ErrHeld and report Holder and Wait
	if !errors.Is(err, ErrHeld) {
		t.Errorf("err = %v, want errors.Is(err, ErrHeld) to be true on timeout", err)
	}
	heldErr, ok := AsHeldError(err)
	if !ok {
		t.Fatalf("expected *HeldError, got %T: %v", err, err)
	}
	if !heldErr.TimedOut {
		t.Errorf("heldErr.TimedOut = false, want true")
	}
	if heldErr.Holder.Label != "first" {
		t.Errorf("heldErr.Holder.Label = %q, want first", heldErr.Holder.Label)
	}
	if heldErr.Wait < timeout {
		t.Errorf("heldErr.Wait = %v, want >= %v", heldErr.Wait, timeout)
	}

	waited := clk.Waited()
	if waited < timeout {
		t.Errorf("virtual clock waited %v, want >= %v", waited, timeout)
	}
}

func TestH1_ContendedTakerVsSharedProbe_ReturnsErrBusy(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "probe_busy.lock")

	// Create the file first so a prober can open it
	initLock, err := TryLock(path, "init")
	if err != nil {
		t.Fatalf("init TryLock failed: %v", err)
	}
	if err := initLock.Unlock(); err != nil {
		t.Fatalf("init Unlock failed: %v", err)
	}

	// Open file and take SHARED lock (simulating a long-running prober or reader)
	f, err := openFileSafe(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("openFileSafe failed: %v", err)
	}
	defer f.Close()

	shOk, shErr := trySharedLock(f)
	if !shOk || shErr != nil {
		t.Fatalf("trySharedLock failed: ok=%v, err=%v", shOk, shErr)
	}
	defer unlockFile(f)

	// An exclusive taker tries to take the lock
	_, tryErr := TryLock(path, "taker")
	if tryErr == nil {
		t.Fatalf("TryLock succeeded unexpectedly while shared lock held")
	}

	// H1: Must return ErrBusy, and must NOT wrap ErrHeld
	if !errors.Is(tryErr, ErrBusy) {
		t.Errorf("tryErr = %v, want ErrBusy", tryErr)
	}
	if errors.Is(tryErr, ErrHeld) {
		t.Errorf("tryErr wraps ErrHeld, want only ErrBusy when prober in the way")
	}

	// Unlock shared lock
	unlockFile(f)

	// Now TryLock succeeds
	takerLock, err := TryLock(path, "taker")
	if err != nil {
		t.Fatalf("TryLock failed after shared lock released: %v", err)
	}
	defer takerLock.Unlock()
}

func TestMutant_LastSleepCappedAtRemaining(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "cap_mutant.lock")

	lock1, err := TryLock(path, "holder")
	if err != nil {
		t.Fatalf("TryLock failed: %v", err)
	}
	defer lock1.Unlock()

	clk := NewLockStepClock(time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC))
	timeout := 50 * time.Millisecond
	opts := Options{
		Clock:        clk,
		PollInterval: 30 * time.Millisecond,
		Jitter: func(d time.Duration) time.Duration {
			return 30 * time.Millisecond // constant 30ms sleep request
		},
	}

	// Step 1: remaining = 50ms, sleep = 30ms.
	// Step 2: remaining = 20ms. If uncapped, sleeps 30ms (waited = 60ms).
	// With cap, sleeps 20ms (waited = 50ms).
	_, err2 := LockWithOptions(path, "waiter", timeout, opts)
	if !errors.Is(err2, ErrTimeout) {
		t.Fatalf("err2 = %v, want ErrTimeout", err2)
	}

	waited := clk.Waited()
	if waited != 50*time.Millisecond {
		t.Errorf("clk.Waited() = %v, want exactly 50ms (last sleep must be capped at remaining)", waited)
	}
}

func TestLock_AcquiresAfterRelease(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "sequential.lock")

	lock1, err := Lock(path, "first", time.Second)
	if err != nil {
		t.Fatalf("first Lock failed: %v", err)
	}
	if err := lock1.Unlock(); err != nil {
		t.Fatalf("first Unlock failed: %v", err)
	}

	lock2, err := Lock(path, "second", time.Second)
	if err != nil {
		t.Fatalf("second Lock failed: %v", err)
	}
	defer lock2.Unlock()

	// Clean unlock truncated previous note, so Previous is nil
	if lock2.Previous() != nil {
		t.Errorf("lock2.Previous() = %+v, want nil after clean unlock", lock2.Previous())
	}
}

func TestUnlock_IdempotentAndNeverDeletes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "keep_file.lock")

	lock, err := TryLock(path, "temp")
	if err != nil {
		t.Fatalf("TryLock failed: %v", err)
	}

	if err := lock.Unlock(); err != nil {
		t.Fatalf("first Unlock failed: %v", err)
	}
	if err := lock.Unlock(); err != nil {
		t.Errorf("second Unlock failed: %v", err)
	}

	var nilLock *FileLock
	if err := nilLock.Unlock(); err != nil {
		t.Errorf("nilLock.Unlock() = %v, want nil", err)
	}

	// File MUST NOT be deleted!
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("lock file missing after Unlock: %v", err)
	}
	// File must be truncated to zero bytes
	if fi.Size() != 0 {
		t.Errorf("lock file size after Unlock = %d, want 0", fi.Size())
	}
}

func TestPrevious_UnreleasedHolderObserved(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "crash.lock")

	// Simulate an unreleased crashed holder: create file with stamp without releasing
	unreleased := Stamp{
		PID:     9999,
		Host:    "crashed-host",
		Started: time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC),
		Label:   "crashed-worker",
	}
	if err := os.WriteFile(path, []byte(unreleased.Format()+"\n"), 0666); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Next process takes the lock
	lock, err := TryLock(path, "recovery-worker")
	if err != nil {
		t.Fatalf("TryLock failed: %v", err)
	}
	defer lock.Unlock()

	if lock.Previous() == nil {
		t.Fatalf("lock.Previous() = nil, want crashed holder stamp")
	}
	if lock.Previous().PID != 9999 {
		t.Errorf("Previous().PID = %d, want 9999", lock.Previous().PID)
	}
	if lock.Previous().Host != "crashed-host" {
		t.Errorf("Previous().Host = %q, want crashed-host", lock.Previous().Host)
	}
	if lock.Previous().Label != "crashed-worker" {
		t.Errorf("Previous().Label = %q, want crashed-worker", lock.Previous().Label)
	}
}

func TestProbe_AbsentNeverCreatesFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.lock")

	state, stamp, err := Probe(path)
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}
	if state != StateAbsent {
		t.Errorf("Probe state = %s, want %s", state, StateAbsent)
	}
	if !stamp.IsZero() {
		t.Errorf("Probe stamp = %+v, want zero", stamp)
	}

	// Invariant: file MUST NOT exist after Probe
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Probe created file at %s: %v", path, err)
	}
}

func TestProbe_Free(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "free.lock")

	// Create and unlock
	lock, err := TryLock(path, "temp")
	if err != nil {
		t.Fatalf("TryLock failed: %v", err)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatalf("Unlock failed: %v", err)
	}

	state, stamp, err := Probe(path)
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}
	if state != StateFree {
		t.Errorf("Probe state = %s, want %s", state, StateFree)
	}
	if !stamp.IsZero() {
		t.Errorf("Probe stamp = %+v, want zero", stamp)
	}
}

func TestProbe_Held(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "held.lock")

	lock, err := TryLock(path, "active-job")
	if err != nil {
		t.Fatalf("TryLock failed: %v", err)
	}
	defer lock.Unlock()

	state, stamp, err := Probe(path)
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}
	if state != StateHeld {
		t.Errorf("Probe state = %s, want %s", state, StateHeld)
	}
	if stamp.Label != "active-job" {
		t.Errorf("Probe stamp.Label = %q, want active-job", stamp.Label)
	}
	if stamp.PID != os.Getpid() {
		t.Errorf("Probe stamp.PID = %d, want %d", stamp.PID, os.Getpid())
	}
}

func TestProbe_ConcurrentProbes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "shared_probe.lock")

	lock, err := TryLock(path, "shared-test")
	if err != nil {
		t.Fatalf("TryLock failed: %v", err)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatalf("Unlock failed: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			state, _, err := Probe(path)
			if err != nil {
				t.Errorf("Probe error: %v", err)
			}
			if state != StateFree {
				t.Errorf("Probe state = %s, want free", state)
			}
		}()
	}
	wg.Wait()
}

func TestSymlink_NotPermitted(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("target"), 0666); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	link := filepath.Join(dir, "link.lock")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not supported in this environment: %v", err)
	}

	if _, err := TryLock(link, "test"); err == nil {
		t.Errorf("TryLock on symlink succeeded, want error")
	}
	if _, _, err := Probe(link); err == nil {
		t.Errorf("Probe on symlink succeeded, want error")
	}
}

func TestDirectory_NotPermitted(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	if _, err := TryLock(dir, "test"); err == nil {
		t.Errorf("TryLock on directory succeeded, want error")
	}
	if _, _, err := Probe(dir); err == nil {
		t.Errorf("Probe on directory succeeded, want error")
	}
}

func TestPathEscapingInErrors(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "path with spaces.lock")

	lock1, err := TryLock(path, "holder label")
	if err != nil {
		t.Fatalf("TryLock failed: %v", err)
	}
	defer lock1.Unlock()

	_, err = TryLock(path, "second")
	if err == nil {
		t.Fatalf("expected error on held lock")
	}

	errStr := err.Error()
	if !strings.Contains(errStr, `"`) {
		t.Errorf("error string %q does not quote path with %%q", errStr)
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
		t.Fatalf("ParseStamp failed: %v", err)
	}

	if parsed.PID != s.PID {
		t.Errorf("parsed.PID = %d, want %d", parsed.PID, s.PID)
	}
	if parsed.Host != s.Host {
		t.Errorf("parsed.Host = %q, want %q", parsed.Host, s.Host)
	}
	if !parsed.Started.Equal(s.Started) {
		t.Errorf("parsed.Started = %v, want %v", parsed.Started, s.Started)
	}
	if parsed.Label != s.Label {
		t.Errorf("parsed.Label = %q, want %q", parsed.Label, s.Label)
	}

	// Single line string format
	str := s.String()
	parsedSingle, err := ParseStamp(str)
	if err != nil {
		t.Fatalf("ParseStamp(s.String()) failed: %v", err)
	}
	if parsedSingle.PID != s.PID || parsedSingle.Host != s.Host || parsedSingle.Label != s.Label {
		t.Errorf("parsedSingle = %+v, unexpected", parsedSingle)
	}
}

func TestReadStamp(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "read_stamp.lock")

	lock, err := TryLock(path, "read-test")
	if err != nil {
		t.Fatalf("TryLock failed: %v", err)
	}
	defer lock.Unlock()

	stamp, err := ReadStamp(path)
	if err != nil {
		t.Fatalf("ReadStamp failed: %v", err)
	}
	if stamp.Label != "read-test" {
		t.Errorf("stamp.Label = %q, want read-test", stamp.Label)
	}
	if stamp.PID != os.Getpid() {
		t.Errorf("stamp.PID = %d, want %d", stamp.PID, os.Getpid())
	}
}

func TestOptionsDefaults(t *testing.T) {
	t.Parallel()

	opts := Options{}
	if opts.pollInterval() != defaultPollInterval {
		t.Errorf("pollInterval = %v, want %v", opts.pollInterval(), defaultPollInterval)
	}
	if opts.pid() != os.Getpid() {
		t.Errorf("pid = %d, want %d", opts.pid(), os.Getpid())
	}
	if opts.jitter(100*time.Millisecond) < 100*time.Millisecond {
		t.Errorf("jitter < base")
	}
	if opts.jitter(0) != 0 {
		t.Errorf("jitter(0) != 0")
	}
	if opts.clock() == nil {
		t.Errorf("clock is nil")
	}

	customOpts := Options{
		Host: "custom-host",
		PID:  777,
		Jitter: func(d time.Duration) time.Duration {
			return d * 2
		},
	}
	if customOpts.host() != "custom-host" {
		t.Errorf("host() = %q, want custom-host", customOpts.host())
	}
	if customOpts.pid() != 777 {
		t.Errorf("pid() = %d, want 777", customOpts.pid())
	}
	if customOpts.jitter(10*time.Millisecond) != 20*time.Millisecond {
		t.Errorf("custom jitter failed")
	}
}

func TestStateStrings(t *testing.T) {
	t.Parallel()

	if StateAbsent.String() != "absent" {
		t.Errorf("StateAbsent.String() = %q", StateAbsent.String())
	}
	if StateFree.String() != "free" {
		t.Errorf("StateFree.String() = %q", StateFree.String())
	}
	if StateHeld.String() != "held" {
		t.Errorf("StateHeld.String() = %q", StateHeld.String())
	}
}

func TestHeldErrorFormatting(t *testing.T) {
	t.Parallel()

	e1 := &HeldError{Path: "lockfile.lock"}
	if !strings.Contains(e1.Error(), "is held") {
		t.Errorf("e1.Error() = %q", e1.Error())
	}

	e2 := &HeldError{Path: "lockfile.lock", Wait: 2 * time.Second}
	if !strings.Contains(e2.Error(), "waited 2s") {
		t.Errorf("e2.Error() = %q", e2.Error())
	}

	holder := Stamp{PID: 1234, Label: "my-holder"}
	e3 := &HeldError{Path: "lockfile.lock", Holder: holder, Wait: 5 * time.Second}
	if !strings.Contains(e3.Error(), "waited 5s") || !strings.Contains(e3.Error(), "my-holder") {
		t.Errorf("e3.Error() = %q", e3.Error())
	}
}

func TestFileLock_StringMethod(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "str.lock")

	lock, err := TryLock(path, "str-worker")
	if err != nil {
		t.Fatalf("TryLock failed: %v", err)
	}
	defer lock.Unlock()

	s := lock.String()
	if !strings.Contains(s, "str-worker") || !strings.Contains(s, path) {
		t.Errorf("lock.String() = %q, want path and label", s)
	}
}

func TestParseStamp_SpecialCases(t *testing.T) {
	t.Parallel()

	empty, err := ParseStamp("")
	if err != nil || !empty.IsZero() {
		t.Errorf("ParseStamp empty = %+v, %v", empty, err)
	}

	dash, err := ParseStamp("-")
	if err != nil || !dash.IsZero() {
		t.Errorf("ParseStamp dash = %+v, %v", dash, err)
	}

	barePID, err := ParseStamp("9876\n")
	if err != nil || barePID.PID != 9876 {
		t.Errorf("ParseStamp barePID = %+v, %v", barePID, err)
	}

	multiWithEmptyLines := "pid=456\n\nhost=box\nstarted=2026-09-27T12:00:00Z\nlabel=foo\n"
	st, err := ParseStamp(multiWithEmptyLines)
	if err != nil || st.PID != 456 || st.Host != "box" || st.Label != "foo" {
		t.Errorf("ParseStamp multi = %+v, %v", st, err)
	}

	singleLine := "pid=789 host=node1 started=2026-09-27T12:00:00Z label=bar"
	stSingle, err := ParseStamp(singleLine)
	if err != nil || stSingle.PID != 789 || stSingle.Host != "node1" || stSingle.Label != "bar" {
		t.Errorf("ParseStamp single = %+v, %v", stSingle, err)
	}
}

func TestHelpers_NilAndErrors(t *testing.T) {
	t.Parallel()

	if ok, err := tryLockFile(nil); ok || err == nil {
		t.Errorf("tryLockFile(nil) = %v, %v", ok, err)
	}
	if ok, err := trySharedLock(nil); ok || err == nil {
		t.Errorf("trySharedLock(nil) = %v, %v", ok, err)
	}
	unlockFile(nil) // should not panic

	empty := readExistingStamp(nil)
	if !empty.IsZero() {
		t.Errorf("readExistingStamp(nil) = %+v, want zero", empty)
	}

	_, err := ReadStamp(filepath.Join(t.TempDir(), "nonexistent"))
	if err == nil {
		t.Errorf("ReadStamp nonexistent should error")
	}

	clk := NewLockStepClock(time.Time{})
	if clk.Now().IsZero() {
		t.Errorf("NewLockStepClock(zero) returned zero time")
	}
}

func TestProbe_NotDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	regularFile := filepath.Join(dir, "regular.txt")
	if err := os.WriteFile(regularFile, []byte("file"), 0666); err != nil {
		t.Fatal(err)
	}
	badPath := filepath.Join(regularFile, "sub.lock")

	if _, _, err := Probe(badPath); err == nil {
		t.Errorf("Probe with non-directory parent should error")
	}
	if _, err := TryLock(badPath, "bad"); err == nil {
		t.Errorf("TryLock with non-directory parent should error")
	}
}

// H1 witness. An asker (a Probe, or another refused taker) holds the shared
// lock for an instant, and the kernel refuses the exclusive lock while it does.
// The asker here never leaves, so every take lands in that instant. Nobody
// holds, so the taker must answer busy (ErrBusy) and never held (ErrHeld):
// tla/FileLock.tla, HeldIsTrue, kept by the Blocked action's shared re-ask.
func TestTryLock_AskerIsNotAHolder(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "asker.lock")
	if err := os.WriteFile(path, nil, 0666); err != nil {
		t.Fatal(err)
	}
	asker, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer asker.Close()
	if ok, err := trySharedLock(asker); err != nil || !ok {
		t.Fatalf("asker's shared lock = %v, %v, want granted", ok, err)
	}

	lock, err := TryLock(path, "taker")
	if lock != nil {
		lock.Unlock()
		t.Fatal("TryLock succeeded while a shared lock was held")
	}
	if errors.Is(err, ErrHeld) {
		t.Errorf("told held with nobody holding: %v", err)
	}
	if _, ok := AsHeldError(err); ok {
		t.Errorf("a *HeldError with nobody holding: %v", err)
	}
	if !errors.Is(err, ErrBusy) {
		t.Errorf("err = %v, want ErrBusy", err)
	}

	// A bounded Lock kept out by the asker alone runs out as busy, not held.
	clk := NewLockStepClock(time.Time{})
	lock, err = LockWithOptions(path, "waiter", 50*time.Millisecond, Options{Clock: clk})
	if lock != nil {
		lock.Unlock()
		t.Fatal("Lock succeeded while a shared lock was held")
	}
	if errors.Is(err, ErrHeld) || !errors.Is(err, ErrBusy) || !errors.Is(err, ErrTimeout) {
		t.Errorf("bounded Lock against an asker: err = %v, want ErrTimeout and ErrBusy, never ErrHeld", err)
	}

	// The asker leaves; the next take is granted.
	unlockFile(asker)
	lock, err = TryLock(path, "taker")
	if err != nil {
		t.Fatalf("TryLock after the asker left: %v", err)
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
		t.Fatal(err)
	}
	defer holder.Unlock()

	for _, bound := range []time.Duration{0, 1, time.Millisecond, 10 * time.Millisecond, 200 * time.Millisecond, time.Second} {
		clk := NewLockStepClock(time.Time{})
		lock, err := LockWithOptions(path, "waiter", bound, Options{Clock: clk})
		if lock != nil {
			lock.Unlock()
			t.Fatalf("bound %s: acquired a held lock", bound)
		}
		if w := clk.Waited(); w > bound {
			t.Errorf("bound %s exceeded: waited %s", bound, w)
		}
		he, ok := AsHeldError(err)
		if !ok {
			t.Errorf("bound %s: the run-out does not name the holder: %v", bound, err)
			continue
		}
		if he.Holder.Label != "holder" || he.Holder.PID != os.Getpid() {
			t.Errorf("bound %s: holder = %s, want pid=%d label=\"holder\"", bound, he.Holder, os.Getpid())
		}
		if he.Wait != bound {
			t.Errorf("bound %s: Wait = %s, want the bound", bound, he.Wait)
		}
		if !errors.Is(err, ErrHeld) || !errors.Is(err, ErrTimeout) {
			t.Errorf("bound %s: errors.Is held=%v timeout=%v, want both: %v", bound, errors.Is(err, ErrHeld), errors.Is(err, ErrTimeout), err)
		}
	}
}

// H2 witness: HeldError.Wait is set by the package on a run-out, and printed.
func TestLock_RunOutSetsWait(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "wait.lock")
	holder, err := TryLock(path, "holder")
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Unlock()

	_, err = LockWithOptions(path, "waiter", 50*time.Millisecond, Options{Clock: NewLockStepClock(time.Time{})})
	he, ok := AsHeldError(err)
	if !ok {
		t.Fatalf("a bounded Lock that ran out against a holder is not a *HeldError: %v", err)
	}
	if he.Wait != 50*time.Millisecond {
		t.Errorf("HeldError.Wait = %s after a 50ms bound ran out", he.Wait)
	}
	if !strings.Contains(err.Error(), "waited 50ms") {
		t.Errorf("run-out text does not say the wait: %q", err.Error())
	}
}
