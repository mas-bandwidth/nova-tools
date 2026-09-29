package sprint

import (
	"strings"
	"testing"
	"time"
)

// One test per decision D1..D8 on the spec (docs/SPEC-SPRINT.md). The fence
// itself (D1) is the store binding's; here, what the core judges of it.

// D1: while an operation is pending, the rules that hold only when none is
// (2, 3, 4, 5, 7, 9) are not judged; the rules that always hold (1, 6, 8) are.
func TestD1PendingOperationSuspendsOnlyTheQuietRules(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.must(Start(w.s, StartReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	// The fleet phase of a start applied, the work phase not yet: a card dealt,
	// its primary still ready.
	pr := w.s.Work.Card("s1-2")
	w.s.Fleet.Put(&Card{ID: "s1-2.w1", Row: "m1", Col: Ready, Score: pr.Score, Rev: 1,
		Fields: map[string]string{"primary": "s1-2", "attempt": "1", "gen": "1"}})
	if v := Check(w.s, nil); len(v) == 0 || v[0].Rule != 2 {
		t.Fatalf("with no operation pending the partial state is a violation: %v", v)
	}
	v := Check(w.s, []string{"start-x-1"})
	if len(v) != 1 || v[0].Rule != 10 || !strings.Contains(v[0].Detail, "pending") {
		t.Fatalf("with the start pending: %v", v)
	}
	// Rule 8 always: a second live work card for one primary.
	w.s.Fleet.Put(&Card{ID: "s1-2.w9", Row: "m2", Col: Ready, Score: pr.Score, Rev: 1,
		Fields: map[string]string{"primary": "s1-2", "attempt": "9", "gen": "1"}})
	found := false
	for _, x := range Check(w.s, []string{"start-x-1"}) {
		found = found || x.Rule == 8
	}
	if !found {
		t.Fatalf("rule 8 is not judged while an operation is pending")
	}
}

// D2: rework delegates at once; with no member up it goes to ready; the fixed
// work is asked of the same readers at the new head; a report against a
// retired read card is refused naming the retirement.
func TestD2ReworkDelegatesAtOnce(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.must(Start(w.s, StartReq{Sel: Sel{IDs: []string{"s1-1", "s1-2"}}}))
	for _, id := range []string{"s1-1", "s1-2"} {
		c := w.s.Fleet.Card(w.s.Work.Card(id).F("work"))
		w.must(Take(w.s, TakeReq{As: c.Row, Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
		w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
	}
	w.must(Ask(w.s, AskReq{}))
	reads := readsAt(w.s, w.s.Work.Card("s1-1"), 1)
	w.must(Read(w.s, ReadReq{As: reads[0].F("reader"), Verdict: "broken", Sel: Sel{IDs: []string{reads[0].ID}}}))
	w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "the fix"}))
	pr := w.s.Work.Card("s1-1")
	card := w.s.Fleet.Placed(pr.F("work"))
	if pr.Col != Working || card == nil || card.ID != "s1-1.w2" || card.Col != Ready || card.F("fix") != "the fix" || card.Score != pr.Score {
		t.Fatalf("rework did not delegate at once: primary %s card %+v", pr.Col, card)
	}
	if pr.F("asked") != reads[0].F("reader")+","+reads[1].F("reader") {
		t.Fatalf("the readers are not kept: %q", pr.F("asked"))
	}
	w.clean("delegated")
	// A report against the retired card is refused, naming the retirement.
	late := Read(w.s, ReadReq{As: reads[1].F("reader"), Verdict: "ok", Sel: Sel{IDs: []string{reads[1].ID}}})
	if len(late.Units) != 0 {
		t.Fatalf("a report against a retired card moved: %+v", late)
	}
	w.s.Readers.Put(w.s.Readers.Card(reads[1].ID)) // the loader reads the retired record by name
	late = Read(w.s, ReadReq{As: reads[1].F("reader"), Verdict: "ok", Sel: Sel{IDs: []string{reads[1].ID}}})
	if len(late.Refused) != 1 || !strings.Contains(late.Refused[0].Why, "retired") {
		t.Fatalf("the refusal does not name the retirement: %+v", late.Refused)
	}
	// The fixed work returns: both readers asked again at the new head.
	w.must(Take(w.s, TakeReq{As: card.Row, Sel: Sel{IDs: []string{card.ID}}, Gens: gensOf(w.s, card.ID)}))
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{card.ID}}, Gens: gensOf(w.s, card.ID), Head: "h2"}))
	again := readsAt(w.s, w.s.Work.Card("s1-1"), 2)
	if len(again) != 2 || again[0].F("head") != "h2" || again[1].F("head") != "h2" {
		t.Fatalf("not asked again at the new head: %v", again)
	}
	w.clean("asked again")

	// No member up: review -> ready with the fix, no card.
	w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m1"}))
	w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m2"}))
	r2 := readsAt(w.s, w.s.Work.Card("s1-2"), 1)
	w.must(Read(w.s, ReadReq{As: r2[0].F("reader"), Verdict: "broken", Sel: Sel{IDs: []string{r2[0].ID}}}))
	w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-2"}}, Fix: "later"}))
	if w.state("s1-2") != Ready || w.s.Fleet.Card("s1-2.w2") != nil || w.s.Work.Card("s1-2").F("fix") != "later" {
		t.Fatalf("rework with nobody up: %s", w.state("s1-2"))
	}
	w.clean("ready with the fix")
}

