package store

// The TLA+ model's findings (tla/SprintTables.tla, the owner's rule), each as
// the model's trace run through the store.

import (
	"fmt"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/require"
)

// openOn is the open judgments on a primary.
func (h *harness) openOn(id string) []sprint.Open {
	h.t.Helper()
	open, err := h.m.OpenNotes(h.ctx)
	require.NoError(h.t, err)
	var out []sprint.Open
	for _, o := range open {
		if o.Subject() == id && o.Note.Kind == sprint.Judgment {
			out = append(out, o)
		}
	}
	return out
}

// toReview deals, takes and finishes s1-1 ok, then asks it.
func (h *harness) toReview() {
	h.t.Helper()
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	c := h.snap().Fleet.Card("s1-1.w1")
	h.must(TakeStep(sprint.TakeReq{As: c.Row, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: 1}}))
	h.must(FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: 1}}))
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
}

// M1: deal, take, finish ok, ask, its CI red at its head (acknowledged: the
// pump holds it), both reads ok (ready to accept opens), ack: refused (ack
// answers only a judgment that lists it), naming the commands that move the
// card; the judgment stays open.
func TestModelAckCannotSilenceACard(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.toReview()
	h.heldByRedCI("s1-1")
	h.readAllOK("s1-1")
	open := h.openOn("s1-1")
	require.Len(t, open, 1, "after two ok reads: %+v", open)
	require.Equal(t, string(sprint.NReadyToAccept), open[0].Note.Type, "after two ok reads: %+v", open)
	res := h.run(AckStep(sprint.AckReq{Notes: []string{open[0].Note.ID}, Reason: "looked"}))
	require.Empty(t, res.Moved, "the ack that silences s1-1: %+v", res)
	require.Len(t, res.Refused, 1, "the ack that silences s1-1: %+v", res)
	require.Contains(t, res.Refused[0].Why, "ack does not answer", "the ack that silences s1-1: %+v", res)
	require.Contains(t, res.Refused[0].Why, "nova-sprint accept --group "+open[0].Note.ID, "the ack that silences s1-1: %+v", res)
	again := h.openOn("s1-1")
	require.Len(t, again, 1, "after the refused ack: %+v", again)
	require.Equal(t, open[0].Note.ID, again[0].Note.ID, "after the refused ack: %+v", again)
	// Returned to review: ack does not answer it either.
	h.must(AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	h.must(ReturnStep(sprint.ReturnReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Reason: "hold it"}))
	ret := h.openOn("s1-1")
	require.Len(t, ret, 1, "returned: %+v", ret)
	require.Equal(t, string(sprint.NReturned), ret[0].Note.Type, "returned: %+v", ret)
	res = h.run(AckStep(sprint.AckReq{Notes: []string{ret[0].Note.ID}, Reason: "looked"}))
	require.Len(t, res.Refused, 1, "ack of returned: %+v", res)
	require.Empty(t, res.Moved, "ack of returned: %+v", res)
	held := h.openOn("s1-1")
	require.Len(t, held, 1, "returned, after the refused ack: %+v", held)
	require.Equal(t, sprint.NReturned, held[0].Note.Type, "returned, after the refused ack: %+v", held)
	h.clean("M1")
}

// A dropped need is detached by resolve. The card is ready and no blocked judgment opens.
func TestModelAckOfBlockedMovesTheCard(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"late"}, Needs: []string{"s1-1"}}))
	seedDroppedNeed(h, "s1-1")
	h.must(ResolveStep(sprint.ResolveReq{}))
	require.Empty(t, h.openOf(sprint.NBlocked), "a dropped need opened a judgment")
	require.Equal(t, sprint.Ready, h.state("late"), "late is %s after the detach", h.state("late"))
	h.clean("M1 blocked")
}

