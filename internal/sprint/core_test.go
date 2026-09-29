package sprint

import (
	"strings"
	"testing"
	"time"
)

func TestLifecycleIsTheSpecTable(t *testing.T) {
	t.Parallel()
	legal := map[[2]State]bool{
		{Waiting, Ready}: true, {Ready, Working}: true, {Working, Review}: true, {Working, Ready}: true,
		{Review, Merging}: true, {Review, Working}: true, {Review, Ready}: true, {Merging, Review}: true, {Merging, Landed}: true,
	}
	for _, a := range States {
		for _, b := range States {
			if got := Legal(a, b); got != legal[[2]State{a, b}] {
				t.Errorf("Legal(%s, %s) = %v", a, b, got)
			}
		}
	}
	if len(Moves) != len(legal) {
		t.Errorf("%d moves, the spec has %d", len(Moves), len(legal))
	}
	for _, s := range States {
		if IsOpen(s) == (s == Landed) {
			t.Errorf("IsOpen(%s) = %v", s, IsOpen(s))
		}
	}
}

func TestIdentitiesRoundTrip(t *testing.T) {
	t.Parallel()
	if p, n, ok := ParseWorkCard(WorkCardID("s1-7", 3)); !ok || p != "s1-7" || n != 3 {
		t.Errorf("work card: %s %d %v", p, n, ok)
	}
	if p, n, r, ok := ParseReadCard(ReadCardID("s1-7", 2, "reader-a")); !ok || p != "s1-7" || n != 2 || r != "reader-a" {
		t.Errorf("read card: %s %d %s %v", p, n, r, ok)
	}
	for _, bad := range []string{"", "a.b", "a b", "-x", strings.Repeat("x", 129)} {
		if ValidID(bad) {
			t.Errorf("ValidID(%q) = true", bad)
		}
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
	if got := w.state("s1-1"); got != Ready {
		t.Fatalf("admitted as %s", got)
	}
	w.must(Start(w.s, StartReq{Sel: Sel{Limit: 4}}))
	if w.s.Fleet.Count("m1", Ready) != 2 || w.s.Fleet.Count("m2", Ready) != 2 {
		t.Fatalf("not dealt to the shortest queues: m1=%d m2=%d", w.s.Fleet.Count("m1", Ready), w.s.Fleet.Count("m2", Ready))
	}
	w.clean("start")
	w.must(Take(w.s, TakeReq{As: "m1", Sel: Sel{Limit: 10}}))
	w.must(Take(w.s, TakeReq{As: "m2", Sel: Sel{Limit: 10}}))
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1", "s1-2.w1", "s1-3.w1"}}}))
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-4.w1"}}, Failed: true, Report: "tests red"}))
	w.clean("finish")
	if len(w.notesOf(NWorkOK)) != 3 || len(w.notesOf(NWorkFailed)) != 1 {
		t.Fatalf("notes: %d ok, %d failed", len(w.notesOf(NWorkOK)), len(w.notesOf(NWorkFailed)))
	}
	if len(w.openOn("s1-4")) != 1 {
		t.Fatalf("failed work is not an open judgment: %v", w.s.Open)
	}
	if ctl := w.s.MemberCtl("m2"); ctl.F("failed") != "1" && w.s.MemberCtl("m1").F("failed") != "1" {
		t.Errorf("failed count not bumped on the member's control card")
	}

	ask := w.must(Ask(w.s, AskReq{}))
	if len(ask.Units) != 3 {
		t.Fatalf("ask dealt %d primaries, want the 3 that came back ok (failed work is not read)", len(ask.Units))
	}
	for _, id := range []string{"s1-1", "s1-2", "s1-3"} {
		reads := readsAt(w.s, w.s.Work.Card(id), 1)
		if len(reads) != 2 || reads[0].F("reader") == reads[1].F("reader") {
			t.Fatalf("%s asked of %d readers: %v", id, len(reads), reads)
		}
	}
	w.clean("ask")

	// One reader's ok is never enough.
	first := readsAt(w.s, w.s.Work.Card("s1-1"), 1)
	w.must(Read(w.s, ReadReq{As: first[0].F("reader"), Verdict: "ok", Sel: Sel{IDs: []string{first[0].ID}}}))
	acc := Accept(w.s, AcceptReq{Sel: Sel{IDs: []string{"s1-1"}}})
	if len(acc.Units) != 0 || len(acc.Refused) != 1 || !strings.Contains(acc.Refused[0].Why, "two different readers") {
		t.Fatalf("accept with one ok: %+v", acc)
	}
	w.must(Read(w.s, ReadReq{As: first[1].F("reader"), Verdict: "ok", Sel: Sel{IDs: []string{first[1].ID}}}))
	if len(w.notesOf(NReadyToAccept)) != 1 {
		t.Fatalf("ready-to-accept notes: %d", len(w.notesOf(NReadyToAccept)))
	}
	w.must(Accept(w.s, AcceptReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	if w.state("s1-1") != Merging || w.s.Merge.Placed("s1-1").Col != Queued {
		t.Fatalf("accept: work %s", w.state("s1-1"))
	}
	if w.s.Merge.Placed("s1-1").Score != w.s.Work.Card("s1-1").Score {
		t.Fatalf("the merge place does not carry the primary's score")
	}
	w.clean("accept")

	// A broken read, rework with the finding; the fixed work is asked of the same readers again.
	second := readsAt(w.s, w.s.Work.Card("s1-2"), 1)
	w.must(Read(w.s, ReadReq{As: second[0].F("reader"), Verdict: "broken", Finding: "off by one", Sel: Sel{IDs: []string{second[0].ID}}}))
	if len(w.openOn("s1-2")) != 1 {
		t.Fatalf("a broken read is not an open judgment")
	}
	score := w.s.Work.Card("s1-2").Score
	w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-2"}}, Fix: "off by one"}))
	if w.state("s1-2") != Working || len(w.openOn("s1-2")) != 0 || len(w.s.Readers.Of("s1-2")) != 0 {
		t.Fatalf("rework: %s, open %v, read cards %d", w.state("s1-2"), w.openOn("s1-2"), len(w.s.Readers.Of("s1-2")))
	}
	if w.s.Work.Card("s1-2").Score != score {
		t.Fatalf("rework changed the score")
	}
	w.clean("rework")
	card := w.s.Work.Card("s1-2").F("work")
	if card != "s1-2.w2" || w.s.Fleet.Card(card).F("fix") != "off by one" {
		t.Fatalf("the next attempt's card is %s", card)
	}
	member := w.s.Fleet.Card(card).Row
	w.must(Take(w.s, TakeReq{As: member, Sel: Sel{IDs: []string{card}}}))
	w.must(Finish(w.s, FinishReq{As: member, Sel: Sel{IDs: []string{card}}}))
	again := readsAt(w.s, w.s.Work.Card("s1-2"), 2)
	if len(again) != 2 || again[0].F("reader") != second[0].F("reader") && again[1].F("reader") != second[0].F("reader") {
		t.Fatalf("fixed work not asked of the same readers: %v", again)
	}
	w.clean("fixed work returned")

	// Merge the one accepted primary; the stream is not landed until all are.
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 10}))
	if w.state("s1-1") != Landed || w.s.Merge.Placed("s1-1").Col != Merged {
		t.Fatalf("merge: %s", w.state("s1-1"))
	}
	if len(w.notesOf(NStartedMerging)) != 1 || len(w.notesOf(NBatchLanded)) != 1 || len(w.notesOf(NStreamLanded)) != 0 {
		t.Fatalf("merge notes: started %d batch %d landed %d", len(w.notesOf(NStartedMerging)), len(w.notesOf(NBatchLanded)), len(w.notesOf(NStreamLanded)))
	}
	w.clean("merge")
}

