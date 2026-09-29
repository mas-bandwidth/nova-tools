package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// Each part of the tick: a sequence that would wait for ever without the
// tick, and moves or notifies with it; and the part again, which changes
// nothing and writes nothing the second time.

// openOf is the open judgments of a type, by subject.
func (h *harness) openOf(typ string) []sprint.Open {
	h.t.Helper()
	open, err := h.m.OpenNotes(h.ctx)
	if err != nil {
		h.t.Fatal(err)
	}
	var out []sprint.Open
	for _, o := range open {
		if o.Note.Type == typ {
			out = append(out, o)
		}
	}
	return out
}

// written is how many notifications of a type the inbox holds.
func (h *harness) written(typ string) int {
	h.t.Helper()
	notes, _, err := h.m.NotesSince(h.ctx, "", 100000)
	if err != nil {
		h.t.Fatal(err)
	}
	n := 0
	for _, x := range notes {
		if x.Type == typ && x.Kind != sprint.Decided {
			n++
		}
	}
	return n
}

// quiet is a tick that must move nothing and write nothing.
func (h *harness) quiet(when string) {
	h.t.Helper()
	res := h.machine()
	if len(res.Moved()) > 0 || res.Notes() > 0 {
		h.t.Fatalf("%s: a second tick moved %v and wrote %d notes", when, res.Moved(), res.Notes())
	}
}

var pokes atomic.Int64

// poke applies one manifest to a table by hand, as a writer outside the
// sprint would: it sets fields of a member, or creates or removes one.
func (h *harness) poke(logical string, e ntable.BatchMemberEntry) {
	h.t.Helper()
	table := h.st.Names.Table(logical)
	n := pokes.Add(1)
	if e.Expect == nil {
		rs, err := h.m.ReadSet(h.ctx, table, []string{e.ID})
		if err != nil || len(rs.Members) != 1 {
			h.t.Fatalf("poke %s: %v", e.ID, err)
		}
		e.Expect = &ntable.MemberExpect{Revision: fmt.Sprint(rs.Members[0].Revision)}
	}
	_, err := h.m.Apply(h.ctx, ntable.BatchManifest{Schema: 1, Table: table, Epoch: "0",
		ExpectedTableRevision: fmt.Sprint(h.m.Revision(table)), OperationID: fmt.Sprintf("poke-%d", n), Actor: "test",
		Members: []ntable.BatchMemberEntry{e}})
	if err != nil {
		h.t.Fatal(err)
	}
}

func TestTheTickAsksTwoReadersAndSaysWhenItCannot(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.startMachine()
	h.machine()
	// one reader less than two free: the readers table keeps one row only
	h.work("m1")
	h.work("m2")
	s := h.snap()
	if n := len(s.Readers.Column(sprint.Asked)); n != 0 {
		t.Fatalf("asked before a tick: %d", n)
	}
	h.machine()
	s = h.snap()
	for _, id := range []string{"s1-1", "s1-2"} {
		if n := len(s.Readers.Of(id)); n != 2 {
			t.Fatalf("%s asked of %d readers", id, n)
		}
	}
	h.quiet("asked")

	// a sprint with one reader: the tick cannot ask, and says so once
	h2 := newHarness(t)
	h2.m = NewMem()
	h2.st.B = h2.m
	if err := h2.st.Init(h2.ctx); err != nil {
		t.Fatal(err)
	}
	if err := h2.m.RowsAdd(h2.ctx, "t-readers", []string{"reader-a"}); err != nil {
		t.Fatal(err)
	}
	h2.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h2.must(AddStep(sprint.AddReq{Stream: "s1", Count: 1}))
	h2.startMachine()
	h2.machine()
	h2.work("m1")
	h2.machine()
	if len(h2.openOf(sprint.NCannotAsk)) != 1 || h2.written(sprint.NCannotAsk) != 1 {
		t.Fatalf("cannot ask: open %d written %d", len(h2.openOf(sprint.NCannotAsk)), h2.written(sprint.NCannotAsk))
	}
	h2.machine()
	h2.tick(2 * time.Minute)
	h2.machine()
	if h2.written(sprint.NCannotAsk) != 1 {
		t.Fatalf("written again: %d", h2.written(sprint.NCannotAsk))
	}
	if err := h2.m.RowsAdd(h2.ctx, "t-readers", []string{"reader-b"}); err != nil {
		t.Fatal(err)
	}
	h2.machine()
	if len(h2.snap().Readers.Of("s1-1")) != 2 || len(h2.openOf(sprint.NCannotAsk)) != 0 {
		t.Fatalf("after a reader was added: reads %d, open %d", len(h2.snap().Readers.Of("s1-1")), len(h2.openOf(sprint.NCannotAsk)))
	}
}

