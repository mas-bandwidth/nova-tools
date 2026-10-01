package store

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// machine is the harness's tick: one tick, which must not fail.
func (h *harness) machine() TickResult {
	h.t.Helper()
	res, err := h.st.Tick(h.ctx)
	if err != nil {
		h.t.Fatalf("tick: %v", err)
	}
	return res
}

func (h *harness) startMachine() {
	h.t.Helper()
	if _, _, _, err := h.st.SetMachine(h.ctx, true); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) stopMachine() {
	h.t.Helper()
	if _, _, _, err := h.st.SetMachine(h.ctx, false); err != nil {
		h.t.Fatal(err)
	}
}

// work plays a worker: it takes every ready card of the member and finishes
// every card it holds, ok.
func (h *harness) work(member string) {
	h.t.Helper()
	h.run(TakeStep(sprint.TakeReq{As: member, Sel: sprint.Sel{Limit: 100}, Who: member}))
	s := h.snap()
	var ids []string
	gens := map[string]int{}
	for _, c := range s.Fleet.Cell(member, sprint.Working) {
		ids = append(ids, c.ID)
		gens[c.ID] = c.Int("gen")
	}
	if len(ids) > 0 {
		h.must(FinishStep(sprint.FinishReq{As: member, Sel: sprint.Sel{IDs: ids}, Gens: gens, Who: member}))
	}
}

// heldBy is the work cards a member holds against its width: ready and
// working.
func heldBy(s *sprint.Snapshot, m string) int {
	return s.Fleet.Count(m, sprint.Ready) + s.Fleet.Count(m, sprint.Working)
}

// readAll plays the readers: every read card asked of them is reported ok.
func (h *harness) readAll() {
	h.t.Helper()
	for _, r := range []string{"reader-a", "reader-b", "reader-c"} {
		h.run(ReadStep(sprint.ReadReq{As: r, Verdict: "ok", Sel: sprint.Sel{Limit: 100}, Who: r}))
	}
}

// landAll plays the coordinator's accept and the merger's step for a stream.
func (h *harness) landAll(stream string) {
	h.t.Helper()
	h.run(AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{Stream: stream}}))
	if len(h.snap().Merge.Cell(stream, sprint.Queued)) > 0 { // a merge step wants something queued
		h.must(MergeStep(sprint.MergeReq{Stream: stream, Batch: 100}))
	}
}

func TestStartAndStopAreIdempotentAndRecorded(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if line := h.st.MachineLine(h.ctx); line != "machine: STOPPED" {
		t.Fatalf("a new sprint: %q", line)
	}
	before, after, res, err := h.st.SetMachine(h.ctx, true)
	if err != nil || before.Running() || !after.Running() || res.Notes != 1 {
		t.Fatalf("start: %+v %+v %+v %v", before, after, res, err)
	}
	if line := h.st.MachineLine(h.ctx); line != "machine: running" {
		t.Fatalf("started: %q", line)
	}
	_, again, res, err := h.st.SetMachine(h.ctx, true)
	if err != nil || !again.Running() || res.Notes != 0 || !again.Since.Equal(after.Since) {
		t.Fatalf("start when running changed something: %+v %+v %v", again, res, err)
	}
	h.tick(MachineSilence + time.Second)
	if line := h.st.MachineLine(h.ctx); line != "machine: STOPPED" || strings.Contains(line, "(no tick") {
		t.Fatalf("no tick: %q", line)
	}
	h.machine()
	if line := h.st.MachineLine(h.ctx); line != "machine: running" {
		t.Fatalf("ticked: %q", line)
	}
	h.stopMachine()
	h.tick(time.Hour)
	_, still, res, _ := h.st.SetMachine(h.ctx, false)
	if still.Running() || res.Notes != 0 {
		t.Fatalf("stop when stopped changed something: %+v", res)
	}
	if got := still.StoppedTotal(h.now); got != time.Hour {
		t.Fatalf("stopped for %s, want 1h", got)
	}
	h.startMachine()
	m, _, _ := h.st.Machine(h.ctx)
	if m.StoppedTotal(h.now) != time.Hour || m.StoppedBetween(t0, h.now) != time.Hour {
		t.Fatalf("the stopped hour: %s %s", m.StoppedTotal(h.now), m.StoppedBetween(t0, h.now))
	}
	v, err := h.st.Inbox(h.ctx, time.Hour, time.Hour, 100)
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	for _, g := range v.Groups {
		seen = append(seen, g.Type)
	}
	if got := strings.Join(seen, "|"); !strings.Contains(got, sprint.NMachineStarted) || !strings.Contains(got, sprint.NMachineStopped) {
		t.Fatalf("the inbox lacks the machine's stops and starts: %s", got)
	}
}

func TestAStoppedMachineMovesNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(3)
	if res := h.machine(); res.State != Stopped || len(res.Parts) > 0 {
		t.Fatalf("a stopped tick: %+v", res)
	}
	if n := h.snap().Work.Count("s1", sprint.Ready); n != 3 {
		t.Fatalf("ready %d", n)
	}
	if _, hb, _ := h.st.Machine(h.ctx); hb.Ticks != 0 {
		t.Fatalf("a stopped machine beat: %+v", hb)
	}
}

func TestAChainIsDealtWithinOneTickOfEachLanding(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"a"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"b"}, Needs: []string{"a"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"c"}, Needs: []string{"b"}}))
	h.startMachine()
	h.machine()
	for i, id := range []string{"a", "b", "c"} {
		if st := h.state(id); st != sprint.Working {
			t.Fatalf("landing %d: %s is %s, not dealt within one tick", i, id, st)
		}
		for _, later := range []string{"a", "b", "c"}[i+1:] {
			if st := h.state(later); st != sprint.Waiting {
				t.Fatalf("%s left waiting before its need landed: %s", later, st)
			}
		}
		h.work("m1")
		h.machine() // asks two readers
		h.readAll()
		h.landAll("s1")
		if st := h.state(id); st != sprint.Landed {
			t.Fatalf("%s is %s", id, st)
		}
		h.machine() // the landing: the next is resolved and dealt in this one tick
		h.clean("after " + id)
	}
}

func TestACrossStreamNeedIsDealtWhenItLands(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"x"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"y"}, Needs: []string{"x"}}))
	h.startMachine()
	h.machine()
	h.work("m1")
	h.machine()
	h.readAll()
	h.landAll("s1")
	// the landing step resolves what waited for it; the tick deals it
	if st := h.state("y"); st != sprint.Ready {
		t.Fatalf("y is %s after the landing", st)
	}
	h.machine()
	if st := h.state("y"); st != sprint.Working {
		t.Fatalf("y is %s after the landing's tick", st)
	}
}

// Every tick reads and plans every table, whatever changed since the last
// (errata 3 amendment 10: "each table should be updated per-tick at least
// once"): a tick with nothing to do reads the four tables, names each of them
// on its result with no rows, and changes nothing.
func TestAnIdleTickReadsEveryTableAndChangesNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.startMachine()
	h.machine()
	h.machine() // the tick after moves: reads everything, finds nothing to do
	before := map[string]int{}
	for k, v := range h.m.Calls {
		before[k] = v
	}
	revs := [4]uint64{}
	for i, tb := range All {
		revs[i] = h.m.Revision(h.st.Names.Table(tb))
	}
	res := h.machine()
	if !res.Idle || len(res.Parts) > 0 {
		t.Fatalf("an idle tick: %+v", res)
	}
	for i, tb := range All {
		if h.m.Revision(h.st.Names.Table(tb)) != revs[i] {
			t.Fatalf("an idle tick changed %s", tb)
		}
	}
	if len(res.Tables) != len(All) {
		t.Fatalf("an idle tick names %d tables, want every one: %+v", len(res.Tables), res.Tables)
	}
	for i, tb := range res.Tables {
		if tb.Table != All[i] || len(tb.Rows) != 0 {
			t.Errorf("an idle tick's table %d: %+v", i, tb)
		}
	}
	if h.m.Calls["cells"]-before["cells"] == 0 && h.m.Calls["readset"]-before["readset"] == 0 {
		t.Errorf("an idle tick read no cards: %v", h.m.Calls)
	}
}

