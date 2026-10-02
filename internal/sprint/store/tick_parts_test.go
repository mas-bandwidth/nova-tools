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
	"github.com/stretchr/testify/require"
)

// Each part of the tick: a sequence that would wait for ever without the
// tick, and moves or notifies with it; and the part again, which changes
// nothing and writes nothing the second time.

// openOf is the open judgments of a type, by subject.
func (h *harness) openOf(typ string) []sprint.Open {
	h.t.Helper()
	open, err := h.m.OpenNotes(h.ctx)
	require.NoError(h.t, err)
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
	require.NoError(h.t, err)
	n := 0
	for _, x := range notes {
		if x.Type == typ && x.Kind != sprint.Decided && x.Kind != sprint.Acknowledged {
			n++
		}
	}
	return n
}

// quiet is the ticks after a change, which must move nothing and write
// nothing but the drain of the work table's queue: the changes the last tick's
// steps queued are the next tick's pump's to apply (the owner's tick, errata 3
// amendment 12), so the first tick may move the drain's lines and no other
// part's, and the tick after it moves nothing at all.
func (h *harness) quiet(when string) {
	h.t.Helper()
	res := h.machine()
	for _, p := range res.Parts {
		if p.Name != sprint.PartDrain && (len(p.Moved) > 0 || p.Notes > 0) {
			require.Fail(h.t, fmt.Sprintf("%s: the tick after moved %v and wrote %d notes in %s", when, p.Moved, p.Notes, p.Name))
		}
	}
	res = h.machine()
	require.Empty(h.t, res.Moved(), "%s: a second tick moved %v and wrote %d notes", when, res.Moved(), res.Notes())
	require.LessOrEqual(h.t, res.Notes(), 0, "%s: a second tick moved %v and wrote %d notes", when, res.Moved(), res.Notes())
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
		require.NoError(h.t, err, "poke %s: %v", e.ID, err)
		require.Len(h.t, rs.Members, 1, "poke %s: %v", e.ID, err)
		e.Expect = &ntable.MemberExpect{Revision: fmt.Sprint(rs.Members[0].Revision)}
	}
	_, err := h.m.Apply(h.ctx, ntable.BatchManifest{Schema: 1, Table: table, Epoch: "0",
		ExpectedTableRevision: fmt.Sprint(h.m.Revision(table)), OperationID: fmt.Sprintf("poke-%d", n), Actor: "test",
		Members: []ntable.BatchMemberEntry{e}})
	require.NoError(h.t, err)
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
	n := len(s.Readers.Column(sprint.Asked))
	require.Equal(t, 0, n, "asked before a tick: %d", n)
	h.machine()
	s = h.snap()
	for _, id := range []string{"s1-1", "s1-2"} {
		n := len(s.Readers.Of(id))
		require.Equal(t, 2, n, "%s asked of %d readers", id, n)
	}
	h.quiet("asked")

	// a sprint with one reader up: the tick asks none, and says so once (one judgment, the sprint's)
	h2 := newHarness(t)
	h2.m = NewMem()
	h2.st.B = h2.m
	require.NoError(t, h2.st.Init(h2.ctx))
	require.NoError(t, h2.m.RowsAdd(h2.ctx, "t-readers", []string{"reader-a"}))
	h2.beat()
	h2.beat()
	h2.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h2.must(AddStep(sprint.AddReq{Stream: "s1", Count: 1}))
	h2.startMachine()
	h2.machine()
	h2.work("m1")
	h2.machine()
	require.Len(t, h2.openOf(sprint.NFewReaders), 1, "few readers: open %d written %d", len(h2.openOf(sprint.NFewReaders)), h2.written(sprint.NFewReaders))
	require.Equal(t, 1, h2.written(sprint.NFewReaders), "few readers: open %d written %d", len(h2.openOf(sprint.NFewReaders)), h2.written(sprint.NFewReaders))
	h2.machine()
	h2.tick(2 * time.Minute)
	h2.machine()
	require.Equal(t, 1, h2.written(sprint.NFewReaders), "written again: %d", h2.written(sprint.NFewReaders))
	require.NoError(t, h2.m.RowsAdd(h2.ctx, "t-readers", []string{"reader-b"}))
	h2.beat()
	h2.machine()
	require.Len(t, h2.snap().Readers.Of("s1-1"), 2, "after a reader was added: reads %d, open %d", len(h2.snap().Readers.Of("s1-1")), len(h2.openOf(sprint.NFewReaders)))
	require.Empty(t, h2.openOf(sprint.NFewReaders), "after a reader was added: reads %d, open %d", len(h2.snap().Readers.Of("s1-1")), len(h2.openOf(sprint.NFewReaders)))
}

