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

	clk := newLockStepClock(time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC))
	timeout := 100 * time.Millisecond
	opts := options{
		clock:        clk,
		pollInterval: 10 * time.Millisecond,
	}

	lock2, err := lockWithOptions(path, "second", timeout, opts)
	if lock2 != nil {
		lock2.Unlock()
		t.Fatalf("lockWithOptions succeeded unexpectedly while held")
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

	opts := options{}
	if opts.getPollInterval() != defaultPollInterval {
		t.Errorf("pollInterval = %v, want %v", opts.getPollInterval(), defaultPollInterval)
	}
	if opts.getPID() != os.Getpid() {
		t.Errorf("pid = %d, want %d", opts.getPID(), os.Getpid())
	}
	if opts.getJitter(100*time.Millisecond) < 100*time.Millisecond {
		t.Errorf("jitter < base")
	}
	if opts.getJitter(0) != 0 {
		t.Errorf("jitter(0) != 0")
	}
	if opts.getClock() == nil {
		t.Errorf("clock is nil")
	}

	customOpts := options{
		host: "custom-host",
		pid:  777,
		jitter: func(d time.Duration) time.Duration {
			return d * 2
		},
	}
	if customOpts.getHost() != "custom-host" {
		t.Errorf("host() = %q, want custom-host", customOpts.getHost())
	}
	if customOpts.getPID() != 777 {
		t.Errorf("pid() = %d, want 777", customOpts.getPID())
	}
	if customOpts.getJitter(10*time.Millisecond) != 20*time.Millisecond {
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

	clk := newLockStepClock(time.Time{})
	if clk.Now().IsZero() {
		t.Errorf("newLockStepClock(zero) returned zero time")
	}
}

func TestRealClock(t *testing.T) {
	t.Parallel()

	rc := realClock{}
	now := rc.Now()
	if now.IsZero() {
		t.Fatal("realClock.Now() is zero")
	}
	rc.Sleep(0)
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
	clk := newLockStepClock(time.Time{})
	lock, err = lockWithOptions(path, "waiter", 50*time.Millisecond, options{clock: clk})
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
		clk := newLockStepClock(time.Time{})
		lock, err := lockWithOptions(path, "waiter", bound, options{clock: clk})
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

	_, err = lockWithOptions(path, "waiter", 50*time.Millisecond, options{clock: newLockStepClock(time.Time{})})
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

func TestMutant_Fsync(t *testing.T) {
	t.Parallel()

	// 1. defaultSync on a closed file descriptor MUST return an error.
	// If defaultSync was mutated to a dummy `return nil`, this fails!
	f, err := os.CreateTemp(t.TempDir(), "fsync-test")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err := defaultSync(f); err == nil {
		t.Fatal("defaultSync on closed file descriptor returned nil, want error")
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
		t.Fatal("tryLockWithOptions succeeded despite sync error")
	}
	if !errors.Is(err, syncErr) {
		t.Fatalf("err = %v, want syncErr", err)
	}

	// Lock file must not be held now
	state, _, probeErr := Probe(path)
	if probeErr != nil {
		t.Fatalf("Probe failed: %v", probeErr)
	}
	if state == StateHeld {
		t.Fatalf("lock still held after sync failure")
	}

	// 3. Verify sync is called on successful lock
	syncCalled := false
	optsSuccess := options{
		sync: func(f *os.File) error {
			syncCalled = true
			return f.Sync()
		},
	}
	successLock, err := tryLockWithOptions(path, "fsync-ok", optsSuccess)
	if err != nil {
		t.Fatalf("tryLockWithOptions failed: %v", err)
	}
	if !syncCalled {
		t.Fatal("sync was not called during successful tryLockWithOptions")
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
		t.Fatal("defaultJitter never produced a duration > d (mutant: return d)")
	}
	if len(seen) < 5 {
		t.Fatalf("defaultJitter produced only %d distinct values across 200 iterations (want >= 5)", len(seen))
	}

	// 2. lockLoop backoff must invoke opts.jitter
	dir := t.TempDir()
	path := filepath.Join(dir, "jitter_hook.lock")
	held, err := TryLock(path, "holder")
	if err != nil {
		t.Fatal(err)
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
		t.Fatal("opts.jitter was never invoked during lockLoop backoff (mutant: jitter removed)")
	}
}

func TestTryLock_DedicatedPathTruncates(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "data_file.lock")

	precious := []byte("critical initial data that should be truncated when taking lock\n")
	if err := os.WriteFile(path, precious, 0666); err != nil {
		t.Fatal(err)
	}

	lock, err := TryLock(path, "dedicated-taker")
	if err != nil {
		t.Fatalf("TryLock on existing file failed: %v", err)
	}
	defer lock.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(data) == string(precious) {
		t.Fatal("TryLock did not truncate existing file contents")
	}
	if !strings.Contains(string(data), "label=dedicated-taker") {
		t.Fatalf("file content does not contain new stamp: %s", string(data))
	}
}

func TestCappedHostileLabelAndPathSanitization(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "huge_label.lock")

	bigLabel := strings.Repeat("X", 1024*1024) // 1 MB label
	lock, err := TryLock(path, bigLabel)
	if err != nil {
		t.Fatalf("TryLock with 1MB label failed: %v", err)
	}
	defer lock.Unlock()

	if len(lock.Stamp().Label) > 1024 {
		t.Fatalf("lock.Stamp().Label length = %d, want <= 1024", len(lock.Stamp().Label))
	}
	if len(lock.String()) > 4096 {
		t.Fatalf("lock.String() length = %d, want <= 4096", len(lock.String()))
	}

	// Second taker fails with HeldError
	_, herr := TryLock(path, "second")
	if herr == nil {
		t.Fatal("second TryLock succeeded, want HeldError")
	}
	if len(herr.Error()) > 4096 {
		t.Fatalf("HeldError.Error() length = %d, want <= 4096", len(herr.Error()))
	}
	// Error string must not contain control characters
	for i := 0; i < len(herr.Error()); i++ {
		if c := herr.Error()[i]; c < 0x20 || c == 0x7f {
			t.Fatalf("HeldError.Error() contains control char 0x%02x: %q", c, herr.Error())
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
						t.Fatalf("error text contains unescaped control char 0x%02x: %q", c, err2.Error())
					}
				}
			}
		}
	}
}
