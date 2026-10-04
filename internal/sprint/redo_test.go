package sprint

import (
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// conflicted is three accepted primaries of s1 merged as a batch of two that
// stopped the stream on a conflict on s1-2: s1-1 landed, s1-2 stuck, s1-3 queued.
func conflicted(t *testing.T) *world {
	t.Helper()
	w := setup(t, 3)
	accepted(w, "s1-1", "s1-2", "s1-3")
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 2, Conflict: "s1-2", Note: "CONFLICT (content): Merge conflict in a.go"}))
	require.Equal(t, StreamStopped, w.s.StreamCtl("s1").F("state"), "the stream did not stop on the conflict")
	require.Equal(t, Stuck, w.s.Merge.Placed("s1-2").Col, "s1-2 is not stuck")
	w.clean("conflicted")
	return w
}

// redo is return, rework "redo the same change on the current tip" and resume,
// the three verbs a conflict took, as one step with one history line
// (docs/SPEC-SPRINT.md section 7, redo); refused when the card is not in a conflict.
func TestRedoReturnsReworksAndResumesInOneVerb(t *testing.T) {
	t.Parallel()
	w := conflicted(t)

	// refused, writing nothing, for a card that is not in a conflict
	for id, why := range map[string]string{"s1-3": "not in a conflict", "s1-1": "not in a conflict", "nosuch": "no such card"} {
		p := Redo(w.s, RedoReq{IDs: []string{id}, Who: "coordinator"})
		assert.Empty(t, p.Units, "redo %s: %+v", id, p)
		if assert.Len(t, p.Refused, 1, "redo %s: %+v", id, p) {
			assert.Contains(t, p.Refused[0].Why, why, "redo %s: %+v", id, p)
		}
	}
	p := Redo(w.s, RedoReq{IDs: []string{"s1-2", "s1-3"}, Who: "coordinator"})
	assert.Empty(t, p.Units, "a redo naming a card not in a conflict moved another: %+v", p)
	p = Redo(w.s, RedoReq{Who: "coordinator"})
	assert.Empty(t, p.Units, "a redo naming nothing: %+v", p)
	require.NotEmpty(t, p.Refused, "a redo naming nothing: %+v", p)

	p = w.must(Redo(w.s, RedoReq{IDs: []string{"s1-2"}, Who: "coordinator"}))
	require.Len(t, p.Units, 1, "redo is one step with one history line: %+v", p.Units)
	moved := p.Units[0].Moved
	assert.Contains(t, moved, "s1-2 merging -> working (redo", "the history line: %s", moved)
	assert.Contains(t, moved, "stream s1 stopped -> merging", "the history line: %s", moved)
	assert.Equal(t, Working, w.state("s1-2"), "redo: s1-2 is %s", w.state("s1-2"))
	assert.Equal(t, Returned, w.s.Merge.Placed("s1-2").Col, "redo: the merge card of s1-2 is %s", w.s.Merge.Placed("s1-2").Col)
	ctl := w.s.StreamCtl("s1")
	assert.Equal(t, StreamMerging, ctl.F("state"), "redo did not resume the stream")
	assert.False(t, ctl.Has("cause"), "the resumed stream keeps its cause: %v", ctl.Fields)
	assert.Equal(t, Queued, w.s.Merge.Placed("s1-3").Col, "s1-3 left the queue")
	assert.Empty(t, w.openOn(StreamSubject("s1")), "the conflict judgment is still open")
	assert.Empty(t, w.openOn("s1-2"), "a judgment is open on s1-2")
	pr := w.s.Work.Card("s1-2")
	require.Equal(t, "s1-2.w2", pr.F("work"), "the next attempt's card")
	wc := w.s.Fleet.Card("s1-2.w2")
	assert.Equal(t, RedoFix, wc.F("fix"), "the next attempt's fix")
	assert.Contains(t, wc.F("why"), "conflict", "the next attempt is not told it conflicted: %q", wc.F("why"))
	assert.Empty(t, w.s.Readers.Of("s1-2"), "the read cards of attempt 1 stand")
	w.clean("redo")

	// a second redo: the card is in no conflict now
	p = Redo(w.s, RedoReq{IDs: []string{"s1-2"}, Who: "coordinator"})
	assert.Empty(t, p.Units, "a second redo: %+v", p)
	assert.NotEmpty(t, p.Refused, "a second redo: %+v", p)

	// the same tables as the three verbs it replaces, the words of why and did aside
	three := conflicted(t)
	three.must(Return(three.s, ReturnReq{Sel: Sel{IDs: []string{"s1-2"}}, Reason: "conflict", Who: "coordinator"}))
	three.must(Rework(three.s, ReworkReq{Sel: Sel{IDs: []string{"s1-2"}}, Fix: RedoFix, Who: "coordinator"}))
	three.must(Resume(three.s, ResumeReq{Stream: "s1", Did: "returned s1-2 for rework", Who: "coordinator"}))
	same := func(a, b *Card, what string) {
		t.Helper()
		require.NotNil(t, a, "%s: redo has none", what)
		require.NotNil(t, b, "%s: the three verbs have none", what)
		assert.Equal(t, b.Row+":"+b.Col, a.Row+":"+a.Col, "%s: placed", what)
		fa, fb := maps.Clone(a.Fields), maps.Clone(b.Fields)
		for _, f := range []string{"why", "did"} {
			delete(fa, f)
			delete(fb, f)
		}
		assert.Equal(t, fb, fa, "%s: fields", what)
	}
	same(w.s.Work.Card("s1-2"), three.s.Work.Card("s1-2"), "the primary")
	same(w.s.Merge.Card("s1-2"), three.s.Merge.Card("s1-2"), "its merge card")
	same(w.s.Merge.Card("s1-3"), three.s.Merge.Card("s1-3"), "the card queued behind it")
	same(w.s.StreamCtl("s1"), three.s.StreamCtl("s1"), "the stream")
	same(w.s.Fleet.Card("s1-2.w2"), three.s.Fleet.Card("s1-2.w2"), "the next attempt's work card")
	for _, rc := range three.s.Readers.Of("s1-2") {
		same(w.s.Readers.Card(rc.ID), rc, "read card "+rc.ID)
	}
	assert.Len(t, w.s.Open, len(three.s.Open), "open judgments: redo %v, the three verbs %v", w.s.Open, three.s.Open)

	// the reworked attempt goes the way of any: finished, read, accepted back
	// into the queue from its returned merge card, and landed
	member := w.s.Fleet.Card("s1-2.w2").Row
	w.must(Take(w.s, TakeReq{As: member, Sel: Sel{IDs: []string{"s1-2.w2"}}, Gens: gensOf(w.s, "s1-2.w2")}))
	w.must(Finish(w.s, FinishReq{As: member, Sel: Sel{IDs: []string{"s1-2.w2"}}, Gens: gensOf(w.s, "s1-2.w2")}))
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-2"}}}))
	for _, rc := range readsAt(w.s, w.s.Work.Card("s1-2"), 2) {
		w.must(Read(w.s, ReadReq{As: rc.F("reader"), Verdict: "ok", Sel: Sel{IDs: []string{rc.ID}}}))
	}
	w.must(Accept(w.s, AcceptReq{Sel: Sel{IDs: []string{"s1-2"}}}))
	assert.Equal(t, Queued, w.s.Merge.Placed("s1-2").Col, "the redone card is not queued again")
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 10}))
	assert.Equal(t, Landed, w.state("s1-2"), "the redone card did not land")
	assert.Equal(t, StreamLanded, w.s.StreamCtl("s1").F("state"), "the stream did not land")
	w.clean("landed")
}