// D3: every redeal, drain, level move and withdrawal changes the card's
// generation; a finish naming a generation that is not the live one is
// refused and changes nothing; a finish that arrives first keeps the card out
// of redistribution.
func TestD3AssignmentGeneration(t *testing.T) {
	t.Parallel()
	w := setup(t, 3)
	w.must(Start(w.s, StartReq{Sel: Sel{IDs: []string{"s1-1", "s1-2", "s1-3"}}}))
	c1 := w.s.Fleet.Card("s1-1.w1")
	if c1.F("gen") != "1" || c1.F("member") != c1.Row {
		t.Fatalf("a dealt card has generation %q member %q", c1.F("gen"), c1.F("member"))
	}
	m := c1.Row
	w.must(Take(w.s, TakeReq{As: m, Sel: Sel{IDs: []string{"s1-1.w1"}}, Gens: map[string]int{"s1-1.w1": 1}}))
	// the member goes down: its card is drained to the other member, generation 2
	w.must(FleetStep(w.s, FleetReq{Op: "down", Member: m}))
	c1 = w.s.Fleet.Card("s1-1.w1")
	if c1.F("gen") != "2" || c1.Row == m || c1.F("member") != c1.Row {
		t.Fatalf("drained card: gen %s at %s", c1.F("gen"), c1.Row)
	}
	// the old holder's finish is stale
	late := Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, Gens: map[string]int{"s1-1.w1": 1}})
	if len(late.Units) != 0 || len(late.Refused) != 1 || !strings.Contains(late.Refused[0].Why, "stale") {
		t.Fatalf("a stale finish: %+v", late)
	}
	// the new holder takes it at generation 2 and finishes
	w.must(Take(w.s, TakeReq{As: c1.Row, Sel: Sel{IDs: []string{"s1-1.w1"}}, Gens: map[string]int{"s1-1.w1": 2}}))
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, Gens: map[string]int{"s1-1.w1": 2}}))
	// a finish that arrived first is not redistributed
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: m}))
	other := w.s.Fleet.Card("s1-1.w1").Row
	w.must(FleetStep(w.s, FleetReq{Op: "down", Member: other}))
	if c := w.s.Fleet.Card("s1-1.w1"); c.Col != Done || c.Row != other || c.F("gen") != "2" {
		t.Fatalf("a done card was redistributed: %+v", c)
	}
	// a level move and a withdrawal change the generation too
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: other}))
	for _, c := range w.s.Fleet.Column(Ready) {
		if c.Int("gen") < 1 {
			t.Fatalf("no generation on %s", c.ID)
		}
	}
	before := map[string]int{}
	for _, c := range w.s.Fleet.Column(Ready, Working) {
		before[c.ID] = c.Int("gen")
	}
	w.must(FleetStep(w.s, FleetReq{Op: "down", Member: m}))
	w.must(FleetStep(w.s, FleetReq{Op: "down", Member: other}))
	for id, g := range before {
		if c := w.s.Fleet.Card(id); c.Col != Withdrawn || c.Int("gen") <= g {
			t.Fatalf("withdrawn %s: at %s gen %d (was %d)", id, placeWord(c), c.Int("gen"), g)
		}
	}
	w.clean("withdrawn")
	// Rule 2 is a bijection: two live cards for one working primary is caught.
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: m}))
	w.must(Start(w.s, StartReq{Sel: Sel{Limit: 1}}))
	live := w.s.Fleet.Column(Ready)[0]
	w.s.Fleet.Put(&Card{ID: live.F("primary") + ".w9", Row: m, Col: Ready, Score: live.Score, Rev: 1, Fields: map[string]string{"primary": live.F("primary")}})
	var rules []int
	for _, v := range Check(w.s, nil) {
		rules = append(rules, v.Rule)
	}
	if len(rules) == 0 || rules[0] != 2 {
		t.Fatalf("two live cards for one working primary: %v", Check(w.s, nil))
	}
}