func TestScoresAreCopiedAndOnlyRankChangesThem(t *testing.T) {
	t.Parallel()
	w := setup(t, 3)
	w.must(Start(w.s, StartReq{Sel: Sel{Stream: "s1"}}))
	c := w.s.Fleet.Card("s1-2.w1")
	if c.Score != w.s.Work.Card("s1-2").Score {
		t.Fatalf("the work card does not copy the score")
	}
	sc := -5.0
	p := w.must(Rank(w.s, RankReq{IDs: []string{"s1-2"}, Score: &sc}))
	if len(p.Units[0].Changes) != 2 || w.s.Fleet.Card("s1-2.w1").Score != -5 || w.s.Work.Card("s1-2").Score != -5 {
		t.Fatalf("rank did not change every copy: %+v", p.Units[0].Changes)
	}
	w.clean("rank")
	w.s.Fleet.Card("s1-2.w1").Score = 9
	if v := Check(w.s, nil); len(v) != 1 || v[0].Rule != 7 {
		t.Fatalf("a copy with another score: %v", v)
	}
}

func TestFleetDownDealsAndWithdrawsWhenNoneIsUp(t *testing.T) {
	t.Parallel()
	w := setup(t, 4)
	w.must(Start(w.s, StartReq{Sel: Sel{Limit: 4}}))
	w.must(Take(w.s, TakeReq{As: "m1", Sel: Sel{Limit: 1}}))
	w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m1"}))
	if w.s.Fleet.Count("m1", Ready)+w.s.Fleet.Count("m1", Working) != 0 || w.s.Fleet.Count("m2", Ready) != 4 {
		t.Fatalf("down did not deal m1's unfinished cards to m2")
	}
	w.clean("down")
	w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m2"}))
	for i := 1; i <= 4; i++ {
		id := "s1-" + itoa(i)
		if w.state(id) != Ready {
			t.Fatalf("%s is %s after the last member went down", id, w.state(id))
		}
	}
	if len(w.notesOf(NWithdrawn)) != 4 || len(w.notesOf(NMemberDown)) != 2 {
		t.Fatalf("notes: withdrawn %d down %d", len(w.notesOf(NWithdrawn)), len(w.notesOf(NMemberDown)))
	}
	w.clean("withdrawn")
	p := Start(w.s, StartReq{Sel: Sel{Limit: 1}})
	if len(p.Units) != 0 || len(p.Refused) != 1 {
		t.Fatalf("start with nobody up: %+v", p)
	}
	// Up again: the next attempt's card is cut.
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	w.must(Start(w.s, StartReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	if w.s.Work.Card("s1-1").F("work") != "s1-1.w2" {
		t.Fatalf("after withdrawal the next card is %s", w.s.Work.Card("s1-1").F("work"))
	}
	w.clean("up again")
}

func TestLevelMovesTheNewestCards(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a", "reader-b")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	w.must(Add(w.s, AddReq{Stream: "s1", Count: 6}))
	w.must(Start(w.s, StartReq{Sel: Sel{Limit: 6}}))
	p := w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m2"}))
	if w.s.Fleet.Count("m1", Ready) != 3 || w.s.Fleet.Count("m2", Ready) != 3 {
		t.Fatalf("not levelled: %d %d", w.s.Fleet.Count("m1", Ready), w.s.Fleet.Count("m2", Ready))
	}
	for _, c := range w.s.Fleet.Cell("m2", Ready) {
		if c.Score < 4 {
			t.Fatalf("an older card moved: %s (%v); units %v", c.ID, c.Score, p.Units)
		}
	}
	w.clean("level")
}

func TestMergeFactsStopTheStreamAndResumeMovesIt(t *testing.T) {
	t.Parallel()
	w := setup(t, 3)
	accepted(w, "s1-1", "s1-2", "s1-3")
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 2, Conflict: "s1-2"}))
	ctl := w.s.StreamCtl("s1")
	if ctl.F("state") != StreamStopped || w.s.Merge.Placed("s1-2").Col != Stuck || w.state("s1-2") != Merging {
		t.Fatalf("conflict: state %s", ctl.F("state"))
	}
	if len(w.openOn(StreamSubject("s1"))) != 1 {
		t.Fatalf("a stopped stream with no open judgment")
	}
	w.clean("stopped")
	if p := MergeStep(w.s, MergeReq{Stream: "s1", Batch: 2}); len(p.Units) != 0 || len(p.Refused) != 1 {
		t.Fatalf("a stopped stream merged: %+v", p)
	}
	// Answering the stream's judgment elsewhere leaves it open: rule 9.
	p := Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-2"}}, Fix: "x", Answers: []string{w.openOn(StreamSubject("s1"))[0].Note.ID}})
	if len(p.Refused) != 2 || len(p.Closes) != 0 || len(p.Units) != 0 {
		t.Fatalf("rework of a merging primary: %+v", p)
	}
	w.must(Resume(w.s, ResumeReq{Stream: "s1", Did: "rebased"}))
	if w.s.StreamCtl("s1").F("state") != StreamMerging || w.s.Merge.Placed("s1-2").Col != Queued || len(w.openOn(StreamSubject("s1"))) != 0 {
		t.Fatalf("resume did not move the stream")
	}
	w.clean("resumed")
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 10}))
	if w.s.StreamCtl("s1").F("state") != StreamLanded || len(w.notesOf(NStreamLanded)) != 1 {
		t.Fatalf("stream not landed: %s", w.s.StreamCtl("s1").F("state"))
	}
	w.clean("landed")

	// Red stops the stream the same way; the second stuck is marked.
	w2 := setup(t, 2)
	accepted(w2, "s1-1", "s1-2")
	w2.must(MergeStep(w2.s, MergeReq{Stream: "s1", Red: true}))
	if w2.s.StreamCtl("s1").F("cause") != "red" || len(w2.notesOf(NRed)) != 1 || w2.notesOf(NRed)[0].Count != 2 {
		t.Fatalf("red: %v", w2.notesOf(NRed))
	}
	w2.must(Resume(w2.s, ResumeReq{Stream: "s1"}))
	w2.must(MergeStep(w2.s, MergeReq{Stream: "s1", Conflict: "s1-1"}))
	w2.must(Resume(w2.s, ResumeReq{Stream: "s1"}))
	w2.must(MergeStep(w2.s, MergeReq{Stream: "s1", Conflict: "s1-1"}))
	last := w2.notesOf(NConflict)
	if !last[len(last)-1].Marked || !contains(last[len(last)-1].Decisions, RepeatDecision) {
		t.Fatalf("a second stuck for the same cause is not marked: %+v", last[len(last)-1])
	}
}

