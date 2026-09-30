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
		if why := heldOK(s); why != "" {
			t.Fatalf("before %s: %s", part, why)
		}
		if strings.HasPrefix(part, "work/") {
			return
		}
		held := 0
		for _, m := range s.Fleet.Rows() {
			held += heldBy(s, m)
		}
		if working := len(s.Work.Column(sprint.Working)); held != working {
			t.Fatalf("before %s the fleet holds %d work cards and %d primaries are working", part, held, working)
		}
	}
	holesRun(x, 4, false, func(round int, res TickResult, ws []holeWrite) {
		if why := heldOK(h.table()); why != "" {
			t.Fatalf("round %d after the tick: %s", round, why)
		}
	})
	if steps < 20 {
		t.Fatalf("%d steps checked", steps)
	}
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
		if len(res.Order) > 12 {
			t.Fatalf("round %d: the tick took %d updates: %v", round, len(res.Order), res.Order)
		}
		for part, n := range workWrites(ws) {
			if !slices.Contains(pumpParts, part) {
				t.Fatalf("round %d: %s wrote %d entries of the work table: only the pump does", round, part, n)
			}
		}
		made := map[string]int{}
		for _, w := range ws {
			for _, e := range w.Members {
				if w.Table == sprint.Fleet && e.Create != nil {
					if w.Part != "work/deal" {
						t.Fatalf("round %d: %s created work card %s on a member", round, w.Part, e.ID)
					}
					made[e.Create.Row]++
				}
			}
		}
		for m, n := range made {
			dealt += n
			if room := max(0, 2-held[m]); n > room {
				t.Fatalf("round %d: the deal gave %s %d cards, its room was %d", round, m, n, room)
			}
		}
		clear(held)
		for _, st := range x.seen {
			if !slices.Contains(pumpParts, st.Part) {
				planned += st.WorkChanges
			}
		}
	})
	if dealt < 12 || planned == 0 {
		t.Fatalf("the watch saw %d cards dealt and %d queued changes of the work table", dealt, planned)
	}
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
		if want := []string{sprint.Work, sprint.Readers, sprint.Merge, sprint.Fleet, "end"}; !slices.Equal(res.Order, want) {
			t.Fatalf("idle tick %d updated %v", i+1, res.Order)
		}
		if ws := x.rec.take(); !res.Idle || res.TickEnd != 0 || len(ws) > 0 || h.queueLen() != 0 {
			t.Fatalf("idle tick %d: idle %v, tick-end %d, %d batches, queue %d", i+1, res.Idle, res.TickEnd, len(ws), h.queueLen())
		}
	}
	h.work("m1")
	h.work("m2")
	x.tick()
	h.readAll()
	x.tick() // accepted: merging, queued to merge
	if st := h.table().StateOf("s1-1"); st != sprint.Merging {
		t.Fatalf("s1-1 is %s, want merging", st)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	x.tick()
	if !fired || h.queueLen() == 0 {
		t.Fatalf("the landing was not recorded in the tick: fired %v, queue %d", fired, h.queueLen())
	}
	if _, woke, err := h.st.WaitLog(h.ctx, 0, before, time.Millisecond); err != nil || !woke {
		t.Fatalf("the wait from before the tick did not wake on the store's log (woke %v, %v)", woke, err)
	}
	h.st.Updates[2].Parts = inner
	x.tick()
	if n := h.table().Work.Count("s1", sprint.Landed); n != 2 || h.queueLen() != 0 {
		t.Fatalf("%d landed and %d queued after the next tick, want 2 and 0", n, h.queueLen())
	}
	h.clean("landed")
}