func TestTheTickLevelsUnevenQueues(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 5}))
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{Limit: 5}})) // five on m1, dealt by hand
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m2"}))    // up levels: 3 and 2
	// a member's queue grows by hand past the other's by more than one
	s := h.snap()
	c := s.Fleet.Cell("m2", sprint.Ready)[0]
	h.poke(sprint.Fleet, ntable.BatchMemberEntry{ID: c.ID, Move: &ntable.MemberMoveOp{Row: "m1", Col: sprint.Ready},
		Set: map[string]string{"member": "m1", "gen": fmt.Sprint(c.Int("gen") + 1)}})
	s = h.snap()
	if a, b := s.Fleet.Count("m1", sprint.Ready), s.Fleet.Count("m2", sprint.Ready); a-b < 2 {
		t.Fatalf("not uneven: %d %d", a, b)
	}
	h.startMachine()
	h.machine()
	s = h.snap()
	if a, b := s.Fleet.Count("m1", sprint.Ready), s.Fleet.Count("m2", sprint.Ready); a-b > 1 || b-a > 1 {
		t.Fatalf("not levelled: %d %d", a, b)
	}
	h.quiet("levelled")
	h.clean("levelled")
}

func TestTheTickFinishesAPendingOperationPastItsGrace(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.startMachine()
	h.m.Fail = func(p string) error {
		if p == "release" {
			return errors.New("lost")
		}
		return nil
	}
	if _, err := h.st.Run(h.ctx, DealStep(sprint.DealReq{Sel: sprint.Sel{Limit: 1}})); !errors.Is(err, ErrUnknown) {
		t.Fatalf("the cut: %v", err)
	}
	h.m.Fail = nil
	if h.m.Pending() == nil {
		t.Fatalf("nothing pending")
	}
	h.tick(2 * time.Minute)
	res := h.machine()
	if len(res.Repaired) != 1 || res.Repaired[0].Done != RepairFinished || h.m.Pending() != nil {
		t.Fatalf("the tick's repair: %+v pending %v", res.Repaired, h.m.Pending())
	}
	h.clean("repaired")
}

func TestTheTickResumesACrossStopWhenTheCardLands(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"a"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"b"}}))
	h.through("a", "b")
	h.must(MergeStep(sprint.MergeReq{Stream: "s1", Cross: "a=b"}))
	if st := h.snap().StreamCtl("s1").F("state"); st != sprint.StreamStopped {
		t.Fatalf("s1 is %s", st)
	}
	h.startMachine()
	h.machine()
	if st := h.snap().StreamCtl("s1").F("state"); st != sprint.StreamStopped {
		t.Fatalf("resumed before b landed: %s", st)
	}
	h.must(MergeStep(sprint.MergeReq{Stream: "s2"}))
	h.machine()
	s := h.snap()
	if st := s.StreamCtl("s1").F("state"); st != sprint.StreamMerging || s.Merge.Placed("a").Col != sprint.Queued {
		t.Fatalf("after b landed: s1 %s, a %s", st, s.Merge.Placed("a").Col)
	}
	if h.written(sprint.NResumed) != 1 || len(h.openOf(sprint.NCross)) != 0 {
		t.Fatalf("resumed notes %d, the cross stop still open %d", h.written(sprint.NResumed), len(h.openOf(sprint.NCross)))
	}
	h.quiet("resumed")
	h.clean("resumed")
}

