package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TakeLock(run.lock, 0) is a probe: supervise uses it to notice that no dispatcher
// holds the pool. A non-positive wait must fail at once when this process already
// holds the lock, not park on the in-process turn.
func TestAZeroWaitLockProbeDoesNotTakeAHeldLock(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "run.lock")
	release, err := takeFileLock(path, 0)
	if err != nil {
		t.Fatalf("the free lock was not taken: %v", err)
	}
	_, err = takeFileLock(path, 0)
	if err == nil || !strings.Contains(err.Error(), "another nova-swarm holds") {
		t.Fatalf("a zero wait acquired a lock this process still holds: %v", err)
	}
	release()
	release()
	again, err := takeFileLock(path, 0)
	if err != nil {
		t.Fatalf("the lock was not released, or a second release wedged the turn: %v", err)
	}
	again()
}

// An open that fails has already taken the turn. It has to give that turn back,
// or the next take on the same path waits out the bound on a lock nobody holds.
func TestAFailedOpenDoesNotKeepTheTurn(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "missing", "slots.lock")
	if _, err := takeFileLock(path, 0); err == nil {
		t.Fatal("opened a lock in a directory that does not exist")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	release, err := takeFileLock(path, 0)
	if err != nil {
		t.Fatalf("the turn stayed held after the open failed: %v", err)
	}
	release()
}

// Several probes at once still describe one lock. The in-process turn is in front
// of the flock; it must not let a second zero-wait caller through while the first
// has not released.
func TestOneZeroWaitWinsWhenSeveralAskTogether(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "slots.lock")
	const n = 32
	var wg sync.WaitGroup
	wins := make(chan func(), n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			release, err := takeFileLock(path, 0)
			if err == nil {
				wins <- release
			}
		}()
	}
	close(start)
	wg.Wait()
	close(wins)
	var got []func()
	for release := range wins {
		got = append(got, release)
	}
	if len(got) != 1 {
		t.Fatalf("zero-wait takes that acquired together: %d, want 1", len(got))
	}
	got[0]()
	release, err := takeFileLock(path, 0)
	if err != nil {
		t.Fatalf("the winner's release did not give the lock back: %v", err)
	}
	release()
}
