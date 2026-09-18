package swarm

import (
	"bytes"
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

// fakeTreeSampler provides deterministic per-card CPU activity snapshots.
// As cards are polled, each process pid is assigned an index (0, 1, ...),
// matching the order cards are defined in the batch.
type fakeTreeSampler struct {
	mu         sync.Mutex
	pids       []int
	cpuForCard func(cardIndex int, pid int) (uint64, bool)
}

func (s *fakeTreeSampler) TreeCPU(pid int) (uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i, p := range s.pids {
		if p == pid {
			idx = i
			break
		}
	}
	if idx == -1 {
		idx = len(s.pids)
		s.pids = append(s.pids, pid)
	}
	if s.cpuForCard != nil {
		return s.cpuForCard(idx, pid)
	}
	return 0, false
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
