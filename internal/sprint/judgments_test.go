package sprint

import (
	"strings"
	"testing"
	"time"
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
	if len(notes) != 2 || notes[0].Kind != Judgment || len(w.openOn("s1-1")) != 1 || len(w.openOn("s1-2")) != 1 {
		t.Fatalf("ready to accept: %+v", notes)
	}
	w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "more"}))
	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-2"}}, Reason: "obsolete"}))
	if len(w.openOn("s1-1"))+len(w.openOn("s1-2")) != 0 {
		t.Fatalf("still open: %v", w.s.Open)
	}
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
	if len(notes) != 1 || notes[0].Type != NBlocked || len(w.openOn("later")) != 1 {
		t.Fatalf("add did not write the blocked judgment: %+v", notes)
	}
	if got := notes[0].Decisions; len(got) != 2 || got[0] != "drop" || got[1] != "ack" {
		t.Fatalf("decisions: %v", got)
	}
	g := Inbox(InboxReq{Now: w.s.Now, Open: w.s.Open})
	var ds []string
	for _, c := range g[0].Commands {
		ds = append(ds, c.Decision+": "+c.Lines[0])
	}
	if len(ds) != 2 || ds[1] != "ack: nova-sprint ack "+g[0].ID+" --reason "+noneText {
		t.Fatalf("commands: %v", ds)
	}
	w.must(Resolve(w.s, ResolveReq{}))
	if len(w.notesOf(NBlocked)) != 1 {
		t.Fatalf("blocked written again")
	}
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
		if len(p.Units) != 0 || len(p.Refused) != 1 || p.Refused[0].Key != "s1-2" || p.Refused[0].Why != "not a card of the batch; the batch of 1 is s1-1" {
			t.Fatalf("%+v: %+v", r, p)
		}
	}
	if w.s.Merge.Placed("s1-1").Col != Queued || w.s.Merge.Placed("s1-2").Col != Queued || w.s.StreamCtl("s1").F("state") != StreamMerging {
		t.Fatalf("a refused fact moved a card")
	}
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 2, Conflict: "s1-2"}))
	if w.s.Merge.Placed("s1-2").Col != Stuck || w.s.Merge.Placed("s1-1").Col != Queued {
		t.Fatalf("a conflict inside the batch")
	}
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
		if rc.Col != verdict || rc.F("begun") != stamp(w.s.Now) || rc.F("read") != rc.F("begun") {
			t.Fatalf("%s on an asked card: %s %v", verdict, rc.Col, rc.Fields)
		}
	}
	w.clean("read")
}

// H6: the step that lands or drops the last open primary of the whole sprint
// writes one judgment, the sprint is done, with the counts; add closes it.
func TestTheSprintIsDoneOnce(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.must(Add(w.s, AddReq{Stream: "s2", Count: 1}))
	accepted(w, "s1-1", "s1-2")
	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s2-1"}}, Reason: "obsolete"}))
	if len(w.notesOf(NSprintDone)) != 0 || w.s.StreamCtl("s2").F("dropped") != "1" {
		t.Fatalf("done too early, or the drop not counted: %v %q", w.notesOf(NSprintDone), w.s.StreamCtl("s2").F("dropped"))
	}
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 1}))
	if len(w.notesOf(NSprintDone)) != 0 {
		t.Fatalf("done with s1-2 merging")
	}
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 1}))
	done := w.notesOf(NSprintDone)
	if len(done) != 1 || done[0].Kind != Judgment || done[0].What != "2 landed, 1 dropped" || len(w.openOn(SprintSubject)) != 1 {
		t.Fatalf("the sprint is done: %+v", done)
	}
	if p := MergeStep(w.s, MergeReq{Stream: "s1"}); len(p.Units) != 0 || len(w.notesOf(NSprintDone)) != 1 {
		t.Fatalf("written twice: %+v", p)
	}
	g := Inbox(InboxReq{Now: w.s.Now, Open: w.s.Open, Prefix: "dev-"})
	if len(g) != 1 || g[0].Type != NSprintDone || g[0].Commands[0].Lines[0] != "nova-sprint clear --confirm dev-" ||
		g[0].Commands[1].Decision != "add" || g[0].Size != 0 {
		t.Fatalf("the inbox: %+v", g)
	}
	w.must(Add(w.s, AddReq{Stream: "s3", Count: 1}))
	if len(w.openOn(SprintSubject)) != 0 {
		t.Fatalf("add left the sprint done")
	}
	w.clean("more work")

	// The last open primary dropped: done, by the drop.
	w2 := setup(t, 2)
	accepted(w2, "s1-1")
	w2.must(MergeStep(w2.s, MergeReq{Stream: "s1"}))
	w2.must(Drop(w2.s, DropReq{Sel: Sel{IDs: []string{"s1-2"}}, Reason: "obsolete"}))
	if done := w2.notesOf(NSprintDone); len(done) != 1 || done[0].What != "1 landed, 1 dropped" {
		t.Fatalf("done by a drop: %+v", done)
	}
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
	if w.state("b") != Ready || w.state("c") != Waiting {
		t.Fatalf("after s1-1 landed: b %s, c %s (%+v)", w.state("b"), w.state("c"), p.Units)
	}
	w.clean("b ready")
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 1}))
	if w.state("c") != Ready {
		t.Fatalf("after s1-2 landed: c %s", w.state("c"))
	}
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
	if len(open) != 1 || open[0].Note.Type != NReturned || strings.Join(open[0].Note.Decisions, ",") != "rework,accept,drop" {
		t.Fatalf("returned: %+v", open)
	}
	w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "again"}))
	if len(w.openOn("s1-1")) != 0 {
		t.Fatalf("rework left it open")
	}
	w.clean("reworked")
	// Its reads gone (a new head), accept is not offered.
	w2 := setup(t, 1)
	accepted(w2, "s1-1")
	w2.s.Work.Card("s1-1").Fields["head"] = "moved"
	w2.must(Return(w2.s, ReturnReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	if d := w2.openOn("s1-1")[0].Note.Decisions; strings.Join(d, ",") != "rework,drop" {
		t.Fatalf("decisions without standing reads: %v", d)
	}
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
	if len(o) != 1 || o[0].Note.Type != NStranded || strings.Join(o[0].Note.Decisions, ",") != "ask,rework,drop" || !strings.Contains(o[0].Note.What, "never asked") {
		t.Fatalf("stranded: %+v", o)
	}
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	if len(w.openOn("s1-1")) != 0 {
		t.Fatalf("ask left it open")
	}
	w.clean("asked")
}
