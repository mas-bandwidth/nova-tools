package sprint

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLifecycleIsTheSpecTable(t *testing.T) {
	t.Parallel()
	legal := map[[2]State]bool{
		{Waiting, Ready}: true, {Ready, Working}: true, {Working, Review}: true, {Working, Ready}: true,
		{Review, Merging}: true, {Review, Working}: true, {Review, Ready}: true, {Merging, Review}: true, {Merging, Landed}: true,
		{Waiting, Landed}: true, {Ready, Waiting}: true, // a sentinel released; a sentinel inserted in front
	}
	for _, a := range States {
		for _, b := range States {
			got := Legal(a, b)
			assert.Equal(t, legal[[2]State{a, b}], got, "Legal(%s, %s) = %v", a, b, got)
		}
	}
	assert.Len(t, Moves, len(legal), "%d moves, the spec has %d", len(Moves), len(legal))
	for _, s := range States {
		assert.Equal(t, s != Landed, IsOpen(s), "IsOpen(%s) = %v", s, IsOpen(s))
	}
}

func TestIdentitiesRoundTrip(t *testing.T) {
	t.Parallel()
	p, n, ok := ParseWorkCard(WorkCardID("s1-7", 3))
	assert.True(t, ok, "work card: %s %d %v", p, n, ok)
	assert.Equal(t, "s1-7", p, "work card: %s %d %v", p, n, ok)
	assert.Equal(t, 3, n, "work card: %s %d %v", p, n, ok)
	p, n, r, ok := ParseReadCard(ReadCardID("s1-7", 2, "reader-a"))
	assert.True(t, ok, "read card: %s %d %s %v", p, n, r, ok)
	assert.Equal(t, "s1-7", p, "read card: %s %d %s %v", p, n, r, ok)
	assert.Equal(t, 2, n, "read card: %s %d %s %v", p, n, r, ok)
	assert.Equal(t, "reader-a", r, "read card: %s %d %s %v", p, n, r, ok)
	for _, bad := range []string{"", "a.b", "a b", "-x", strings.Repeat("x", 129)} {
		assert.False(t, ValidID(bad), "ValidID(%q) = true", bad)
	}
}

// setup is two up members, three readers and n primaries in stream s1.
func setup(t *testing.T, n int) *world {
	w := newWorld(t, "reader-a", "reader-b", "reader-c")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m2"}))
	w.must(Add(w.s, AddReq{Stream: "s1", Count: n}))
	w.clean("setup")
	return w
}

