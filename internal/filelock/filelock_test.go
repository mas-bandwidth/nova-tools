//go:build !windows

package filelock

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestTryLock_Success(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.lock")

	l, err := TryLock(path, "test-job")
	if err != nil {
		t.Fatalf("TryLock failed: %v", err)
	}
	if l.Path() != path {
		t.Errorf("Path() = %q, want %q", l.Path(), path)
	}
	if l.Stamp().PID != os.Getpid() {
		t.Errorf("Stamp().PID = %d, want %d", l.Stamp().PID, os.Getpid())
	}
	if l.Stamp().Label != "test-job" {
		t.Errorf("Stamp().Label = %q, want %q", l.Stamp().Label, "test-job")
	}
	if !strings.Contains(l.String(), "test.lock") || !strings.Contains(l.String(), "test-job") {
		t.Errorf("String() = %q, missing expected details", l.String())
	}

	state, stamp, err := Probe(path)
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}
	if state != StateHeld {
		t.Errorf("Probe state = %s, want %s", state, StateHeld)
	}
	if stamp.PID != os.Getpid() {
		t.Errorf("Probe stamp PID = %d, want %d", stamp.PID, os.Getpid())
	}

	if err := l.Unlock(); err != nil {
		t.Fatalf("Unlock failed: %v", err)
	}

	stateAfter, _, err := Probe(path)
	if err != nil {
		t.Fatalf("Probe after unlock failed: %v", err)
	}
	if stateAfter != StateFree {
		t.Errorf("Probe state after unlock = %s, want %s", stateAfter, StateFree)
	}
}

func TestTryLock_Held(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "held.lock")

	first, err := TryLock(path, "first-holder")
	if err != nil {
		t.Fatalf("first TryLock failed: %v", err)
	}
	defer first.Unlock()

	second, err := TryLock(path, "second-holder")
	if err == nil {
		second.Unlock()
		t.Fatal("second TryLock succeeded on held lock, want ErrHeld")
	}
	if !errors.Is(err, ErrHeld) {
		t.Fatalf("err = %v, want errors.Is(err, ErrHeld)", err)
	}

	heldErr, ok := AsHeldError(err)
	if !ok {
		t.Fatalf("AsHeldError(err) returned false for %v", err)
	}
	if heldErr.Path != path {
		t.Errorf("heldErr.Path = %q, want %q", heldErr.Path, path)
	}
	if heldErr.Holder.PID != os.Getpid() {
		t.Errorf("heldErr.Holder.PID = %d, want %d", heldErr.Holder.PID, os.Getpid())
	}
	if heldErr.Holder.Label != "first-holder" {
		t.Errorf("heldErr.Holder.Label = %q, want %q", heldErr.Holder.Label, "first-holder")
	}
}

func TestTryLock_Stale(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "stale.lock")

	deadStamp := Stamp{
		PID:     99999999,
		Host:    "testhost",
		Started: time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC),
		Label:   "crashed-worker",
	}
	if err := os.WriteFile(path, []byte(deadStamp.Format()), 0o644); err != nil {
		t.Fatal(err)
	}

	// opts with dead PID check
	opts := Options{
		ProcessAlive: func(pid int) bool {
			return false
		},
	}

	l, err := TryLockWithOptions(path, "new-claim", opts)
	if err == nil {
		l.Unlock()
		t.Fatal("TryLock on stale lock succeeded, want ErrStale (must never steal stale locks silently)")
	}
	if !errors.Is(err, ErrStale) {
		t.Fatalf("err = %v, want errors.Is(err, ErrStale)", err)
	}

	staleErr, ok := AsStaleError(err)
	if !ok {
		t.Fatalf("AsStaleError(err) returned false for %v", err)
	}
	if staleErr.Holder.PID != 99999999 {
		t.Errorf("staleErr.Holder.PID = %d, want 99999999", staleErr.Holder.PID)
	}

	// Verify file was NOT stolen or overwritten
	st, err := ReadStamp(path)
	if err != nil {
		t.Fatalf("ReadStamp failed: %v", err)
	}
	if st.PID != 99999999 || st.Label != "crashed-worker" {
		t.Fatalf("file content was modified, want dead stamp preserved; got %v", st)
	}
}

