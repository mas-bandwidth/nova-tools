package sprint

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// H2: the read that completes two different readers' ok writes one judgment,
// ready to accept; rework and drop close it as accept does.
func TestReadyToAcceptIsClosedByReworkAndDrop(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1", "s1-2"}}}))
	for _, id := range []string{"s1-1", "s1-2"} {
		c := w.s.Fleet.Card(w.s.Work.Card(id).F("work"))
		w.must(Take(w.s, TakeReq{As: c.Row, Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
		w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
	}
	w.must(Ask(w.s, AskReq{}))
	for _, id := range []string{"s1-1", "s1-2"} {
		for _, rc := range readsAt(w.s, w.s.Work.Card(id), 1) {
			w.must(Read(w.s, ReadReq{As: rc.F("reader"), Verdict: "ok", Sel: Sel{IDs: []string{rc.ID}}}))
		}
	}
	notes := w.notesOf(NReadyToAccept)
	require.Len(t, notes, 2, "ready to accept: %+v", notes)
	require.Equal(t, Judgment, notes[0].Kind, "ready to accept: %+v", notes)
	require.Len(t, w.openOn("s1-1"), 1, "ready to accept: %+v", notes)
	require.Len(t, w.openOn("s1-2"), 1, "ready to accept: %+v", notes)
	w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "more"}))
	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-2"}}, Reason: "obsolete"}))
	require.Empty(t, w.openOn("s1-1"), "still open: %v", w.s.Open)
	require.Empty(t, w.openOn("s1-2"), "still open: %v", w.s.Open)
	w.clean("closed")
}

// H3: add with a need that names a dropped primary writes the blocked
// judgment itself, in the same step; its decisions are drop and ack.
func TestAddOnADroppedNeedIsBlockedAtOnce(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "obsolete"}))
	p := w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"later"}, Needs: []string{"s1-1"}}))
	var notes []Note
	for _, u := range p.Units {
		notes = append(notes, u.Notes...)
	}
	require.Len(t, notes, 1, "add did not write the blocked judgment: %+v", notes)
	require.Equal(t, NBlocked, notes[0].Type, "add did not write the blocked judgment: %+v", notes)
	require.Len(t, w.openOn("later"), 1, "add did not write the blocked judgment: %+v", notes)
	got := notes[0].Decisions
	require.Equal(t, []string{"drop", "ack"}, got, "decisions: %v", got)
	g := Inbox(InboxReq{Now: w.s.Now, Open: w.s.Open})
	var ds []string
	for _, c := range g[0].Commands {
		ds = append(ds, c.Decision+": "+c.Lines[0])
	}
	require.Len(t, ds, 2, "commands: %v", ds)
	require.Equal(t, "ack: nova-sprint ack "+g[0].ID+" --reason "+noneText, ds[1], "commands: %v", ds)
	w.must(Resolve(w.s, ResolveReq{}))
	require.Len(t, w.notesOf(NBlocked), 1, "blocked written again")
	w.clean("blocked")
}

// H4: the card a merge fact names is a card of the batch, the first n queued
// by score; one outside it is refused, listing the batch, and nothing moves.
func TestAMergeFactNamesACardOfTheBatch(t *testing.T) {
	t.Parallel()
	w := setup(t, 3)
	accepted(w, "s1-1", "s1-2", "s1-3")
	w.must(Add(w.s, AddReq{Stream: "s2", Count: 1}))
	for _, r := range []MergeReq{
		{Stream: "s1", Batch: 1, Conflict: "s1-2"},
		{Stream: "s1", Batch: 1, Cross: "s1-2=s2-1"},
	} {
		p := MergeStep(w.s, r)
		require.Empty(t, p.Units, "%+v: %+v", r, p)
		require.Len(t, p.Refused, 1, "%+v: %+v", r, p)
		require.Equal(t, "s1-2", p.Refused[0].Key, "%+v: %+v", r, p)
		require.Equal(t, "not a card of the batch; the batch of 1 is s1-1", p.Refused[0].Why, "%+v: %+v", r, p)
	}
	require.Equal(t, Queued, w.s.Merge.Placed("s1-1").Col, "a refused fact moved a card")
	require.Equal(t, Queued, w.s.Merge.Placed("s1-2").Col, "a refused fact moved a card")
	require.Equal(t, StreamMerging, w.s.StreamCtl("s1").F("state"), "a refused fact moved a card")
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 2, Conflict: "s1-2"}))
	require.Equal(t, Stuck, w.s.Merge.Placed("s1-2").Col, "a conflict inside the batch")
	require.Equal(t, Queued, w.s.Merge.Placed("s1-1").Col, "a conflict inside the batch")
	w.clean("stuck")
}

