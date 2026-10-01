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
	res := h.machine() // accepted: ready to merge, once for the stream, and the reads' notes
	if ends() != 1 || res.TickEnd < 1 {
		t.Fatalf("the accepting tick: %d tick-end notes, count %d, want one note counting at least the ready to merge", ends(), res.TickEnd)
	}
	h.machine()
	if ends() != 1 {
		t.Fatalf("a quiet tick wrote a tick-end note: %d", ends())
	}
}

// A drain a step makes before it plans is said, never silent: a pump part
// that finds a change queued after the tick's first read drains it first and
// the tick names that drain among its parts; a verb on a STOPPED machine
// that drains the queue it left says so in its result.
func TestEveryDrainIsNamed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.startMachine()
	h.machine() // deals both
	h.run(TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 100}, Who: "m1"}))
	queued := false
	work := sprint.TickTables[0]
	work.Parts = append([]sprint.TickPartDef{{Name: "world", Fn: func(s *sprint.Snapshot, _ sprint.TickReq) (sprint.Plan, int) {
		if !queued {
			queued = true
			h.work("m1") // a finish queued after the tick's first read
			return sprint.Plan{Notes: []sprint.Note{{Kind: sprint.Happened, Type: "world", At: s.Now}}}, 0
		}
		return sprint.Plan{}, 0
	}}}, work.Parts...)
	h.st.Updates = []sprint.TableUpdate{work, sprint.TickTables[1], sprint.TickTables[2], sprint.TickTables[3]}
	res := h.machine()
	named := false
	for _, p := range res.Parts {
		named = named || p.Name == sprint.PartDrain && len(p.Moved) > 0
	}
	if !named {
		t.Fatalf("the drain before a pump part is not among the tick's parts: %+v", res.Parts)
	}
	h.st.Updates = nil
	h.work("m2") // queued: the machine is running
	if q, _ := h.m.QueueRead(h.ctx); len(q) == 0 {
		t.Fatal("nothing queued before the stop")
	}
	_, _, r, err := h.st.SetMachine(h.ctx, false) // STOPPED: its own step drains first
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Drained) == 0 || len(r.Drained[0].Moved) == 0 {
		t.Fatalf("the stop drained the queue silently: %+v", r)
	}
	if q, _ := h.m.QueueRead(h.ctx); len(q) != 0 {
		t.Fatalf("a STOPPED machine keeps a queue of %d", len(q))
	}
}

// "accept is mechanical": on a RUNNING machine the reads that make a primary
// acceptable open no "ready to accept" judgment (no wake for nothing), and
// the next pump accepts it; on a STOPPED machine the judgment opens as
// before, the coordinator's to answer.
func TestAReadOnARunningMachineOpensNoReadyToAccept(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.startMachine()
	h.machine()
	h.work("m1")
	h.work("m2")
	h.machine() // review, asked
	h.readAll()
	if n := len(h.openOf(sprint.NReadyToAccept)); n != 0 {
		t.Fatalf("the reads on a running machine opened %d ready-to-accept judgments", n)
	}
	h.machine() // the pump accepts
	for _, id := range []string{"s1-1", "s1-2"} {
		if st := h.table().StateOf(id); st != sprint.Merging {
			t.Fatalf("%s is %s after the pump, want merging", id, st)
		}
	}
	h.clean("accepted by the pump")

	g := newHarness(t)
	g.setup(1)
	g.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	g.work("m1")
	g.work("m2")
	g.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	g.readAll()
	if n := len(g.openOf(sprint.NReadyToAccept)); n != 1 {
		t.Fatalf("the reads on a stopped machine opened %d ready-to-accept judgments, want 1", n)
	}
}

