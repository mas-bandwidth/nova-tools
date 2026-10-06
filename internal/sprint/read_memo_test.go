package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A read is kept by its head (readers.go, FieldReadMemo; tla/ReadMemo.tla): a
// card entering review at a head that already holds a verdict at the same base
// and read tier inherits it instead of asking again. The night of 2026-10-05
// read 79 twins again at the head their old cards carried.

// finishedAt drives a primary of s1 whose next attempt is ready or dealt to review,
// its work ok at head on base.
func finishedAt(w *world, id, head, base string) {
	w.t.Helper()
	if w.s.Work.Card(id).Col == Ready {
		w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{id}}}))
	}
	c := w.s.Fleet.Card(w.s.Work.Card(id).F("work"))
	w.must(Take(w.s, TakeReq{As: c.Row, Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID), Head: head, Base: base, Report: "r"}))
}

// readsAsked is how many read cards of the primary stand asked or reading.
func readsAsked(w *world, id string) int {
	n := 0
	for _, rc := range w.s.Readers.Of(id) {
		if rc.Col == Asked || rc.Col == Reading {
			n++
		}
	}
	return n
}

// twinned replaces s1-1 by s1-3 (add --replaces), and drives the twin to review
// at head on base.
func twinned(w *world, head, base string) {
	w.t.Helper()
	w.must(Replace(w.s, AddReq{Stream: "s1", IDs: []string{"s1-3"}, Brief: proBrief, Replaces: []string{"s1-1"}}))
	finishedAt(w, "s1-3", head, base)
}

