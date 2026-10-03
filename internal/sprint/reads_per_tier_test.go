package sprint

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Cost rule 4 (the owner, 2026-10-02, nova-tools#5174: "Reads: one cold read per
// flash card on a flash route; two per pro card; readers still equal workers per
// machine"; readers.go ReadsNeeded): a flash card is accepted on one reader's ok,
// read on a flash route; a pro card needs two different readers' oks, each read on
// a pro route.

// proBrief is a pro card's brief: line 1 names tier pro, so its reads are two,
// from two different readers (ReadsNeeded). The tests of the two-reader
// machinery (the pair, the level of reads, a return, ask --another) admit
// pro cards; a card with no tier is flash and needs one read.
const proBrief = "tier: pro"

// proCards makes every primary on the work table a pro card (its brief names
// tier pro): the world's own cards are admitted with no brief, flash.
func proCards(w *world) {
	for _, c := range w.s.Work.Cards() {
		if c.F("kind") == "primary" {
			c.Fields["brief"] = proBrief
		}
	}
}

// tierWorld is three readers, two members up, two routes of each tier, and two
// primaries worked to review: s1-1 a flash card (its brief names no tier) and
// s1-2 a pro card.
func tierWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t, "reader-a", "reader-b", "reader-c")
	for _, m := range []string{"m1", "m2"} {
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: m}))
	}
	for _, r := range []struct{ name, tier string }{{"flash-a", "flash"}, {"flash-b", "flash"}, {"pro-a", "pro"}, {"pro-b", "pro"}} {
		rt := Route{Name: r.name, Tier: r.tier, Provider: "p", Model: r.name, Tokens: 1000, Enabled: true}
		rt.Deadline = 600 // seconds, as the route row holds it
		w.s.Routes = append(w.s.Routes, rt)
	}
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-1"}}))
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-2"}, Brief: proBrief}))
	toReview(w, "s1-1", "s1-2")
	return w
}

// routeTier is the tier of the route named, "" when no route has the name.
func routeTier(s *Snapshot, name string) string {
	for _, r := range s.Routes {
		if r.Name == name {
			return r.Tier
		}
	}
	return ""
}

// The ask asks a flash card of one reader and a pro card of two, each read on a
// route of its card's tier; one ok accepts the flash card and leaves the pro card
// in review until a second reader's ok, and every rule holds after each accept.
func TestAFlashCardIsAcceptedOnOneReadAndAProCardOnTwo(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		id, tier string
		reads    int
	}{
		{"s1-1", "flash", 1},
		{"s1-2", "pro", 2},
	} {
		t.Run(tc.tier, func(t *testing.T) {
			t.Parallel()
			w := tierWorld(t)
			w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{tc.id}}}))
			pr := w.s.Work.Card(tc.id)
			assert.Equal(t, tc.reads, ReadsNeeded(pr))
			reads := liveReadsAt(w.s, pr, 1)
			require.Len(t, reads, tc.reads, "%s, a %s card, asked of %d readers", tc.id, tc.tier, tc.reads)
			for _, rc := range reads {
				assert.Equal(t, tc.tier, rc.F(FieldTier), "%s is drawn from its card's tier", rc.ID)
				assert.Equal(t, tc.tier, routeTier(w.s, rc.F(FieldRoute)), "%s runs on a %s route, not %q", rc.ID, tc.tier, rc.F(FieldRoute))
			}
			for i, rc := range reads {
				w.must(Read(w.s, ReadReq{As: rc.Row, Verdict: "ok", Sel: Sel{IDs: []string{rc.ID}}}))
				last := i == len(reads)-1
				assert.Equal(t, last, acceptable(w.s, w.s.Work.Card(tc.id)), "after %d of %d oks", i+1, tc.reads)
				if !last {
					p := Accept(w.s, AcceptReq{Sel: Sel{IDs: []string{tc.id}}})
					require.Len(t, p.Refused, 1, "accepted on %d of %d oks", i+1, tc.reads)
					assert.Contains(t, p.Refused[0].Why, "needs ok from two different readers")
					assert.NotContains(t, openTypes(w, tc.id), NReadyToAccept, "ready to accept on %d of %d oks", i+1, tc.reads)
				}
			}
			assert.Contains(t, openTypes(w, tc.id), NReadyToAccept)
			w.must(Accept(w.s, AcceptReq{Sel: Sel{IDs: []string{tc.id}}}))
			assert.Equal(t, Merging, w.s.Work.Card(tc.id).Col)
			assert.Len(t, Split(w.s.Work.Card(tc.id).F("readers")), tc.reads, "the readers it was accepted on")
			w.clean("accepted on " + tc.tier + " reads")
		})
	}
}