func TestTheWholeLifeOfAPrimary(t *testing.T) {
	t.Parallel()
	w := setup(t, 4)
	got := w.state("s1-1")
	require.Equal(t, Ready, got, "admitted as %s", got)
	w.must(Deal(w.s, DealReq{Sel: Sel{Limit: 4}}))
	require.Equal(t, 2, w.s.Fleet.Count("m1", Ready), "not dealt to the shortest queues: m1=%d m2=%d", w.s.Fleet.Count("m1", Ready), w.s.Fleet.Count("m2", Ready))
	require.Equal(t, 2, w.s.Fleet.Count("m2", Ready), "not dealt to the shortest queues: m1=%d m2=%d", w.s.Fleet.Count("m1", Ready), w.s.Fleet.Count("m2", Ready))
	w.clean("deal")
	w.must(Take(w.s, TakeReq{As: "m1", Sel: Sel{Limit: 10}}))
	w.must(Take(w.s, TakeReq{As: "m2", Sel: Sel{Limit: 10}}))
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1", "s1-2.w1", "s1-3.w1"}}, Gens: gensOf(w.s, "s1-1.w1", "s1-2.w1", "s1-3.w1")}))
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-4.w1"}}, Gens: gensOf(w.s, "s1-4.w1"), Failed: true, Report: "tests red"}))
	w.clean("finish")
	require.Len(t, w.notesOf(NWorkOK), 3, "notes: %d ok, %d failed", len(w.notesOf(NWorkOK)), len(w.notesOf(NWorkFailed)))
	require.Len(t, w.notesOf(NWorkFailed), 1, "notes: %d ok, %d failed", len(w.notesOf(NWorkOK)), len(w.notesOf(NWorkFailed)))
	require.Len(t, w.openOn("s1-4"), 1, "failed work is not an open judgment: %v", w.s.Open)
	assert.Equal(t, 1, w.s.Fleet.Count("m1", DoneFailed)+w.s.Fleet.Count("m2", DoneFailed), "the failed work card is not in a member's failed cell")

	ask := w.must(Ask(w.s, AskReq{}))
	require.Len(t, ask.Units, 3, "ask dealt %d primaries, want the 3 that came back ok (failed work is not read)", len(ask.Units))
	for _, id := range []string{"s1-1", "s1-2", "s1-3"} {
		reads := readsAt(w.s, w.s.Work.Card(id), 1)
		require.Len(t, reads, 2, "%s asked of %d readers: %v", id, len(reads), reads)
		require.NotEqual(t, reads[0].F("reader"), reads[1].F("reader"), "%s asked of %d readers: %v", id, len(reads), reads)
	}
	w.clean("ask")

	// One reader's ok is never enough.
	first := readsAt(w.s, w.s.Work.Card("s1-1"), 1)
	w.must(Read(w.s, ReadReq{As: first[0].F("reader"), Verdict: "ok", Sel: Sel{IDs: []string{first[0].ID}}}))
	acc := Accept(w.s, AcceptReq{Sel: Sel{IDs: []string{"s1-1"}}})
	require.Empty(t, acc.Units, "accept with one ok: %+v", acc)
	require.Len(t, acc.Refused, 1, "accept with one ok: %+v", acc)
	require.Contains(t, acc.Refused[0].Why, "two different readers", "accept with one ok: %+v", acc)
	w.must(Read(w.s, ReadReq{As: first[1].F("reader"), Verdict: "ok", Sel: Sel{IDs: []string{first[1].ID}}}))
	require.Len(t, w.notesOf(NReadyToAccept), 1, "ready-to-accept notes: %d", len(w.notesOf(NReadyToAccept)))
	w.must(Accept(w.s, AcceptReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	require.Equal(t, Merging, w.state("s1-1"), "accept: work %s", w.state("s1-1"))
	require.Equal(t, Queued, w.s.Merge.Placed("s1-1").Col, "accept: work %s", w.state("s1-1"))
	require.Equal(t, w.s.Work.Card("s1-1").Score, w.s.Merge.Placed("s1-1").Score, "the merge place does not carry the primary's score")
	w.clean("accept")

	// A broken read, rework with the finding; the fixed work is asked of two different readers again.
	second := readsAt(w.s, w.s.Work.Card("s1-2"), 1)
	w.must(Read(w.s, ReadReq{As: second[0].F("reader"), Verdict: "broken", Finding: "line 1: off by one", Sel: Sel{IDs: []string{second[0].ID}}}))
	require.Len(t, w.openOn("s1-2"), 1, "a broken read is not an open judgment")
	score := w.s.Work.Card("s1-2").Score
	w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-2"}}, Fix: "off by one"}))
	require.Equal(t, Working, w.state("s1-2"), "rework: %s, open %v, read cards %d", w.state("s1-2"), w.openOn("s1-2"), len(w.s.Readers.Of("s1-2")))
	require.Empty(t, w.openOn("s1-2"), "rework: %s, open %v, read cards %d", w.state("s1-2"), w.openOn("s1-2"), len(w.s.Readers.Of("s1-2")))
	require.Empty(t, w.s.Readers.Of("s1-2"), "rework: %s, open %v, read cards %d", w.state("s1-2"), w.openOn("s1-2"), len(w.s.Readers.Of("s1-2")))
	require.Equal(t, score, w.s.Work.Card("s1-2").Score, "rework changed the score")
	w.clean("rework")
	card := w.s.Work.Card("s1-2").F("work")
	require.Equal(t, "s1-2.w2", card, "the next attempt's card is %s", card)
	require.Equal(t, "off by one", w.s.Fleet.Card(card).F("fix"), "the next attempt's card is %s", card)
	member := w.s.Fleet.Card(card).Row
	w.must(Take(w.s, TakeReq{As: member, Sel: Sel{IDs: []string{card}}, Gens: gensOf(w.s, card)}))
	w.must(Finish(w.s, FinishReq{As: member, Sel: Sel{IDs: []string{card}}, Gens: gensOf(w.s, card)}))
	w.must(Ask(w.s, AskReq{})) // the machine's ask: round the readers
	again := readsAt(w.s, w.s.Work.Card("s1-2"), 2)
	require.Len(t, again, 2, "fixed work not asked of two different readers: %v", again)
	require.NotEqual(t, again[0].F("reader"), again[1].F("reader"), "fixed work not asked of two different readers: %v", again)
	w.clean("fixed work returned")

	// Merge the one accepted primary; the stream is not landed until all are.
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 10}))
	require.Equal(t, Landed, w.state("s1-1"), "merge: %s", w.state("s1-1"))
	require.Equal(t, Merged, w.s.Merge.Placed("s1-1").Col, "merge: %s", w.state("s1-1"))
	require.Len(t, w.notesOf(NStartedMerging), 1, "merge notes: started %d batch %d landed %d", len(w.notesOf(NStartedMerging)), len(w.notesOf(NBatchLanded)), len(w.notesOf(NStreamLanded)))
	require.Len(t, w.notesOf(NBatchLanded), 1, "merge notes: started %d batch %d landed %d", len(w.notesOf(NStartedMerging)), len(w.notesOf(NBatchLanded)), len(w.notesOf(NStreamLanded)))
	require.Empty(t, w.notesOf(NStreamLanded), "merge notes: started %d batch %d landed %d", len(w.notesOf(NStartedMerging)), len(w.notesOf(NBatchLanded)), len(w.notesOf(NStreamLanded)))
	w.clean("merge")
}