// accepted drives primaries of s1 to merging queued.
func accepted(w *world, ids ...string) {
	w.t.Helper()
	w.must(Start(w.s, StartReq{Sel: Sel{IDs: ids}}))
	for _, id := range ids {
		c := w.s.Fleet.Card(w.s.Work.Card(id).F("work"))
		w.must(Take(w.s, TakeReq{As: c.Row, Sel: Sel{IDs: []string{c.ID}}}))
		w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{c.ID}}}))
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
	if w.state("s1-1") != Review || w.s.Merge.Placed("s1-1").Col != Returned {
		t.Fatalf("return: %s", w.state("s1-1"))
	}
	w.clean("returned")
	p := w.must(Accept(w.s, AcceptReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	var merge Change
	for _, c := range p.Units[0].Changes {
		if c.Table == Merge {
			merge = c
		}
	}
	if merge.Entry.Move == nil || merge.Entry.Create != nil {
		t.Fatalf("accepting a returned primary creates instead of moving: %+v", merge.Entry)
	}
	w.clean("accepted again")
}

func TestDropTakesItsCardsAndBlocksWhatNeedsIt(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"later"}, Needs: []string{"s1-1"}}))
	if w.state("later") != Waiting {
		t.Fatalf("a primary with needs is %s", w.state("later"))
	}
	w.must(Start(w.s, StartReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "obsolete"}))
	if w.state("s1-1") != "" || w.s.Work.Card("s1-1").F("outcome") != "dropped" || w.s.Fleet.Placed("s1-1.w1") != nil {
		t.Fatalf("drop left cards behind")
	}
	if len(w.notesOf(NBlocked)) != 1 || len(w.openOn("later")) != 1 {
		t.Fatalf("the waiting primary is not reported blocked")
	}
	w.clean("dropped")
	// resolve does not report it twice
	w.must(Resolve(w.s, ResolveReq{}))
	if len(w.notesOf(NBlocked)) != 1 {
		t.Fatalf("blocked reported again")
	}
	if p := Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "x"}); len(p.Refused) != 1 {
		t.Fatalf("dropped twice: %+v", p)
	}
}