// At width 2 the deal keeps every queue short: DealAhead times two cards a
// member, the oldest first, the rest left in ready.
func TestTheDealingKeepsEveryReadyQueueShortInScoreOrder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const width = 2
	for _, m := range []string{"m1", "m2"} {
		h.must(FleetStep(sprint.FleetReq{Op: "up", Member: m, Width: width}))
	}
	h.setup(10)
	h.startMachine()
	h.machine()
	s := h.snap()
	for _, m := range []string{"m1", "m2"} {
		if n := s.Fleet.Count(m, sprint.Ready); n != sprint.DealAhead*width || s.Width(m) != width {
			t.Fatalf("%s holds %d ready, want DealAhead times its width %d", m, n, width)
		}
	}
	for i, c := range s.Work.Column(sprint.Ready) {
		if want := []string{"s1-9", "s1-10"}[i]; c.ID != want {
			t.Fatalf("left in ready: %s, want %s (the oldest are dealt first)", c.ID, want)
		}
	}
	if res := h.machine(); len(res.Moved()) > 0 {
		t.Fatalf("a full queue took more: %v", res.Moved())
	}
	h.work("m1")
	h.machine()
	// m1 took and finished its queue: the level gives it m2's ready cards past
	// m2's lanes and the deal the two left, none past DealAhead times a width
	s = h.snap()
	if n := s.Fleet.Count("m1", sprint.Ready); n < 2 || h.state("s1-9") != sprint.Working || h.state("s1-10") != sprint.Working {
		t.Fatalf("after m1 took its queue: ready %d, s1-9 %s, s1-10 %s", n, h.state("s1-9"), h.state("s1-10"))
	}
	for _, m := range []string{"m1", "m2"} {
		held := s.Fleet.Count(m, sprint.Ready) + s.Fleet.Count(m, sprint.Working)
		require.LessOrEqual(t, held, sprint.DealAhead*width, "%s holds %d, past DealAhead times its width %d", m, held, width)
	}
	h.clean("dealt")
}

// One clock for overdue: the inbox counts running time, as the tick's
// deadlines do. A judgment open for four hours of which the machine was
// STOPPED for three is not overdue at a two-hour deadline; a stream
// unchanged for as long is not stale.
func TestTheInboxCountsRunningTimeOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{Limit: 1}}))
	c := h.snap().Fleet.Card("s1-1.w1")
	h.must(TakeStep(sprint.TakeReq{As: c.Row, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: 1}}))
	h.must(FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: 1}, Failed: true}))
	h.tick(30 * time.Minute)
	h.stopMachine()
	h.tick(3 * time.Hour)
	h.startMachine()
	h.tick(30 * time.Minute)
	v, err := h.st.Inbox(h.ctx, 2*time.Hour, 2*time.Hour, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range v.Groups {
		if strings.HasPrefix(g.ID, "machine:") {
			continue // the harness moves the clock with no run loop ticking
		}
		if g.Kind == sprint.Judgment && (g.Overdue || g.Waited != time.Hour || !g.Due.Equal(t0.Add(5*time.Hour))) {
			t.Fatalf("stopped hours counted: %+v", g)
		}
	}
	h.tick(90 * time.Minute)
	v, _ = h.st.Inbox(h.ctx, 2*time.Hour, 0, 1000)
	overdue := false
	for _, g := range v.Groups {
		overdue = overdue || g.Type == sprint.NWorkFailed && g.Overdue
	}
	if !overdue {
		t.Fatalf("past the deadline in running time: %+v", v.Groups)
	}
}

