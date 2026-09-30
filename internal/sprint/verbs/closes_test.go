package verbs

import (
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
)

// The judgments the decisions close (section 3, 2.2; the model's DropEff and
// VEff "rework", tla/SprintEvents.tla), and refused unset (1.3.5, 1.5.3).

// openJ opens a judgment of a type and cause on subjects, as its raiser would.
func (w *rv) openJ(typ, cause string, subjects ...string) {
	w.t.Helper()
	w.raw(&sprintfn.Request{Body: sprintfn.Body{Notes: []sprintfn.NoteReq{{Op: sprintfn.JOpOpen, Type: typ, Cause: cause, Subjects: subjects, Text: "raised"}}}})
}

// wantNoJ says no judgment is open or held on the subjects.
func (w *rv) wantNoJ(what string, subjects ...string) {
	w.t.Helper()
	for _, s := range subjects {
		if j := w.jopen(s); len(j) != 0 {
			w.t.Fatalf("%s: judgments still open on %s: %v", what, s, j)
		}
	}
}

func TestDropClosesEveryJudgment(t *testing.T) {
	t.Parallel()
	w := newRV(t)
	w.stream("s1", sprint.StreamWaiting)
	w.readers("r1")
	w.inReview("s1", "p1", 1, "r1")
	w.openJ(sprint.TypeCIRed, sprint.CauseCI, "p1")
	w.openJ(sprint.NReadsExhausted, "exhausted", "p1")
	// A sentinel reached prints `release G` while its judgment is open.
	w.card(sprint.Work, "s1:waiting", "g1", 5, "kind", "sentinel", "attempt", "0", "open", "0")
	w.openJ(sprint.NSentinelReached, sprint.CauseNone, "g1")
	// A waiter blocked on a need with no record.
	w.card(sprint.Work, "s1:waiting", "q1", 6, "kind", "work", "attempt", "0", "open", "1", "needs", "n1")
	w.openJ(sprint.NMissingNeed, "n1", "q1")

	res, err := Drop(context.Background(), w.env, DropReq{IDs: []string{"g1", "p1", "q1"}, Reason: "out of scope"})
	mustOK(t, "drop g1 p1 q1", res, err)
	w.wantNoJ("a dropped card holds no judgment (DropEff)", "p1", "g1", "q1")
	if len(w.noteLines(sprint.TypeCIRed)) != 2 || len(w.noteLines(sprint.NSentinelReached)) != 2 {
		t.Fatal("each judgment has its open line and the drop's close line")
	}

	// A drop of a stream closes the judgments of every card it removes.
	w.stream("s2", sprint.StreamWaiting)
	w.card(sprint.Work, "s2:ready", "d1", 1, "kind", "work", "attempt", "0")
	w.openJ(sprint.NStalled, "ready, in fresh below the first sentinel or in again", "d1")
	res, err = Drop(context.Background(), w.env, DropReq{Streams: []string{"s2"}, Reason: "out of scope"})
	mustOK(t, "drop --stream s2", res, err)
	w.wantNoJ("a card a drop of streams removed", "d1")
}

func TestReworkClosesAndUnsetsRefused(t *testing.T) {
	t.Parallel()
	w := newRV(t)
	w.stream("s1", sprint.StreamWaiting)
	w.member("m1")
	w.card(sprint.Work, "s1:review", "p1", 1, "kind", "work", "attempt", "1", "head", "h1", "result", "ok", "refused", "rework: no member")
	w.openJ(sprint.NStranded, "stranded", "p1")
	w.openJ(sprint.NStalled, "review", "p1")
	w.openJ("the machine could not move a card", "rework", "p1")
	w.openJ(sprint.TypeCannotAsk, "readers", "p1")
	res, err := Rework(context.Background(), w.env, ReworkReq{IDs: []string{"p1"}, Fix: "x"})
	mustOK(t, "rework p1", res, err)
	w.wantNoJ("rework closes what ReworkAt answers and could-not-move", "p1")
	if w.field(sprint.Work, "p1", "refused") != "" {
		t.Fatal("rework unsets refused (1.3.5; the model's rework)")
	}

	// Return unsets refused and closes could-not-move too.
	w.card(sprint.Work, "s1:merging", "p2", 2, "kind", "work", "attempt", "1", "head", "h1", "refused", "accept: no ok pair")
	w.card(sprint.Merge, "s1:queued", "p2", 2, "kind", "merge", sprint.PrimaryField, "p2", "stream", "s1")
	w.openJ("the machine could not move a card", "accept", "p2")
	res, err = Return(context.Background(), w.env, ReturnReq{IDs: []string{"p2"}})
	mustOK(t, "return p2", res, err)
	if w.field(sprint.Work, "p2", "refused") != "" {
		t.Fatal("return unsets refused")
	}
	if _, open := w.jopen("p2")["the machine could not move a card|accept"]; open {
		t.Fatal("return closes could-not-move")
	}
	if _, open := w.jopen("p2")[typeReturned+"|"+causeReturn]; !open {
		t.Fatal("return opens returned to review")
	}
}

func TestAskClosesR16(t *testing.T) {
	t.Parallel()
	w := newRV(t)
	w.stream("s1", sprint.StreamWaiting)
	w.readers("r1", "r2", "r3")
	w.card(sprint.Work, "s1:review", "p1", 1, "kind", "work", "attempt", "1", "head", "h1", "result", "ok")
	w.openJ(sprint.NStranded, "stranded", "p1")
	w.openJ(sprint.NStalled, "review", "p1")
	res, err := Ask(context.Background(), w.env, AskReq{IDs: []string{"p1"}})
	mustOK(t, "ask p1", res, err)
	w.wantNoJ("ask closes stranded and stalled (IT09's AskResolves)", "p1")

	w.openJ(sprint.NReadsExhausted, "exhausted", "p1")
	res, err = Ask(context.Background(), w.env, AskReq{IDs: []string{"p1"}, Another: true})
	mustOK(t, "ask --another p1", res, err)
	w.wantNoJ("ask --another closes reads exhausted (AskAnotherResolves)", "p1")
}