// A broken verdict on a flash card's one read is the bounce of today: the
// coordinator is told, rework sends the card back with the finding, and the next
// attempt is asked of one reader at its new head, on a flash route, and accepted
// on its ok.
func TestABounceOnAFlashCardsOneReadReworksAsToday(t *testing.T) {
	t.Parallel()
	w := tierWorld(t)
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	first := liveReadsAt(w.s, w.s.Work.Card("s1-1"), 1)
	require.Len(t, first, 1)
	w.must(Read(w.s, ReadReq{As: first[0].Row, Verdict: "broken", Finding: "f.go:1: the empty case", Sel: Sel{IDs: []string{first[0].ID}}}))
	assert.Equal(t, NReadBroken, openTypes(w, "s1-1"))
	assert.False(t, acceptable(w.s, w.s.Work.Card("s1-1")))
	w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "handle the empty case"}))
	wc := w.s.Fleet.Placed(w.s.Work.Card("s1-1").F("work"))
	require.NotNil(t, wc, "rework delegates at once")
	assert.Equal(t, "handle the empty case", wc.F("fix"))
	w.must(Take(w.s, TakeReq{As: wc.Row, Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID)}))
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID), Head: "h2"}))
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	again := liveReadsAt(w.s, w.s.Work.Card("s1-1"), 2)
	require.Len(t, again, 1, "the next attempt is a flash card's one read")
	assert.Equal(t, "h2", again[0].F("head"))
	assert.Equal(t, "flash", routeTier(w.s, again[0].F(FieldRoute)))
	readOK(w, "s1-1")
	w.must(Accept(w.s, AcceptReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	w.clean("a bounced flash card accepted on its next attempt's one read")
}

// The tier is the card's current one: a flash card reworked to pro is read twice,
// on pro routes, at its next attempt.
func TestAFlashCardReworkedToProIsReadTwiceOnPro(t *testing.T) {
	t.Parallel()
	w := tierWorld(t)
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	first := liveReadsAt(w.s, w.s.Work.Card("s1-1"), 1)
	require.Len(t, first, 1)
	w.must(Read(w.s, ReadReq{As: first[0].Row, Verdict: "broken", Finding: "f.go:1: too hard for flash", Sel: Sel{IDs: []string{first[0].ID}}}))
	w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "again, on pro", Tier: "pro"}))
	wc := w.s.Fleet.Placed(w.s.Work.Card("s1-1").F("work"))
	require.NotNil(t, wc)
	w.must(Take(w.s, TakeReq{As: wc.Row, Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID)}))
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID), Head: "h2"}))
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	again := liveReadsAt(w.s, w.s.Work.Card("s1-1"), 2)
	require.Len(t, again, 2, "a pro card's two reads")
	for _, rc := range again {
		assert.Equal(t, "pro", routeTier(w.s, rc.F(FieldRoute)), rc.ID)
	}
}

// With one reader up the tick asks the flash card of it and holds the pro card,
// which needs two: the sprint's one judgment says fewer than two readers are up,
// and the moves due count the flash card alone.
func TestOneReaderUpReadsTheFlashCardAndHoldsTheProCard(t *testing.T) {
	t.Parallel()
	w := tierWorld(t)
	w.s.ReaderStates = map[string]string{"reader-a": ReaderUp, "reader-b": ReaderAway, "reader-c": ReaderDown}
	assert.Equal(t, 1, MovesDue(w.s)-len(w.s.Fleet.Column(Withdrawn)), "the flash card is due to be asked, the pro card is not")
	p, _ := TickAsk(w.s, TickReq{})
	w.must(p)
	assert.Len(t, liveReadsAt(w.s, w.s.Work.Card("s1-1"), 1), 1, "the flash card asked of the reader up")
	assert.Empty(t, liveReadsAt(w.s, w.s.Work.Card("s1-2"), 1), "the pro card waits for a second reader")
	var few []Note
	for _, n := range p.Notes {
		if n.Type == NFewReaders {
			few = append(few, n)
		}
	}
	require.Len(t, few, 1, "one judgment for the sprint: %+v", p.Notes)
}

// readersWith sets each reader's state: up for the readers named up, away for
// those named away, down for every other.
func readersWith(w *world, up, away []string) {
	w.s.ReaderStates = map[string]string{}
	for _, r := range w.s.Readers.Rows() {
		switch {
		case contains(up, r):
			w.s.ReaderStates[r] = ReaderUp
		case contains(away, r):
			w.s.ReaderStates[r] = ReaderAway
		default:
			w.s.ReaderStates[r] = ReaderDown
		}
	}
}

