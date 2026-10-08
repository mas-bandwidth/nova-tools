package sprint

import (
	"cmp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Fix is a priority level: reworks go to the head (a-rework-is-priority-fix-bb; the owner,
// 2026-10-07 5:52 PM ET: "maybe it's even a new priority level 'fix', below critical, but
// above everything else"). The ladder is blocker > critical > fix > high > reader > normal >
// low, and every next attempt a rework opens raises a normal or low card to fix. Every test is
// on the in-memory world with its fixed clock, no store and no socket.

// leveled is a primary of the stream at the level, "" for none set.
func leveled(id, stream, level string) *Card {
	c := &Card{ID: id, Row: stream, Fields: map[string]string{"kind": "primary", "stream": stream}}
	if level != "" {
		c.Fields[FieldPriority] = level
	}
	return c
}

func cardIDs(cards []*Card) []string {
	out := make([]string, 0, len(cards))
	for _, c := range cards {
		out = append(out, c.ID)
	}
	return out
}

// The ladder orders fix between critical and high in the deal (ladderOrder), the ask
// (readOrder) and the landing (LandOrder).
func TestTheLadderOrdersFixBetweenCriticalAndHigh(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{PriorityBlocker, PriorityCritical, PriorityFix, PriorityHigh, PriorityReader, PriorityNormal, PriorityLow}, PriorityLadder)
	assert.Contains(t, PrioritySettable, PriorityFix, "priority <id> --fix sets it")
	lvl, why := PriorityOfBrief("c: a card\nPRIORITY: fix\n\nThe task.")
	assert.Equal(t, [2]string{PriorityFix, ""}, [2]string{lvl, why}, "a brief may name it")

	given := []*Card{leveled("n", "s1", ""), leveled("h", "s1", PriorityHigh), leveled("f", "s1", PriorityFix), leveled("c", "s1", PriorityCritical), leveled("l", "s1", PriorityLow)}

	t.Run("the deal", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, []string{"c", "f", "h", "n", "l"}, cardIDs(ladderOrder(given)))
	})

	t.Run("the ask", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, []string{"c", "f", "h", "n", "l"}, cardIDs(readOrder(given)), "a fix primary's reads before a high primary's, after a critical's")
		assert.Equal(t, PriorityFix, ReadPriority(leveled("f", "s1", PriorityFix)), "a read inherits fix")
	})

	t.Run("the landing", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.s.Work.SetRows([]string{"s1", "s2", "s3", "s4"})
		for i, c := range []*Card{leveled("s1-1", "s1", ""), leveled("s2-1", "s2", PriorityHigh), leveled("s3-1", "s3", PriorityFix), leveled("s4-1", "s4", PriorityCritical)} {
			c.Col, c.Score, c.Rev = Merging, float64(i+1), 1
			w.s.Work.Put(c)
		}
		assert.Equal(t, []string{"s4", "s3", "s2", "s1"}, LandOrder(w.s, []string{"s1", "s2", "s3", "s4"}), "a fix stream lands after a critical one, before a high one")
	})

	t.Run("the machines' deal", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, "reader-a")
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1", Width: 1}))
		w.must(Add(w.s, AddReq{Stream: "s1", Cards: []CardAdd{
			{ID: "s1-1", Brief: "c: a normal card\n\nThe task."},
			{ID: "s1-2", Brief: "c: a high card\nPRIORITY: high\n\nThe task."},
			{ID: "s1-3", Brief: "c: a fix card\nPRIORITY: fix\n\nThe task."},
		}}))
		w.part(TickDeal, TickReq{})
		// width 1 holds DealAhead (2) cards: the fix card and the high card, the normal one left
		assert.Equal(t, Working, w.s.StateOf("s1-3"))
		assert.Equal(t, Working, w.s.StateOf("s1-2"))
		assert.Equal(t, Ready, w.s.StateOf("s1-1"))
	})

	t.Run("the machines' ask", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, "reader-m1")
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1", Width: 1}))
		putReview(w, "s1-1", "s1-1: older (s1) tier: flash\n", 1, 1, "h1")
		w.s.Work.Card("s1-1").Fields[FieldPriority] = PriorityHigh
		w.s.Work.Card("s1-1").Fields[FieldFinishedAt] = stamp(t0.Add(-60 * 60e9))
		w.s.Work.Put(&Card{ID: "s1-2", Row: "s1", Col: Review, Score: 2, Rev: 1, Fields: map[string]string{
			"kind": "primary", "attempt": "2", "stream": "s1", "brief": "s1-2: reworked (s1) tier: flash\n", "head": "h2", FieldPriority: PriorityFix}})
		askReaders(t, w, nil)
		rc := w.s.Readers.Card(ReadCardID("s1-2", 2, "reader-m1"))
		require.NotNil(t, rc, "the fix primary's read takes the reader's one lane")
		assert.Nil(t, w.s.Readers.Card(ReadCardID("s1-1", 1, "reader-m1")), "the older high read waits")
		assert.Equal(t, PriorityFix, QueuePriority(rc), "the read card carries fix")
	})
}

// raisedNotes is the priority set notes on the card's timeline.
func raisedNotes(w *world, id string) []Note {
	var out []Note
	for _, n := range w.notesOf(NPrioritySet) {
		if len(n.Primaries) > 0 && n.Primaries[0] == id {
			out = append(out, n)
		}
	}
	return out
}