// The second audit's gap, inverted (TestAudit2GapAckedSentinelStopsItsStreamForEver):
// an ack of "sentinel reached" is refused, naming release; the sentinel's
// judgment stays open, and release moves what waits behind it.
func TestAckOfAReachedSentinelIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	require.NoError(t, h.m.SetCoordinator(h.ctx, "tester"))
	h.setup(1)
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"stop"}, Sentinel: true}))
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"after"}}))
	h.startMachine()
	h.machine()
	h.work("m1")
	h.work("m2")
	h.machine()
	h.readAll()
	h.landAll("s1")
	reached := h.openOf(sprint.NSentinelReached)
	require.Equal(t, sprint.Landed, h.state("s1-1"), "s1-1 %s, reached %d", h.state("s1-1"), len(reached))
	require.Len(t, reached, 1, "s1-1 %s, reached %d", h.state("s1-1"), len(reached))
	res := h.run(AckStep(sprint.AckReq{Notes: []string{reached[0].Note.ID}, Reason: "looked"}))
	require.Empty(t, res.Moved, "ack of sentinel reached: %+v", res)
	require.Len(t, res.Refused, 1, "ack of sentinel reached: %+v", res)
	require.Contains(t, res.Refused[0].Why, "nova-sprint release stop", "ack of sentinel reached: %+v", res)
	for i := 0; i < 5; i++ {
		h.tick(time.Hour)
		h.machine()
	}
	if len(h.openOf(sprint.NSentinelReached)) != 1 || h.state("stop") != sprint.Waiting || h.state("after") != sprint.Waiting {
		require.Failf(t, "", "after the refused ack: reached %d, stop %s, after %s", len(h.openOf(sprint.NSentinelReached)), h.state("stop"), h.state("after"))
	}
	h.must(ReleaseStep(sprint.ReleaseReq{IDs: []string{"stop"}, Reason: "green", Coordinator: "tester", Who: "tester", Answers: []string{reached[0].Note.ID}}))
	h.machine()
	require.Equal(t, sprint.Landed, h.state("stop"), "after the release: stop %s after %s", h.state("stop"), h.state("after"))
	require.Equal(t, sprint.Working, h.state("after"), "after the release: stop %s after %s", h.state("stop"), h.state("after"))
	h.clean("released")
}

// M2: the no-member judgment is kept true by the tick in every epoch: both
// members down, clear, start, add a card, tick: the judgment is open.
func TestModelNoMemberJudgmentAfterAClear(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	h.must(FleetStep(sprint.FleetReq{Op: "hold", Member: "m1"}))
	h.must(FleetStep(sprint.FleetReq{Op: "hold", Member: "m2"}))
	h.machine()
	require.Len(t, h.openOf(sprint.NNoMember), 1, "no member before the clear: %d", len(h.openOf(sprint.NNoMember)))
	_, err := h.st.Clear(h.ctx)
	require.NoError(t, err)
	h.startMachine()
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"p3"}}))
	h.machine()
	pinned, err := h.st.Pinned(h.ctx)
	require.NoError(t, err)
	all, err := pinned.B.OpenNotes(h.ctx)
	require.NoError(t, err)
	var open []sprint.Open
	for _, o := range all {
		if o.Note.Type == sprint.NNoMember && o.Note.Kind == sprint.Judgment {
			open = append(open, o)
		}
	}
	if pinned.PinnedEpoch() != 1 || len(open) != 1 || sprint.IDEpoch(open[0].Note.ID) != 1 || h.state("p3") != sprint.Ready {
		require.Failf(t, "", "after the clear: open %+v, p3 %s", open, h.state("p3"))
	}
	h.clean("M2")
}

// M3: ask --another before the first ask of the primary is refused, naming
// ask (or the tick) as what asks first.
func TestModelAskAnotherBeforeTheFirstAsk(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	c := h.snap().Fleet.Card("s1-1.w1")
	h.must(TakeStep(sprint.TakeReq{As: c.Row, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: 1}}))
	h.must(FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: 1}}))
	res := h.run(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Another: true}))
	require.Empty(t, res.Moved, "ask --another before the first ask: %+v", res)
	require.Len(t, res.Refused, 1, "ask --another before the first ask: %+v", res)
	require.Contains(t, res.Refused[0].Why, "nova-sprint ask s1-1", "ask --another before the first ask: %+v", res)
	require.Contains(t, res.Refused[0].Why, "tick", "ask --another before the first ask: %+v", res)
	n := len(h.snap().Readers.Of("s1-1"))
	require.Equal(t, 0, n, "asked of %d readers", n)
}