// H5: a report on a read card still asked is accepted: the begin and the
// report in one step, begun stamped with it.
func TestAReportOnAnAskedCardBeginsIt(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	c := w.s.Fleet.Card("s1-1.w1")
	w.must(Take(w.s, TakeReq{As: c.Row, Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
	w.must(Ask(w.s, AskReq{}))
	reads := readsAt(w.s, w.s.Work.Card("s1-1"), 1)
	w.tick(time.Minute)
	for i, verdict := range []string{"ok", "broken"} {
		rc := reads[i]
		w.must(Read(w.s, ReadReq{As: rc.F("reader"), Verdict: verdict, Sel: Sel{IDs: []string{rc.ID}}}))
		require.Equal(t, verdict, rc.Col, "%s on an asked card: %s %v", verdict, rc.Col, rc.Fields)
		require.Equal(t, stamp(w.s.Now), rc.F("begun"), "%s on an asked card: %s %v", verdict, rc.Col, rc.Fields)
		require.Equal(t, rc.F("begun"), rc.F("read"), "%s on an asked card: %s %v", verdict, rc.Col, rc.Fields)
	}
	w.clean("read")
}

// H6, as errata 3 amendment 6 amends it: no step that lands or drops the
// last open primary writes "the sprint is done"; the tick's done part does,
// once the sprint has nothing open: one happened note, addressed to the
// coordinator, with the counts, the time from the first start and the hint,
// and no judgment. The inbox shows it first.
func TestTheSprintIsDoneOnce(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	started := w.s.Now
	req := TickReq{Started: started}
	w.must(Add(w.s, AddReq{Stream: "s2", Count: 1}))
	accepted(w, "s1-1", "s1-2")
	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s2-1"}}, Reason: "obsolete"}))
	p, _ := TickDone(w.s, req)
	require.True(t, p.Empty(), "done too early, or the drop not counted: %+v %q", p, w.s.StreamCtl("s2").F("dropped"))
	require.Equal(t, "1", w.s.StreamCtl("s2").F("dropped"), "done too early, or the drop not counted: %+v %q", p, w.s.StreamCtl("s2").F("dropped"))
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 1}))
	p, _ = TickDone(w.s, req)
	require.True(t, p.Empty(), "done with s1-2 merging: %+v", p)
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 1}))
	require.Empty(t, w.notesOf(NSprintDone), "the merge step wrote the sprint done: %+v", w.notesOf(NSprintDone))
	w.tick(90 * time.Minute)
	w.must(tickDone(w.s, req))
	done := w.notesOf(NSprintDone)
	require.Len(t, done, 1, "the sprint is done: %+v", done)
	require.Equal(t, Happened, done[0].Kind, "the sprint is done: %+v", done)
	require.Equal(t, "2 landed, 1 dropped, took 1h30m0s from the first start", done[0].What, "the sprint is done: %+v", done)
	require.Equal(t, "coordinator", done[0].To, "the sprint is done: %+v", done)
	require.Equal(t, DoneHint, done[0].Hint, "the sprint is done: %+v", done)
	require.Empty(t, w.openOn(SprintSubject), "the sprint is done: %+v", done)
	g := Inbox(InboxReq{Now: w.s.Now, Open: w.s.Open, Recent: w.notes})
	require.NotEmpty(t, g, "the inbox does not show the sprint done first: %+v", g)
	require.Equal(t, NSprintDone, g[0].Type, "the inbox does not show the sprint done first: %+v", g)
	require.Equal(t, Happened, g[0].Kind, "the inbox does not show the sprint done first: %+v", g)
	require.Equal(t, "coordinator", g[0].To, "the inbox does not show the sprint done first: %+v", g)
	require.Equal(t, DoneHint, g[0].Hint, "the inbox does not show the sprint done first: %+v", g)
	require.Equal(t, done[0].What, g[0].What, "the inbox does not show the sprint done first: %+v", g)
	require.Empty(t, g[0].Commands, "the inbox does not show the sprint done first: %+v", g)
	// Not known when the machine first started: the counts alone.
	p, _ = TickDone(w.s, TickReq{})
	require.Len(t, p.Notes, 1, "with no first start: %+v", p.Notes)
	require.Equal(t, "2 landed, 1 dropped", p.Notes[0].What, "with no first start: %+v", p.Notes)
	w.must(Add(w.s, AddReq{Stream: "s3", Count: 1}))
	p, _ = TickDone(w.s, req)
	require.True(t, p.Empty(), "more work, and still done: %+v", p)
	w.clean("more work")

	// The last open primary dropped: done, once the tick looks.
	w2 := setup(t, 2)
	accepted(w2, "s1-1")
	w2.must(MergeStep(w2.s, MergeReq{Stream: "s1"}))
	w2.must(Drop(w2.s, DropReq{Sel: Sel{IDs: []string{"s1-2"}}, Reason: "obsolete"}))
	w2.must(tickDone(w2.s, TickReq{}))
	done = w2.notesOf(NSprintDone)
	require.Len(t, done, 1, "done by a drop: %+v", done)
	require.Equal(t, "1 landed, 1 dropped", done[0].What, "done by a drop: %+v", done)
	require.Equal(t, Happened, done[0].Kind, "done by a drop: %+v", done)
	w2.clean("done")
}