// "changes queued after the pump's drain wait for the next tick": a world
// that queues changes all through the pump (here a drop of a ready card
// between the drain and the deal, and more at every part) never fails the
// tick, the card a queued change names is not moved by this pump (the deal
// leaves it, saying so), and the next tick's drain applies the change.
func TestChangesQueuedDuringThePumpWaitForTheNextTick(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 3}))
	h.startMachine()
	dropped, ranks := false, 0
	busy := func(s *sprint.Snapshot, _ sprint.TickReq) (sprint.Plan, int) {
		if !dropped {
			dropped = true
			h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Reason: "the world"}))
		}
		ranks++
		h.must(RankStep(sprint.RankReq{IDs: []string{"s1-3"}, First: ranks%2 == 0}))
		return sprint.Plan{Notes: []sprint.Note{{Kind: sprint.Happened, Type: "world", At: s.Now}}}, 0
	}
	work := sprint.TickTables[0]
	parts := []sprint.TickPartDef{work.Parts[0]}
	for _, p := range work.Parts[1:] {
		parts = append(parts, sprint.TickPartDef{Name: "world", Fn: busy}, p)
	}
	work.Parts = parts
	h.st.Updates = []sprint.TableUpdate{work, sprint.TickTables[1], sprint.TickTables[2], sprint.TickTables[3]}
	res := h.machine()
	if ranks < MaxDrains+1 {
		t.Fatalf("the world queued %d times during the pump, want more than %d", ranks, MaxDrains)
	}
	if st := h.table().StateOf("s1-1"); st != sprint.Ready {
		t.Fatalf("s1-1, dropped during the pump, is %s on the table: the pump moved a card a queued change names", st)
	}
	left := false
	for _, p := range res.Parts {
		for _, r := range p.Refused {
			left = left || r.Key == "s1-1" && strings.Contains(r.Why, "waits for the next tick")
		}
	}
	if !left {
		t.Fatalf("the deal left s1-1 silently: %+v", res.Parts)
	}
	h.st.Updates = nil
	h.machine()
	if c := h.table().Work.Card("s1-1"); c.Placed() {
		t.Fatalf("the next tick's drain did not apply the drop: s1-1 at %s", c.Col)
	}
	h.clean("the drop applied a tick later")
}

// The pump's second drain (a card added and dropped before one drain: the
// removal is requeued and drained at once) is named in the tick's report,
// its moves and its time.
func TestThePumpsSecondDrainIsInTheReport(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"brief"}}))
	h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"brief"}}, Reason: "gone"}))
	res := h.machine()
	drains := 0
	for _, p := range res.Parts {
		if p.Name == sprint.PartDrain {
			drains++
		}
	}
	if drains != 2 {
		t.Fatalf("the tick names %d drains, want the drain and its second: %+v", drains, res.Parts)
	}
	timed := false
	for _, pt := range res.Times {
		timed = timed || pt.Name == sprint.PartDrain
	}
	if !timed {
		t.Fatalf("the drains' time is not in the report: %+v", res.Times)
	}
	if c := h.table().Work.Card("brief"); c.Placed() {
		t.Fatalf("brief is on the table after the tick: %s", c.Col)
	}
}