func TestResolveMovesWhenNeedsLand(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"b"}, Needs: []string{"s1-1"}}))
	if p := w.must(Resolve(w.s, ResolveReq{})); len(p.Units) != 0 {
		t.Fatalf("resolved before the need landed")
	}
	accepted(w, "s1-1")
	w.must(MergeStep(w.s, MergeReq{Stream: "s1"}))
	w.must(Resolve(w.s, ResolveReq{}))
	if w.state("b") != Ready {
		t.Fatalf("b is %s", w.state("b"))
	}
	w.clean("resolved")
}

func TestCIResultIsAlwaysANotification(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.must(RecordCI(w.s, CIReq{Sel: Sel{IDs: []string{"s1-1"}}, Red: true}))
	w.must(RecordCI(w.s, CIReq{Sel: Sel{IDs: []string{"s1-2"}}}))
	if len(w.notesOf(NCIRed)) != 1 || w.notesOf(NCIRed)[0].Kind != Judgment || len(w.notesOf(NCIGreen)) != 1 || w.notesOf(NCIGreen)[0].Kind != Happened {
		t.Fatalf("ci notes: %v", w.notes)
	}
}

func TestAddRefusesWhatExists(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	p := Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-1", "new", "new", "ctl-x", "a.b"}})
	if len(p.Units) != 1 || len(p.Refused) != 4 {
		t.Fatalf("add: units %d refused %v", len(p.Units), p.Refused)
	}
	if ids := AddIDs(w.s, AddReq{Stream: "s1", Count: 2}); ids[0] != "s1-3" || ids[1] != "s1-4" {
		t.Fatalf("generated %v", ids)
	}
}