func TestABrokenInvariantIsOneJudgmentUntilItHolds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	// a stray live work card for a primary that is not working: rule 2
	h.poke(sprint.Fleet, ntable.BatchMemberEntry{ID: "s1-1.w9", Expect: &ntable.MemberExpect{Absent: true},
		Create: &ntable.MemberCreateOp{Row: "m2", Col: sprint.Working, Score: 1}, Set: map[string]string{"kind": "work", "primary": "s1-1", "gen": "1"}})
	h.machine()
	open := h.openOf(sprint.NInvariant)
	if len(open) == 0 || !strings.Contains(open[0].Note.What, "rule") || open[0].Subject() != "s1-1" {
		t.Fatalf("the invariant judgment: %+v", open)
	}
	n := h.written(sprint.NInvariant)
	h.tick(2 * time.Minute)
	h.machine()
	h.machine()
	if h.written(sprint.NInvariant) != n {
		t.Fatalf("written again: %d then %d", n, h.written(sprint.NInvariant))
	}
	h.poke(sprint.Fleet, ntable.BatchMemberEntry{ID: "s1-1.w9", Remove: true})
	h.machine()
	if len(h.openOf(sprint.NInvariant)) != 0 {
		t.Fatalf("still open when the rule holds: %+v", h.openOf(sprint.NInvariant))
	}
}

func TestNoMemberUpIsOneJudgmentUntilAMemberIsUp(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 3}))
	h.startMachine()
	h.machine()
	h.machine()
	if len(h.openOf(sprint.NNoMember)) != 1 || h.written(sprint.NNoMember) != 1 {
		t.Fatalf("no member: open %d written %d", len(h.openOf(sprint.NNoMember)), h.written(sprint.NNoMember))
	}
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.machine()
	if len(h.openOf(sprint.NNoMember)) != 0 || h.state("s1-1") != sprint.Working {
		t.Fatalf("after a member came up: open %d, s1-1 %s", len(h.openOf(sprint.NNoMember)), h.state("s1-1"))
	}
}

func TestDeadlinesCountRunningTimeAndNotifyOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.startMachine()
	h.machine()
	h.run(TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 1}, Who: "m1"}))
	// stopped for three hours: no deadline runs
	h.stopMachine()
	h.tick(3 * time.Hour)
	h.startMachine()
	h.machine()
	if h.written(sprint.NWorkLate) != 0 {
		t.Fatalf("a deadline ran while the machine was stopped")
	}
	h.tick(sprint.DeadlineUnfinished + time.Minute)
	h.machine()
	if h.written(sprint.NWorkLate) != 1 || len(h.openOf(sprint.NWorkLate)) != 1 {
		t.Fatalf("the late work card: written %d open %d", h.written(sprint.NWorkLate), len(h.openOf(sprint.NWorkLate)))
	}
	h.tick(2 * time.Minute)
	h.machine()
	h.quiet("late")
	if h.written(sprint.NWorkLate) != 1 {
		t.Fatalf("written again")
	}
	h.work("m1")
	h.machine()
	if len(h.openOf(sprint.NWorkLate)) != 0 {
		t.Fatalf("still open after the finish")
	}
	// N5: a read card asked long ago and not begun (the stamp as the ask writes it)
	s := h.snap()
	rc := s.Readers.Of("s1-1")[0]
	h.poke(sprint.Readers, ntable.BatchMemberEntry{ID: rc.ID, Set: map[string]string{"asked": h.now.Add(-sprint.DeadlineUnbegun - time.Minute).Format(time.RFC3339)}})
	h.machine()
	if len(h.openOf(sprint.NReadLate)) != 1 {
		t.Fatalf("the late read card: %d", len(h.openOf(sprint.NReadLate)))
	}
	// N6: a merging stream with no merge step
	h.readAll()
	h.run(AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{Stream: "s1"}}))
	h.machine()
	if len(h.openOf(sprint.NReadLate)) != 0 {
		t.Fatalf("the read card's judgment outlived the read")
	}
	h.tick(sprint.DeadlineMergeIdle + time.Minute)
	h.machine()
	if len(h.openOf(sprint.NMergeLate)) != 1 {
		t.Fatalf("the idle stream: %d", len(h.openOf(sprint.NMergeLate)))
	}
	h.must(MergeStep(sprint.MergeReq{Stream: "s1", Batch: 10}))
	h.machine()
	if len(h.openOf(sprint.NMergeLate)) != 0 {
		t.Fatalf("the idle stream's judgment outlived the merge")
	}
}