// H10: the merge step that lands a card moves every waiting primary whose
// needs have all now landed to ready in the same step, in any stream.
func TestLandingResolvesWhatWaitsOnIt(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"b"}, Needs: []string{"s1-1"}}))
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"c"}, Needs: []string{"s1-1", "s1-2"}}))
	accepted(w, "s1-1", "s1-2")
	p := w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 1}))
	require.Equal(t, Ready, w.state("b"), "after s1-1 landed: b %s, c %s (%+v)", w.state("b"), w.state("c"), p.Units)
	require.Equal(t, Waiting, w.state("c"), "after s1-1 landed: b %s, c %s (%+v)", w.state("b"), w.state("c"), p.Units)
	w.clean("b ready")
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 1}))
	require.Equal(t, Ready, w.state("c"), "after s1-2 landed: c %s", w.state("c"))
	w.clean("c ready")
}

// H8: return opens "returned to review"; accept is a decision only while its
// reads stand at its head; rework closes it.
func TestReturnOpensAJudgment(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	accepted(w, "s1-1")
	w.must(Return(w.s, ReturnReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "suspect"}))
	open := w.openOn("s1-1")
	require.Len(t, open, 1, "returned: %+v", open)
	require.Equal(t, NReturned, open[0].Note.Type, "returned: %+v", open)
	require.Equal(t, "rework,accept,drop", strings.Join(open[0].Note.Decisions, ","), "returned: %+v", open)
	w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "again"}))
	require.Empty(t, w.openOn("s1-1"), "rework left it open")
	w.clean("reworked")
	// Its reads gone (a new head), accept is not offered.
	w2 := setup(t, 1)
	accepted(w2, "s1-1")
	w2.s.Work.Card("s1-1").Fields["head"] = "moved"
	w2.must(Return(w2.s, ReturnReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	d := w2.openOn("s1-1")[0].Note.Decisions
	require.Equal(t, "rework,drop", strings.Join(d, ","), "decisions without standing reads: %v", d)
}

// H9: a primary in review that nothing moves gets a judgment from the step
// that causes it: never asked, after its last judgment is acknowledged; ask
// closes it; an ack of the stranded judgment itself does not write it again.
func TestAStrandedPrimaryIsAJudgment(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	c := w.s.Fleet.Card("s1-1.w1")
	w.must(Take(w.s, TakeReq{As: c.Row, Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
	w.must(RecordCI(w.s, CIReq{Sel: Sel{IDs: []string{"s1-1"}}, Red: true, Run: "r1"}))
	ci := w.openOn("s1-1")[0].Note.ID
	w.must(Ack(w.s, AckReq{Notes: []string{ci}, Reason: "flaky"}))
	o := w.openOn("s1-1")
	require.Len(t, o, 1, "stranded: %+v", o)
	require.Equal(t, NStranded, o[0].Note.Type, "stranded: %+v", o)
	require.Equal(t, "ask,rework,drop", strings.Join(o[0].Note.Decisions, ","), "stranded: %+v", o)
	require.Contains(t, o[0].Note.What, "never asked", "stranded: %+v", o)
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	require.Empty(t, w.openOn("s1-1"), "ask left it open")
	w.clean("asked")
}
