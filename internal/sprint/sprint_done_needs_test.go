package sprint

import (
	"strings"
	"testing"
	"time"
)

// The sprint is done: an add that only opens a stream admits no card and
// leaves it done; it has no due time and is never overdue.
func TestSprintDoneOutlastsAnAddOfNoCardAndIsNeverOverdue(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	accepted(w, "s1-1")
	w.must(MergeStep(w.s, MergeReq{Stream: "s1"}))
	if len(w.openOn(SprintSubject)) != 1 {
		t.Fatalf("not done: %+v", w.s.Open)
	}
	w.must(Add(w.s, AddReq{Stream: "s2"}))
	if w.s.StreamCtl("s2") == nil || len(w.openOn(SprintSubject)) != 1 {
		t.Fatalf("an add that only opens a stream: s2 %v, done open %d", w.s.StreamCtl("s2") != nil, len(w.openOn(SprintSubject)))
	}
	for _, g := range Inbox(InboxReq{Now: w.s.Now.Add(1000 * time.Hour), Open: w.s.Open, Deadline: time.Minute}) {
		if g.Type == NSprintDone && (g.Overdue || g.Marked || !g.Due.IsZero() || contains(g.Decisions, "act")) {
			t.Fatalf("the sprint is done, shown overdue: %+v", g)
		}
	}
	if !w.openOn(SprintSubject)[0].Note.Due(time.Minute).IsZero() {
		t.Fatalf("the sprint is done has a due time")
	}
	w.must(Add(w.s, AddReq{Stream: "s2", Count: 1}))
	if len(w.openOn(SprintSubject)) != 0 {
		t.Fatalf("an add of a card left the sprint done")
	}
}

// Every primary dropped, none landed: the sprint is done, 0 landed.
func TestSprintDoneWithNothingLanded(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-1", "s1-2"}}, Reason: "obsolete"}))
	done := w.notesOf(NSprintDone)
	if len(done) != 1 || done[0].What != "0 landed, 2 dropped" || len(w.openOn(SprintSubject)) != 1 {
		t.Fatalf("all dropped: %+v", done)
	}
}

// Acknowledging a blocked judgment waives only the needs it names; a need
// dropped after it was written is its own judgment.
func TestAckWaivesOnlyTheNeedsItsJudgmentNames(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"b"}, Needs: []string{"s1-1", "s1-2"}}))
	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "gone"}))
	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-2"}}, Reason: "gone too"}))
	open := w.openOn("b")
	if len(open) != 2 || strings.Join(open[0].Note.Needs, ",")+"|"+strings.Join(open[1].Note.Needs, ",") != "s1-1|s1-2" {
		t.Fatalf("blocked judgments on b: %+v", open)
	}
	// A resolve writes none again.
	w.do(Resolve(w.s, ResolveReq{}))
	if len(w.notesOf(NBlocked)) != 2 {
		t.Fatalf("blocked notes after resolve: %d", len(w.notesOf(NBlocked)))
	}
	w.must(Ack(w.s, AckReq{Notes: []string{open[0].Note.ID}, Reason: "fine"}))
	if b := w.s.Work.Card("b"); b.Col != Waiting || b.F("waived") != "s1-1" {
		t.Fatalf("the first ack: %s waived=%q", b.Col, b.F("waived"))
	}
	w.must(Ack(w.s, AckReq{Notes: []string{open[1].Note.ID}, Reason: "fine too"}))
	if b := w.s.Work.Card("b"); b.Col != Ready || b.F("waived") != "s1-1,s1-2" {
		t.Fatalf("the second ack: %s waived=%q", b.Col, b.F("waived"))
	}
	needs, _ := NeedsOf(w.s, "b")
	if len(needs) != 2 || !needs[0].Waived || !needs[1].Waived || needs[1].WaivedAt == "" {
		t.Fatalf("needs of b: %+v", needs)
	}
	w.clean("waived")
}
