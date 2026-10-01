//go:build functional

package store

// The holes of the dirty-driven tick (holes_test.go) on a store: the same
// scenarios on a Redis of the test's own with the table layer's functions
// loaded, where the apply, the fence, the queue and the wait on the log are
// the store's own. These run only in the functional tier
// (make test-functional-container).

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// G2 on a store, over a whole sprint: no machine is over its width before any
// step of any tick or after it, and the fleet holds exactly the work cards the
// working primaries hold at every step after the pump: the deal's fleet write
// is in the pump's step.
func TestG2OnAStoreNoMachineIsOverItsWidthAtAnyStep(t *testing.T) {
	t.Parallel()
	h, _ := liveHarness(t)
	holesUp(h, 4)
	x := newHoleTick(h)
	steps := 0
	x.onPlan = func(part string, s *sprint.Snapshot) {
		steps++
		why := heldOK(s)
		require.Empty(t, why, "before %s: %s", part, why)
		if strings.HasPrefix(part, "work/") {
			return
		}
		held := 0
		for _, m := range s.Fleet.Rows() {
			held += heldBy(s, m)
		}
		working := len(s.Work.Column(sprint.Working))
		require.Equal(t, working, held, "before %s the fleet holds %d work cards and %d primaries are working", part, held, working)
	}
	holesRun(x, 4, false, func(round int, res TickResult, ws []holeWrite) {
		why := heldOK(h.table())
		require.Empty(t, why, "round %d after the tick: %s", round, why)
	})
	require.GreaterOrEqual(t, steps, 20, "%d steps checked", steps)
	h.clean("landed")
}

// G2 and G3 on a store, over a whole sprint with a machine lapsing: the deal
// gives a machine no more than its room, only the pump's parts write the work
// table (the changes of the others are the log's queue, drained by the next
// pump), no step but the deal creates a work card on a machine, and every tick
// ends within the model's bound.
func TestG2AndG3OnAStoreOverASprintWithALapse(t *testing.T) {
	t.Parallel()
	h, _ := liveHarness(t)
	holesUp(h, 4)
	x := newHoleTick(h)
	held := map[string]int{}
	x.onPlan = func(part string, s *sprint.Snapshot) {
		if part == "work/deal" {
			for _, m := range s.UpMembers() {
				held[m] = heldBy(s, m)
			}
		}
	}
	planned, dealt := 0, 0
	holesRun(x, 4, true, func(round int, res TickResult, ws []holeWrite) {
		require.LessOrEqual(t, len(res.Order), 12, "round %d: the tick took %d updates: %v", round, len(res.Order), res.Order)
		for part, n := range workWrites(ws) {
			require.True(t, slices.Contains(pumpParts, part), "round %d: %s wrote %d entries of the work table: only the pump does", round, part, n)
		}
		made := map[string]int{}
		for _, w := range ws {
			for _, e := range w.Members {
				if w.Table == sprint.Fleet && e.Create != nil {
					require.Equal(t, "work/deal", w.Part, "round %d: %s created work card %s on a member", round, w.Part, e.ID)
					made[e.Create.Row]++
				}
			}
		}
		for m, n := range made {
			dealt += n
			room := max(0, 2-held[m])
			require.LessOrEqual(t, n, room, "round %d: the deal gave %s %d cards, its room was %d", round, m, n, room)
		}
		clear(held)
		for _, st := range x.seen {
			if !slices.Contains(pumpParts, st.Part) {
				planned += st.WorkChanges
			}
		}
	})
	require.GreaterOrEqual(t, dealt, 12, "the watch saw %d cards dealt and %d queued changes of the work table", dealt, planned)
	require.NotZero(t, planned, "the watch saw %d cards dealt and %d queued changes of the work table", dealt, planned)
	h.clean("landed")
}

// W12 and W13 on a store: a tick with nothing to do ends after the four first
// updates and writes nothing; a landing recorded late in a tick leaves the
// queue one entry a change, and the wait from before the tick wakes at once on
// the store's log, so the next tick pumps it.
func TestW12AndW13OnAStore(t *testing.T) {
	t.Parallel()
	h, _ := liveHarness(t)
	h.setup(2)
	h.startMachine()
	x := newHoleTick(h)
	x.tick() // deals both
	x.rec.take()
	for i := 0; i < 3; i++ {
		h.tick(time.Second)
		res := x.tick()
		want := []string{"start", sprint.Work, sprint.Readers, sprint.Merge, sprint.Fleet, "end"}
		require.Equal(t, want, res.Order, "idle tick %d updated %v", i+1, res.Order)
		ws := x.rec.take()
		require.True(t, res.Idle, "idle tick %d: idle %v, tick-end %d, %d batches, queue %d", i+1, res.Idle, res.TickEnd, len(ws), h.queueLen())
		require.Zero(t, res.TickEnd, "idle tick %d: idle %v, tick-end %d, %d batches, queue %d", i+1, res.Idle, res.TickEnd, len(ws), h.queueLen())
		require.Empty(t, ws, "idle tick %d: idle %v, tick-end %d, %d batches, queue %d", i+1, res.Idle, res.TickEnd, len(ws), h.queueLen())
		require.Zero(t, h.queueLen(), "idle tick %d: idle %v, tick-end %d, %d batches, queue %d", i+1, res.Idle, res.TickEnd, len(ws), h.queueLen())
	}
	h.work("m1")
	h.work("m2")
	x.tick()
	h.readAll()
	x.tick() // accepted: merging, queued to merge
	st := h.table().StateOf("s1-1")
	require.Equal(t, sprint.Merging, st, "s1-1 is %s, want merging", st)
	fired := false
	inner := h.st.Updates[2].Parts
	h.st.Updates[2].Parts = append([]sprint.TickPartDef{{Name: "land", Fn: func(s *sprint.Snapshot, r sprint.TickReq) (sprint.Plan, int) {
		if !fired {
			fired = true
			h.must(MergeStep(sprint.MergeReq{Stream: "s1", Batch: 2}))
		}
		return sprint.Plan{}, 0
	}}}, inner...)
	before, err := h.st.LogTail(h.ctx)
	require.NoError(t, err)
	x.tick()
	require.True(t, fired, "the landing was not recorded in the tick: fired %v, queue %d", fired, h.queueLen())
	require.NotZero(t, h.queueLen(), "the landing was not recorded in the tick: fired %v, queue %d", fired, h.queueLen())
	_, woke, err := h.st.WaitLog(h.ctx, 0, before, time.Millisecond)
	require.NoError(t, err, "the wait from before the tick did not wake on the store's log (woke %v, %v)", woke, err)
	require.True(t, woke, "the wait from before the tick did not wake on the store's log (woke %v, %v)", woke, err)
	h.st.Updates[2].Parts = inner
	x.tick()
	n := h.table().Work.Count("s1", sprint.Landed)
	require.EqualValues(t, 2, n, "%d landed and %d queued after the next tick, want 2 and 0", n, h.queueLen())
	require.Zero(t, h.queueLen(), "%d landed and %d queued after the next tick, want 2 and 0", n, h.queueLen())
	h.clean("landed")
}