func TestLock_Success(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "lock_success.lock")

	l, err := Lock(path, "worker", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("Lock failed: %v", err)
	}
	defer l.Unlock()

	if l.Stamp().Label != "worker" {
		t.Errorf("Label = %q, want worker", l.Stamp().Label)
	}
}

func TestLock_TimeoutBound(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "timeout.lock")

	first, err := TryLock(path, "holder")
	if err != nil {
		t.Fatalf("first TryLock failed: %v", err)
	}
	defer first.Unlock()

	clk := NewLockStepClock(time.Time{})
	opts := Options{
		Clock:        clk,
		PollInterval: 25 * time.Millisecond,
		Jitter:       func(d time.Duration) time.Duration { return d },
	}

	_, err2 := LockWithOptions(path, "waiter", 200*time.Millisecond, opts)
	if err2 == nil {
		t.Fatal("LockWithOptions succeeded while held, want ErrHeld")
	}
	if !errors.Is(err2, ErrHeld) {
		t.Fatalf("err2 = %v, want errors.Is(err2, ErrHeld)", err2)
	}

	if waited := clk.Waited(); waited < 200*time.Millisecond {
		t.Errorf("virtual wait duration = %v, want at least 200ms", waited)
	}
}

func TestLock_AcquiresAfterRelease(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "acquire_after_release.lock")

	first, err := TryLock(path, "first")
	if err != nil {
		t.Fatalf("first TryLock failed: %v", err)
	}

	// Clock seam that releases the first lock after 2 virtual sleep cycles
	var mu sync.Mutex
	var sleeps int
	clk := NewLockStepClock(time.Time{})
	seamClock := &hookClock{
		clk: clk,
		onSleep: func() {
			mu.Lock()
			sleeps++
			if sleeps == 2 {
				_ = first.Unlock()
			}
			mu.Unlock()
		},
	}

	opts := Options{
		Clock:        seamClock,
		PollInterval: 25 * time.Millisecond,
		Jitter:       func(d time.Duration) time.Duration { return d },
	}

	second, err := LockWithOptions(path, "second", 500*time.Millisecond, opts)
	if err != nil {
		t.Fatalf("second LockWithOptions failed after release: %v", err)
	}
	defer second.Unlock()

	if second.Stamp().Label != "second" {
		t.Errorf("Label = %q, want second", second.Stamp().Label)
	}
}

type hookClock struct {
	clk     *LockStepClock
	onSleep func()
}

func (h *hookClock) Now() time.Time {
	return h.clk.Now()
}

func (h *hookClock) Sleep(d time.Duration) {
	if h.onSleep != nil {
		h.onSleep()
	}
	h.clk.Sleep(d)
}

func TestLock_StaleRefusesToSteal(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "stale_lock_wait.lock")

	deadStamp := Stamp{
		PID:     99999999,
		Host:    "box",
		Started: time.Date(2026, 9, 27, 11, 0, 0, 0, time.UTC),
		Label:   "dead-runner",
	}
	if err := os.WriteFile(path, []byte(deadStamp.Format()), 0o644); err != nil {
		t.Fatal(err)
	}

	clk := NewLockStepClock(time.Time{})
	opts := Options{
		Clock: clk,
		ProcessAlive: func(pid int) bool {
			return false
		},
	}

	_, err := LockWithOptions(path, "taker", 500*time.Millisecond, opts)
	if err == nil {
		t.Fatal("Lock on stale file succeeded, want ErrStale")
	}
	if !errors.Is(err, ErrStale) {
		t.Fatalf("err = %v, want errors.Is(err, ErrStale)", err)
	}

	// Must fail immediately without waiting out the budget
	if waited := clk.Waited(); waited != 0 {
		t.Errorf("Lock on stale lock waited %v, want 0", waited)
	}
}

func TestUnlock_Idempotent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "idempotent.lock")

	l, err := TryLock(path, "holder")
	if err != nil {
		t.Fatalf("TryLock failed: %v", err)
	}

	if err := l.Unlock(); err != nil {
		t.Errorf("first Unlock failed: %v", err)
	}
	if err := l.Unlock(); err != nil {
		t.Errorf("second Unlock failed: %v", err)
	}

	var nilLock *FileLock
	if err := nilLock.Unlock(); err != nil {
		t.Errorf("nilLock.Unlock failed: %v", err)
	}
}