// orphanInMerging is s1-1 merging with no merge card (a repair skipped the
// merge table's create while the work move applied) and the repair's skip
// judgment open on it.
func orphanInMerging(t *testing.T) (*harness, string) {
	h := newHarness(t)
	h.setup(1)
	h.through("s1-1")
	s := h.snap()
	m := s.Merge.Card("s1-1")
	_, err := h.m.Apply(h.ctx, ntable.BatchManifest{Schema: 1, Table: "t-merge", Epoch: "0", ExpectedTableRevision: fmt.Sprint(s.Merge.Revision),
		OperationID: "outside-remove", Members: []ntable.BatchMemberEntry{{ID: m.ID, Expect: &ntable.MemberExpect{Revision: fmt.Sprint(m.Rev)}, Remove: true}}})
	require.NoError(t, err)
	// as the repair that skipped the create leaves the log: no merge card
	h.m.appendLine(sprint.Line{Kind: sprint.LineMove, At: s.Now, Card: m.ID, Table: sprint.Merge, From: m.Row + ":" + m.Col, Removed: true, Verb: "repair"})
	h.must(Step{Verb: "repair", Plan: func(s *sprint.Snapshot) sprint.Plan {
		n := sprint.Note{Kind: sprint.Judgment, Type: sprint.NRepairSkipped, Stream: "s1", Primaries: []string{"s1-1"}, Count: 1, At: s.Now,
			What: "the merge card of s1-1 was skipped", Decisions: append([]string(nil), sprint.Decisions[sprint.NRepairSkipped]...)}
		return sprint.Plan{Notes: []sprint.Note{n}}
	}})
	open := h.openOf(sprint.NRepairSkipped)
	if len(open) != 1 || h.state("s1-1") != sprint.Merging || h.snap().Merge.Card("s1-1").Placed() {
		require.Failf(t, "", "the orphan: open %v, s1-1 %s", open, h.state("s1-1"))
	}
	return h, open[0].Note.ID
}

// Model read 2: ack is judged by the one no-stall rule on the state after
// it: the skip judgment is what holds an orphan in merging, so its ack is
// refused; return takes the orphan back to review; rework is refused with
// the card still judged; drop ends it.
func TestAnOrphanInMergingIsNeverSilent(t *testing.T) {
	t.Parallel()
	h, id := orphanInMerging(t)
	res := h.run(AckStep(sprint.AckReq{Notes: []string{id}, Reason: "looked"}))
	require.Len(t, res.Refused, 1, "the ack that silences the orphan: %+v", res)
	require.Len(t, h.openOf(sprint.NRepairSkipped), 1, "the ack that silences the orphan: %+v", res)
	res = h.run(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Fix: "f", Answers: []string{id}}))
	require.NotEmpty(t, res.Refused, "rework of the orphan: %+v", res)
	require.Len(t, h.openOf(sprint.NRepairSkipped), 1, "rework of the orphan: %+v", res)
	res = h.run(ReturnStep(sprint.ReturnReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Reason: "no merge card", Answers: []string{id}}))
	require.Empty(t, res.Refused, "return of the orphan: %+v, s1-1 %s", res, h.state("s1-1"))
	require.Equal(t, sprint.Review, h.state("s1-1"), "return of the orphan: %+v, s1-1 %s", res, h.state("s1-1"))
	h.clean("returned")
	h2, id2 := orphanInMerging(t)
	res = h2.run(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Reason: "gone", Answers: []string{id2}}))
	require.Empty(t, res.Refused, "drop of the orphan: %+v", res)
	h2.clean("dropped")
}

// Model read 3: resume of a stopped stream whose every card has landed or
// been dropped settles it: landed, as every other step settles a stream.
func TestResumeSettlesAStreamWhoseCardsAllEnded(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.through("s1-1")
	h.must(MergeStep(sprint.MergeReq{Stream: "s1", Batch: 1}))
	h.through("s1-2")
	h.must(MergeStep(sprint.MergeReq{Stream: "s1", Batch: 1, Conflict: "s1-2"}))
	h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-2"}}, Reason: "gone"}))
	st := h.snap().StreamCtl("s1").F("state")
	require.Equal(t, string(sprint.StreamStopped), st, "s1 is %s after the drop", st)
	h.must(ResumeStep(sprint.ResumeReq{Stream: "s1", Did: "dropped the conflicting card"}))
	st = h.snap().StreamCtl("s1").F("state")
	require.Equal(t, string(sprint.StreamLanded), st, "s1 is %s after the resume", st)
	h.clean("resumed")
}