// stopper is a store that sets the machine STOPPED in the middle of a tick,
// as a stop run beside the tick would.
type stopper struct {
	*Mem
	once sync.Once
	h    *harness
}

func (s *stopper) Apply(ctx context.Context, m ntable.BatchManifest) (ntable.Receipt, error) {
	s.once.Do(func() {
		_ = s.Mem.SetKey(ctx, keyMachine, `{"state":"STOPPED"}`)
	})
	return s.Mem.Apply(ctx, m)
}

func TestStopLetsTheTickInFlightFinish(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(4)
	h.startMachine()
	h.st.B = &stopper{Mem: h.m, h: h}
	res := h.machine()
	if len(res.Moved()) != 4 || h.m.Pending() != nil {
		t.Fatalf("the tick in flight: moved %v, pending %v", res.Moved(), h.m.Pending())
	}
	h.clean("stopped mid-tick")
	h.tick(time.Second)
	if res := h.machine(); res.State != Stopped || len(res.Parts) > 0 {
		t.Fatalf("a tick began after the stop: %+v", res)
	}
}

func TestOutsideActorsWorkWhileStopped(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.startMachine()
	h.machine()
	h.stopMachine()
	h.work("m1")
	h.work("m2")
	h.startMachine()
	h.machine()
	h.stopMachine()
	h.readAll()
	h.landAll("s1")
	s := h.snap()
	if s.Work.Count("s1", sprint.Landed) != 2 {
		t.Fatalf("landed %d while stopped", s.Work.Count("s1", sprint.Landed))
	}
	h.clean("stopped")
}

// sprintOf drives a sprint of n primaries over three streams to landed by
// the tick, the outside actors (workers, readers, the merger) and the
// coordinator's decisions read from the inbox; stopAt stops the machine for
// three rounds from that round (0 never), while the outside actors go on.
func sprintOf(t *testing.T, n, stopAt int) *harness {
	h := newHarness(t)
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m2"}))
	for _, s := range []string{"s1", "s2", "s3"} {
		h.must(AddStep(sprint.AddReq{Stream: s, Count: n / 3}))
	}
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"late"}, Needs: []string{"s1-3", "s3-2"}}))
	h.startMachine()
	failed := map[string]bool{}
	for round := 1; round <= 400; round++ {
		if round == stopAt {
			h.stopMachine()
		}
		if round == stopAt+3 {
			h.startMachine()
		}
		h.machine()
		h.clean(fmt.Sprintf("round %d after the tick", round))
		h.quiet(fmt.Sprintf("round %d", round))
		// workers: every fifth card fails on its first attempt
		for _, m := range []string{"m1", "m2"} {
			h.run(TakeStep(sprint.TakeReq{As: m, Sel: sprint.Sel{Limit: 100}, Who: m}))
			s := h.snap()
			for _, c := range s.Fleet.Cell(m, sprint.Working) {
				fail := c.Int("attempt") == 1 && strings.HasSuffix(c.F("primary"), "5") && !failed[c.ID]
				failed[c.ID] = true
				h.must(FinishStep(sprint.FinishReq{As: m, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}, Failed: fail, Report: "red", Who: m}))
			}
		}
		h.readAll()
		// the coordinator, from the inbox
		v, err := h.st.Inbox(h.ctx, time.Hour, time.Hour, 10000)
		if err != nil {
			t.Fatal(err)
		}
		for _, g := range v.Groups {
			if g.Kind == sprint.Judgment && g.Type == sprint.NWorkFailed {
				h.run(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{Only: groupSubjects(v, g)}, Fix: "the fix"}))
			}
		}
		for _, s := range []string{"s1", "s2", "s3"} {
			h.run(AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{Stream: s}}))
			h.run(MergeStep(sprint.MergeReq{Stream: s, Batch: 5}))
		}
		h.tick(time.Second)
		s := h.snap()
		if s.Work.Count("s1", sprint.Landed)+s.Work.Count("s2", sprint.Landed)+s.Work.Count("s3", sprint.Landed) == n+1 {
			return h
		}
	}
	t.Fatalf("not landed after 400 rounds")
	return nil
}

