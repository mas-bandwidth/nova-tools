package store

// Cold reader's probes of the machine (rowan/sprint-tick at de92cc545).

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// crTicks runs n ticks, a second apart, each followed by the check.
func (h *harness) crTicks(n int, when string) {
	h.t.Helper()
	for i := 0; i < n; i++ {
		h.machine()
		h.clean(fmt.Sprintf("%s tick %d", when, i))
		h.tick(time.Second)
	}
}

// PROBE A1: more than TickMaxMoves primaries become ready at one landing. The
// resolve part is bounded to 200 units, and it runs only when Scan is set
// (landed or all changed). The remainder must still leave waiting.
func TestCRResolveBoundLeavesTheRestWaitingForever(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"root"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", Count: 250, Needs: []string{"root"}}))
	h.through("root")
	h.must(MergeStep(sprint.MergeReq{Stream: "s1"}))
	require.Equal(t, sprint.Landed, h.state("root"), "root %s", h.state("root"))
	h.startMachine()
	h.crTicks(5, "after the landing")
	// and past a full read
	h.tick(2 * sprint.DeadlineMergeIdle)
	h.crTicks(5, "after a minute")
	s := h.snap()
	waiting := s.Work.Column(sprint.Waiting)
	if len(waiting) > 0 {
		var open []string
		for _, o := range h.openOf(sprint.NBlocked) {
			open = append(open, o.Subject())
		}
		require.Fail(t, fmt.Sprintf("STALL: %d primaries still waiting with their only need landed (e.g. %s), no judgment names them (blocked open: %d)",
			len(waiting), waiting[0].ID, len(open)))
	}
}

// PROBE A1b: the resolve part loses every plan to other writers ("the sprint
// kept changing under this step"): the tick returns nil error and records the
// landing as seen, so the next tick does not scan.
type loseResolve struct {
	*Mem
	left int
}

func (l *loseResolve) Acquire(ctx context.Context, gen uint64, op OpRecord) (bool, error) {
	if op.Verb == "tick resolve" && l.left > 0 {
		l.left--
		return false, nil
	}
	return l.Mem.Acquire(ctx, gen, op)
}

func TestCRResolveThatLosesToOtherWritersIsNeverRetried(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"a"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"b"}, Needs: []string{"a"}}))
	h.through("a")
	h.startMachine()
	h.machine()
	h.must(MergeStep(sprint.MergeReq{Stream: "s1"}))
	h.st.B = &loseResolve{Mem: h.m, left: FenceTries}
	res, err := h.st.Tick(h.ctx)
	require.NoError(t, err, "tick: %v", err)
	t.Logf("contended tick: parts %+v", res.Parts)
	h.st.B = h.m
	h.crTicks(5, "after the contention")
	h.tick(2 * time.Minute)
	h.crTicks(3, "after a full read")
	require.NotEqual(t, sprint.Waiting, h.state("b"), "STALL: b is waiting with its need a landed; no judgment; the tick recorded the landing as seen")
}

// PROBE D1: the deadlines read stamps "dealt" (work card in ready), "asked"
// and "begun" (read cards). Nothing writes them.
func TestCRDeadlinesThatNeverFire(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.startMachine()
	h.machine() // dealt: s1-1 to m1, s1-2 to m2
	h.tick(sprint.DealtMaxDefault + time.Hour)
	h.crTicks(2, "untaken")
	assert.NotEqual(t, 0, h.written(sprint.NWorkLate), "MISSING: a work card dealt 7h ago and never taken raised nothing")
	// readers: finish both, the tick asks, nobody begins
	h.work("m1")
	h.work("m2")
	h.machine()
	h.tick(3 * time.Hour)
	h.crTicks(2, "unbegun")
	assert.NotEqual(t, 0, h.written(sprint.NReadLate), "MISSING: read cards asked 3h ago and never begun raised nothing")
	// begin and never report
	for _, r := range []string{"reader-a", "reader-b", "reader-c"} {
		h.run(ReadStep(sprint.ReadReq{As: r, Begin: true, Sel: sprint.Sel{Limit: 100}, Who: r}))
	}
	h.tick(5 * time.Hour)
	h.crTicks(2, "unreported")
	assert.NotEqual(t, 0, h.written(sprint.NReadLate), "MISSING: read cards begun 5h ago and never reported raised nothing")
	s := h.snap()
	for _, c := range s.Readers.Column(sprint.Reading) {
		t.Logf("read card %s fields: %v", c.ID, c.Fields)
	}
	for _, c := range s.Fleet.Column(sprint.Done) {
		t.Logf("work card %s fields: %v", c.ID, c.Fields)
	}
}