func TestAReadVerdictIsInheritedAtTheSameHead(t *testing.T) {
	t.Parallel()

	t.Run("a twin at the carried head inherits both ok reads and asks none", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 2)
		finishedAt(w, "s1-1", "h1", "sprint/b")
		readOK(w, "s1-1")
		require.Len(t, okReaders(w.s, w.s.Work.Card("s1-1")), 2)
		var readers []string
		for _, rc := range okReaders(w.s, w.s.Work.Card("s1-1")) {
			readers = append(readers, rc.Row)
			assert.Equal(t, "sprint/b", rc.F(FieldReadBase), "the ask stamps the attempt's base on its read")
		}
		twinned(w, "h1", "sprint/b")

		p := w.part(TickAsk, TickReq{})
		assert.Zero(t, readsAsked(w, "s1-3"), "a twin at its old card's head is asked no new read: %+v", p.Units)
		pr := w.s.Work.Card("s1-3")
		oks := okReaders(w.s, pr)
		require.Len(t, oks, 2)
		for _, rc := range oks {
			assert.Contains(t, readers, rc.Row)
			assert.Equal(t, "s1-1@1", rc.F(FieldInherited))
			assert.Equal(t, "h1", rc.F("head"))
		}
		require.Len(t, p.Units, 1)
		assert.Contains(t, p.Units[0].Moved, "read inherited from s1-1@1")
		assert.Len(t, readMemo(pr), 2, "the twin keeps what it inherited, for a twin of it")
		w.must(Accept(w.s, AcceptReq{Sel: Sel{IDs: []string{"s1-3"}}}))
		assert.Equal(t, Merging, w.state("s1-3"))
		w.clean("inherited")
	})

	t.Run("a twin with one ok kept inherits it and asks the second read alone", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 2)
		finishedAt(w, "s1-1", "h1", "sprint/b")
		w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}}))
		first := askedRead(w, "s1-1")
		reader := first.Row
		w.must(Read(w.s, ReadReq{Usage: "input=1000 output=100", As: reader, Verdict: "ok", Sel: Sel{IDs: []string{first.ID}}}))
		twinned(w, "h1", "sprint/b")

		p := w.part(TickAsk, TickReq{})
		pr := w.s.Work.Card("s1-3")
		require.Len(t, okReaders(w.s, pr), 1)
		assert.Equal(t, reader, okReaders(w.s, pr)[0].Row)
		second := askedRead(w, "s1-3")
		assert.NotEqual(t, reader, second.Row, "the second read is of a different reader")
		assert.Contains(t, p.Units[0].Moved, "read inherited from s1-1@1 ("+reader+" ok at h1); asked of "+second.Row)
	})

	t.Run("a rework at an unchanged head inherits the broken read as its finding", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 2)
		finishedAt(w, "s1-1", "h1", "sprint/b")
		w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}}))
		first := askedRead(w, "s1-1")
		finder := first.Row // the card itself moves on: it is retired by the rework
		w.must(Read(w.s, ReadReq{Usage: "input=1000 output=100", As: finder, Verdict: "broken", Finding: "a.go:1 off by one; fix the bound", Sel: Sel{IDs: []string{first.ID}}}))
		w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "the bound"}))
		finishedAt(w, "s1-1", "h1", "sprint/b")

		p := w.part(TickAsk, TickReq{})
		assert.Zero(t, readsAsked(w, "s1-1"), "an unchanged head is not read again: %+v", p.Units)
		rc := w.s.Readers.Placed(ReadCardID("s1-1", 2, finder))
		require.NotNil(t, rc)
		assert.Equal(t, Broken, rc.Col)
		assert.Equal(t, "s1-1@1", rc.F(FieldInherited))
		assert.Equal(t, "a.go:1 off by one; fix the bound", rc.F("finding"))
		var found bool
		for _, o := range w.openOn("s1-1") {
			found = found || (o.Note.Type == NReadBroken || o.Note.Type == NBriefWrong) && o.Note.Attempt == 2
		}
		assert.True(t, found, "the inherited finding is judged: %s", openTypes(w, "s1-1"))
	})

	t.Run("a new base, a new head or a weaker tier is read afresh", func(t *testing.T) {
		t.Parallel()
		for name, at := range map[string][2]string{"base": {"h1", "sprint/c"}, "head": {"h2", "sprint/b"}} {
			w := setup(t, 2)
			finishedAt(w, "s1-1", "h1", "sprint/b")
			readOK(w, "s1-1")
			twinned(w, at[0], at[1])
			w.part(TickAsk, TickReq{})
			assert.Equal(t, 1, readsAsked(w, "s1-3"), "another %s: its first read is asked", name)
			for _, rc := range w.s.Readers.Of("s1-3") {
				assert.Empty(t, rc.F(FieldInherited), "another %s: nothing inherited", name)
			}
		}
		// a flash read kept does not stand for a pro read
		memo := keepVerdicts(nil, readVerdict{Verdict: "ok", Head: "h1", Tier: "flash", Reader: "reader-a", Card: "x", Attempt: 1})
		w := setup(t, 1)
		pr := withField(withField(w.s.Work.Card("s1-1"), FieldReadMemo, memo), "head", "h1")
		assert.Empty(t, w.s.inheritedReads(pr, 1))
		assert.True(t, tierCovers("heavy", "pro"))
		assert.False(t, tierCovers("flash", "pro"))
	})

	t.Run("readers that disagreed at the head leave the attempt read afresh", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 1)
		memo := keepVerdicts(nil,
			readVerdict{Verdict: "ok", Head: "h1", Tier: "pro", Reader: "reader-a", Card: "x", Attempt: 1},
			readVerdict{Verdict: "broken", Head: "h1", Tier: "pro", Reader: "reader-b", Card: "x", Attempt: 1, Finding: "a.go:1"})
		pr := withField(withField(w.s.Work.Card("s1-1"), FieldReadMemo, memo), "head", "h1")
		assert.Empty(t, w.s.inheritedReads(pr, 1))
	})

	t.Run("a split head read afresh ok is not asked of a reader whose ok is kept", func(t *testing.T) {
		t.Parallel()
		// tla/ReadMemo.tla's counterexample before the ask inherited at every read: r1
		// ok and r3 broken at h1; the rework at h1 reads afresh, r3 now says ok, and
		// the second read went to r1, whose ok at h1 was kept
		w := setup(t, 1)
		finishedAt(w, "s1-1", "h1", "sprint/b")
		w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}}))
		first := askedRead(w, "s1-1")
		okReader := first.Row
		w.must(Read(w.s, ReadReq{Usage: "input=1000 output=100", As: okReader, Verdict: "ok", Sel: Sel{IDs: []string{first.ID}}}))
		w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}}))
		second := askedRead(w, "s1-1")
		brokenReader := second.Row
		w.must(Read(w.s, ReadReq{Usage: "input=1000 output=100", As: brokenReader, Verdict: "broken", Finding: "a.go:1 off by one; fix the bound", Sel: Sel{IDs: []string{second.ID}}}))
		w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "the bound"}))
		finishedAt(w, "s1-1", "h1", "sprint/b")

		w.part(TickAsk, TickReq{})
		fresh := askedRead(w, "s1-1")
		assert.Empty(t, fresh.F(FieldInherited), "a split head is read afresh")
		require.Equal(t, brokenReader, fresh.Row, "the finder checks the fix")
		w.must(Read(w.s, ReadReq{Usage: "input=1000 output=100", As: brokenReader, Verdict: "ok", Sel: Sel{IDs: []string{fresh.ID}}}))
		w.part(TickAsk, TickReq{})
		assert.Zero(t, readsAsked(w, "s1-1"), "the second read is inherited, not asked")
		rc := w.s.Readers.Placed(ReadCardID("s1-1", 2, okReader))
		require.NotNil(t, rc)
		assert.Equal(t, OK, rc.Col)
		assert.Equal(t, "s1-1@1", rc.F(FieldInherited))
		assert.Len(t, okReaders(w.s, w.s.Work.Card("s1-1")), 2)
	})

	t.Run("the memo keeps one verdict per reader at a head and the newest few", func(t *testing.T) {
		t.Parallel()
		var memo []readVerdict
		for i := range MaxReadMemo + 3 {
			memo = readMemo(&Card{Fields: map[string]string{FieldReadMemo: keepVerdicts(memo, readVerdict{Verdict: "ok", Head: "h" + itoa(i), Reader: "reader-a"})}})
		}
		require.Len(t, memo, MaxReadMemo)
		assert.Equal(t, "h"+itoa(MaxReadMemo+2), memo[MaxReadMemo-1].Head)
		memo = readMemo(&Card{Fields: map[string]string{FieldReadMemo: keepVerdicts(memo, readVerdict{Verdict: "broken", Head: "h" + itoa(MaxReadMemo+2), Reader: "reader-a"})}})
		require.Len(t, memo, MaxReadMemo)
		assert.Equal(t, "broken", memo[MaxReadMemo-1].Verdict, "a reader's newer verdict at the head replaces its older")
	})
}