// A judgment the tick wrote, waited on while its condition holds, is not
// written again; when the condition clears the hold is closed, and when it
// comes back the judgment is written again at once.
func TestAWaitedConditionIsClosedWhenItClears(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 2}))
	h.startMachine()
	h.machine()
	open := h.openOf(sprint.NNoMember)
	if len(open) != 1 || h.written(sprint.NNoMember) != 1 {
		t.Fatalf("no member: open %d written %d", len(open), h.written(sprint.NNoMember))
	}
	if _, held, err := h.st.Wait(h.ctx, open[0].Note.ID, h.now.Add(time.Hour)); err != nil || !held {
		t.Fatalf("wait: %v %v", held, err)
	}
	for i := 0; i < 3; i++ {
		h.tick(time.Minute)
		h.machine()
	}
	if h.written(sprint.NNoMember) != 1 {
		t.Fatalf("written again while waited: %d", h.written(sprint.NNoMember))
	}
	if held := h.openOf(sprint.NNoMember); len(held) != 1 || held[0].Note.Kind != sprint.Acknowledged {
		t.Fatalf("the hold is not kept: %+v", held)
	}
	v, _ := h.st.Inbox(h.ctx, time.Hour, 0, 1000)
	for _, g := range v.Groups {
		if g.Kind != sprint.Happened && g.Kind != sprint.Decided {
			t.Fatalf("the inbox while waited: %+v", g)
		}
	}
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.machine()
	if held := h.openOf(sprint.NNoMember); len(held) != 0 {
		t.Fatalf("the condition cleared and the hold is kept: %+v", held)
	}
	h.must(FleetStep(sprint.FleetReq{Op: "hold", Member: "m1"}))
	h.machine()
	if h.written(sprint.NNoMember) != 2 || len(h.openOf(sprint.NNoMember)) != 1 {
		t.Fatalf("the condition came back: written %d open %d", h.written(sprint.NNoMember), len(h.openOf(sprint.NNoMember)))
	}
	h.clean("after the wait")
}

// An operation pending that the tick cannot finish: the tick fails and says
// why, and keeps a stuck record; the first step that writes once repair has
// freed the fence writes one judgment: the operation, how long it was stuck,
// and what repair did.
func TestAStuckOperationIsReportedOnceByTheFirstWriterAfterRepair(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.startMachine()
	h.m.Fail = func(p string) error {
		if strings.HasPrefix(p, "apply ") {
			return errors.New("no answer")
		}
		return nil
	}
	if _, err := h.st.Run(h.ctx, DealStep(sprint.DealReq{Sel: sprint.Sel{Limit: 2}})); err == nil || h.m.Pending() == nil {
		t.Fatalf("an operation left pending: %v", err)
	}
	h.tick(10 * time.Minute)
	if _, err := h.st.Tick(h.ctx); err == nil || !strings.Contains(err.Error(), "could not finish it") {
		t.Fatalf("the tick with an operation it cannot finish: %v", err)
	}
	if line := h.st.MachineLine(h.ctx); line != "machine: running" {
		t.Fatalf("the machine line: %s", line)
	}
	h.m.Fail = nil
	h.tick(5 * time.Minute)
	rr, err := h.st.Repair(h.ctx)
	if err != nil || len(rr) != 1 || rr[0].Done != RepairFinished {
		t.Fatalf("repair: %+v %v", rr, err)
	}
	if n := len(h.openOf(sprint.NOpStuck)); n != 0 {
		t.Fatalf("written by the repair itself: %d", n)
	}
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m3"}))
	open := h.openOf(sprint.NOpStuck)
	if len(open) != 1 || !strings.Contains(open[0].Note.What, "(deal) was stuck 15m0s") || !strings.Contains(open[0].Note.What, "repair: finished") {
		t.Fatalf("the stuck judgment: %+v", open)
	}
	h.must(FleetStep(sprint.FleetReq{Op: "down", Member: "m3"}))
	h.machine()
	if h.written(sprint.NOpStuck) != 1 {
		t.Fatalf("written %d times", h.written(sprint.NOpStuck))
	}
	v, _ := h.st.Inbox(h.ctx, time.Hour, 0, 1000)
	for _, g := range v.Groups {
		if g.Type == sprint.NOpStuck && (len(g.Commands) != 2 || g.Commands[0].Lines[0] != "nova-sprint check") {
			t.Fatalf("its commands: %+v", g.Commands)
		}
	}
	h.clean("after the stuck operation")
}