func TestTheTickLevelsUnevenQueues(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 5}))
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{Limit: 5}})) // five on m1, dealt by hand
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m2"}))  // up levels: 3 and 2
	// a member's queue grows by hand past the other's by more than one
	s := h.snap()
	c := s.Fleet.Cell("m2", sprint.Ready)[0]
	h.poke(sprint.Fleet, ntable.BatchMemberEntry{ID: c.ID, Move: &ntable.MemberMoveOp{Row: "m1", Col: sprint.Ready},
		Set: map[string]string{"member": "m1", "gen": fmt.Sprint(c.Int("gen") + 1)}})
	s = h.snap()
	a, b := s.Fleet.Count("m1", sprint.Ready), s.Fleet.Count("m2", sprint.Ready)
	require.GreaterOrEqual(t, a-b, 2, "not uneven: %d %d", a, b)
	h.startMachine()
	h.machine()
	s = h.snap()
	a, b = s.Fleet.Count("m1", sprint.Ready), s.Fleet.Count("m2", sprint.Ready)
	require.LessOrEqual(t, a-b, 1, "not levelled: %d %d", a, b)
	require.LessOrEqual(t, b-a, 1, "not levelled: %d %d", a, b)
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
	_, err := h.st.Run(h.ctx, DealStep(sprint.DealReq{Sel: sprint.Sel{Limit: 1}}))
	require.ErrorIs(t, err, ErrUnknown, "the cut: %v", err)
	h.m.Fail = nil
	require.NotNil(t, h.m.Pending(), "nothing pending")
	h.tick(2 * time.Minute)
	res := h.machine()
	if len(res.Repaired) != 1 || res.Repaired[0].Done != RepairFinished || h.m.Pending() != nil {
		require.Failf(t, "", "the tick's repair: %+v pending %v", res.Repaired, h.m.Pending())
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
	st := h.snap().StreamCtl("s1").F("state")
	require.Equal(t, string(sprint.StreamStopped), st, "s1 is %s", st)
	h.startMachine()
	h.machine()
	st = h.snap().StreamCtl("s1").F("state")
	require.Equal(t, string(sprint.StreamStopped), st, "resumed before b landed: %s", st)
	h.must(MergeStep(sprint.MergeReq{Stream: "s2"}))
	h.machine()
	s := h.snap()
	if st := s.StreamCtl("s1").F("state"); st != sprint.StreamMerging || s.Merge.Placed("a").Col != sprint.Queued {
		require.Failf(t, "", "after b landed: s1 %s, a %s", st, s.Merge.Placed("a").Col)
	}
	require.Equal(t, 1, h.written(sprint.NResumed), "resumed notes %d, the cross stop still open %d", h.written(sprint.NResumed), len(h.openOf(sprint.NCross)))
	require.Empty(t, h.openOf(sprint.NCross), "resumed notes %d, the cross stop still open %d", h.written(sprint.NResumed), len(h.openOf(sprint.NCross)))
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
	require.NotEmpty(t, open, "the invariant judgment: %+v", open)
	require.Contains(t, open[0].Note.What, "rule", "the invariant judgment: %+v", open)
	require.Equal(t, "s1-1", open[0].Subject(), "the invariant judgment: %+v", open)
	n := h.written(sprint.NInvariant)
	h.tick(2 * time.Minute)
	h.machine()
	h.machine()
	require.Equal(t, n, h.written(sprint.NInvariant), "written again: %d then %d", n, h.written(sprint.NInvariant))
	h.poke(sprint.Fleet, ntable.BatchMemberEntry{ID: "s1-1.w9", Remove: true})
	h.machine()
	require.Empty(t, h.openOf(sprint.NInvariant), "still open when the rule holds: %+v", h.openOf(sprint.NInvariant))
}