// All or nothing (reader finding 2): an ack naming several judgments, one of
// which it may not answer, closes none of them and names every one; the
// card the answerable one holds stays where it is.
func TestAnAckOfSeveralIsAllOrNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"late"}, Needs: []string{"s1-1"}}))
	// late still needs the live card, so an ack of one real judgment beside a
	// missing id is all or none and moves nothing
	h.must(Step{Verb: "inject", Plan: func(s *sprint.Snapshot) sprint.Plan {
		n := sprint.Note{Kind: sprint.Judgment, Type: sprint.NRepairSkipped, Stream: "s1", Primaries: []string{"late"},
			Count: 1, At: s.Now, What: "injected", Decisions: append([]string(nil), sprint.Decisions[sprint.NRepairSkipped]...)}
		return sprint.Plan{Notes: []sprint.Note{n}}
	}})
	open := h.openOf(sprint.NRepairSkipped)
	require.Len(t, open, 1, "injected: %+v", open)
	r := h.run(AckStep(sprint.AckReq{Notes: []string{open[0].Note.ID, "no-such-note"}, Reason: "not needed"}))
	require.Len(t, r.Refused, 2, "a partial ack: %+v", r)
	require.Empty(t, r.Moved, "a partial ack: %+v", r)
	require.Contains(t, fmt.Sprint(r.Refused), "all or none", "a partial ack: %+v", r)
	require.Len(t, h.openOf(sprint.NRepairSkipped), 1, "the refused ack closed the judgment or moved the card: %s %+v", h.state("late"), h.openOf(sprint.NRepairSkipped))
	require.Equal(t, sprint.Waiting, h.state("late"), "the refused ack closed the judgment or moved the card: %s %+v", h.state("late"), h.openOn("late"))
	h.clean("all or nothing")
}

// Stalled is answered by wait, never by ack (reader finding 5): a stalled
// judgment's decisions do not list ack, and an ack of it is refused as a
// condition the tick keeps, whatever else would refuse it.
func TestAStalledJudgmentIsNeverAckable(t *testing.T) {
	t.Parallel()
	h, _ := orphanInMerging(t)
	skip := h.openOf(sprint.NRepairSkipped)
	h.must(Step{Verb: "persisted", Plan: func(*sprint.Snapshot) sprint.Plan { return sprint.Plan{Closes: skip} }})
	for _, f := range sprint.Unheld(sprint.HeldState{Snap: h.snap(), Running: true}, h.now) {
		require.False(t, contains(f.Decisions, "ack"), "a stall's decisions: %v", f.Decisions)
		require.True(t, contains(f.Decisions, "wait"), "a stall's decisions: %v", f.Decisions)
	}
	h.startMachine()
	h.machine()
	stalled := h.openOf(sprint.NStalled)
	require.Len(t, stalled, 1, "the stalled judgment: %+v", stalled)
	require.False(t, contains(stalled[0].Note.Decisions, "ack"), "the stalled judgment: %+v", stalled)
	res := h.run(AckStep(sprint.AckReq{Notes: []string{stalled[0].Note.ID}, Reason: "looked"}))
	require.Len(t, res.Refused, 1, "an ack of stalled: %+v", res)
	require.Contains(t, res.Refused[0].Why, "a condition the tick keeps; wait sets when it is shown again", "an ack of stalled: %+v", res)
}

// Two dropped needs of one sentinel detach in one resolve. It is reached once,
// and no blocked judgment opens.
func TestTwoBlockedJudgmentsOnOneSentinelWaiveOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(0)
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"a", "b"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"stop"}, Sentinel: true, Needs: []string{"a", "b"}}))
	seedDroppedNeed(h, "a")
	seedDroppedNeed(h, "b")
	h.must(ResolveStep(sprint.ResolveReq{}))
	c := h.snap().Work.Card("stop")
	require.Equal(t, sprint.Waiting, c.Col, "stop: %+v", c)
	require.Empty(t, c.F("needs"), "stop: %+v", c)
	require.NotEmpty(t, c.F("reached"), "stop: %+v", c)
	require.Empty(t, h.openOf(sprint.NBlocked))
	notes, _, err := h.m.NotesSince(h.ctx, "", 100000)
	require.NoError(t, err)
	reached := 0
	for _, n := range notes {
		if n.Type == sprint.NSentinelReached {
			reached++
		}
	}
	require.Equal(t, 1, reached, "%d sentinel-reached notes", reached)
	h.clean("reached once")
}

// appendLine writes a line to the log as a writer outside the engine would.
func (m *Mem) appendLine(l sprint.Line) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	lg := m.log()
	lg.lines = append(lg.lines, memLine{fmt.Sprintf("%d-0", m.seq), l})
}