// PROBE A2: a primary with two ok reads is accepted by the machine ("accept is
// mechanical, but the merge step is not": the pump moves it to merging), and
// then waits on the coordinator's merge with only a happened note; the stream's
// merge deadline is the judgment that names it.
func TestCRReadyToAcceptIsNeverAJudgment(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	h.work("m1")
	h.machine()
	h.readAll()
	h.crTicks(2, "accepted") // the pump applies the reads, then accepts
	got := h.state("s1-1")
	require.Equal(t, sprint.Merging, got, "two oks: s1-1 is %s, not accepted to merging", got)
	h.tick(48 * time.Hour)
	h.crTicks(3, "two oks")
	open, _ := h.m.OpenNotes(h.ctx)
	for _, o := range open {
		if o.Subject() == "s1-1" || o.Subject() == "stream:s1" {
			return
		}
	}
	assert.Fail(t, fmt.Sprintf("A (by the letter): s1-1 accepted and unmerged for 48h: no open judgment names it or its stream; open=%d", len(open)))
}

// PROBE A3: the coordinator acks a work-failed judgment: nothing asks, nothing
// re-raises.
func TestCRAckOfWorkFailedLeavesAPrimaryWithNoJudgment(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	h.run(TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 1}, Who: "m1"}))
	c := h.snap().Fleet.Cell("m1", sprint.Working)[0]
	h.must(FinishStep(sprint.FinishReq{As: "m1", Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}, Failed: true}))
	h.machine()
	o := h.openOf(sprint.NWorkFailed)
	require.Len(t, o, 1, "failed: %d", len(o))
	r := h.run(AckStep(sprint.AckReq{Notes: []string{o[0].Note.ID}, Reason: "looked"}))
	if len(r.Refused) > 0 {
		t.Skipf("ack refused: %v", r.Refused)
	}
	h.tick(24 * time.Hour)
	h.crTicks(3, "after ack")
	open, _ := h.m.OpenNotes(h.ctx)
	if len(open) == 0 && h.state("s1-1") == sprint.Review {
		assert.Fail(t, "A: s1-1 in review (failed), judgment acked, no open judgment, no actor; the tick never re-raises")
	}
}

// PROBE D2: an ack of a tick judgment whose condition still holds is refused:
// wait is what answers it.
func TestCRAckedTickJudgmentComesBack(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 1}))
	h.startMachine()
	h.machine()
	o := h.openOf(sprint.NNoMember)
	require.Len(t, o, 1, "no member: %d", len(o))
	res := h.run(AckStep(sprint.AckReq{Notes: []string{o[0].Note.ID}, Reason: "the fleet is off tonight"}))
	require.Len(t, res.Refused, 1, "ack of no member up: %+v", res)
	for i := 0; i < 5; i++ {
		h.tick(time.Minute + time.Second)
		h.machine()
	}
	if n := h.written(sprint.NNoMember); n != 1 || len(h.openOf(sprint.NNoMember)) != 1 {
		require.Fail(t, fmt.Sprintf("written %d, open %d", n, len(h.openOf(sprint.NNoMember))))
	}
}

// PROBE 3: every member down with cards in ready and working; a member up.
func TestCRAllMembersDownThenOneUp(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(6)
	h.startMachine()
	h.machine()
	h.run(TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 1}, Who: "m1"}))
	h.must(FleetStep(sprint.FleetReq{Op: "hold", Member: "m1"}))
	h.must(FleetStep(sprint.FleetReq{Op: "hold", Member: "m2"}))
	h.crTicks(3, "all down")
	s := h.snap()
	n := len(s.Work.Column(sprint.Working))
	require.Equal(t, 0, n, "working with nobody up: %d", n)
	require.Len(t, h.openOf(sprint.NNoMember), 1, "no-member judgment: %d", len(h.openOf(sprint.NNoMember)))
	up := h.must(FleetStep(sprint.FleetReq{Op: "release", Member: "m2"}))
	h.crTicks(2, "one up")
	s = h.snap()
	if n := s.Fleet.Count("m2", sprint.Ready); n != 6 {
		require.Fail(t, fmt.Sprintf("m2 ready %d; the up: %+v; m2 %v; ready %d", n, up.Moved, s.MemberCtl("m2").Fields, len(s.Work.Column(sprint.Ready))))
	}
	require.Empty(t, h.openOf(sprint.NNoMember), "no-member still open")
	for i := 0; i < 10; i++ {
		h.work("m2")
		h.crTicks(1, "draining")
	}
	s = h.snap()
	n = len(s.Work.Column(sprint.Ready)) + len(s.Work.Column(sprint.Working))
	require.Equal(t, 0, n, "left ready/working %d", n)
}