func TestScoresAreCopiedAndOnlyRankChangesThem(t *testing.T) {
	t.Parallel()
	w := setup(t, 3)
	w.must(Deal(w.s, DealReq{Sel: Sel{Stream: "s1"}}))
	c := w.s.Fleet.Card("s1-2.w1")
	require.Equal(t, w.s.Work.Card("s1-2").Score, c.Score, "the work card does not copy the score")
	sc := -5.0
	p := w.must(Rank(w.s, RankReq{IDs: []string{"s1-2"}, Score: &sc}))
	require.Len(t, p.Units[0].Changes, 2, "rank did not change every copy: %+v", p.Units[0].Changes)
	require.Equal(t, sc, w.s.Fleet.Card("s1-2.w1").Score, "rank did not change every copy: %+v", p.Units[0].Changes)
	require.Equal(t, sc, w.s.Work.Card("s1-2").Score, "rank did not change every copy: %+v", p.Units[0].Changes)
	w.clean("rank")
	w.s.Fleet.Card("s1-2.w1").Score = 9
	v := Check(w.s, nil)
	require.Len(t, v, 1, "a copy with another score: %v", v)
	require.Equal(t, 7, v[0].Rule, "a copy with another score: %v", v)
}

func TestFleetDownDealsAndWithdrawsWhenNoneIsUp(t *testing.T) {
	t.Parallel()
	w := setup(t, 4)
	w.must(Deal(w.s, DealReq{Sel: Sel{Limit: 4}}))
	w.must(Take(w.s, TakeReq{As: "m1", Sel: Sel{Limit: 1}}))
	w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m1"}))
	require.Zero(t, w.s.Fleet.Count("m1", Ready)+w.s.Fleet.Count("m1", Working), "down did not deal m1's unfinished cards to m2")
	require.Equal(t, 4, w.s.Fleet.Count("m2", Ready), "down did not deal m1's unfinished cards to m2")
	w.clean("down")
	w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m2"}))
	for i := 1; i <= 4; i++ {
		id := "s1-" + itoa(i)
		require.Equal(t, Ready, w.state(id), "%s is %s after the last member went down", id, w.state(id))
	}
	require.Len(t, w.notesOf(NWithdrawn), 4, "notes: withdrawn %d down %d", len(w.notesOf(NWithdrawn)), len(w.notesOf(NMemberDown)))
	require.Len(t, w.notesOf(NMemberDown), 2, "notes: withdrawn %d down %d", len(w.notesOf(NWithdrawn)), len(w.notesOf(NMemberDown)))
	w.clean("withdrawn")
	p := Deal(w.s, DealReq{Sel: Sel{Limit: 1}})
	require.Empty(t, p.Units, "deal with nobody up: %+v", p)
	require.Len(t, p.Refused, 1, "deal with nobody up: %+v", p)
	// Up again: the same card is dealt again.
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	require.Equal(t, "s1-1.w1", w.s.Work.Card("s1-1").F("work"), "after withdrawal the card is %s", w.s.Work.Card("s1-1").F("work"))
	w.clean("up again")
}

