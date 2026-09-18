package swarm

import (
	"bytes"
	"os"
	"sync"
	"testing"
	"time"
)

// manualClock is the batchClock the timing tests inject. The idle window and the
// whole-batch deadline move only when the test moves them, so a card is asserted
// killed by the code and never by how quickly a loaded machine scheduled its
// process (#916). It is the seam batch.go reads through.
type manualClock struct {
	mu        sync.Mutex
	now       time.Time
	afterCh   chan time.Time
	afterAt   time.Time
	afterDone chan struct{}
	tickCh    chan time.Time
	tickDone  chan struct{}
	afterOnce sync.Once
	tickOnce  sync.Once
}

func newManualClock() *manualClock {
	return &manualClock{
		now:       time.Unix(1_700_000_000, 0),
		afterCh:   make(chan time.Time, 1),
		afterDone: make(chan struct{}),
		tickCh:    make(chan time.Time),
		tickDone:  make(chan struct{}),
	}
}

func (c *manualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *manualClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	c.afterAt = c.now.Add(d)
	c.mu.Unlock()
	c.afterOnce.Do(func() { close(c.afterDone) })
	return c.afterCh
}

func (c *manualClock) NewTicker(time.Duration) (<-chan time.Time, func()) {
	c.tickOnce.Do(func() { close(c.tickDone) })
	return c.tickCh, func() {}
}

// waitDeadline blocks until the batch has taken its deadline, so advance can fire it.
func (c *manualClock) waitDeadline() { <-c.afterDone }

// waitTick blocks until the idle monitor has taken its ticker.
func (c *manualClock) waitTick() { <-c.tickDone }

// advance moves the clock forward by d and fires the deadline when it is reached.
func (c *manualClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	fire := !c.afterAt.IsZero() && !c.afterAt.After(c.now)
	now := c.now
	c.mu.Unlock()
	if fire {
		select {
		case c.afterCh <- now:
		default:
		}
	}
}

// tick delivers one idle poll and waits for the monitor to receive it, so the
// monitor's state after the call is settled.
func (c *manualClock) tick() {
	c.mu.Lock()
	now := c.now
	c.mu.Unlock()
	c.tickCh <- now
}

// runBatchClock drives one Batch under the given manual clock. drive runs while the
// batch is under way; it is where a test advances time or waits for a file the
// runner wrote. The process is a real executable and its exit is real: only the
// clock the kill logic reads is injected.
func runBatchClock(in BatchInput, clk *manualClock, drive func()) (int, string, string) {
	in.clock = clk
	var out, errb bytes.Buffer
	in.Stdout = &out
	in.Stderr = &errb
	done := make(chan int, 1)
	go func() { done <- Batch(in) }()
	drive()
	code := <-done
	return code, out.String(), errb.String()
}

// waitForTreeCPU waits, against a five-second real bound and never an assertion, until the
// spin card's process tree has accrued CPU -- the silent grandchild burning a core. The spin
// card's runner is the only child of this test that forks a child of its own, so it is named
// without a command line, and the sleeping card's runner never reaches the floor. A loaded
// runner can take a moment to fork the burner, and a first activity sample taken before it
// exists has no CPU to compare against, which is how a busy-and-silent card was read as idle;
// the test takes its first sample only after this returns.
func waitForTreeCPU(t *testing.T) {
	t.Helper()
	const floor = 50 * time.Millisecond
	me := os.Getpid()
	base := map[int]uint64{}
	deadline := time.Now().Add(5 * time.Second) // wall-ok: a readiness poll on a real child, never an assertion
	for time.Now().Before(deadline) {
		snap := newProcSnapshot()
		for _, pid := range snap.children[me] {
			if len(snap.children[pid]) == 0 {
				continue // the sleeping card's runner forks nothing
			}
			cpu, ok := snap.TreeCPU(pid)
			if !ok {
				continue
			}
			if b, seen := base[pid]; seen {
				if cpu >= b+uint64(floor) {
					return
				}
			} else {
				base[pid] = cpu
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("waiting for the spin card's process tree to accrue CPU: timed out")
}

// waitForFile waits, against a thirty-second real bound and never an assertion,
// until path exists. The runner is a real process and this is a readiness wait on
// its work, not a claim about the machine.
func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if fileExists(path) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("waiting for %s: timed out", path)
}

// waitForLog waits until path holds want non-header output lines.
func waitForLog(t *testing.T, path string, want int) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if logOutputLines(path) >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("waiting for %s to hold %d log lines: timed out", path, want)
}