// redo --stream is the card the stream stopped on; a stream stopped for
// another cause, or not stopped, has no card in a conflict.
func TestRedoByStreamAndRefusedOffAConflict(t *testing.T) {
	t.Parallel()
	w := conflicted(t)
	p := Redo(w.s, RedoReq{IDs: []string{"s1-2"}, Stream: "s2", Who: "coordinator"})
	assert.Empty(t, p.Units, "redo of s1-2 in stream s2: %+v", p)
	assert.NotEmpty(t, p.Refused, "redo of s1-2 in stream s2: %+v", p)
	p = w.must(Redo(w.s, RedoReq{Stream: "s1", Fix: "take the tip's a.go and add the change again", Who: "coordinator"}))
	require.Len(t, p.Units, 1, "redo --stream: %+v", p.Units)
	assert.Equal(t, Working, w.state("s1-2"), "redo --stream did not redo the card the stream stopped on")
	assert.Equal(t, "take the tip's a.go and add the change again", w.s.Fleet.Card("s1-2.w2").F("fix"), "--fix is not the next attempt's fix")
	w.clean("redo --stream")
	p = Redo(w.s, RedoReq{Stream: "s1", Who: "coordinator"})
	assert.Empty(t, p.Units, "redo of a stream not stopped: %+v", p)
	if assert.Len(t, p.Refused, 1, "redo of a stream not stopped: %+v", p) {
		assert.Contains(t, p.Refused[0].Why, "not stopped on a conflict", "redo of a stream not stopped: %+v", p)
	}

	// a red stop is not a conflict: redo refuses its cards
	r := setup(t, 2)
	accepted(r, "s1-1", "s1-2")
	r.must(MergeStep(r.s, MergeReq{Stream: "s1", Red: true}))
	for _, q := range []RedoReq{{IDs: []string{"s1-2"}}, {Stream: "s1"}} {
		p = Redo(r.s, q)
		assert.Empty(t, p.Units, "redo on a red stop %+v: %+v", q, p)
		if assert.Len(t, p.Refused, 1, "redo on a red stop %+v: %+v", q, p) {
			assert.Contains(t, p.Refused[0].Why, "not", "redo on a red stop %+v: %+v", q, p)
		}
	}
}