func TestCheckFindsEveryBrokenRule(t *testing.T) {
	t.Parallel()
	w := setup(t, 3)
	accepted(w, "s1-1")
	w.must(Start(w.s, StartReq{Sel: Sel{IDs: []string{"s1-2"}}}))
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
		if !got[r] {
			t.Errorf("rule %d not found; got %v", r, Check(s, nil))
		}
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
	if len(g) != 5 {
		t.Fatalf("groups: %+v", g)
	}
	if !g[0].Marked || !g[1].Marked || g[2].Marked {
		t.Fatalf("marked not first: %+v", g)
	}
	if !g[0].Overdue || g[0].Stream != "s2" {
		t.Fatalf("the overdue judgment is not first: %+v", g[0])
	}
	if g[2].Count != 2 || g[3].Type != NStreamStale || g[4].Kind != Happened || g[4].Count != 2 {
		t.Fatalf("grouping: %+v", g)
	}
}

func TestMergeNotesGroupsOneTypeStreamAndCause(t *testing.T) {
	t.Parallel()
	a := happened(NWorkOK, "s1", t0, "p1")
	b := happened(NWorkOK, "s1", t0, "p2")
	c := happened(NWorkOK, "s2", t0, "p3")
	got := MergeNotes([]Note{a, b, c})
	if len(got) != 2 || got[0].Count != 2 || len(got[0].Primaries) != 2 {
		t.Fatalf("merged: %+v", got)
	}
}