// PROBE 5: 1,000 ready, 3 members; workers take everything each round.
func TestCRThousandReadyThreeMembers(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for _, m := range []string{"m1", "m2", "m3"} {
		h.must(FleetStep(sprint.FleetReq{Op: "up", Member: m}))
	}
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: crScale.Ready}))
	h.startMachine()
	var dealtOrder []string
	ticks := 0
	for len(h.snap().Work.Column(sprint.Ready)) > 0 && ticks < 2000 {
		res := h.machine()
		ticks++
		for _, p := range res.Parts {
			if p.Name == "deal" && len(p.Moved) > 3*sprint.DefaultWidth {
				require.Fail(t, fmt.Sprintf("tick %d dealt %d", ticks, len(p.Moved)))
			}
			for _, m := range p.Moved {
				if p.Name == "deal" {
					dealtOrder = append(dealtOrder, strings.Fields(m)[0])
				}
			}
		}
		s := h.snap()
		for _, m := range []string{"m1", "m2", "m3"} {
			n := heldBy(s, m)
			require.LessOrEqual(t, n, s.Width(m), "tick %d: %s holds %d, its width %d", ticks, m, n, s.Width(m))
		}
		for _, m := range []string{"m1", "m2", "m3"} {
			h.work(m)
		}
		h.tick(time.Second)
	}
	s := h.snap()
	for i := 1; i < len(dealtOrder); i++ {
		a, b := s.Work.Card(dealtOrder[i-1]), s.Work.Card(dealtOrder[i])
		if a.Score > b.Score {
			require.Fail(t, fmt.Sprintf("order: %s (%v) dealt before %s (%v)", a.ID, a.Score, b.ID, b.Score))
		}
	}
	h.clean("thousand")
}

// PROBE 4: fewer than two readers, then one added: asked without a verb.
func TestCROneReaderThenTwo(t *testing.T) {
	t.Parallel()
	h := &harness{t: t, m: NewMem(), ctx: context.Background(), now: t0, live: []string{"m1"}}
	n := 0
	h.st = &Store{B: h.m, Names: sprint.Names{Prefix: "t-"}, Actor: "tester",
		Now: func() time.Time { h.mu.Lock(); defer h.mu.Unlock(); return h.now }, NewID: func() string { n++; return fmt.Sprint(n) }, Sleep: func(time.Duration) {}}
	require.NoError(t, h.st.Init(h.ctx))
	h.beat()
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(AddStep(sprint.AddReq{Brief: proBrief, Stream: "s1", Count: 3}))
	h.startMachine()
	h.machine()
	h.work("m1")
	h.crTicks(3, "no readers")
	got := len(h.openOf(sprint.NFewReaders))
	require.Equal(t, 1, got, "fewer than two readers up: open %d, want 1 (one judgment for the three dealt in one tick, at m1's width)", got)
	w := h.written(sprint.NFewReaders)
	require.NoError(t, h.m.RowsAdd(h.ctx, "t-readers", []string{"reader-a"}))
	h.beat()
	h.crTicks(3, "one reader")
	t.Logf("few-readers written %d then %d after the first reader (the why text changes 0 free -> 1 free)", w, h.written(sprint.NFewReaders))
	require.NoError(t, h.m.RowsAdd(h.ctx, "t-readers", []string{"reader-b"}))
	h.beat()
	h.crTicks(2, "two readers")
	got = len(h.openOf(sprint.NFewReaders))
	require.Equal(t, 0, got, "still open %d", got)
	s := h.snap()
	for _, c := range s.Work.Column(sprint.Review) {
		require.Len(t, s.Readers.Of(c.ID), 2, "%s asked of %d", c.ID, len(s.Readers.Of(c.ID)))
	}
}

// PROBE 9: the idle cost.

var _ = ntable.BatchMemberEntry{}
