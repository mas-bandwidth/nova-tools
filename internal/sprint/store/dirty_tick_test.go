package store

// The owner's tick (2026-09-30, errata 3 amendment 12; tla/DirtyTick.tla):
// "Each table gets one update in turn per-tick. 1. work streams, 2. readers,
// 3. merge, 4. fleet."; "nothing advances the work stream table EXCEPT on the
// next tick"; "the previous tick does queue up all the changes for the work
// stream table, to process start of next tick"; "dirty bits are acted on
// IMMEDIATELY"; "the tick doesn't end until all dirty bits are cleared".

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// (a) The order of a tick's updates: work, readers, merge, fleet, then end.
func TestTheTickUpdatesTheTablesInTheOwnersOrder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(3)
	h.startMachine()
	for i := 0; i < 3; i++ {
		res := h.machine()
		if got, want := res.Order, []string{sprint.Work, sprint.Readers, sprint.Merge, sprint.Fleet, "end"}; !slices.Equal(got, want) {
			t.Fatalf("tick %d updated %v, want %v", i+1, got, want)
		}
		t.Logf("tick %d: %s", i+1, strings.Join(res.Order, " -> "))
		h.work("m1")
		h.work("m2")
	}
}

// (b) Only the pump writes the work table while the machine runs: the
// outside world's verbs (take, finish, read, accept, merge) leave the stored
// work table as it is and queue their changes; the tick's pump applies them.
// The check is shown to catch a merge let write the work table itself (the
// mutation: its step marked as the pump's).
func TestOnlyThePumpWritesTheWorkTable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.startMachine()
	h.machine() // deals both
	rev := func() uint64 { return h.table().Work.Revision }
	still := func(what string, verb func()) {
		t.Helper()
		before := rev()
		verb()
		if after := rev(); after != before {
			t.Fatalf("%s wrote the work table (revision %d -> %d): only the pump writes it while the machine runs", what, before, after)
		}
	}
	still("take and finish", func() { h.work("m1"); h.work("m2") })
	if st := h.table().StateOf("s1-1"); st != sprint.Working {
		t.Fatalf("the stored s1-1 is %s before the tick, want working", st)
	}
	before := rev()
	h.machine() // the pump moves both to review; the readers are asked
	if rev() == before || h.table().StateOf("s1-1") != sprint.Review {
		t.Fatalf("the pump did not apply the queue: s1-1 is %s", h.table().StateOf("s1-1"))
	}
	still("the reads", h.readAll)
	h.machine() // the pump accepts both (two ok reads): merging
	if st := h.table().StateOf("s1-1"); st != sprint.Merging {
		t.Fatalf("s1-1 is %s after the pump, want merging", st)
	}
	merge := MergeStep(sprint.MergeReq{Stream: "s1", Batch: 1})
	still("the merge", func() { h.must(merge) })
	if st := h.table().StateOf("s1-1"); st != sprint.Merging {
		t.Fatalf("the stored s1-1 is %s before the tick, want merging: the merge landed it itself", st)
	}
	// the mutation: a merge let write the work table directly is caught
	merge.Pump = true
	before = rev()
	h.must(merge)
	if rev() == before {
		t.Fatalf("the mutation (the merge as the pump) did not write the work table: the check above would not catch it")
	}
	h.machine()
	if h.table().StateOf("s1-1") != sprint.Landed || h.table().StateOf("s1-2") != sprint.Landed {
		t.Fatalf("after the tick: %s %s", h.table().StateOf("s1-1"), h.table().StateOf("s1-2"))
	}
	h.clean("landed")
}

// (c) The queue is drained exactly once: every change queued before a tick
// is applied by that tick's pump, once, with one move line each; a change
// queued during the tick, after the pump, waits for the next tick's pump and
// is not lost.
func TestTheQueueIsDrainedExactlyOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(4)
	h.startMachine()
	h.machine()
	h.run(TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 100}, Who: "m1"}))
	h.run(TakeStep(sprint.TakeReq{As: "m2", Sel: sprint.Sel{Limit: 100}, Who: "m2"}))
	s := h.snap()
	finish := func(m string) Step {
		var ids []string
		gens := map[string]int{}
		for _, c := range s.Fleet.Cell(m, sprint.Working) {
			ids = append(ids, c.ID)
			gens[c.ID] = c.Int("gen")
		}
		return FinishStep(sprint.FinishReq{As: m, Sel: sprint.Sel{IDs: ids}, Gens: gens, Who: m})
	}
	h.must(finish("m1"))
	q, _ := h.m.QueueRead(h.ctx)
	first := len(q)
	if first == 0 {
		t.Fatal("the finish queued nothing")
	}
	// m2's finish runs inside the tick, as the readers' update: after the pump
	fired := false
	h.st.Updates = []sprint.TableUpdate{sprint.TickTables[0], {Table: sprint.Readers, Parts: []sprint.TickPartDef{{Name: "ask", Fn: func(s *sprint.Snapshot, r sprint.TickReq) (sprint.Plan, int) {
		if !fired {
			fired = true
			h.must(finish("m2"))
		}
		return sprint.TickAsk(s, r)
	}}}}, sprint.TickTables[2], sprint.TickTables[3]}
	h.machine()
	q, _ = h.m.QueueRead(h.ctx)
	mid := 0
	for _, x := range q {
		if x.Entry != nil && strings.HasPrefix(x.Verb, "finish") {
			mid++
		}
	}
	if mid == 0 {
		t.Fatalf("the finish during the tick is not in the queue after it: %d entries", len(q))
	}
	h.st.Updates = nil
	h.machine()
	h.machine()
	if q, _ := h.m.QueueRead(h.ctx); len(q) != 0 {
		t.Fatalf("the queue holds %d after two more ticks", len(q))
	}
	lines, err := h.st.Log(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	queued, drained := map[string]int{}, map[string]int{}
	for _, l := range lines {
		if l.Table != sprint.Work || l.Verb == "add" {
			continue
		}
		switch {
		case l.Kind == sprint.LineQueued && l.Verb == "finish":
			queued[l.Card]++
		case l.Kind == sprint.LineMove && l.Verb == sprint.DrainVerb && strings.Contains(l.Cause, "(finish"):
			drained[l.Card]++
		}
	}
	for _, id := range []string{"s1-1", "s1-2", "s1-3", "s1-4"} {
		if queued[id] != 1 || drained[id] != 1 {
			t.Fatalf("%s: queued %d times, drained %d times: want once each (queued %v, drained %v)", id, queued[id], drained[id], queued, drained)
		}
		if st := h.table().StateOf(id); st != sprint.Review {
			t.Fatalf("%s is %s", id, st)
		}
	}
	h.clean("drained")
}

