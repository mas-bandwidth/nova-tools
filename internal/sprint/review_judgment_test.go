package sprint

import (
	"sort"
	"strings"
	"testing"
)

// finished drives a ready primary of s1 to review, its work ok or failed.
func finished(w *world, id string, failed bool) {
	w.t.Helper()
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{id}}}))
	c := w.s.Fleet.Card(w.s.Work.Card(id).F("work"))
	w.must(Take(w.s, TakeReq{As: c.Row, Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID), Failed: failed, Report: "r"}))
}

// readOK has every outstanding read card of the primary say ok.
func readOK(w *world, id string) {
	w.t.Helper()
	for _, rc := range w.s.Readers.Of(id) {
		if rc.Col == Asked || rc.Col == Reading {
			w.must(Read(w.s, ReadReq{As: rc.Row, Verdict: "ok", Sel: Sel{IDs: []string{rc.ID}}}))
		}
	}
}

// openTypes is the types of the judgments open on the subject, sorted.
func openTypes(w *world, id string) string {
	var out []string
	for _, o := range w.openOn(id) {
		out = append(out, o.Note.Type)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// ackAll acknowledges every judgment open on the subject, in one call.
func ackAll(w *world, id string) Plan {
	w.t.Helper()
	var ids []string
	for _, o := range w.openOn(id) {
		ids = append(ids, o.Note.ID)
	}
	return w.must(Ack(w.s, AckReq{Notes: ids, Reason: "seen"}))
}

// Finish: failed work is its own judgment and nothing more; ok work that
// arrives in review is asked by the machine, and is no judgment.
func TestReviewJudgmentFinish(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	finished(w, "s1-1", false)
	if got := openTypes(w, "s1-1"); got != "" {
		t.Fatalf("ok work, never asked yet: %q", got)
	}
	finished(w, "s1-2", true)
	if got := openTypes(w, "s1-2"); got != NWorkFailed {
		t.Fatalf("failed work: %q", got)
	}
	w.clean("finished")
}

// Read: the second different ok is ready to accept; a third ok while one is
// open writes none; reads done without two oks are reads exhausted.
func TestReviewJudgmentRead(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	finished(w, "s1-1", false)
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	readOK(w, "s1-1")
	if got := openTypes(w, "s1-1"); got != NReadyToAccept {
		t.Fatalf("two oks: %q", got)
	}
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}, Another: true}))
	readOK(w, "s1-1")
	if n := len(w.notesOf(NReadyToAccept)); n != 1 {
		t.Fatalf("a third ok wrote another ready to accept: %d", n)
	}
	finished(w, "s1-2", false)
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-2"}}}))
	rcs := readsAt(w.s, w.s.Work.Card("s1-2"), 1)
	w.must(Read(w.s, ReadReq{As: rcs[0].Row, Verdict: "ok", Sel: Sel{IDs: []string{rcs[0].ID}}}))
	w.must(Read(w.s, ReadReq{As: rcs[1].Row, Verdict: "broken", Finding: "f", Sel: Sel{IDs: []string{rcs[1].ID}}}))
	ackAll(w, "s1-2")
	if got := openTypes(w, "s1-2"); got != NReadsExhausted {
		t.Fatalf("one ok, one broken acknowledged: %q", got)
	}
	w.clean("read")
}

// Return: a primary sent back to review with its reads standing has the one
// judgment returned to review, which offers accept; a further ok read writes
// nothing more, and acknowledging it leaves ready to accept.
func TestReviewJudgmentReturnAndAck(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	accepted(w, "s1-1")
	w.must(Return(w.s, ReturnReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "hold it"}))
	open := w.openOn("s1-1")
	if len(open) != 1 || open[0].Note.Type != NReturned || !contains(open[0].Note.Decisions, "accept") {
		t.Fatalf("returned: %+v", open)
	}
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}, Another: true}))
	readOK(w, "s1-1")
	if got := openTypes(w, "s1-1"); got != NReturned {
		t.Fatalf("a third ok after return: %q", got)
	}
	ackAll(w, "s1-1")
	if got := openTypes(w, "s1-1"); got != NReadyToAccept {
		t.Fatalf("returned acknowledged, acceptable: %q", got)
	}
	// Acknowledging ready to accept itself does not write it again.
	ackAll(w, "s1-1")
	if got := openTypes(w, "s1-1"); got != "" {
		t.Fatalf("ready to accept acknowledged: %q", got)
	}
	w.clean("returned")
}

// Ask: one more reader for a primary acceptable and silent (its ready to
// accept acknowledged) writes ready to accept again.
func TestReviewJudgmentAsk(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	finished(w, "s1-1", false)
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	readOK(w, "s1-1")
	ackAll(w, "s1-1")
	if got := openTypes(w, "s1-1"); got != "" {
		t.Fatalf("acknowledged: %q", got)
	}
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}, Another: true}))
	if got := openTypes(w, "s1-1"); got != NReadyToAccept {
		t.Fatalf("asked of another: %q", got)
	}
	w.clean("asked")
}

// A refused rework leaves the primary in review with the judgment it needs.
func TestReviewJudgmentRefusedRework(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	finished(w, "s1-1", false)
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	readOK(w, "s1-1")
	ackAll(w, "s1-1")
	p := w.do(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	if len(p.Refused) != 1 || w.state("s1-1") != Review {
		t.Fatalf("rework with no fix: %+v", p)
	}
	if got := openTypes(w, "s1-1"); got != NReadyToAccept {
		t.Fatalf("refused rework: %q", got)
	}
	w.clean("refused")
}

// CI: green on the current head closes red; a primary never asked is then
// stranded in review.
func TestReviewJudgmentCI(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	finished(w, "s1-1", false)
	w.must(RecordCI(w.s, CIReq{Sel: Sel{IDs: []string{"s1-1"}}, Red: true, Run: "r1"}))
	w.must(RecordCI(w.s, CIReq{Sel: Sel{IDs: []string{"s1-1"}}, Run: "r2"}))
	open := w.openOn("s1-1")
	if len(open) != 1 || open[0].Note.Type != NStranded || !strings.Contains(open[0].Note.What, "never asked") {
		t.Fatalf("green after red: %+v", open)
	}
	w.clean("ci")
}