func TestNoMemberUpIsOneJudgmentUntilAMemberIsUp(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 3}))
	h.startMachine()
	h.machine()
	h.machine()
	require.Len(t, h.openOf(sprint.NNoMember), 1, "no member: open %d written %d", len(h.openOf(sprint.NNoMember)), h.written(sprint.NNoMember))
	require.Equal(t, 1, h.written(sprint.NNoMember), "no member: open %d written %d", len(h.openOf(sprint.NNoMember)), h.written(sprint.NNoMember))
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.machine()
	require.Empty(t, h.openOf(sprint.NNoMember), "after a member came up: open %d, s1-1 %s", len(h.openOf(sprint.NNoMember)), h.state("s1-1"))
	require.Equal(t, sprint.Working, h.state("s1-1"), "after a member came up: open %d, s1-1 %s", len(h.openOf(sprint.NNoMember)), h.state("s1-1"))
}

func TestDeadlinesCountRunningTimeAndNotifyOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.startMachine()
	h.machine()
	h.run(TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 1}, Who: "m1"}))
	h.run(TakeStep(sprint.TakeReq{As: "m2", Sel: sprint.Sel{Limit: 1}, Who: "m2"}))
	// stopped for three hours: no deadline runs
	h.stopMachine()
	h.tick(3 * time.Hour)
	h.startMachine()
	h.machine()
	require.Equal(t, 0, h.written(sprint.NWorkLate), "a deadline ran while the machine was stopped")
	h.tick(sprint.DeadlineUnfinished + time.Minute)
	h.machine()
	// each member's work card, taken and not finished
	require.Equal(t, 2, h.written(sprint.NWorkLate), "the late work cards: written %d open %d", h.written(sprint.NWorkLate), len(h.openOf(sprint.NWorkLate)))
	require.Len(t, h.openOf(sprint.NWorkLate), 2, "the late work cards: written %d open %d", h.written(sprint.NWorkLate), len(h.openOf(sprint.NWorkLate)))
	h.tick(2 * time.Minute)
	h.machine()
	h.quiet("late")
	require.Equal(t, 2, h.written(sprint.NWorkLate), "written again")
	h.work("m1")
	h.work("m2")
	h.machine()
	require.Empty(t, h.openOf(sprint.NWorkLate), "still open after the finish")
	// N5: read cards asked and not begun past the deadline, by the stamp the
	// ask writes: one judgment per read card
	h.tick(sprint.DeadlineUnbegun + time.Minute)
	h.machine()
	if n := len(h.snap().Readers.Column(sprint.Asked)); n != 4 || len(h.openOf(sprint.NReadLate)) != n {
		require.Failf(t, "", "the late read cards: %d asked, %d open", n, len(h.openOf(sprint.NReadLate)))
	}
	// N6: a merging stream with no merge step
	h.readAll()
	h.run(AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{Stream: "s1"}}))
	h.machine()
	require.Empty(t, h.openOf(sprint.NReadLate), "the read card's judgment outlived the read")
	h.tick(sprint.DeadlineMergeIdle + time.Minute)
	h.machine()
	require.Len(t, h.openOf(sprint.NMergeLate), 1, "the idle stream: %d", len(h.openOf(sprint.NMergeLate)))
	h.must(MergeStep(sprint.MergeReq{Stream: "s1", Batch: 10}))
	h.machine()
	require.Empty(t, h.openOf(sprint.NMergeLate), "the idle stream's judgment outlived the merge")
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
	require.Len(t, res.Moved(), 4, "the tick in flight: moved %v, pending %v", res.Moved(), h.m.Pending())
	require.Nil(t, h.m.Pending(), "the tick in flight: moved %v, pending %v", res.Moved(), h.m.Pending())
	h.clean("stopped mid-tick")
	h.tick(time.Second)
	res = h.machine()
	require.Equal(t, Stopped, res.State, "a tick began after the stop: %+v", res)
	require.Empty(t, res.Parts, "a tick began after the stop: %+v", res)
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
	require.Equal(t, 2, s.Work.Count("s1", sprint.Landed), "landed %d while stopped", s.Work.Count("s1", sprint.Landed))
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
		require.NoError(t, err)
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
	require.FailNow(t, "not landed after 400 rounds")
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
		got := h.snap().StreamCtl(st).F("state")
		require.Equal(t, string(sprint.StreamLanded), got, "stream %s is %s", st, got)
	}
	h.clean("landed")
}