// The rolling indexes are up by one a placement MADE and one a name passed
// over (errata 3, the form of the index): a deal unit a queued change holds
// back (LeaveQueued) places nothing, so the fleet's deal_index and the work
// table's stream_index move for the kept placements only, and the card held
// back is dealt by the next tick's pump. Three streams of one card each, three
// machines; the world ranks one card between the pump's drain and its deal.
func TestADealHeldBackByTheQueueMovesNoIndex(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		held          string
		dealIndex     string // three names: a held-back second is passed over, a held-back third is not reached
		streamIndex   string
		kept, dealtTo []string
	}{
		{held: "b", dealIndex: "3", streamIndex: "3", kept: []string{"a", "c"}, dealtTo: []string{"m1", "m3"}},
		{held: "c", dealIndex: "2", streamIndex: "2", kept: []string{"a", "b"}, dealtTo: []string{"m1", "m2"}},
	} {
		h := newHarness(t)
		h.mu.Lock()
		h.live = append(h.live, "m3")
		h.mu.Unlock()
		h.beat()
		for _, m := range []string{"m1", "m2", "m3"} {
			h.must(FleetStep(sprint.FleetReq{Op: "up", Member: m}))
		}
		for i, id := range []string{"a", "b", "c"} {
			h.must(AddStep(sprint.AddReq{Stream: "s" + strconv.Itoa(i+1), IDs: []string{id}}))
		}
		h.startMachine()
		ranked := false
		work := sprint.TickTables[0]
		var parts []sprint.TickPartDef
		for _, p := range work.Parts {
			if p.Name == "deal" {
				parts = append(parts, sprint.TickPartDef{Name: "world", Fn: func(s *sprint.Snapshot, _ sprint.TickReq) (sprint.Plan, int) {
					if !ranked {
						ranked = true
						h.must(RankStep(sprint.RankReq{IDs: []string{tc.held}, First: true}))
						return sprint.Plan{Notes: []sprint.Note{{Kind: sprint.Happened, Type: "world", At: s.Now}}}, 0
					}
					return sprint.Plan{}, 0
				}})
			}
			parts = append(parts, p)
		}
		work.Parts = parts
		h.st.Updates = []sprint.TableUpdate{work, sprint.TickTables[1], sprint.TickTables[2], sprint.TickTables[3]}
		h.machine()
		s := h.table()
		for i, id := range tc.kept {
			if c := s.Work.Card(id); c.Col != sprint.Working || s.Fleet.Card(c.F("work")).Row != tc.dealtTo[i] {
				t.Fatalf("held %s: %s is %s on %s, want working on %s", tc.held, id, c.Col, s.Fleet.Card(c.F("work")).Row, tc.dealtTo[i])
			}
		}
		if st := s.StateOf(tc.held); st != sprint.Ready {
			t.Fatalf("held %s: it is %s, want ready: the queued rank holds it back", tc.held, st)
		}
		di, _ := s.Fleet.Prop(sprint.PropDealIndex)
		si, _ := s.Work.Prop(sprint.PropStreamIndex)
		if di != tc.dealIndex || si != tc.streamIndex {
			t.Fatalf("held %s: deal_index %s, stream_index %s; want %s and %s: up by the kept placements and the names they pass over only", tc.held, di, si, tc.dealIndex, tc.streamIndex)
		}
		h.st.Updates = nil
		h.machine()
		if st := h.table().StateOf(tc.held); st != sprint.Working {
			t.Fatalf("held %s: the next tick left it %s", tc.held, st)
		}
		t.Logf("held %s: deal_index %s, stream_index %s after the tick; dealt the next tick", tc.held, di, si)
	}
}

// Every step but the pump judges the work table through the queued view: an
// accept on a running machine (the coordinator's, its work change queued, its
// merge card written at once) and a merge in the same tick, before the pump,
// agree: MERGE lands it, and check is clean before and after the pump.
func TestAMergeBeforeThePumpSeesTheQueuedAccept(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.stopMachine()
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1", "s1-2"}}}))
	h.work("m1")
	h.work("m2")
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1", "s1-2"}}}))
	h.readAll()
	h.startMachine()
	h.must(AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{IDs: []string{"s1-1", "s1-2"}}}))
	if st := h.table().StateOf("s1-1"); st != sprint.Review {
		t.Fatalf("the accept's work change is not queued: s1-1 is %s on the table", st)
	}
	h.clean("accepted, before the pump")
	h.must(MergeStep(sprint.MergeReq{Stream: "s1", Batch: 10}))
	h.clean("merged, before the pump")
	h.machine()
	for _, id := range []string{"s1-1", "s1-2"} {
		if st := h.table().StateOf(id); st != sprint.Landed {
			t.Fatalf("%s is %s after the pump", id, st)
		}
	}
	h.clean("landed")
}

