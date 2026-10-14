package swarm

// A HARNESS THAT NEVER STARTED IS NOT A QUIET THINK.
//
// The idle watch has no notion of "never started": a launch that produces no byte and no CPU
// is the same to it as a harness that started and then went quiet, so it waits the whole idle
// window, holds a lane and a bench slot, and the attempt counts against the card. The start
// phase here gives the launch its own window: until the first sign of life the watch is
// "starting", and a window that passes with no sign ends the card as never started.
//
// The tests inject the clock and the tree/log readers through the IdleWatch seams and never
// sleep: the poll is 100 ms of fake time, nativeidle_test.go's own shape.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// startSnap is one reading of the tree that can also name a foreign descendant: the start
// phase's third sign of life. foreign is what the test sets to say a process beyond the
// launch chain has appeared.
type startSnap struct {
	cpu     uint64
	ok      bool
	foreign bool
}

func (s *startSnap) TreeCPU(int) (uint64, bool) { return s.cpu, s.ok }
func (s *startSnap) ForeignDescendant(int) bool { return s.foreign }

// startBench is one watch under an injected clock with a start window: a log the test grows
// or does not, a ticker the test drives, and a tree the test sets.
type startBench struct {
	job   string
	log   string
	ticks chan time.Time
	snap  *startSnap
	out   <-chan IdleEnd
	stop  chan struct{}
}

func newStartBench(t *testing.T, idle, start time.Duration) *startBench {
	t.Helper()
	job := t.TempDir()
	b := &startBench{
		job:   job,
		log:   filepath.Join(job, "harness-output.log"),
		ticks: make(chan time.Time),
		snap:  &startSnap{},
		stop:  make(chan struct{}),
	}
	require.NoError(t, os.WriteFile(b.log, []byte("start\n"), 0o644))
	w := IdleWatch{Log: b.log, Job: job, Pid: 4242, Idle: idle, Start: start, Reader: NewWallReader("c", nil)}
	w.now = func() time.Time { return time.Unix(0, 0) }
	w.ticker = func(time.Duration) (<-chan time.Time, func()) { return b.ticks, func() {} }
	w.snapshot = func() activitySnapshot { return b.snap }
	b.out = WatchIdle(w, b.stop)
	t.Cleanup(func() { close(b.stop) })
	return b
}

// tick drives the watch one poll at at seconds past the start and returns whether it ended
// the card at it. It is nativeidle_test.go's shape: the second poll either reads the verdict
// or takes another poll at the same instant.
func (b *startBench) tick(t *testing.T, at time.Duration) (IdleEnd, bool) {
	t.Helper()
	now := time.Unix(0, 0).Add(at)
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

// appendByte adds one byte to the card's log, the way its harness prints its first character.
func appendByte(t *testing.T, path string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(".")
	require.NoError(t, err)
	require.NoError(t, f.Close())
}

// TestACardWithNoSignOfStartingIsEndedAtItsStartDeadline is the whole card: the start phase,
// its three signs, the typed no-start end, and the unchanged behaviour when Start is zero.
func TestACardWithNoSignOfStartingIsEndedAtItsStartDeadline(t *testing.T) {
	t.Parallel()

	t.Run("no byte, no CPU and no descendant for the window: ended at the start deadline", func(t *testing.T) {
		b := newStartBench(t, 300*time.Second, DefaultNativeStart)
		var end IdleEnd
		var ended bool
		for i := 1; i <= 800 && !ended; i++ {
			end, ended = b.tick(t, time.Duration(i)*100*time.Millisecond)
		}
		require.True(t, ended, "a launch with no sign of life is ended at its start deadline")
		require.Equal(t, "no-start", end.Kind, "the end is typed no-start, got %+v", end)
		require.Equal(t, 60*time.Second, end.Idle, "the end names the start window, got %v", end.Idle)
		require.Equal(t, "CARD NOSTART task=t step=- start=60s", CardNoStartLine("t", end.Idle))
	})

	t.Run("one byte at 59s ends the start phase and holds the card to the idle window", func(t *testing.T) {
		b := newStartBench(t, 300*time.Second, DefaultNativeStart)
		var end IdleEnd
		var ended bool
		for i := 1; i <= 4000 && !ended; i++ {
			at := time.Duration(i) * 100 * time.Millisecond
			if at == 59*time.Second {
				appendByte(t, b.log)
			}
			end, ended = b.tick(t, at)
		}
		require.True(t, ended, "the started card is then held to the idle window")
		require.NotEqual(t, "no-start", end.Kind, "a byte before the window ends the start phase, got %+v", end)
		require.GreaterOrEqual(t, end.Idle, 300*time.Second, "the idle window runs from the first sign, got %v", end.Idle)
	})

	t.Run("CPU at the busy share in one poll ends the start phase", func(t *testing.T) {
		b := newStartBench(t, 300*time.Second, DefaultNativeStart)
		b.snap.ok = true
		var end IdleEnd
		var ended bool
		for i := 1; i <= 4000 && !ended; i++ {
			at := time.Duration(i) * 100 * time.Millisecond
			if at <= 15*time.Second {
				b.snap.cpu += uint64(20 * time.Millisecond)
			}
			end, ended = b.tick(t, at)
		}
		require.True(t, ended, "a tree spending the busy share started")
		require.NotEqual(t, "no-start", end.Kind, "the CPU sign ends the start phase, got %+v", end)
	})

	t.Run("a foreign descendant at 10s ends the start phase", func(t *testing.T) {
		b := newStartBench(t, 300*time.Second, DefaultNativeStart)
		var end IdleEnd
		var ended bool
		for i := 1; i <= 4000 && !ended; i++ {
			at := time.Duration(i) * 100 * time.Millisecond
			if at >= 10*time.Second {
				b.snap.foreign = true
			}
			end, ended = b.tick(t, at)
		}
		require.True(t, ended, "a tree holding a process beyond the launch chain started")
		require.NotEqual(t, "no-start", end.Kind, "the descendant sign ends the start phase, got %+v", end)
	})

	t.Run("Start 0 is the idle watch unchanged and never reads the start reader", func(t *testing.T) {
		b := newStartBench(t, 10*time.Second, NoIdleWindow)
		b.snap.foreign = true
		var end IdleEnd
		var ended bool
		for i := 1; i <= 800 && !ended; i++ {
			end, ended = b.tick(t, time.Duration(i)*100*time.Millisecond)
		}
		require.True(t, ended, "with no start window the card is still ended at its idle window")
		require.NotEqual(t, "no-start", end.Kind, "no start watch means no no-start end, got %+v", end)
	})
}

// TestCardNoStartLineIsNotAWallLineOrAnIdleLine: the line a never-started card is reported on
// names the start window and nothing else.
func TestCardNoStartLineIsNotAWallLineOrAnIdleLine(t *testing.T) {
	t.Parallel()

	line := CardNoStartLine("the-tla-records-check.r2", 60*time.Second)
	require.Equal(t, "CARD NOSTART task=the-tla-records-check.r2 step=- start=60s", line)
}