func TestASprintStoppedMidFlightLandsTheSame(t *testing.T) {
	t.Parallel()
	a, b := sprintOf(t, 30, 0), sprintOf(t, 30, 2) // the whole fleet is dealt in the first tick: the stop comes early
	sa, sb := a.snap(), b.snap()
	for _, st := range []string{"s1", "s2", "s3"} {
		var la, lb []string
		for _, c := range sa.Merge.Cell(st, sprint.Merged) {
			la = append(la, c.ID)
		}
		for _, c := range sb.Merge.Cell(st, sprint.Merged) {
			lb = append(lb, c.ID)
		}
		require.Equal(t, strings.Join(lb, ","), strings.Join(la, ","), "stream %s merged %v and, stopped mid-flight, %v", st, la, lb)
	}
	m, _, _ := b.st.Machine(b.ctx)
	require.NotEqual(t, time.Duration(0), m.StoppedFor, "the stopped sprint was never stopped")
}

// The tick and a coordinator's verb racing on the same cards: exactly one
// wins per card; the other is refused by name or plans again; nothing is lost.
func TestTheTickAndAVerbRaceSafely(t *testing.T) {
	t.Parallel()
	for i := 0; i < 20; i++ {
		h := newHarness(t)
		h.setup(4)
		h.startMachine()
		coord := &Store{B: h.m, Names: h.st.Names, Actor: "coordinator", Now: h.st.Now, NewID: h.st.NewID, Sleep: func(time.Duration) {}}
		var wg sync.WaitGroup
		wg.Add(2)
		var dropped Result
		go func() { defer wg.Done(); _, _ = h.st.Tick(h.ctx) }()
		go func() {
			defer wg.Done()
			dropped, _ = coord.Run(h.ctx, DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-1", "s1-2"}}, Reason: "race"}))
		}()
		wg.Wait()
		h.machine()
		h.clean(fmt.Sprintf("race %d", i))
		s := h.snap()
		for _, id := range []string{"s1-1", "s1-2"} {
			require.False(t, s.Work.Placed(id) != nil && len(dropped.Refused) == 0, "race %d: %s is still on the table and the drop was not refused: %+v", i, id, dropped)
			c := s.Fleet.Card(id + ".w1")
			require.False(t, c.Placed() && s.Work.Placed(id) == nil, "race %d: a live work card of a dropped primary", i)
		}
		require.Equal(t, sprint.Working, s.StateOf("s1-3"), "race %d: s1-3 %s s1-4 %s", i, s.StateOf("s1-3"), s.StateOf("s1-4"))
		require.Equal(t, sprint.Working, s.StateOf("s1-4"), "race %d: s1-3 %s s1-4 %s", i, s.StateOf("s1-3"), s.StateOf("s1-4"))
	}
}