func TestLevelMovesTheNewestCards(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a", "reader-b")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	w.must(Add(w.s, AddReq{Stream: "s1", Count: 6}))
	w.must(Deal(w.s, DealReq{Sel: Sel{Limit: 6}}))
	p := w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m2"}))
	require.Equal(t, 3, w.s.Fleet.Count("m1", Ready), "not levelled: %d %d", w.s.Fleet.Count("m1", Ready), w.s.Fleet.Count("m2", Ready))
	require.Equal(t, 3, w.s.Fleet.Count("m2", Ready), "not levelled: %d %d", w.s.Fleet.Count("m1", Ready), w.s.Fleet.Count("m2", Ready))
	for _, c := range w.s.Fleet.Cell("m2", Ready) {
		require.GreaterOrEqual(t, c.Score, 4.0, "an older card moved: %s (%v); units %v", c.ID, c.Score, p.Units)
	}
	w.clean("level")
}

func TestMergeFactsStopTheStreamAndResumeMovesIt(t *testing.T) {
	t.Parallel()
	w := setup(t, 3)
	accepted(w, "s1-1", "s1-2", "s1-3")
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 2, Conflict: "s1-2"}))
	ctl := w.s.StreamCtl("s1")
	require.Equal(t, StreamStopped, ctl.F("state"), "conflict: state %s", ctl.F("state"))
	require.Equal(t, Stuck, w.s.Merge.Placed("s1-2").Col, "conflict: state %s", ctl.F("state"))
	require.Equal(t, Merging, w.state("s1-2"), "conflict: state %s", ctl.F("state"))
	require.Len(t, w.openOn(StreamSubject("s1")), 1, "a stopped stream with no open judgment")
	w.clean("stopped")
	p := MergeStep(w.s, MergeReq{Stream: "s1", Batch: 2})
	require.Empty(t, p.Units, "a stopped stream merged: %+v", p)
	require.Len(t, p.Refused, 1, "a stopped stream merged: %+v", p)
	// Answering the stream's judgment elsewhere leaves it open: rule 9.
	p = Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-2"}}, Fix: "x", Answers: []string{w.openOn(StreamSubject("s1"))[0].Note.ID}})
	require.Len(t, p.Refused, 2, "rework of a merging primary: %+v", p)
	require.Empty(t, p.Closes, "rework of a merging primary: %+v", p)
	require.Empty(t, p.Units, "rework of a merging primary: %+v", p)
	w.must(Resume(w.s, ResumeReq{Stream: "s1", Did: "rebased"}))
	require.Equal(t, StreamMerging, w.s.StreamCtl("s1").F("state"), "resume did not move the stream")
	require.Equal(t, Queued, w.s.Merge.Placed("s1-2").Col, "resume did not move the stream")
	require.Empty(t, w.openOn(StreamSubject("s1")), "resume did not move the stream")
	w.clean("resumed")
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 10}))
	require.Equal(t, StreamLanded, w.s.StreamCtl("s1").F("state"), "stream not landed: %s", w.s.StreamCtl("s1").F("state"))
	require.Len(t, w.notesOf(NStreamLanded), 1, "stream not landed: %s", w.s.StreamCtl("s1").F("state"))
	w.clean("landed")

	// Red stops the stream the same way; the second stuck is marked.
	w2 := setup(t, 2)
	accepted(w2, "s1-1", "s1-2")
	w2.must(MergeStep(w2.s, MergeReq{Stream: "s1", Red: true}))
	require.Equal(t, "red", w2.s.StreamCtl("s1").F("cause"), "red: %v", w2.notesOf(NRed))
	require.Len(t, w2.notesOf(NRed), 1, "red: %v", w2.notesOf(NRed))
	require.Equal(t, 2, w2.notesOf(NRed)[0].Count, "red: %v", w2.notesOf(NRed))
	w2.must(Resume(w2.s, ResumeReq{Stream: "s1", Did: "reverted the suspect"}))
	w2.must(MergeStep(w2.s, MergeReq{Stream: "s1", Conflict: "s1-1"}))
	w2.must(Resume(w2.s, ResumeReq{Stream: "s1"}))
	w2.must(MergeStep(w2.s, MergeReq{Stream: "s1", Conflict: "s1-1"}))
	last := w2.notesOf(NConflict)
	require.True(t, last[len(last)-1].Marked, "a second stuck for the same cause is not marked: %+v", last[len(last)-1])
	require.Contains(t, last[len(last)-1].Decisions, RepeatDecision, "a second stuck for the same cause is not marked: %+v", last[len(last)-1])
}