// D4: named ids are all or nothing; a selection moves the eligible.
func TestD4AcceptNamedIsAllOrNothing(t *testing.T) {
	t.Parallel()
	w := setup(t, 3)
	w.must(Start(w.s, StartReq{Sel: Sel{Limit: 3}}))
	for _, c := range w.s.Fleet.Column(Ready) {
		w.must(Take(w.s, TakeReq{As: c.Row, Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
		w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
	}
	w.must(Ask(w.s, AskReq{}))
	for _, id := range []string{"s1-1", "s1-2"} {
		for _, rc := range readsAt(w.s, w.s.Work.Card(id), 1) {
			w.must(Read(w.s, ReadReq{As: rc.F("reader"), Verdict: "ok", Sel: Sel{IDs: []string{rc.ID}}}))
		}
	}
	p := Accept(w.s, AcceptReq{Sel: Sel{IDs: []string{"s1-1", "s1-2", "s1-3"}}})
	if len(p.Units) != 0 || len(p.Refused) != 3 {
		t.Fatalf("named set with one ineligible: %+v", p)
	}
	for _, r := range p.Refused {
		if r.Key == "s1-3" && !strings.Contains(r.Why, "two different readers") || r.Key != "s1-3" && !strings.Contains(r.Why, "eligible, not moved") {
			t.Fatalf("refusal: %+v", r)
		}
	}
	p = w.do(Accept(w.s, AcceptReq{Sel: Sel{Stream: "s1"}}))
	if len(p.Units) != 2 || w.state("s1-1") != Merging || w.state("s1-2") != Merging || w.state("s1-3") != Review {
		t.Fatalf("a selection: %+v", p)
	}
}

// D5: a verb discharges only the per-card, per-cause obligations it resolved.
func TestD5AnswersDischargeOnlyWhatWasResolved(t *testing.T) {
	t.Parallel()
	w := setup(t, 3)
	w.must(Start(w.s, StartReq{Sel: Sel{Limit: 3}}))
	var ids []string
	for _, c := range w.s.Fleet.Column(Ready) {
		w.must(Take(w.s, TakeReq{As: c.Row, Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
		ids = append(ids, c.ID)
	}
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: ids}, Gens: gensOf(w.s, ids...), Failed: true}))
	w.must(RecordCI(w.s, CIReq{Sel: Sel{IDs: []string{"s1-1"}}, Red: true, Run: "r1"}))
	failed := w.openOn("s1-1")
	if len(failed) != 2 {
		t.Fatalf("open on s1-1: %v", failed)
	}
	// Accept is refused and resolves nothing; ci green resolves only the red.
	w.must(RecordCI(w.s, CIReq{Sel: Sel{IDs: []string{"s1-1"}}, Run: "r2"}))
	if o := w.openOn("s1-1"); len(o) != 1 || o[0].Note.Type != NWorkFailed {
		t.Fatalf("after green: %v", o)
	}
	// Rework of one card of a group of three leaves the other two open.
	nid := w.openOn("s1-2")[0].Note.ID
	w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-2"}}, Fix: "x", Answers: []string{nid}}))
	if len(w.openOn("s1-2")) != 0 || len(w.openOn("s1-3")) != 1 || len(w.openOn("s1-1")) != 1 {
		t.Fatalf("rework of one card closed others: %v", w.s.Open)
	}
	// Naming a judgment the verb does not resolve is refused by its id.
	p := Rank(w.s, RankReq{IDs: []string{"s1-3"}, Score: ptr(-1), Answers: []string{w.openOn("s1-3")[0].Note.ID}})
	if len(p.Refused) != 1 || !strings.Contains(p.Refused[0].Why, "resolves no obligation") {
		t.Fatalf("rank answering a failure: %+v", p.Refused)
	}
}

func ptr(f float64) *float64 { return &f }

// D6: a cross-stream need is data on the stuck card; resume is refused naming
// it until the needed card has landed (ranking it is not landing it); a stuck
// card is a barrier the merge step never passes.
func TestD6StuckCardsAndResume(t *testing.T) {
	t.Parallel()
	w := setup(t, 3)
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"b1"}}))
	accepted(w, "s1-1", "s1-2", "s1-3")
	score := w.s.Merge.Card("s1-2").Score
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Cross: "s1-2=b1"}))
	st := w.s.Merge.Card("s1-2")
	if st.Col != Stuck || st.F("need_card") != "b1" || st.F("need_stream") != "s2" {
		t.Fatalf("cross: %+v", st)
	}
	n := w.notesOf(NCross)[0]
	if !strings.Contains(n.What, "s1") || !strings.Contains(n.What, "s2") || !contains(n.Primaries, "b1") || !contains(n.Primaries, "s1-2") {
		t.Fatalf("the notification does not name both streams and cards: %+v", n)
	}
	p := Resume(w.s, ResumeReq{Stream: "s1"})
	if len(p.Units) != 0 || len(p.Refused) != 1 || !strings.Contains(p.Refused[0].Why, "b1") {
		t.Fatalf("resume with the need unresolved: %+v", p)
	}
	w.must(Rank(w.s, RankReq{IDs: []string{"b1"}, First: true}))
	if p := Resume(w.s, ResumeReq{Stream: "s1"}); len(p.Refused) != 1 {
		t.Fatalf("ranking the needed card resolved it: %+v", p)
	}
	accepted(w, "b1")
	w.must(MergeStep(w.s, MergeReq{Stream: "s2"}))
	w.must(Resume(w.s, ResumeReq{Stream: "s1", Did: "b1 landed"}))
	if c := w.s.Merge.Card("s1-2"); c.Col != Queued || c.Score != score || c.F("need_card") != "" {
		t.Fatalf("resumed card: %+v", c)
	}
	if w.s.StreamCtl("s1").F("state") != StreamMerging {
		t.Fatalf("stream not merging after resume")
	}
	w.clean("resumed")
	// The barrier: with a stuck card at s1-2, only the cards before it merge.
	w.s.Merge.Card("s1-2").Col = Stuck
	w.s.Merge.cells = nil
	p = MergeStep(w.s, MergeReq{Stream: "s1", Batch: 10})
	for _, u := range p.Units {
		if u.Key == "s1-3" {
			t.Fatalf("the merge step passed a stuck card: %+v", p.Units)
		}
	}
}