// ping is a table update that writes another table's control card (the
// stream's in the merge table, or m1's in the fleet table) until the card's
// ping field reaches limit, then writes nothing: a pure planner, as every
// part is (it may be planned more than once).
func ping(table string, limit int) sprint.TickPartFn {
	return func(s *sprint.Snapshot, _ sprint.TickReq) (sprint.Plan, int) {
		c := s.MemberCtl("m1")
		if table == sprint.Merge {
			c = s.StreamCtl("s1")
		}
		n := c.Int("ping")
		if n >= limit {
			return sprint.Plan{}, 0
		}
		e := ntable.BatchMemberEntry{ID: c.ID, Expect: &ntable.MemberExpect{Revision: strconv.FormatUint(c.Rev, 10), Place: &ntable.PlaceExpect{Row: c.Row, Col: c.Col}},
			Set: map[string]string{"ping": strconv.Itoa(n + 1)}}
		return sprint.Plan{Units: []sprint.Unit{{Key: c.ID, Changes: []sprint.Change{{Table: table, Entry: e}}, Moved: "ping " + table}}}, 0
	}
}

// (d) A tick whose merge and fleet updates write each other's tables updates
// the written table at once, again and again, until neither writes: the tick
// ends; one that never stops fails at MaxSettle, naming the tables.
func TestAMutuallyDirtyingTickSettles(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.st.Updates = []sprint.TableUpdate{
		{Table: sprint.Work}, {Table: sprint.Readers},
		{Table: sprint.Merge, Parts: []sprint.TickPartDef{{Name: "to-fleet", Fn: ping(sprint.Fleet, 3)}}},
		{Table: sprint.Fleet, Parts: []sprint.TickPartDef{{Name: "to-merge", Fn: ping(sprint.Merge, 3)}}},
	}
	res := h.machine()
	want := []string{sprint.Work, sprint.Readers, sprint.Merge, sprint.Fleet, sprint.Merge, sprint.Fleet, sprint.Merge, sprint.Fleet, sprint.Merge, "end"}
	if !slices.Equal(res.Order, want) {
		t.Fatalf("the tick updated %v, want %v", res.Order, want)
	}
	t.Logf("settled: %s", strings.Join(res.Order, " -> "))
	// the tick after writes nothing more: its first pass only
	if res := h.machine(); !slices.Equal(res.Order, []string{sprint.Work, sprint.Readers, sprint.Merge, sprint.Fleet, "end"}) {
		t.Fatalf("the next tick updated %v", res.Order)
	}

	g := newHarness(t)
	g.setup(1)
	g.startMachine()
	g.st.Updates = []sprint.TableUpdate{
		{Table: sprint.Merge, Parts: []sprint.TickPartDef{{Name: "to-fleet", Fn: ping(sprint.Fleet, 1<<30)}}},
		{Table: sprint.Fleet, Parts: []sprint.TickPartDef{{Name: "to-merge", Fn: ping(sprint.Merge, 1<<30)}}},
	}
	_, err := g.st.Tick(g.ctx)
	if err == nil || !strings.Contains(err.Error(), "did not settle") || !strings.Contains(err.Error(), sprint.Merge) {
		t.Fatalf("a tick that never settles: %v", err)
	}
	s := g.snap()
	if n := s.MemberCtl("m1").Int("ping") + s.StreamCtl("s1").Int("ping"); n != 2+MaxSettle {
		t.Fatalf("%d updates wrote, want the first pass's 2 and the bound's %d", n, MaxSettle)
	}
	t.Logf("unsettled: %v", err)
}

// The tick-end note (errata 3 amendment 8): one note a tick to the
// coordinator, judgments=N, when the tick addressed them; none otherwise.
func TestOneTickEndNoteOnlyWhenTheTickAddressedTheCoordinator(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.startMachine()
	ends := func() int { return h.written(sprint.NTickEnd) }
	h.machine() // deals: nothing for the coordinator
	if ends() != 0 {
		t.Fatalf("a tick that addressed nothing wrote %d tick-end notes", ends())
	}
	h.work("m1")
	h.work("m2")
	h.machine() // to review, asked
	h.readAll()
	res := h.machine() // accepted: ready to merge, once for the stream
	if ends() != 1 || res.TickEnd != 1 {
		t.Fatalf("the accepting tick: %d tick-end notes, count %d, want 1 and 1", ends(), res.TickEnd)
	}
	h.machine()
	if ends() != 1 {
		t.Fatalf("a quiet tick wrote a tick-end note: %d", ends())
	}
}