// queueARankAfterTheDrain makes the one world event of a tick: a part of the
// work table's update, before accept, queues a rank of s1-1 after the pump's
// drain. The rank names the card, so the accept step holds it for the next
// tick's pump (LeaveQueued; docs/SPEC-SPRINT.md's accept row). The part runs
// once; the later ticks run the tick's own parts.
func (h *harness) queueARankAfterTheDrain() {
	h.t.Helper()
	ranked := false
	work := sprint.TickTables[0]
	var parts []sprint.TickPartDef
	for _, p := range work.Parts {
		if p.Name == "accept" {
			parts = append(parts, sprint.TickPartDef{Name: "world", Fn: func(s *sprint.Snapshot, _ sprint.TickReq) (sprint.Plan, int) {
				if ranked {
					return sprint.Plan{}, 0
				}
				ranked = true
				h.must(RankStep(sprint.RankReq{IDs: []string{"s1-1"}, First: true}))
				return sprint.Plan{Notes: []sprint.Note{{Kind: sprint.Happened, Type: "world", At: s.Now}}}, 0
			}})
		}
		parts = append(parts, p)
	}
	work.Parts = parts
	h.st.Updates = []sprint.TableUpdate{work, sprint.TickTables[1], sprint.TickTables[2], sprint.TickTables[3]}
}

// readyToMerge is the one "ready to merge" note written so far.
func (h *harness) readyToMerge() sprint.Note {
	h.t.Helper()
	notes, _, _ := h.m.NotesSince(h.ctx, "", 100000)
	var found []sprint.Note
	for _, n := range notes {
		if n.Type == sprint.NReadyToMerge {
			found = append(found, n)
		}
	}
	require.Len(h.t, found, 1)
	return found[0]
}

// A review primary a change queued after the pump's work drain (a queued rank)
// waits for the next tick's pump: the accept part does not plan it, so it moves
// to no merge queue and no note says it did (no phantom "ready to merge"), and
// the stream's state and its "started merging" note follow the cards accepted
// (docs/SPEC-SPRINT.md's accept row; tla/DirtyTick.tla). Each subtest is red
// without the accept part's skip of a held card.
func TestAnAcceptHeldBackByTheQueueEmitsNoPhantomReadyToMergeNote(t *testing.T) {
	t.Parallel()
	t.Run("all held", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.setup(1)
		h.startMachine()
		h.machine() // deals s1-1
		h.work("m1")
		h.machine() // moves s1-1 to review, asks readers
		h.readAll() // readers read and give ok

		h.queueARankAfterTheDrain()
		h.machine()
		require.Equal(t, sprint.Review, h.table().StateOf("s1-1"), "the held card stays in review")
		require.Equal(t, sprint.StreamWaiting, h.table().StreamCtl("s1").F("state"))
		require.Zero(t, h.written(sprint.NReadyToMerge), "a phantom ready to merge note")
		require.Zero(t, h.written(sprint.NStartedMerging))

		// the next tick drains the rank and accepts the card
		h.st.Updates = nil
		h.machine()
		require.Equal(t, sprint.Merging, h.table().StateOf("s1-1"))
		require.Equal(t, sprint.StreamMerging, h.table().StreamCtl("s1").F("state"))
		require.Equal(t, 1, h.written(sprint.NReadyToMerge))
	})

	t.Run("first held second kept", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.setup(2)
		h.startMachine()
		h.machine() // deals both
		h.work("m1")
		h.work("m2")
		h.machine() // moves both to review, asks readers
		h.readAll() // readers read both

		h.queueARankAfterTheDrain()
		h.machine()
		require.Equal(t, sprint.Review, h.table().StateOf("s1-1"), "the held card stays in review")
		require.Equal(t, sprint.Merging, h.table().StateOf("s1-2"))
		require.Equal(t, sprint.StreamMerging, h.table().StreamCtl("s1").F("state"), "the stream follows the card accepted")
		require.Equal(t, 1, h.written(sprint.NStartedMerging))
		rtm := h.readyToMerge()
		require.Equal(t, []string{"s1-2"}, rtm.Primaries)
		require.Equal(t, 1, rtm.Count)
		require.NotContains(t, rtm.What, "s1-1")

		// the next tick accepts the held card
		h.st.Updates = nil
		h.machine()
		require.Equal(t, sprint.Merging, h.table().StateOf("s1-1"))
		require.Equal(t, 2, h.written(sprint.NReadyToMerge))
	})
}