func TestProbe_AbsentNeverCreatesFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.lock")

	state, stamp, err := Probe(path)
	if err != nil {
		t.Fatalf("Probe on absent file failed: %v", err)
	}
	if state != StateAbsent {
		t.Errorf("Probe state = %s, want %s", state, StateAbsent)
	}
	if !stamp.IsZero() {
		t.Errorf("Probe stamp on absent file = %+v, want zero", stamp)
	}

	// Confirm Probe NEVER created the file
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Probe created file at %s: err = %v", path, err)
	}
}

func TestProbe_Free(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "free.lock")

	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	state, stamp, err := Probe(path)
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}
	if state != StateFree {
		t.Errorf("state = %s, want %s", state, StateFree)
	}
	if !stamp.IsZero() {
		t.Errorf("stamp = %+v, want zero", stamp)
	}
}

func TestProbe_Held(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "probe_held.lock")

	l, err := TryLock(path, "holding-proc")
	if err != nil {
		t.Fatalf("TryLock failed: %v", err)
	}
	defer l.Unlock()

	state, stamp, err := Probe(path)
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}
	if state != StateHeld {
		t.Errorf("state = %s, want %s", state, StateHeld)
	}
	if stamp.PID != os.Getpid() || stamp.Label != "holding-proc" {
		t.Errorf("stamp = %+v, unexpected", stamp)
	}
}

func TestProbe_Stale(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "probe_stale.lock")

	deadStamp := Stamp{
		PID:     99999999,
		Host:    "hostA",
		Started: time.Date(2026, 9, 27, 8, 0, 0, 0, time.UTC),
		Label:   "dead-job",
	}
	if err := os.WriteFile(path, []byte(deadStamp.Format()), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := Options{
		ProcessAlive: func(pid int) bool { return false },
	}

	state, stamp, err := ProbeWithOptions(path, opts)
	if err != nil {
		t.Fatalf("ProbeWithOptions failed: %v", err)
	}
	if state != StateStale {
		t.Errorf("state = %s, want %s", state, StateStale)
	}
	if stamp.PID != 99999999 || stamp.Label != "dead-job" {
		t.Errorf("stamp = %+v, unexpected", stamp)
	}
}

func TestProbe_Symlink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "target.lock")
	if err := os.WriteFile(target, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(dir, "link.lock")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	_, _, err := Probe(link)
	if err == nil {
		t.Fatal("Probe on symlink succeeded, want error")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("err = %q, want symlink mention", err.Error())
	}
}

func TestProbe_Directory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	subDir := filepath.Join(dir, "somedir.lock")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	_, _, err := Probe(subDir)
	if err == nil {
		t.Fatal("Probe on directory succeeded, want error")
	}
	if !strings.Contains(err.Error(), "directory at the lock path") {
		t.Errorf("err = %q, want directory refusal", err.Error())
	}
}

func TestProcessAlive(t *testing.T) {
	t.Parallel()

	if ProcessAlive(0) {
		t.Error("ProcessAlive(0) = true, want false")
	}
	if ProcessAlive(-1) {
		t.Error("ProcessAlive(-1) = true, want false")
	}
	if ProcessAlive(-42) {
		t.Error("ProcessAlive(-42) = true, want false")
	}
	if !ProcessAlive(os.Getpid()) {
		t.Errorf("ProcessAlive(os.Getpid()=%d) = false, want true", os.Getpid())
	}
	if ProcessAlive(99999999) {
		t.Error("ProcessAlive(99999999) = true, want false")
	}
}

func TestClearStale(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "clear.lock")

	// Absent file is a no-op / success
	if err := ClearStale(filepath.Join(dir, "absent.lock")); err == nil {
		// absent probe returns StateAbsent, which is not stale
	}

	// Non-stale (free) file cannot be cleared as stale
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ClearStale(path); err == nil {
		t.Fatal("ClearStale on free lock succeeded, want error")
	}

	// Non-stale (held) file cannot be cleared as stale
	l, err := TryLock(path, "holding")
	if err != nil {
		t.Fatalf("TryLock failed: %v", err)
	}
	if err := ClearStale(path); err == nil {
		l.Unlock()
		t.Fatal("ClearStale on held lock succeeded, want error")
	}
	l.Unlock()

	// Stale file is cleared successfully
	deadStamp := Stamp{PID: 99999999, Label: "stale-card"}
	if err := os.WriteFile(path, []byte(deadStamp.Format()), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := Options{ProcessAlive: func(int) bool { return false }}
	if err := ClearStaleWithOptions(path, opts); err != nil {
		t.Fatalf("ClearStaleWithOptions failed on stale lock: %v", err)
	}

	if state, _, _ := Probe(path); state != StateAbsent {
		t.Errorf("state after ClearStale = %s, want %s", state, StateAbsent)
	}
}