// accepted drives primaries of s1 to merging queued.
func accepted(w *world, ids ...string) {
	w.t.Helper()
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: ids}}))
	for _, id := range ids {
		c := w.s.Fleet.Card(w.s.Work.Card(id).F("work"))
		w.must(Take(w.s, TakeReq{As: c.Row, Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
		w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
	}
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: ids}}))
	for _, id := range ids {
		for _, rc := range readsAt(w.s, w.s.Work.Card(id), w.s.Work.Card(id).Int("attempt")) {
			w.must(Read(w.s, ReadReq{As: rc.F("reader"), Verdict: "ok", Sel: Sel{IDs: []string{rc.ID}}}))
		}
	}
	w.must(Accept(w.s, AcceptReq{Sel: Sel{IDs: ids}}))
}

func TestReturnThenAcceptAgainMovesTheMergePlace(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	accepted(w, "s1-1")
	w.must(Return(w.s, ReturnReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "suspect"}))
	require.Equal(t, Review, w.state("s1-1"), "return: %s", w.state("s1-1"))
	require.Equal(t, Returned, w.s.Merge.Placed("s1-1").Col, "return: %s", w.state("s1-1"))
	w.clean("returned")
	p := w.must(Accept(w.s, AcceptReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	var merge Change
	for _, c := range p.Units[0].Changes {
		if c.Table == Merge && c.Entry.ID == "s1-1" {
			merge = c
		}
	}
	require.NotNil(t, merge.Entry.Move, "accepting a returned primary creates instead of moving: %+v", merge.Entry)
	require.Nil(t, merge.Entry.Create, "accepting a returned primary creates instead of moving: %+v", merge.Entry)
	w.clean("accepted again")
}

func TestDropTakesItsCardsAndBlocksWhatNeedsIt(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"later"}, Needs: []string{"s1-1"}}))
	require.Equal(t, Waiting, w.state("later"), "a primary with needs is %s", w.state("later"))
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "obsolete"}))
	require.Equal(t, "", w.state("s1-1"), "drop left cards behind")
	require.Equal(t, "dropped", w.s.Work.Card("s1-1").F("outcome"), "drop left cards behind")
	require.Nil(t, w.s.Fleet.Placed("s1-1.w1"), "drop left cards behind")
	require.Len(t, w.notesOf(NBlocked), 1, "the waiting primary is not reported blocked")
	require.Len(t, w.openOn("later"), 1, "the waiting primary is not reported blocked")
	w.clean("dropped")
	// resolve does not report it twice
	w.must(Resolve(w.s, ResolveReq{}))
	require.Len(t, w.notesOf(NBlocked), 1, "blocked reported again")
	p := Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "x"})
	require.Len(t, p.Refused, 1, "dropped twice: %+v", p)
}