// D7: every open judgment is shown whatever the cursor, with its due time; a
// wait sets the review time without hiding it; a stream whose progress is
// older than the deadline is shown stalled.
func TestD7InboxDueTimesAndStalledStreams(t *testing.T) {
	t.Parallel()
	now := t0.Add(time.Hour)
	waited := Note{ID: "n2", Kind: Judgment, Type: NReadBroken, Stream: "s1", At: t0, Review: now.Add(time.Minute)}
	open := []Open{
		{Key: "n1|a", Note: Note{ID: "n1", Kind: Judgment, Type: NWorkFailed, Stream: "s1", At: t0}},
		{Key: "n2|b", Note: waited},
	}
	g := Inbox(InboxReq{Now: now, Open: open, Deadline: 10 * time.Minute, Stale: 10 * time.Minute, Streams: []StreamClock{
		{Stream: "s1", State: StreamWaiting, Since: t0, Progress: t0},
		{Stream: "s2", State: StreamWaiting, Since: t0, Progress: now},
		{Stream: "s3", State: StreamLanded, Since: t0, Progress: t0},
	}})
	if len(g) != 3 {
		t.Fatalf("groups: %+v", g)
	}
	if !g[0].Overdue || !g[0].Due.Equal(t0.Add(10*time.Minute)) {
		t.Fatalf("overdue judgment: %+v", g[0])
	}
	if g[1].Overdue || !g[1].Due.Equal(waited.Review) || g[1].Type != NReadBroken {
		t.Fatalf("a waited judgment is hidden or overdue: %+v", g[1])
	}
	if g[2].Type != NStreamStale || g[2].Stream != "s1" {
		t.Fatalf("stalled stream: %+v", g[2])
	}
}

// D8: ci records head, run, status and source on a primary in any state,
// always notifies, moves nothing, labels an old head, and records a run once.
func TestD8CIObservation(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.must(Start(w.s, StartReq{Sel: Sel{Limit: 1}}))
	p := w.must(RecordCI(w.s, CIReq{Sel: Sel{IDs: []string{"s1-1"}}, Red: true, Run: "run-7", Source: "ci", Head: "old"}))
	c := w.s.Work.Card("s1-1")
	if c.Col != Working || c.F("ci_run") != "run-7" || c.F("ci_source") != "ci" || c.F("ci_head") != "old" {
		t.Fatalf("ci fields: %+v", c.Fields)
	}
	if !strings.Contains(p.Units[0].Moved, "old head") || !strings.Contains(w.notesOf(NCIRed)[0].What, "old head") {
		t.Fatalf("an old head is not labelled: %s / %s", p.Units[0].Moved, w.notesOf(NCIRed)[0].What)
	}
	again := RecordCI(w.s, CIReq{Sel: Sel{IDs: []string{"s1-1"}}, Red: true, Run: "run-7"})
	if len(again.Units) != 0 || len(again.Refused) != 1 {
		t.Fatalf("a retried report recorded twice: %+v", again)
	}
}