// groupSubjects is the primaries an inbox group's judgments are open on.
func groupSubjects(v InboxView, g sprint.Group) []string {
	var out []string
	for _, o := range v.Open {
		for _, id := range g.Notes {
			if o.Note.ID == id && !strings.HasPrefix(o.Subject(), "stream:") {
				out = append(out, o.Subject())
			}
		}
	}
	return out
}

func TestSixtyPrimariesLandDrivenOnlyByTheTick(t *testing.T) {
	t.Parallel()
	h := sprintOf(t, 60, 0)
	for _, st := range []string{"s1", "s2", "s3"} {
		if got := h.snap().StreamCtl(st).F("state"); got != sprint.StreamLanded {
			t.Fatalf("stream %s is %s", st, got)
		}
	}
	h.clean("landed")
}

func TestASprintStoppedMidFlightLandsTheSame(t *testing.T) {
	t.Parallel()
	a, b := sprintOf(t, 30, 0), sprintOf(t, 30, 6)
	sa, sb := a.snap(), b.snap()
	for _, st := range []string{"s1", "s2", "s3"} {
		var la, lb []string
		for _, c := range sa.Merge.Cell(st, sprint.Merged) {
			la = append(la, c.ID)
		}
		for _, c := range sb.Merge.Cell(st, sprint.Merged) {
			lb = append(lb, c.ID)
		}
		if strings.Join(la, ",") != strings.Join(lb, ",") {
			t.Fatalf("stream %s merged %v and, stopped mid-flight, %v", st, la, lb)
		}
	}
	m, _, _ := b.st.Machine(b.ctx)
	if m.StoppedFor == 0 {
		t.Fatalf("the stopped sprint was never stopped")
	}
}

// The tick and a coordinator's verb racing on the same cards: exactly one
// wins per card; the other is refused by name or plans again; nothing is lost.
func TestTheTickAndAVerbRaceSafely(t *testing.T) {
	t.Parallel()
	for i := 0; i < 20; i++ {
		h := newHarness(t)
		h.setup(4)
		h.startMachine()
		var wg sync.WaitGroup
		wg.Add(2)
		var dropped Result
		go func() { defer wg.Done(); _, _ = h.st.Tick(h.ctx) }()
		go func() {
			defer wg.Done()
			dropped, _ = h.st.Run(h.ctx, DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-1", "s1-2"}}, Reason: "race"}))
		}()
		wg.Wait()
		h.machine()
		h.clean(fmt.Sprintf("race %d", i))
		s := h.snap()
		for _, id := range []string{"s1-1", "s1-2"} {
			if s.Work.Placed(id) != nil && len(dropped.Refused) == 0 {
				t.Fatalf("race %d: %s is still on the table and the drop was not refused: %+v", i, id, dropped)
			}
			if c := s.Fleet.Card(id + ".w1"); c.Placed() && s.Work.Placed(id) == nil {
				t.Fatalf("race %d: a live work card of a dropped primary", i)
			}
		}
		if s.StateOf("s1-3") != sprint.Working || s.StateOf("s1-4") != sprint.Working {
			t.Fatalf("race %d: s1-3 %s s1-4 %s", i, s.StateOf("s1-3"), s.StateOf("s1-4"))
		}
	}
}