func TestResolveMovesWhenNeedsLand(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"b"}, Needs: []string{"s1-1"}}))
	p := w.must(Resolve(w.s, ResolveReq{}))
	require.Empty(t, p.Units, "resolved before the need landed")
	accepted(w, "s1-1")
	w.must(MergeStep(w.s, MergeReq{Stream: "s1"}))
	w.must(Resolve(w.s, ResolveReq{}))
	require.Equal(t, Ready, w.state("b"), "b is %s", w.state("b"))
	w.clean("resolved")
}

func TestCIResultIsAlwaysANotification(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.must(RecordCI(w.s, CIReq{Sel: Sel{IDs: []string{"s1-1"}}, Red: true}))
	w.must(RecordCI(w.s, CIReq{Sel: Sel{IDs: []string{"s1-2"}}}))
	require.Len(t, w.notesOf(NCIRed), 1, "ci notes: %v", w.notes)
	require.Equal(t, Judgment, w.notesOf(NCIRed)[0].Kind, "ci notes: %v", w.notes)
	require.Len(t, w.notesOf(NCIGreen), 1, "ci notes: %v", w.notes)
	require.Equal(t, Happened, w.notesOf(NCIGreen)[0].Kind, "ci notes: %v", w.notes)
}

func TestAddRefusesWhatExists(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	p := Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-1", "new", "new", "ctl-x", "a.b"}})
	require.Len(t, p.Units, 1, "add: units %d refused %v", len(p.Units), p.Refused)
	require.Len(t, p.Refused, 4, "add: units %d refused %v", len(p.Units), p.Refused)
	ids := AddIDs(w.s, AddReq{Stream: "s1", Count: 2})
	require.Equal(t, "s1-3", ids[0], "generated %v", ids)
	require.Equal(t, "s1-4", ids[1], "generated %v", ids)
}

func TestCheckFindsEveryBrokenRule(t *testing.T) {
	t.Parallel()
	w := setup(t, 3)
	accepted(w, "s1-1")
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-2"}}}))
	w.clean("before")
	s := w.s
	s.Fleet.Card("s1-2.w1").Row, s.Fleet.Card("s1-2.w1").Col = "", "" // lost work card: rules 2 and 8
	s.Merge.Card("s1-1").Col = Merged                                 // merged but not landed: rules 4 and 5
	s.Readers.Put(&Card{ID: "s1-3.r1.reader-a", Row: "reader-a", Col: Asked, Score: s.Work.Card("s1-3").Score,
		Fields: map[string]string{"primary": "s1-3", "reader": "reader-a", "attempt": "1"}}) // rule 3
	s.StreamCtl("s1").Fields["state"] = StreamStopped // rule 9
	s.Work.Card("s1-1").Fields["head"] = "other"      // rule 6
	for _, tb := range []*Table{s.Work, s.Readers, s.Merge, s.Fleet} {
		tb.cells, tb.byPrimary = nil, nil
	}
	got := map[int]bool{}
	for _, v := range Check(s, nil) {
		got[v.Rule] = true
	}
	for _, r := range []int{2, 3, 4, 5, 6, 9} {
		assert.True(t, got[r], "rule %d not found; got %v", r, Check(s, nil))
	}
}

