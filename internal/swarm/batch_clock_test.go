package swarm

import (
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
	polled    chan struct{}
	gone      chan struct{}
	afterOnce sync.Once
	tickOnce  sync.Once
	// giveUp, when set, is tick's give-up bound in place of testWait(): a test
	// that proves the give-up path fires it at once instead of waiting it out.
	giveUp func() <-chan time.Time
}

func newManualClock() *manualClock {
	return &manualClock{
		now:       time.Unix(1_700_000_000, 0),
		afterCh:   make(chan time.Time, 1),
		afterDone: make(chan struct{}),
		tickCh:    make(chan time.Time),
		tickDone:  make(chan struct{}),
		polled:    make(chan struct{}, 1),
		gone:      make(chan struct{}),
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

// testWait is the allowed poll bound: NOVA_TEST_WAIT when set, thirty seconds
// otherwise. It is read at the call, never written as a constant, so a loaded
// machine lengthens the wait rather than flaking a test.
func testWait() time.Duration {
	if v := os.Getenv("NOVA_TEST_WAIT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 30 * time.Second
}

// tick delivers one idle poll and waits for the monitor to FINISH it -- every card
// sampled, every decision taken, every kill reaped -- so the monitor's state after the
// call is settled. Waiting only for the receive was not enough: the monitor could
// still be reading a card's store when the test's next write landed, and read the
// write as part of the tick before it (#2958).
//
// A tick after the batch has returned is a no-op: every card is done, the monitor has
// left on allDone, and a bare send would block the test forever (the hang a loaded
// bench found in TestBatchIdleDoesNotKillAWritingCard, whose card can finish before
// its second tick). A tick that nothing receives within testWait is dropped, not
// held, so a monitor gone without a batch fails one test instead of hanging the
// package (nova-tools #1983).
func (c *manualClock) tick() {
	c.mu.Lock()
	now := c.now
	c.mu.Unlock()
	select {
	case c.tickCh <- now:
	case <-c.gone:
		return
	case <-c.giveUpBound():
		// Nothing received the tick within the allowed poll bound: the monitor is gone
		// without a batch to close c.gone (TestIssue1983, nova-tools #1983). Drop it.
		return
	}
	select {
	case <-c.polled:
	case <-c.gone:
	case <-time.After(testWait()):
	}
}

// giveUpBound is how long tick holds a tick nothing receives: testWait(), or the
// test's own bound when it set giveUp.
func (c *manualClock) giveUpBound() <-chan time.Time {
	if c.giveUp != nil {
		return c.giveUp()
	}
	return time.After(testWait())
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

// TestIssue1983 reproduces nova-tools #1983 as its title states it:
// TestBatchIdleDoesNotKillAWritingCard hangs the whole internal/swarm package
// for 10m under the gate's capped flags. The hang is the fixture's, not any
// head's: the idle monitor exits with the batch (batch.go closes stopMonitor,
// or allDone fires), and under a tight scheduler -- GOMAXPROCS=8 -p 2
// -parallel 4 -race on a loaded bench, 2026-09-19 -- the drive callback still
// held a tick when it was gone, so tick blocked sending a tick that nothing
// was left to receive, with no timeout of its own: the goroutine dump named a
// nine-minute chan send at batch_clock_test.go:82 under
// TestBatchIdleDoesNotKillAWritingCard.func1 (batch_test.go:348), and the test
// could not fail, only hang until the package's bound took every other result
// with it. Here the monitor is gone by construction -- its ticker taken, no
// receiver left -- and the same send is watched with a bound of its own, so
// the fault fails one test in seconds instead of hanging the package for ten
// minutes.
func TestIssue1983(t *testing.T) {
	t.Parallel()

	// The give-up bound this reproduction runs under: the no-receiver state is
	// built here by construction, never won from the machine's scheduler, and the
	// bound is the test's to fire -- it has already passed -- so the proof waits
	// out no wall clock (nova-tools#4328; it used to be NOVA_TEST_WAIT=1s).
	clk := newManualClock()
	clk.giveUp = func() <-chan time.Time {
		passed := make(chan time.Time)
		close(passed)
		return passed
	}
	// The monitor has taken its ticker and is gone, exactly as it is once the
	// batch has ended: nothing is left to receive a tick.
	clk.NewTicker(idlePollInterval)
	returned := make(chan struct{})
	go func() {
		clk.tick()
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(30 * time.Second): // wall-ok: the reproduction's watchdog give-up, never a product bound
		t.Fatalf("manualClock.tick blocked sending a tick nothing is left to receive (nova-tools #1983): " +
			"the idle monitor is gone and the send has no bound of its own, so a test can only hang " +
			"until the package's 10m bound takes every other result with it")
	}
}