// The tick never moves a sentinel: when everything before it has landed the
// tick marks it reached and opens its judgment; what waits behind it stays
// waiting, and is dealt only after the coordinator releases it.
func TestTheTickStopsAtASentinelUntilItIsReleased(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if err := h.m.SetCoordinator(h.ctx, "tester"); err != nil {
		t.Fatal(err)
	}
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 2}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"stop"}, Sentinel: true}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"after"}}))
	h.startMachine()
	for i := 0; i < 4; i++ {
		h.machine()
		h.work("m1")
		h.machine()
		h.readAll()
		h.landAll("s1")
	}
	h.machine()
	if h.state("s1-1") != sprint.Landed || h.state("s1-2") != sprint.Landed {
		t.Fatalf("before the sentinel: %s %s", h.state("s1-1"), h.state("s1-2"))
	}
	if h.state("stop") != sprint.Waiting || h.state("after") != sprint.Waiting || len(h.openOf(sprint.NSentinelReached)) != 1 {
		t.Fatalf("at the sentinel: stop %s after %s reached %d", h.state("stop"), h.state("after"), len(h.openOf(sprint.NSentinelReached)))
	}
	h.quiet("at the sentinel")
	h.must(ReleaseStep(sprint.ReleaseReq{IDs: []string{"stop"}, Reason: "the first two are green", Coordinator: "tester", Who: "tester"}))
	h.machine()
	if h.state("stop") != sprint.Landed || h.state("after") != sprint.Working {
		t.Fatalf("after the release: stop %s after %s", h.state("stop"), h.state("after"))
	}
	h.clean("after the release")
}

// The landing step resolves what waited on it and marks a due sentinel
// reached at once; the tick is the backstop and does neither again.
func TestTheLandingStepAndTheTickDoNothingTwice(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if err := h.m.SetCoordinator(h.ctx, "tester"); err != nil {
		t.Fatal(err)
	}
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"x"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"stop"}, Sentinel: true}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"y"}, Needs: []string{"x"}}))
	h.startMachine()
	h.machine()
	h.work("m1")
	h.machine()
	h.readAll()
	h.landAll("s1")
	if h.state("y") != sprint.Ready || len(h.openOf(sprint.NSentinelReached)) != 1 {
		t.Fatalf("after the landing step: y %s, reached %d", h.state("y"), len(h.openOf(sprint.NSentinelReached)))
	}
	before := h.written(sprint.NSentinelReached)
	res := h.machine()
	for _, p := range res.Parts {
		if p.Name == "resolve" && (len(p.Moved) > 0 || p.Notes > 0) {
			t.Fatalf("the tick resolved again: %+v", p)
		}
	}
	if h.written(sprint.NSentinelReached) != before || h.state("y") != sprint.Working {
		t.Fatalf("after the tick: reached written %d (was %d), y %s", h.written(sprint.NSentinelReached), before, h.state("y"))
	}
	h.quiet("after the landing")
	h.clean("after the landing")
}

