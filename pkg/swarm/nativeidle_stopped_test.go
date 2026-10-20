package swarm

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A STOPPED TREE IS THE ENVIRONMENT'S, NOT THE CARD'S. A process stopped at state T (as
// `leaks --atExit -- <bin>` leaves its target while it waits for a task-port authorization)
// burns no CPU and prints nothing, so the idle watch cannot tell it from a stalled model by
// the readings it had. The snapshot answers the stopped state beside the CPU, and a stop
// that covers the whole idle window is named on the end.

// stoppedSnap is one reading of a still tree that may hold a stopped process. The clock is
// an argument: the test starts it at 1000.0 and drives every tick.
type stoppedSnap struct {
	cpu     uint64
	ok      bool
	stopped bool
	pid     int
	cmd     string
}

func (s *stoppedSnap) TreeCPU(int) (uint64, bool) { return s.cpu, s.ok }

func (s *stoppedSnap) TreeStopped(int) (int, string, bool) {
	if !s.stopped {
		return 0, "", false
	}
	return s.pid, s.cmd, true
}

// stoppedBench is one watch with a fake snapshot and a ticker the test drives.
type stoppedBench struct {
	start time.Time
	ticks chan time.Time
	out   <-chan IdleEnd
	stop  chan struct{}
}

func newStoppedBench(t *testing.T, idle time.Duration, snap *stoppedSnap) *stoppedBench {
	t.Helper()
	job := t.TempDir()
	log := filepath.Join(job, "harness-output.log")
	require.NoError(t, os.WriteFile(log, []byte("start\n"), 0o644))
	b := &stoppedBench{
		start: time.Unix(1000, 0),
		ticks: make(chan time.Time),
		stop:  make(chan struct{}),
	}
	w := IdleWatch{Log: log, Job: job, Pid: 4242, Idle: idle, Reader: NewWallReader("c", nil)}
	w.now = func() time.Time { return b.start }
	w.ticker = func(time.Duration) (<-chan time.Time, func()) { return b.ticks, func() {} }
	w.snapshot = func() activitySnapshot { return snap }
	b.out = WatchIdle(w, b.stop)
	t.Cleanup(func() { close(b.stop) })
	return b
}

// tickAt drives the watch one poll at the seconds past 1000.0 and returns whether the watch
// ended the card at it.
func (b *stoppedBench) tickAt(t *testing.T, at time.Duration) (IdleEnd, bool) {
	t.Helper()
	now := b.start.Add(at)
	select {
	case b.ticks <- now:
	case end, open := <-b.out:
		return end, open
	}
	select {
	case end, open := <-b.out:
		return end, open
	case b.ticks <- now:
		return IdleEnd{}, false
	}
}

// TestIdleWatchNamesAStoppedTree: a tree that holds a stopped process for the whole window
// ends with the cause and, on the CARD IDLE line, the stopped pid and its command.
func TestIdleWatchNamesAStoppedTree(t *testing.T) {
	t.Parallel()

	snap := &stoppedSnap{ok: true, stopped: true, pid: 99, cmd: "leaks"}
	b := newStoppedBench(t, 10*time.Second, snap)
	_, ended := b.tickAt(t, 9*time.Second)
	require.False(t, ended, "a stopped tree is not ended before the idle window")
	end, ended := b.tickAt(t, 11*time.Second)
	require.True(t, ended, "a tree stopped for the whole idle window is ended")
	require.Equal(t, "tree-stopped", end.Cause, "the end names the stopped tree")
	line := CardIdleLine("c", end)
	require.Contains(t, line, "cause=tree-stopped", "the CARD IDLE line names the cause")
	require.Contains(t, line, "pid=99", "the CARD IDLE line names the stopped pid")
	require.Contains(t, line, "cmd=leaks", "the CARD IDLE line names the stopped command")
	require.NotContains(t, line, "WALL", "a stopped tree is not a wall refusal")
	// The NATIVE line carries the cause as one safe token, tree-stopped:<pid>:<cmd>,
	// because a cause field holds no blanks; the CARD IDLE line spells it back out.
	tokenLine := CardIdleLine("c", IdleEnd{Idle: 300 * time.Second, Cause: "tree-stopped:99:leaks"})
	require.Contains(t, tokenLine, "cause=tree-stopped", "the token's cause is the cause")
	require.Contains(t, tokenLine, "pid=99", "the token's pid is named")
	require.Contains(t, tokenLine, "cmd=leaks", "the token's command is named")
}

// TestIdleWatchStoppedTreeNeedsTheWholeIdleWindow: a stop shorter than the window changes
// nothing, even though the tree is still by CPU and log.
func TestIdleWatchStoppedTreeNeedsTheWholeIdleWindow(t *testing.T) {
	t.Parallel()

	snap := &stoppedSnap{ok: true}
	b := newStoppedBench(t, 10*time.Second, snap)
	for _, at := range []time.Duration{3 * time.Second, 6 * time.Second} {
		_, ended := b.tickAt(t, at)
		require.False(t, ended, "the still card is held before its window")
	}
	snap.stopped, snap.pid, snap.cmd = true, 99, "leaks"
	_, ended := b.tickAt(t, 9*time.Second)
	require.False(t, ended, "the still card is held before its window")
	end, ended := b.tickAt(t, 11*time.Second)
	require.True(t, ended, "the still card is ended at its window")
	require.Empty(t, end.Cause, "a stop shorter than the idle window is not the environment's")
	require.NotContains(t, CardIdleLine("c", end), "tree-stopped")
}
