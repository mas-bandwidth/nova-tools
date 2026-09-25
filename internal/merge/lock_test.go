package merge

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Work list 2 and demanded test 2.

func TestASecondHolderWaitsTheBoundedTimeAndNamesTheFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), StateLock)
	release, err := Lock(path, LockWait)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if pid := HolderPID(path); pid != os.Getpid() {
		t.Errorf("the holder writes its pid into the lock file, got %d want %d", pid, os.Getpid())
	}
	// The bounded wait runs on an injected clock: Now stands still until Sleep moves it, so
	// the second holder reaches its deadline in as many polls as it would in real time and
	// not one wall-clock millisecond. The assertion is against that clock, never time.Since.
	start := time.Date(2026, 9, 18, 2, 45, 0, 0, time.UTC)
	nowFn, sleepFn, at := waitClock(start)
	_, err = lockWait(path, 150*time.Millisecond, nowFn, sleepFn)
	if err == nil {
		t.Fatal("a second holder against a live one must be refused")
	}
	if waited := at.Sub(start); waited < 100*time.Millisecond {
		t.Errorf("the second holder waited %s; it waits the bounded time before refusing", waited)
	}
	if !strings.Contains(err.Error(), "pid="+strconv.Itoa(os.Getpid())) {
		t.Errorf("the refusal must name the holder's pid, got %v", err)
	}
}

func TestTheLockIsFreeTheInstantItsHolderIsKilled(t *testing.T) {
	// SIGKILL is the case rule 2 is written for: the kernel releases the lock, so there
	// is no age to compute and nothing to break. The holder is a child process, because
	// a lock released by a goroutine would prove something else entirely.
	dir := t.TempDir()
	path := filepath.Join(dir, StateLock)
	helper := exec.Command(os.Args[0], "-test.run=TestHelperHoldsTheLock", "--", path)
	helper.Env = append(os.Environ(), "NOVA_MERGE_LOCK_HELPER="+path)
	ready, err := helper.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	// The child blocks on this pipe until the kill below; the wait is that pipe, never a
	// wall-clock sleep in the helper process.
	held, err := helper.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := ready.Read(buf); err != nil {
		t.Fatalf("the helper never said it had the lock: %v", err)
	}
	nowFn, sleepFn, _ := waitClock(time.Date(2026, 9, 18, 2, 45, 0, 0, time.UTC))
	if _, err := lockWait(path, 150*time.Millisecond, nowFn, sleepFn); err == nil {
		t.Fatal("the helper holds the lock; this take must be refused")
	}
	if err := helper.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	held.Close()
	_, _ = helper.Process.Wait()
	release, err := Lock(path, 2*time.Second)
	if err != nil {
		t.Fatalf("a killed holder holds nothing: the next writer takes the lock at once, with no age and no break; got %v", err)
	}
	release()
}

// TestHelperHoldsTheLock is the child of the test above: it takes the lock, says so, and
// waits to be killed. It is a test function because that is how a Go test spawns a child
// of itself without a second binary, and it does nothing at all unless the parent asked.
func TestHelperHoldsTheLock(t *testing.T) {
	path := os.Getenv("NOVA_MERGE_LOCK_HELPER")
	if path == "" {
		t.Skip("not the helper: this runs only as the child of TestTheLockIsFreeTheInstantItsHolderIsKilled")
	}
	if _, err := Lock(path, LockWait); err != nil {
		t.Fatal(err)
	}
	os.Stdout.WriteString("held\n")
	// Hold until the parent kills us: the parent keeps this pipe's write end open, so the
	// read blocks with no wall-clock sleep and no bound of its own.
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func TestAKillMidWriteLeavesTheOldStateEntireAndTheTempNameIsSteppedOver(t *testing.T) {
	lane := t.TempDir()
	if err := Init(lane, LaneConfig{Repo: "o/n", Base: "main", LaneBranch: "nova-merge/l"}); err != nil {
		t.Fatal(err)
	}
	if err := Update(lane, LockWait, func(st *State) error {
		st.PRs = append(st.PRs, &Entry{PR: 951, NeedsRead: "yes", State: StateNew})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(StatePath(lane))
	if err != nil {
		t.Fatal(err)
	}
	// A writer killed mid-write leaves the fixed temp name behind. It is a name a person
	// can see, and the next writer steps over it rather than reporting it as a stray.
	if err := os.WriteFile(filepath.Join(lane, StateTmpName), []byte("half a f"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(StatePath(lane)); string(got) != string(before) {
		t.Fatal("the old state must be entire")
	}
	if err := Update(lane, LockWait, func(st *State) error {
		st.PRs = append(st.PRs, &Entry{PR: 942, NeedsRead: "no", State: StateNew})
		return nil
	}); err != nil {
		t.Fatalf("the next writer steps over the stranded temp file: %v", err)
	}
	st, err := Load(lane)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.PRs) != 2 {
		t.Errorf("both writes are in the state, got %d", len(st.PRs))
	}
}