// A STOPPED machine's tick moves nothing and counts no tick, and says it
// looked: start can tell a run loop is waiting.
func TestAStoppedTickSaysItLooked(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.tick(time.Minute)
	h.machine()
	_, hb, err := h.st.Machine(h.ctx)
	if err != nil || hb.Ticks != 0 || !hb.Alive().Equal(h.now) {
		t.Fatalf("a stopped tick: %+v %v", hb, err)
	}
}

// A member whose beat lapses again and again has its card dealt to another
// and back each time, re-stamping dealt: the unfinished deadline counts from
// the attempt's first deal, so three hours of it are late all the same.
func TestAFlappingMemberCannotHideALateCard(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.live = []string{"m1"}
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 1}))
	h.startMachine()
	h.machine()
	first := h.snap().Fleet.Card("s1-1.w1").F("first_dealt")
	if first == "" {
		t.Fatalf("no first_dealt on the card")
	}
	// m1 beats for 10 s and falls silent for 50 s (past the beat windows it may
	// miss), over and over, for three hours; its card goes withdrawn and back
	for elapsed := time.Duration(0); elapsed < 3*time.Hour; elapsed += 10 * time.Second {
		if elapsed%(60*time.Second) < 10*time.Second {
			h.live = []string{"m1"}
		} else {
			h.live = nil
		}
		h.tick(10 * time.Second)
		h.machine()
	}
	h.live = []string{"m1"}
	h.tick(time.Second)
	h.machine()
	if c := h.snap().Fleet.Card("s1-1.w1"); c.F("first_dealt") != first {
		t.Fatalf("first_dealt moved: %s, was %s", c.F("first_dealt"), first)
	}
	if h.written(sprint.NWorkLate) == 0 {
		t.Fatalf("three hours of a card dealt and never finished, and no deadline")
	}
}

// The deadlines count from the attempt's first deal and first take: a member
// whose beat lapses and returns four times, its card withdrawn and dealt
// again each time, is late "not taken" once 15 minutes of running time have
// passed since the first deal; taken, then lapsing three times, it is late
// "not finished" 2 hours after the first take.
func TestAFlappingMemberIsLateFromTheFirstDealAndTheFirstTake(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.live = []string{"m1"}
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 1}))
	h.startMachine()
	h.machine()
	lap := func() {
		h.live = nil // silent past the beat deadline: withdrawn
		for i := 0; i < 3; i++ {
			h.tick(pastDown / 3)
			h.machine()
		}
		h.live = []string{"m1"} // back: dealt again
		h.tick(time.Second)
		h.machine()
		h.tick(5 * time.Minute) // under the 15 minutes from any one deal
		h.machine()
	}
	for i := 0; i < 4; i++ {
		lap()
	}
	notTaken := func() int {
		n := 0
		notes, _, _ := h.m.NotesSince(h.ctx, "", 100000)
		for _, x := range notes {
			if x.Type == sprint.NWorkLate && x.Kind == sprint.Judgment && strings.Contains(x.What, "not taken") {
				n++
			}
		}
		return n
	}
	if n := notTaken(); n != 1 {
		t.Fatalf("four laps, over 15 minutes of running time from the first deal: %d not-taken judgments", n)
	}
	// taken, then lapsing: late not finished two hours from the first take
	h2 := newHarness(t)
	h2.live = []string{"m1"}
	h2.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h2.must(AddStep(sprint.AddReq{Stream: "s1", Count: 1}))
	h2.startMachine()
	h2.machine()
	h2.run(TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 1}, Who: "m1"}))
	for i := 0; i < 3; i++ {
		h2.live = nil
		for j := 0; j < 3; j++ {
			h2.tick(pastDown / 3)
			h2.machine()
		}
		h2.live = []string{"m1"}
		h2.tick(time.Second)
		h2.machine() // back: its presence is the fleet's update, after the pump
		h2.tick(time.Second)
		h2.machine() // the next pump redeals the card to it
		h2.run(TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 1}, Who: "m1"}))
		h2.tick(45 * time.Minute)
		h2.machine()
	}
	notes, _, _ := h2.m.NotesSince(h2.ctx, "", 100000)
	late := 0
	for _, x := range notes {
		if x.Type == sprint.NWorkLate && x.Kind == sprint.Judgment && strings.Contains(x.What, "not finished") {
			late++
		}
	}
	if late != 1 {
		t.Fatalf("three laps after a take, past 2 hours from the first take: %d not-finished judgments", late)
	}
}