func TestStamp_FormatsAndParses(t *testing.T) {
	t.Parallel()

	// Zero stamp
	var zero Stamp
	if !zero.IsZero() {
		t.Error("zero.IsZero() = false, want true")
	}
	if zero.String() != "-" {
		t.Errorf("zero.String() = %q, want \"-\"", zero.String())
	}
	parsedZero, err := ParseStamp("")
	if err != nil || !parsedZero.IsZero() {
		t.Errorf("ParseStamp(\"\") = %+v, %v", parsedZero, err)
	}

	// Bare PID
	bare, err := ParseStamp("54321")
	if err != nil || bare.PID != 54321 {
		t.Errorf("ParseStamp bare PID = %+v, %v", bare, err)
	}

	// Multi-line
	original := Stamp{
		PID:     1234,
		Host:    "bench-mac",
		Started: time.Date(2026, 9, 27, 14, 30, 0, 123456000, time.UTC),
		Label:   "card 01/42=run",
	}
	formatted := original.Format()
	parsed, err := ParseStamp(formatted)
	if err != nil {
		t.Fatalf("ParseStamp failed: %v", err)
	}
	if parsed.PID != original.PID {
		t.Errorf("parsed.PID = %d, want %d", parsed.PID, original.PID)
	}
	if parsed.Host != original.Host {
		t.Errorf("parsed.Host = %q, want %q", parsed.Host, original.Host)
	}
	if !parsed.Started.Equal(original.Started) {
		t.Errorf("parsed.Started = %v, want %v", parsed.Started, original.Started)
	}
	if parsed.Label != original.Label {
		t.Errorf("parsed.Label = %q, want %q", parsed.Label, original.Label)
	}

	// Single line space-separated format
	line := "pid=9876 host=box started=2026-09-27T12:00:00Z label=simple"
	fromLine, err := ParseStamp(line)
	if err != nil {
		t.Fatalf("ParseStamp single-line failed: %v", err)
	}
	if fromLine.PID != 9876 || fromLine.Host != "box" || fromLine.Label != "simple" {
		t.Errorf("fromLine = %+v, unexpected", fromLine)
	}

	// ReadStamp missing file
	_, err = ReadStamp("/nonexistent/file/stamp")
	if err == nil {
		t.Error("ReadStamp on missing file succeeded, want error")
	}
}

func TestLockStepClock(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	clk := NewLockStepClock(start)

	if !clk.Now().Equal(start) {
		t.Errorf("Now() = %v, want %v", clk.Now(), start)
	}
	clk.Sleep(150 * time.Millisecond)
	if clk.Waited() != 150*time.Millisecond {
		t.Errorf("Waited() = %v, want 150ms", clk.Waited())
	}
	wantNow := start.Add(150 * time.Millisecond)
	if !clk.Now().Equal(wantNow) {
		t.Errorf("Now() = %v, want %v", clk.Now(), wantNow)
	}
}

func TestErrors(t *testing.T) {
	t.Parallel()

	held := &HeldError{
		Path:   "foo.lock",
		Holder: Stamp{PID: 123, Label: "job"},
		Wait:   100 * time.Millisecond,
	}
	if !errors.Is(held, ErrHeld) {
		t.Error("errors.Is(held, ErrHeld) = false, want true")
	}
	if !strings.Contains(held.Error(), "foo.lock") || !strings.Contains(held.Error(), "123") {
		t.Errorf("held.Error() = %q, missing details", held.Error())
	}

	heldZero := &HeldError{Path: "bar.lock"}
	if !strings.Contains(heldZero.Error(), "bar.lock is held") {
		t.Errorf("heldZero.Error() = %q", heldZero.Error())
	}

	stale := &StaleError{
		Path:   "foo.lock",
		Holder: Stamp{PID: 456},
	}
	if !errors.Is(stale, ErrStale) {
		t.Error("errors.Is(stale, ErrStale) = false, want true")
	}
	if !strings.Contains(stale.Error(), "foo.lock is stale") || !strings.Contains(stale.Error(), "456") {
		t.Errorf("stale.Error() = %q, missing details", stale.Error())
	}

	staleZero := &StaleError{Path: "bar.lock"}
	if !strings.Contains(staleZero.Error(), "bar.lock is stale") {
		t.Errorf("staleZero.Error() = %q", staleZero.Error())
	}

	otherErr := errors.New("other")
	if _, ok := AsHeldError(otherErr); ok {
		t.Error("AsHeldError(otherErr) = true, want false")
	}
	if _, ok := AsStaleError(otherErr); ok {
		t.Error("AsStaleError(otherErr) = true, want false")
	}
}