func TestInboxJudgmentFirstMarkedFirstOverdueAtReadTime(t *testing.T) {
	t.Parallel()
	now := t0.Add(time.Hour)
	open := []Open{
		{Key: "n1|a", Note: Note{ID: "n1", Kind: Judgment, Type: NWorkFailed, Stream: "s1", At: t0.Add(50 * time.Minute)}},
		{Key: "n1|b", Note: Note{ID: "n1", Kind: Judgment, Type: NWorkFailed, Stream: "s1", At: t0.Add(50 * time.Minute)}},
		{Key: "n2|c", Note: Note{ID: "n2", Kind: Judgment, Type: NReadBroken, Stream: "s1", At: t0.Add(55 * time.Minute), Marked: true}},
		{Key: "n3|d", Note: Note{ID: "n3", Kind: Judgment, Type: NWorkFailed, Stream: "s2", At: t0}},
	}
	recent := []Note{{ID: "n4", Kind: Happened, Type: NWorkOK, Stream: "s1", Primaries: []string{"x", "y"}, Count: 2, At: t0}}
	g := Inbox(InboxReq{Now: now, Open: open, Recent: recent, Deadline: 30 * time.Minute, Stale: 10 * time.Minute,
		Streams: []StreamClock{{Stream: "s3", State: StreamMerging, Since: t0, Progress: t0}}})
	require.Len(t, g, 5, "groups: %+v", g)
	require.True(t, g[0].Marked, "marked not first: %+v", g)
	require.True(t, g[1].Marked, "marked not first: %+v", g)
	require.False(t, g[2].Marked, "marked not first: %+v", g)
	require.True(t, g[0].Overdue, "the overdue judgment is not first: %+v", g[0])
	require.Equal(t, "s2", g[0].Stream, "the overdue judgment is not first: %+v", g[0])
	require.Equal(t, 2, g[2].Count, "grouping: %+v", g)
	require.Equal(t, NStreamStale, g[3].Type, "grouping: %+v", g)
	require.Equal(t, Happened, g[4].Kind, "grouping: %+v", g)
	require.Equal(t, 2, g[4].Count, "grouping: %+v", g)
}

func TestMergeNotesGroupsOneTypeStreamAndCause(t *testing.T) {
	t.Parallel()
	a := happened(NWorkOK, "s1", t0, "p1")
	b := happened(NWorkOK, "s1", t0, "p2")
	c := happened(NWorkOK, "s2", t0, "p3")
	got := MergeNotes([]Note{a, b, c})
	require.Len(t, got, 2, "merged: %+v", got)
	require.Equal(t, 2, got[0].Count, "merged: %+v", got)
	require.Len(t, got[0].Primaries, 2, "merged: %+v", got)
}

// G5: a plan that moves a primary outside the lifecycle is refused at run
// time, not only in a test of the table.
func TestLawfulRefusesAMoveOutsideTheLifecycle(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	c := w.s.Work.Card("s1-1")
	p := Lawful(Plan{Units: []Unit{
		{Key: "jump", Changes: []Change{change(Work, moveEntry(c, c.Row, Landed, nil))}},
		{Key: "fine", Changes: []Change{change(Work, moveEntry(c, c.Row, Working, nil))}},
	}})
	require.Len(t, p.Units, 1, "lawful: %+v", p)
	require.Equal(t, "fine", p.Units[0].Key, "lawful: %+v", p)
	require.Len(t, p.Refused, 1, "lawful: %+v", p)
	require.Contains(t, p.Refused[0].Why, "ready -> landed", "lawful: %+v", p)
	landed := &Card{ID: "x", Row: "s1", Col: Landed, Rev: 1, Fields: map[string]string{}}
	p = Lawful(Plan{Units: []Unit{{Key: "x", Changes: []Change{change(Work, removeEntry(landed, nil))}}}})
	require.Empty(t, p.Units, "a landed primary taken off the table: %+v", p)
}

// A subject is listed once in a merged note, and counted once.
func TestMergeNotesListsASubjectOnce(t *testing.T) {
	t.Parallel()
	a := happened(NWorkOK, "s1", t0, "p1")
	got := MergeNotes([]Note{a, a, happened(NWorkOK, "s1", t0, "p2")})
	require.Len(t, got, 1, "merged: %+v", got)
	require.Equal(t, 2, got[0].Count, "merged: %+v", got)
	require.Len(t, got[0].Primaries, 2, "merged: %+v", got)
}

// No field of a table gives its cards or its rows: a read of either goes
// through the methods (Cards, Rows, Cell, Column), and a card is written
// through Put and Drop, which reset the table's index of cells and primaries.
func TestNoExportedFieldOfATableGivesItsCardsOrRows(t *testing.T) {
	t.Parallel()
	typ := reflect.TypeOf(Table{})
	var exported []string
	for i := 0; i < typ.NumField(); i++ {
		if f := typ.Field(i); f.IsExported() {
			exported = append(exported, f.Name)
		}
	}
	want := []string{"Name", "Epoch", "Revision", "Texts"}
	require.Equal(t, want, exported, "the exported fields of a table are %v, want %v: a card or a row would be read past the index", exported, want)
}