// begunReads is tierWorld asked with every reader up and every read begun:
// s1-1's one read is reader-a's, s1-2's two are reader-b's and reader-c's.
func begunReads(t *testing.T) *world {
	t.Helper()
	w := tierWorld(t)
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1", "s1-2"}}}))
	for id, readers := range map[string][]string{"s1-1": {"reader-a"}, "s1-2": {"reader-b", "reader-c"}} {
		got := readerNames(liveReadsAt(w.s, w.s.Work.Card(id), 1))
		require.ElementsMatch(t, readers, got, "%s asked of %v", id, got)
		for _, rd := range readers {
			w.must(Read(w.s, ReadReq{As: rd, Begin: true, Sel: Sel{IDs: []string{ReadCardID(id, 1, rd)}}}))
		}
	}
	return w
}

// The readers' rebalance takes back a flash card's read begun by a reader that
// went away when the one reader up could take it, and the tick's ask asks it
// there (readers.go, sweepReads: one reader up is enough for a flash card).
func TestTheRebalanceTakesAFlashReadToTheOneReaderUp(t *testing.T) {
	t.Parallel()
	w := begunReads(t)
	readersWith(w, []string{"reader-b"}, []string{"reader-a"})
	w.part(TickLevelReads, TickReq{})
	left := w.s.Readers.Card(ReadCardID("s1-1", 1, "reader-a"))
	assert.False(t, left.Placed(), "the read begun on the reader away is taken back")
	assert.Equal(t, "away", left.F("retired_by"))
	w.part(TickAsk, TickReq{})
	assert.Equal(t, []string{"reader-b"}, readerNames(liveReadsAt(w.s, w.s.Work.Card("s1-1"), 1)), "asked of the one reader up")
}

// A pro card's read begun by a reader that went away stays where it is while
// one reader is up: the ask could not place it, as the pro card needs two
// (readers.go, sweepReads).
func TestTheRebalanceLeavesAProReadWhenOneReaderIsUp(t *testing.T) {
	t.Parallel()
	w := begunReads(t)
	// reader-a, up, holds no read of s1-2 and could take one; s1-2's readers are away and down
	readersWith(w, []string{"reader-a"}, []string{"reader-b"})
	w.part(TickLevelReads, TickReq{})
	for _, rd := range []string{"reader-b", "reader-c"} {
		rc := w.s.Readers.Card(ReadCardID("s1-2", 1, rd))
		assert.True(t, rc.Placed(), "the pro card's read on %s stays with its reader", rd)
		assert.Equal(t, Reading, rc.Col, rd)
	}
}

// "Fewer than two readers up" holds only the cards that need more readers than
// are up (held.go): with one reader up, a flash card the next tick does not ask
// (past the tick's bound) is not told as waiting on that judgment, by the tick
// that writes it (tickOn) or once it is open (judgment); a pro card is, both ways.
func TestFewReadersHoldsOnlyTheCardsThatNeedMoreReaders(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a", "reader-b", "reader-c")
	w.s.Work.SetRows(append(w.s.Work.Rows(), "s1"))
	put := func(id, brief string, score float64) {
		w.s.Work.Put(&Card{ID: id, Row: "s1", Col: Review, Score: score, Rev: 1,
			Fields: map[string]string{"kind": "primary", "attempt": "1", "stream": "s1", "head": "h-" + id, "brief": brief}})
	}
	put("pro", proBrief, 0)
	for i := 1; i <= TickMaxMoves+1; i++ {
		put(fmt.Sprintf("f%04d", i), "", float64(i))
	}
	last := fmt.Sprintf("f%04d", TickMaxMoves+1) // past the tick's bound: the next tick does not ask it
	readersWith(w, []string{"reader-a"}, []string{"reader-b", "reader-c"})
	few := func(why string) bool {
		return strings.Contains(why, NFewReaders) || strings.Contains(why, "fewer than two readers are up")
	}
	// the next tick writes the judgment (the pro card waits): tickOn
	for _, running := range []bool{false, true} {
		c := newHeld(HeldState{Snap: w.s, Running: running}, w.s.Now)
		require.True(t, c.fewReaders, "the next tick writes fewer than two readers up")
		assert.False(t, few(c.tickOn(w.s.Work.Card(last))), "the flash card past the bound (running %v): %s", running, c.tickOn(w.s.Work.Card(last)))
		assert.True(t, few(c.tickOn(w.s.Work.Card("pro"))), "the pro card (running %v): %s", running, c.tickOn(w.s.Work.Card("pro")))
	}
	// the judgment is open: judgment
	p, _ := TickAsk(w.s, TickReq{})
	w.must(p)
	require.NotEmpty(t, w.openOn(StreamSubject("")), "fewer than two readers up is open")
	c := newHeld(HeldState{Snap: w.s, Running: true}, w.s.Now)
	assert.False(t, few(c.judgment(w.s.Work.Card(last))), "the flash card past the bound: %s", c.judgment(w.s.Work.Card(last)))
	assert.True(t, few(c.judgment(w.s.Work.Card("pro"))), "the pro card: %s", c.judgment(w.s.Work.Card("pro")))
}
