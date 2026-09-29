package store

import (
	"strings"
	"testing"
	"time"

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
	h.must(MergeStep(sprint.MergeReq{Stream: stream, Batch: 100}))
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
	h.tick(6 * time.Second)
	if line := h.st.MachineLine(h.ctx); line != "machine: STOPPED (no tick for 6s)" {
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
	if st := h.state("y"); st != sprint.Waiting {
		t.Fatalf("y is %s before a tick", st)
	}
	h.machine()
	if st := h.state("y"); st != sprint.Working {
		t.Fatalf("y is %s after the landing's tick", st)
	}
}

func TestAnIdleTickReadsLittleAndChangesNothing(t *testing.T) {
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
	for k, v := range h.m.Calls {
		if d := v - before[k]; d > 0 && k != "shapes" && k != "fence" {
			t.Errorf("an idle tick called %s %d times", k, d)
		}
	}
	if h.m.Calls["shapes"]-before["shapes"] != 1 || h.m.Calls["fence"]-before["fence"] != 1 {
		t.Errorf("an idle tick: shapes %d, fence %d", h.m.Calls["shapes"]-before["shapes"], h.m.Calls["fence"]-before["fence"])
	}
}

func TestTheDealingKeepsEveryReadyQueueShortInScoreOrder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(7)
	h.startMachine()
	h.machine()
	s := h.snap()
	for _, m := range []string{"m1", "m2"} {
		if n := s.Fleet.Count(m, sprint.Ready); n != sprint.MaxReadyPerMember {
			t.Fatalf("%s holds %d ready", m, n)
		}
	}
	for i, c := range s.Work.Column(sprint.Ready) {
		if want := []string{"s1-5", "s1-6", "s1-7"}[i]; c.ID != want {
			t.Fatalf("left in ready: %s, want %s (the oldest are dealt first)", c.ID, want)
		}
	}
	if res := h.machine(); len(res.Moved()) > 0 {
		t.Fatalf("a full queue took more: %v", res.Moved())
	}
	h.work("m1")
	h.machine()
	if n := h.snap().Fleet.Count("m1", sprint.Ready); n != 2 || h.state("s1-5") != sprint.Working || h.state("s1-6") != sprint.Working {
		t.Fatalf("after m1 took its queue: ready %d, s1-5 %s, s1-6 %s", n, h.state("s1-5"), h.state("s1-6"))
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
		if g.Kind == sprint.Judgment && (g.Overdue || g.Waited != time.Hour || !g.Due.Equal(t0.Add(5*time.Hour))) {
			t.Fatalf("stopped hours counted: %+v", g)
		}
	}
	h.tick(90 * time.Minute)
	v, _ = h.st.Inbox(h.ctx, 2*time.Hour, 0, 1000)
	if len(v.Groups) == 0 || !v.Groups[0].Overdue {
		t.Fatalf("past the deadline in running time: %+v", v.Groups)
	}
}

// A judgment the tick wrote, acknowledged while its condition holds, is not
// written again until the condition has cleared and come back; the
// acknowledgement is held on the condition and shows in no inbox.
func TestAnAcknowledgedConditionIsNotWrittenAgainUntilItComesBack(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 2}))
	h.startMachine()
	h.machine()
	open := h.openOf(sprint.NNoMember)
	if len(open) != 1 || h.written(sprint.NNoMember) != 1 {
		t.Fatalf("no member: open %d written %d", len(open), h.written(sprint.NNoMember))
	}
	h.must(AckStep(sprint.AckReq{Notes: []string{open[0].Note.ID}, Reason: "members come up later"}))
	for i := 0; i < 3; i++ {
		h.tick(time.Minute)
		h.machine()
	}
	if h.written(sprint.NNoMember) != 1 {
		t.Fatalf("written again while acknowledged: %d", h.written(sprint.NNoMember))
	}
	if held := h.openOf(sprint.NNoMember); len(held) != 1 || held[0].Note.Kind != sprint.Acknowledged {
		t.Fatalf("the acknowledgement is not held: %+v", held)
	}
	v, _ := h.st.Inbox(h.ctx, time.Hour, 0, 1000)
	for _, g := range v.Groups {
		if g.Kind != sprint.Happened && g.Kind != sprint.Decided {
			t.Fatalf("the inbox after the acknowledgement: %+v", g)
		}
	}
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.machine()
	if held := h.openOf(sprint.NNoMember); len(held) != 0 {
		t.Fatalf("the condition cleared and the acknowledgement is held: %+v", held)
	}
	h.must(FleetStep(sprint.FleetReq{Op: "down", Member: "m1"}))
	h.machine()
	if h.written(sprint.NNoMember) != 2 || len(h.openOf(sprint.NNoMember)) != 1 {
		t.Fatalf("the condition came back: written %d open %d", h.written(sprint.NNoMember), len(h.openOf(sprint.NNoMember)))
	}
	h.clean("after the acknowledgement")
}
