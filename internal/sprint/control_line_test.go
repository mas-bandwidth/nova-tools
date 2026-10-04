package sprint

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// machineSilence is the store's MachineSilence: a RUNNING machine whose last tick is older
// reads STOPPED (docs/SPEC-SPRINT.md section 14).
const machineSilence = 15 * time.Second

// lineLoad is a load on the server's line of control: workers that each ask again as soon
// as their batch ends, every batch holding the line batch, and the run loop's tick holding
// it tick and asking again TickTurnFloor after it began (the loop's pace with a busy log).
type lineLoad struct {
	name         string
	batches      int
	batch, tick  time.Duration
	runFor       time.Duration
	ticksAtLeast int
}

// lineRun is what a simulated run measured.
type lineRun struct {
	ticks               int
	worstWait, worstGap time.Duration
	batchShare          float64
}

// simulateLine steps the line's rule (lineTurns) through load.runFor of simulated time:
// no goroutine and no clock. The batches always wait (every worker has asked), as they did
// while the tick was held.
func simulateLine(load lineLoad) lineRun {
	var s lineTurns
	for range load.batches {
		s.ticket()
	}
	start := time.Date(2026, 10, 4, 12, 52, 0, 0, time.UTC)
	now, ask := start, start
	var run lineRun
	var lastBegan time.Time
	var batchTime time.Duration
	for now.Sub(start) < load.runFor {
		s.tickWants = !now.Before(ask)
		switch {
		case s.tickWants && s.tickMay(now):
			run.worstWait = max(run.worstWait, now.Sub(ask))
			if run.ticks > 0 {
				run.worstGap = max(run.worstGap, now.Sub(lastBegan))
			}
			run.ticks++
			lastBegan = now
			s.takeTick(now)
			now = now.Add(load.tick)
			s.release(now)
			ask = maxTime(now, lastBegan.Add(TickTurnFloor))
		case s.batchMay(s.serving, now):
			s.takeBatch(now)
			now = now.Add(load.batch)
			batchTime += load.batch
			s.release(now)
			s.ticket() // its worker asks again
		default:
			now = ask
		}
	}
	if !now.Before(ask) {
		// a tick still asking at the end has waited since it asked
		run.worstWait = max(run.worstWait, now.Sub(ask))
	}
	return finish(run, batchTime, now.Sub(start))
}

func finish(run lineRun, batchTime, ran time.Duration) lineRun {
	run.batchShare = float64(batchTime) / float64(ran)
	return run
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// TestNoTickStepExceedsItsBound pins the tick's wait for the server's line of control
// (docs/SPEC-SPRINT.md section 14, The server, "The tick's turn"). A tick queued behind every
// worker's batch waiting runs in a second or two and begins tens of seconds after the one
// before, and a RUNNING machine reads STOPPED (no tick for 15 s) with no stop given; the
// first load is the one of 2026-10-04, 12:52 to 12:56 PM. Under every load here the tick
// waits at most its turn (as long as the tick before held the line, floor and cap applied)
// and one batch in flight, two ticks begin less than 15 s apart, and the
// batches keep the line for at least their turn's share.
func TestNoTickStepExceedsItsBound(t *testing.T) {
	t.Parallel()
	loads := []lineLoad{
		{name: "the window's load: forty workers' batches of half a second, ticks of two", batches: 40, batch: 500 * time.Millisecond, tick: 2 * time.Second, runFor: 10 * time.Minute, ticksAtLeast: 100},
		{name: "short batches and short ticks", batches: 10, batch: 20 * time.Millisecond, tick: 300 * time.Millisecond, runFor: time.Minute, ticksAtLeast: 100},
		{name: "a tick shorter than the floor", batches: 8, batch: 50 * time.Millisecond, tick: 50 * time.Millisecond, runFor: time.Minute, ticksAtLeast: 300},
		{name: "batches as long as a client's timeout", batches: 20, batch: 8 * time.Second, tick: time.Second, runFor: 30 * time.Minute, ticksAtLeast: 100},
		{name: "a tick longer than the cap", batches: 40, batch: 300 * time.Millisecond, tick: 9 * time.Second, runFor: 30 * time.Minute, ticksAtLeast: 100},
	}
	for _, load := range loads {
		t.Run(load.name, func(t *testing.T) {
			t.Parallel()
			run := simulateLine(load)
			t.Logf("ticks=%d worst wait=%s worst gap=%s batch share=%.2f", run.ticks, run.worstWait, run.worstGap, run.batchShare)
			turn := min(max(load.tick, TickTurnFloor), TickTurnCap)
			bound := turn + load.batch
			assert.GreaterOrEqual(t, run.ticks, load.ticksAtLeast, "the tick runs")
			assert.LessOrEqual(t, run.worstWait, bound, "the tick waits at most its turn and one batch in flight")
			assert.LessOrEqual(t, run.worstGap, load.tick+bound, "two ticks begin at most a tick, a turn and a batch apart")
			assert.Less(t, run.worstGap, machineSilence, "a RUNNING machine never reads STOPPED for want of the line")
			assert.GreaterOrEqual(t, run.batchShare, float64(turn)/float64(load.tick+turn)-0.01, "the batches keep their turn's share of the line")
		})
	}
}

// TestTheTickTakesTheLineAtItsTurn steps ControlLine itself in a bubble (no real time): a tick
// whose turn is due takes the line after the batch in flight and before the batches
// waiting; a tick whose turn is not due goes after the batches waiting, and takes the line
// at once when none waits; TryLock takes a free line only when nothing waits for it.
func TestTheTickTakesTheLineAtItsTurn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var l ControlLine
		var mu sync.Mutex
		var order []string
		took := func(who string) {
			mu.Lock()
			order = append(order, who)
			mu.Unlock()
		}
		batch := func(who string) {
			l.Lock()
			took(who)
			l.Unlock()
		}
		tick := func() {
			l.TickLock()
			took("tick")
			l.Unlock()
		}

		// the first tick's turn is due: it goes before the batches that asked before it
		l.Lock()
		go batch("b1")
		synctest.Wait()
		go batch("b2")
		synctest.Wait()
		go tick()
		synctest.Wait()
		assert.False(t, l.TryLock(), "a held line")
		l.Unlock()
		synctest.Wait()
		assert.Equal(t, []string{"tick", "b1", "b2"}, order)

		// a tick ended just now: the batches waiting have their turn first
		order = nil
		l.Lock()
		go batch("b3")
		synctest.Wait()
		go tick()
		synctest.Wait()
		l.Unlock()
		synctest.Wait()
		assert.Equal(t, []string{"b3", "tick"}, order)

		// nothing waits: the tick takes the line at once, whatever its turn
		order = nil
		require.Zero(t, l.TickLock(), "a free line with nothing waiting")
		l.Unlock()
		require.True(t, l.TryLock(), "a free line with nothing waiting")
		l.Unlock()
		assert.Panics(t, func() { l.Unlock() }, "an Unlock of a free line")
	})
}