// A card taken, its member silent, redealt to a member that is up and never
// taken again: the clock follows the card's state. It is late not taken, 15
// minutes from the redeal (the first deal since its last take), on the full
// tick, and no stalled judgment speaks for it instead; the holder and the
// deadline part end at the same moment.
func TestARedealtCardAfterATakeIsLateNotTaken(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.live = []string{"m1", "m2"}
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m2"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 1}))
	h.startMachine()
	h.machine()
	wc := h.snap().Fleet.Column(sprint.Ready)
	if len(wc) != 1 {
		t.Fatalf("one ready work card, got %d", len(wc))
	}
	card, first := wc[0].ID, wc[0].Row
	other := "m2"
	if first == "m2" {
		other = "m1"
	}
	h.must(TakeStep(sprint.TakeReq{As: first, Sel: sprint.Sel{Limit: 1}, Who: first}))
	for i := 0; i < 30; i++ { // half an hour of work, the machine ticking
		h.tick(time.Minute)
		h.machine()
	}
	h.live = []string{other} // the taker goes silent
	for i := 0; i < 3; i++ {
		h.tick(pastDown / 3)
		h.machine()
	}
	c := h.snap().Fleet.Card(card)
	if c == nil || c.Col != sprint.Ready || c.Row != other {
		t.Fatalf("redealt to %s: %+v", other, c)
	}
	judged := func(typ, word string) int {
		n := 0
		notes, _, _ := h.m.NotesSince(h.ctx, "", 100000)
		for _, x := range notes {
			if x.Type == typ && x.Kind == sprint.Judgment && strings.Contains(x.What, card) && strings.Contains(x.What, word) {
				n++
			}
		}
		return n
	}
	h.tick(time.Minute) // a full tick just after the redeal: the new member has its 15 minutes
	h.machine()
	if n := judged(sprint.NWorkLate, ""); n != 0 {
		t.Fatalf("late at once after the redeal, though its first deal was over 15 minutes ago: %d", n)
	}
	for i := 0; i < 16; i++ {
		h.tick(time.Minute)
		h.machine() // a full tick
		h.machine() // an idle tick
	}
	if n := judged(sprint.NWorkLate, "not taken"); n != 1 {
		t.Fatalf("16 minutes after the redeal after a take: %d not-taken judgments, want 1", n)
	}
	if n := judged(sprint.NWorkLate, "not finished"); n != 0 {
		t.Fatalf("a ready card is never late not finished: %d", n)
	}
	notes, _, _ := h.m.NotesSince(h.ctx, "", 100000)
	for _, x := range notes {
		if x.Type == sprint.NStalled && x.Kind == sprint.Judgment {
			t.Fatalf("a stalled judgment speaks for the late card: %s", x.What)
		}
	}
}