func TestInvalidPath(t *testing.T) {
	t.Parallel()

	badPath := "/nonexistent/directory/that/cannot/exist/file.lock"
	if _, err := TryLock(badPath, "bad"); err == nil {
		t.Fatal("TryLock on invalid path succeeded, want error")
	}
	if _, err := Lock(badPath, "bad", 10*time.Millisecond); err == nil {
		t.Fatal("Lock on invalid path succeeded, want error")
	}
}

func TestOptionsDefaults(t *testing.T) {
	t.Parallel()

	opts := Options{
		Host: "custom-host",
	}
	if opts.host() != "custom-host" {
		t.Errorf("host() = %q, want custom-host", opts.host())
	}
	if opts.pollInterval() != defaultPollInterval {
		t.Errorf("pollInterval() = %v, want default", opts.pollInterval())
	}
	jittered := opts.jitter(100 * time.Millisecond)
	if jittered < 100*time.Millisecond {
		t.Errorf("jittered = %v, want >= 100ms", jittered)
	}
}

func TestTryLockFile_NilFile(t *testing.T) {
	t.Parallel()

	ok, err := tryLockFile(nil)
	if ok || err == nil {
		t.Errorf("tryLockFile(nil) = %v, %v, want false, error", ok, err)
	}
	unlockFile(nil) // should not panic
}

func TestKindOfMode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		mode os.FileMode
		want string
	}{
		{os.ModeDir, "directory"},
		{os.ModeNamedPipe, "fifo"},
		{os.ModeSocket, "socket"},
		{os.ModeDevice, "device"},
		{os.ModeSymlink, "symlink"},
		{os.ModeIrregular, "something that is not a regular file"},
	}

	for _, tc := range cases {
		if got := kindOfMode(tc.mode); got != tc.want {
			t.Errorf("kindOfMode(%v) = %q, want %q", tc.mode, got, tc.want)
		}
	}
}

func TestParseStamp_MoreCases(t *testing.T) {
	t.Parallel()

	// Multi-line with empty lines, lines without '=', and RFC3339 without nano
	text := "pid=4321\n\nignored line\nhost=box1\nstarted=2026-09-27T12:00:00Z\nlabel=multi\n"
	st, err := ParseStamp(text)
	if err != nil {
		t.Fatalf("ParseStamp failed: %v", err)
	}
	if st.PID != 4321 || st.Host != "box1" || st.Label != "multi" {
		t.Errorf("st = %+v, unexpected", st)
	}

	// Single line with tokens without '=', and RFC3339 without nano
	single := "notoken pid=777 host=box2 started=2026-09-27T12:00:00Z label=test"
	stSingle, err := ParseStamp(single)
	if err != nil {
		t.Fatalf("ParseStamp single failed: %v", err)
	}
	if stSingle.PID != 777 || stSingle.Host != "box2" || stSingle.Label != "test" {
		t.Errorf("stSingle = %+v, unexpected", stSingle)
	}
}

func TestUnlock_NilFile(t *testing.T) {
	t.Parallel()

	fl := &FileLock{}
	if err := fl.Unlock(); err != nil {
		t.Errorf("fl.Unlock() with nil file = %v, want nil", err)
	}
}

func TestLock_HeldWithStaleStamp(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "held_stale.lock")

	first, err := TryLock(path, "first")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Unlock()

	// Write dead PID into the file while first holds flock
	deadStamp := Stamp{PID: 99999999, Label: "dead"}
	if err := os.WriteFile(path, []byte(deadStamp.Format()), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := Options{
		ProcessAlive: func(pid int) bool { return false },
	}

	_, err = LockWithOptions(path, "second", 100*time.Millisecond, opts)
	if err == nil {
		t.Fatal("Lock on held file with dead stamp succeeded, want ErrStale")
	}
	if !errors.Is(err, ErrStale) {
		t.Fatalf("err = %v, want errors.Is(err, ErrStale)", err)
	}
}