// The failed rule's rework on a normal card raises it to fix: the primary, its next work card,
// and a priority set note on its timeline; its reads inherit fix.
func TestAFailedRuleOnANormalCardYieldsFix(t *testing.T) {
	t.Parallel()
	w := heldFor(t, "tests red")
	a := answerOn(t, w, on(), NWorkFailed, "s1-1")
	require.Equal(t, RuleFailed, a.Rule, a.Why)
	require.Equal(t, ActRework, a.Act, a.Why)
	rules(w, on())
	pr := w.s.Work.Card("s1-1")
	require.Equal(t, 2, pr.Int("attempt"), "reworked by rule")
	assert.Equal(t, PriorityFix, pr.F(FieldPriority))
	wc := w.s.Fleet.Card(pr.F("work"))
	require.NotNil(t, wc)
	assert.Equal(t, PriorityFix, QueuePriority(wc), "its next work card carries fix")
	ns := raisedNotes(w, "s1-1")
	require.Len(t, ns, 1, "the raise is on its timeline")
	assert.Contains(t, ns[0].What, "s1-1 priority normal -> fix")
	w.clean("reworked at fix")

	// its reads inherit fix
	w.must(Take(w.s, TakeReq{As: wc.Row, Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID)}))
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID), Report: "r"}))
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	reads := readsAt(w.s, w.s.Work.Card("s1-1"), 2)
	require.NotEmpty(t, reads)
	for _, rc := range reads {
		assert.Equal(t, PriorityFix, QueuePriority(rc), "read %s inherits fix", rc.ID)
	}
	assert.Equal(t, PriorityFix, ReadPriority(w.s.Work.Card("s1-1")))
}

// The seat's rework and a brief edited in place raise a normal card to fix as the rules do.
func TestTheSeatsReworkAndABriefEditYieldFix(t *testing.T) {
	t.Parallel()

	t.Run("rework after a broken read", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 1)
		finished(w, "s1-1", false)
		brokenOnce(t, w, "a.go:12 drops the error")
		w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Who: "coordinator"}))
		assert.Equal(t, PriorityFix, w.s.Work.Card("s1-1").F(FieldPriority))
		require.Len(t, raisedNotes(w, "s1-1"), 1)
		assert.Equal(t, "coordinator", raisedNotes(w, "s1-1")[0].Who)
	})

	t.Run("a brief defect re-briefed", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 1)
		finished(w, "s1-1", true)
		w.s.Work.Card("s1-1").Fields[FieldBriefDefect] = stamp(t0)
		w.must(Brief(w.s, BriefReq{ID: "s1-1", Brief: proBrief + "\nthe brief corrected", Who: "coordinator"}))
		pr := w.s.Work.Card("s1-1")
		assert.Equal(t, Ready, pr.Col, "its next attempt opened")
		assert.Equal(t, PriorityFix, pr.F(FieldPriority), "a card re-briefed from the defect column enters at fix")
	})
}

// A card at high, critical or blocker keeps its level on a rework.
func TestAHighCardStaysHigh(t *testing.T) {
	t.Parallel()
	for _, level := range []string{PriorityHigh, PriorityCritical, PriorityBlocker, PriorityFix} {
		t.Run(level, func(t *testing.T) {
			t.Parallel()
			w := heldFor(t, "tests red")
			w.s.Work.Card("s1-1").Fields[FieldPriority] = level
			rules(w, on())
			pr := w.s.Work.Card("s1-1")
			require.Equal(t, 2, pr.Int("attempt"))
			assert.Equal(t, level, pr.F(FieldPriority))
			assert.Empty(t, raisedNotes(w, "s1-1"), "no raise, no note")
		})
	}
	t.Run("low is raised", func(t *testing.T) {
		t.Parallel()
		w := heldFor(t, "tests red")
		w.s.Work.Card("s1-1").Fields[FieldPriority] = PriorityLow
		rules(w, on())
		assert.Equal(t, PriorityFix, w.s.Work.Card("s1-1").F(FieldPriority))
	})
}

// set --rework-priority turns the raise: keep leaves a normal card normal, high raises it to
// high, default is fix; a word it does not take and a stream's are refused.
func TestReworkPriorityKeepLeavesNormal(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ word, want string }{
		{ReworkPriorityKeep, ""},
		{PriorityHigh, PriorityHigh},
		{ReadTierDefault, PriorityFix},
		{PriorityFix, PriorityFix},
	} {
		t.Run(tc.word, func(t *testing.T) {
			t.Parallel()
			w := heldFor(t, "tests red")
			p := w.must(Set(w.s, SetReq{ReworkPriority: tc.word, Who: "coordinator"}))
			require.Len(t, p.Props, 1)
			assert.Equal(t, PropReworkPriority, p.Props[0].Name)
			rules(w, on())
			pr := w.s.Work.Card("s1-1")
			require.Equal(t, 2, pr.Int("attempt"))
			assert.Equal(t, tc.want, pr.F(FieldPriority))
			l, _ := CardPriority(pr)
			assert.Equal(t, cmp.Or(tc.want, PriorityNormal), l)
		})
	}
	t.Run("refused", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 1)
		p := Set(w.s, SetReq{ReworkPriority: "urgent", Who: "coordinator"})
		require.Len(t, p.Refused, 1)
		assert.Contains(t, p.Refused[0].Why, "--rework-priority wants fix, high, keep or default")
		p = Set(w.s, SetReq{Streams: []string{"s1"}, ReworkPriority: PriorityHigh, Who: "coordinator"})
		require.Len(t, p.Refused, 1)
		assert.True(t, strings.Contains(p.Refused[0].Why, "--rework-priority is the sprint's, not a stream's"), p.Refused[0].Why)
	})
}