// A late read or work card is its own judgment (reader finding 6): a read
// card late while another read of the same primary is judged late is a
// second judgment, each naming its card and closing when its own card moves.
func TestTwoLateReadsOfOnePrimaryAreTwoJudgments(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"p"}}))
	h.startMachine()
	h.machine()
	h.takeAndFinish(false, "p")
	h.machine() // asks two readers
	cards := h.snap().Readers.Of("p")
	if len(cards) != 2 {
		t.Fatalf("asked: %d", len(cards))
	}
	h.must(ReadStep(sprint.ReadReq{As: cards[0].Row, Begin: true, Sel: sprint.Sel{IDs: []string{cards[0].ID}}}))
	h.tick(sprint.DeadlineUnbegun + time.Minute)
	h.machine() // the unbegun read is late
	late := h.openOf(sprint.NReadLate)
	if len(late) != 1 || late[0].Note.Card != cards[1].ID {
		t.Fatalf("the unbegun read late: %+v", late)
	}
	h.tick(sprint.DeadlineUnreported)
	h.machine() // the begun read is late too, while the first is open
	late = h.openOf(sprint.NReadLate)
	if len(late) != 2 || late[0].Note.Card == late[1].Note.Card {
		t.Fatalf("two late reads: %+v", late)
	}
	h.must(ReadStep(sprint.ReadReq{As: cards[1].Row, Begin: true, Sel: sprint.Sel{IDs: []string{cards[1].ID}}}))
	h.tick(time.Second)
	h.machine()
	late = h.openOf(sprint.NReadLate)
	if len(late) != 1 || late[0].Note.Card != cards[0].ID {
		t.Fatalf("after %s began: %+v", cards[1].ID, late)
	}
}

// A card late at the moment it is redealt (withdrawn past its deadline with
// no member up) does not name the member just handed it (reader finding 6):
// its decisions are wait and drop, not fleet down of that member.
func TestACardLateAtItsRedealDoesNotBlameTheNewMember(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.live = []string{"m1"}
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 1}))
	h.startMachine()
	h.machine() // dealt to m1
	h.live = nil
	for i := 0; i < 25; i++ { // m1 silent 25 minutes: withdrawn, nobody to deal it to
		h.tick(time.Minute)
		h.machine()
	}
	if n := len(h.openOf(sprint.NWorkLate)); n != 1 {
		t.Fatalf("late while withdrawn: %d", n)
	}
	h.live = []string{"m1"}
	h.tick(time.Second)
	h.machine() // m1 back: its presence is the fleet's update, after the pump
	h.tick(time.Second)
	h.machine() // the next pump redeals the card to it
	c := h.snap().Fleet.Card("s1-1.w1")
	if c == nil || c.Col != sprint.Ready || c.Row != "m1" {
		t.Fatalf("redealt: %+v", c)
	}
	h.tick(time.Minute)
	h.machine()
	for _, o := range h.openOf(sprint.NWorkLate) {
		if contains(o.Note.Decisions, "fleet down m1") || !contains(o.Note.Decisions, "wait") {
			t.Fatalf("late at its redeal, decisions %v", o.Note.Decisions)
		}
	}
}

// A machine started and never ticked is silent from its start: the inbox
// says for how long since then, never the time since the zero clock.
func TestAMachineThatNeverTickedIsSilentFromItsStart(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.tick(20 * time.Second)
	v, err := h.st.Inbox(h.ctx, time.Hour, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range v.Groups {
		if g.ID == "machine:silent" {
			if !strings.Contains(g.What, "for 20s") {
				t.Fatalf("the silent machine: %s", g.What)
			}
			return
		}
	}
	t.Fatalf("no machine:silent group: %+v", v.Groups)
}

// A part the store refuses whole on a bound moves nothing, so the tick names no
// row of the tables as changed: the rows are the rows a part's step moved.
func TestATickPartRefusedWholeNamesNoRows(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.startMachine()
	st := *h.st
	r := &refusing{Mem: h.m, table: "t-fleet"}
	st.B = r
	res, err := st.Tick(h.ctx)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if r.calls == 0 || len(res.Parts) == 0 {
		t.Fatalf("the store refused %d manifests, the tick ran parts %+v", r.calls, res.Parts)
	}
	for _, p := range res.Parts {
		if len(p.Moved) != 0 {
			t.Fatalf("part %s moved %v through a refused manifest", p.Name, p.Moved)
		}
	}
	for _, tb := range res.Tables {
		if len(tb.Rows) != 0 {
			t.Errorf("the tick names rows %v of %s as changed, and nothing moved", tb.Rows, tb.Table)
		}
	}
}