func TestClearStale_AlreadyRemoved(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "missing_clear.lock")

	deadStamp := Stamp{PID: 99999999, Label: "stale"}
	if err := os.WriteFile(path, []byte(deadStamp.Format()), 0o644); err != nil {
		t.Fatal(err)
	}

	// Fake ProcessAlive that removes the file during check
	opts := Options{
		ProcessAlive: func(pid int) bool {
			_ = os.Remove(path)
			return false
		},
	}

	// ClearStale when file vanished before OpenFile
	err := ClearStaleWithOptions(path, opts)
	if err != nil {
		t.Errorf("ClearStale on vanished file = %v, want nil", err)
	}
}

func TestProbe_PermissionDenied(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "noperm.lock")
	if err := os.WriteFile(path, []byte{}, 0o000); err != nil {
		t.Fatal(err)
	}

	_, _, err := Probe(path)
	if err == nil {
		t.Fatal("Probe on 0000 file succeeded, want error")
	}
}

func TestClearStale_ProbeError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	subDir := filepath.Join(dir, "dir.lock")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	err := ClearStale(subDir)
	if err == nil {
		t.Fatal("ClearStale on directory succeeded, want error")
	}
}

func TestClearStale_HeldDuringClear(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "held_race.lock")

	deadStamp := Stamp{PID: 99999999, Label: "stale"}
	if err := os.WriteFile(path, []byte(deadStamp.Format()), 0o644); err != nil {
		t.Fatal(err)
	}

	var lockFd *os.File
	opts := Options{
		ProcessAlive: func(pid int) bool {
			if lockFd == nil {
				f, err := os.OpenFile(path, os.O_RDWR, 0o644)
				if err == nil {
					if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
						lockFd = f
					} else {
						_ = f.Close()
					}
				}
			}
			return false
		},
	}

	err := ClearStaleWithOptions(path, opts)
	if lockFd != nil {
		_ = syscall.Flock(int(lockFd.Fd()), syscall.LOCK_UN)
		_ = lockFd.Close()
	}
	if err == nil {
		t.Fatal("ClearStale succeeded while file was held, want error")
	}
}

func TestClearStale_RevivedDuringClear(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "revived.lock")

	stamp := Stamp{PID: 99999999, Label: "reviving"}
	if err := os.WriteFile(path, []byte(stamp.Format()), 0o644); err != nil {
		t.Fatal(err)
	}

	callCount := 0
	opts := Options{
		ProcessAlive: func(pid int) bool {
			callCount++
			// First call (Probe): returns false (stale)
			// Subsequent calls (ClearStale double-check): returns true (alive!)
			return callCount > 1
		},
	}

	err := ClearStaleWithOptions(path, opts)
	if err == nil {
		t.Fatal("ClearStale succeeded when holder became alive, want error")
	}
	if !strings.Contains(err.Error(), "alive") {
		t.Errorf("err = %q, want mention of alive", err.Error())
	}
}

func TestHelpersWithClosedFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "closed.lock")
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	st := readExistingStamp(f)
	if !st.IsZero() {
		t.Errorf("readExistingStamp(closed) = %+v, want zero", st)
	}

	if err := stampHolder(f, Stamp{PID: 123}); err == nil {
		t.Error("stampHolder(closed) = nil, want error")
	}

	ok, err := tryLockFile(f)
	if ok || err == nil {
		t.Errorf("tryLockFile(closed) = %v, %v, want false, error", ok, err)
	}
}

func TestParseStamp_TokensWithoutEqualsAndPrefixes(t *testing.T) {
	t.Parallel()

	st1, err := ParseStamp("pid=100 start=2026-09-27T10:00:00Z")
	if err != nil || st1.PID != 100 || st1.Started.IsZero() {
		t.Errorf("st1 = %+v, %v", st1, err)
	}
	st2, err := ParseStamp("pid=200 at=2026-09-27T10:00:00Z")
	if err != nil || st2.PID != 200 || st2.Started.IsZero() {
		t.Errorf("st2 = %+v, %v", st2, err)
	}
}



