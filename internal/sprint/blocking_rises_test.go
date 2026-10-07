package sprint

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Cards that block many rise to high (blocking_rises.go; the owner, 2026-10-07: "when we find
// that certain cards are blocking a lot of new work, we should increase the priority of those
// cards naturally to high"). Every test is on the in-memory world with its fixed clock, no
// store and no socket; the model is tla/BlockingRises.tla.

// blockingWorld is a world with one stream s1, the root card s1-root ready, and n cards
// s1-1..s1-n waiting on it; the threshold is set when it is not zero.
func blockingWorld(t *testing.T, n, threshold int) *world {
	t.Helper()
	w := newWorld(t)
	if threshold > 0 {
		w.s.Work.SetProps(map[string]string{PropBlockingHigh: itoa(threshold)})
	}
	cards := []CardAdd{{ID: "s1-root", Brief: "s1-root: the root"}}
	for i := 1; i <= n; i++ {
		id := "s1-" + itoa(i)
		cards = append(cards, CardAdd{ID: id, Brief: id + ": waits on the root", Needs: []string{"s1-root"}})
	}
	w.must(Add(w.s, AddReq{Stream: "s1", Cards: cards, Who: "coordinator"}))
	return w
}

// landed lands the card in the world, as the lander would: off the open table.
func landed(w *world, id string) {
	c := w.s.Work.Card(id)
	c.Col, c.Rev = string(Landed), c.Rev+1
	w.s.Work.cells, w.s.Work.byPrimary, w.s.Work.lines, w.s.Work.stops = nil, nil, nil, nil
}

// rises is the rule's plan applied, as the tick's part runs it (TickBlockingRises).
func rises(w *world) Plan {
	w.t.Helper()
	p, _ := TickBlockingRises(w.s, TickReq{})
	return w.must(p)
}

func TestACardThatBlocksManyRisesToHigh(t *testing.T) {
	t.Parallel()
	w := blockingWorld(t, BlockingHighDefault, 0)
	root := w.s.Work.Card("s1-root")
	l, src := CardPriority(root)
	require.Equal(t, [2]string{PriorityNormal, "default"}, [2]string{l, src}, "before the rule: normal")

	p := rises(w)
	require.Len(t, p.Units, 1, "the root alone: its dependents have nothing behind them")
	assert.Equal(t, "rule blocking-rises: s1-root normal -> high (behind=8)", p.Units[0].Moved)
	require.Len(t, p.Units[0].Notes, 1)
	assert.Equal(t, NPrioritySet, p.Units[0].Notes[0].Type, "a priority set note on the card's timeline")
	assert.Equal(t, MachineActor, p.Units[0].Notes[0].Who)
	assert.Equal(t, []string{"s1-root"}, p.Units[0].Notes[0].Primaries)
	l, src = CardPriority(root)
	assert.Equal(t, [2]string{PriorityHigh, "rule"}, [2]string{l, src})
	assert.Equal(t, PriorityByRule, root.F(FieldPriorityBy))
	assert.Equal(t, map[string]int{"s1-root": 8}, RuleRaised(w.s))

	assert.Empty(t, rises(w).Units, "at high already: nothing moves")

	// the level orders the deal, the ask and the land as a hand-set high does
	plain := &Card{ID: "p", Fields: map[string]string{"kind": "primary"}}
	assert.Equal(t, []*Card{root, plain}, ladderOrder([]*Card{plain, root}))
	assert.Equal(t, PriorityHigh, ReadPriority(root), "its reads inherit the level")

	// under the threshold it stays normal
	w2 := blockingWorld(t, BlockingHighDefault-1, 0)
	assert.Empty(t, rises(w2).Units)
	assert.Empty(t, RuleRaised(w2.s))
}

func TestTheThresholdIsASetting(t *testing.T) {
	t.Parallel()
	w := blockingWorld(t, 3, 3)
	assert.Equal(t, 3, w.s.BlockingHigh())
	require.Len(t, rises(w).Units, 1)
	assert.Equal(t, PriorityHigh, w.s.Work.Card("s1-root").F(FieldPriority))

	// set writes it; a word that is no whole number from 1 is refused; default takes it off
	w3 := newWorld(t)
	w3.must(Set(w3.s, SetReq{BlockingHigh: "5", Who: "coordinator"}))
	assert.Equal(t, 5, w3.s.BlockingHigh())
	p := Set(w3.s, SetReq{BlockingHigh: "0", Who: "coordinator"})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "--blocking-high wants a whole number from 1")
	p = Set(w3.s, SetReq{Streams: []string{"s1"}, BlockingHigh: "5", Who: "coordinator"})
	require.NotEmpty(t, p.Refused)
	assert.Contains(t, p.Refused[0].Why, "--blocking-high is the sprint's, not a stream's")
	p = w3.must(Set(w3.s, SetReq{BlockingHigh: ReadTierDefault, Who: "coordinator"}))
	assert.Equal(t, BlockingHighDefault, w3.s.BlockingHigh())
	assert.Contains(t, p.Units[0].Moved, "blocking-high default (8 behind)")
}

func TestAPersonsLevelIsNeverChangedByTheRule(t *testing.T) {
	t.Parallel()
	w := blockingWorld(t, BlockingHighDefault+1, 0)
	// a hand-set low on the root, nine behind it: the rule leaves it low
	w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-root"}, Level: PriorityLow, Reason: "later", Who: "coordinator"}))
	root := w.s.Work.Card("s1-root")
	assert.Equal(t, "coordinator", root.F(FieldPriorityBy), "the verb marks the level a person's")
	assert.Empty(t, rises(w).Units)
	assert.Equal(t, PriorityLow, root.F(FieldPriority))

	// a hand-set high with nothing behind it is never dropped back
	w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-1"}, Level: PriorityHigh, Reason: "the release waits", Who: "coordinator"}))
	rises(w)
	assert.Equal(t, PriorityHigh, w.s.Work.Card("s1-1").F(FieldPriority))

	// a level set before the field existed counts as a person's too
	w.s.Work.Card("s1-2").Fields[FieldPriority] = PriorityNormal
	rises(w)
	assert.Equal(t, PriorityNormal, w.s.Work.Card("s1-2").F(FieldPriority))
	assert.Empty(t, w.s.Work.Card("s1-2").F(FieldPriorityBy))

	// a brief's PRIORITY line at admission is a person's
	w.must(Add(w.s, AddReq{Stream: "s1", Cards: []CardAdd{{ID: "s1-seeded", Brief: "s1-seeded: seeded\nPRIORITY: critical\n\nbody"}}, Who: "coordinator"}))
	assert.Equal(t, "coordinator", w.s.Work.Card("s1-seeded").F(FieldPriorityBy))

	// the verb over a rule-set level makes it a person's: the rule drops it back no more
	w4 := blockingWorld(t, 2, 2)
	rises(w4)
	require.Equal(t, PriorityByRule, w4.s.Work.Card("s1-root").F(FieldPriorityBy))
	w4.must(SetPriority(w4.s, PriorityReq{IDs: []string{"s1-root"}, Level: PriorityHigh, Reason: "keep it", Who: "coordinator"}))
	assert.Equal(t, "coordinator", w4.s.Work.Card("s1-root").F(FieldPriorityBy))
	landed(w4, "s1-1")
	landed(w4, "s1-2")
	assert.Empty(t, rises(w4).Units)
	assert.Equal(t, PriorityHigh, w4.s.Work.Card("s1-root").F(FieldPriority))
}

func TestTheChainCarriesTheLevelToTheNeeds(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.must(Add(w.s, AddReq{Stream: "s1", Cards: []CardAdd{
		{ID: "s1-a", Brief: "s1-a: the first"},
		{ID: "s1-b", Brief: "s1-b: needs a", Needs: []string{"s1-a"}},
		{ID: "s1-c", Brief: "s1-c: needs b", Needs: []string{"s1-b"}},
		{ID: "s1-d", Brief: "s1-d: apart"},
	}, Who: "coordinator"}))
	assert.Empty(t, rises(w).Units, "two behind a: under the threshold")

	// a person sets c high: b and a rise with it, so c never asks for a lane ahead of them
	w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-c"}, Level: PriorityBlocker, Reason: "now", Who: "coordinator"}))
	p := rises(w)
	var moved []string
	for _, u := range p.Units {
		moved = append(moved, u.Moved)
	}
	assert.Equal(t, []string{
		"rule blocking-rises: s1-a normal -> high (behind=2, needed by s1-b)",
		"rule blocking-rises: s1-b normal -> high (behind=1, needed by s1-c)",
	}, moved)
	assert.Equal(t, PriorityBlocker, w.s.Work.Card("s1-c").F(FieldPriority), "the person's level stands")
	assert.Empty(t, w.s.Work.Card("s1-d").F(FieldPriority), "a card apart is not touched")
	for _, id := range []string{"s1-a", "s1-b"} {
		c := w.s.Work.Card(id)
		assert.True(t, atLeastHigh(c.F(FieldPriority)), "%s is at least high", id)
	}

	// the person takes c back to normal: the chain lets go
	w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-c"}, Level: PriorityNormal, Reason: "not now", Who: "coordinator"}))
	p = rises(w)
	require.Len(t, p.Units, 2)
	assert.Equal(t, "rule blocking-rises: s1-a high -> normal (behind=2)", p.Units[0].Moved)
	assert.Empty(t, w.s.Work.Card("s1-a").F(FieldPriority))
	assert.Empty(t, w.s.Work.Card("s1-a").F(FieldPriorityBy))
}

func TestARuleRaisedCardDropsBackWhenItsDependentsGo(t *testing.T) {
	t.Parallel()
	w := blockingWorld(t, 3, 3)
	rises(w)
	root := w.s.Work.Card("s1-root")
	require.Equal(t, PriorityHigh, root.F(FieldPriority))

	// one dependent dropped: two behind, under the threshold
	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-3"}}, Reason: "not wanted", Who: "coordinator"}))
	p := rises(w)
	require.Len(t, p.Units, 1)
	assert.Equal(t, "rule blocking-rises: s1-root high -> normal (behind=2)", p.Units[0].Moved)
	assert.Equal(t, NPrioritySet, p.Units[0].Notes[0].Type)
	l, src := CardPriority(root)
	assert.Equal(t, [2]string{PriorityNormal, "default"}, [2]string{l, src})
	assert.Empty(t, root.F(FieldPriorityBy))

	// when the root lands nothing is left to do: it is off the open table
	w2 := blockingWorld(t, 3, 3)
	rises(w2)
	landed(w2, "s1-root")
	assert.Empty(t, rises(w2).Units)
	assert.Empty(t, RuleRaised(w2.s))
}

// The deal never deals a card whose needs have not landed, whatever its level: a card with
// an unlanded need is waiting, and the deal offers ready cards only; the level orders the
// ready cards.
func TestTheRuleNeverDealsACardBeforeItsNeeds(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.s.Fleet.SetRows([]string{FriendRow("amy")})
	w.must(Add(w.s, AddReq{Stream: "s1", Cards: []CardAdd{
		{ID: "s1-1", Brief: friendBrief("only friend amy")},
		{ID: "s1-2", Brief: friendBrief("only friend amy")},
		{ID: "s1-3", Brief: friendBrief("only friend amy"), Needs: []string{"s1-2"}},
	}, Who: "coordinator"}))
	require.Equal(t, Waiting, w.s.StateOf("s1-3"), "it waits for s1-2")
	w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-3"}, Level: PriorityHigh, Reason: "wanted", Who: "coordinator"}))
	p := rises(w)
	require.Len(t, p.Units, 1)
	assert.Equal(t, "s1-2", p.Units[0].Key, "the chain raises its need")
	require.Equal(t, PriorityHigh, w.s.Work.Card("s1-2").F(FieldPriority))
	require.Equal(t, PriorityHigh, w.s.Work.Card("s1-3").F(FieldPriority))

	// one lane: the raised need goes first, ahead of the older normal card; the high card
	// that waits on it is dealt to no one
	amy := FriendSeat{Name: "amy", Width: 1, Status: Up, Class: "flash,pro"}
	dp, _ := TickDeal(w.s, TickReq{Friends: []FriendSeat{amy}})
	w.must(dp)
	assert.NotNil(t, w.s.Fleet.Card(WorkCardID("s1-2", 1)), "the raised need is dealt first")
	assert.Nil(t, w.s.Fleet.Card(WorkCardID("s1-1", 1)), "the older normal card waits for a lane")
	assert.Nil(t, w.s.Fleet.Card(WorkCardID("s1-3", 1)), "never a card before its needs land")
	assert.Equal(t, Waiting, w.s.StateOf("s1-3"))
	for _, c := range w.s.Work.Column(Ready, Working) {
		for _, n := range Split(c.F("needs")) {
			assert.Equal(t, Landed, w.s.StateOf(n), "%s is past waiting with %s not landed", c.ID, n)
		}
	}
}

// Admission orders the many-brief form's cards by their dependents: a card with more cards
// waiting on it comes as early as its own needs allow; equals keep the order given; a
// sentinel keeps its place and what follows it stays after it.
func TestAdmissionOrdersByDependents(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.must(Add(w.s, AddReq{Stream: "s1", Cards: []CardAdd{
		{ID: "s1-d", Brief: "s1-d: apart"},
		{ID: "s1-c", Brief: "s1-c: needs b", Needs: []string{"s1-b"}},
		{ID: "s1-b", Brief: "s1-b: needs a", Needs: []string{"s1-a"}},
		{ID: "s1-a", Brief: "s1-a: the root"},
		{ID: "s1-gate", Sentinel: true},
		{ID: "s1-f", Brief: "s1-f: needs e", Needs: []string{"s1-e"}},
		{ID: "s1-e", Brief: "s1-e: after the gate"},
	}, Who: "coordinator"}))
	var line []string
	for _, c := range w.s.Work.openLine("s1") {
		line = append(line, c.ID)
	}
	assert.Equal(t, []string{"s1-a", "s1-b", "s1-d", "s1-c", "s1-gate", "s1-e", "s1-f"}, line)
	assert.Equal(t, Ready, w.s.StateOf("s1-a"))
	assert.Equal(t, Waiting, w.s.StateOf("s1-b"), "it still waits for a to land")
	assert.Equal(t, Waiting, w.s.StateOf("s1-e"), "behind the gate by its place")

	// an existing stream keeps its order: a later add goes after it, ordered among itself
	w.must(Add(w.s, AddReq{Stream: "s1", Cards: []CardAdd{
		{ID: "s1-h", Brief: "s1-h: needs g", Needs: []string{"s1-g"}},
		{ID: "s1-g", Brief: "s1-g: later root"},
	}, Who: "coordinator"}))
	line = line[:0]
	for _, c := range w.s.Work.openLine("s1") {
		line = append(line, c.ID)
	}
	assert.Equal(t, []string{"s1-a", "s1-b", "s1-d", "s1-c", "s1-gate", "s1-e", "s1-f", "s1-g", "s1-h"}, line)

	// the pure order: the open cards of the table count as dependents too
	ordered := admissionOrder(w.s, []CardAdd{{ID: "x", Needs: []string{"s1-a"}}, {ID: "y"}})
	assert.Equal(t, "x", ordered[0].ID, "equal dependents (none): the order given")
	ordered = admissionOrder(nil, []CardAdd{{ID: "p"}, {ID: "q"}})
	assert.Equal(t, []string{"p", "q"}, []string{ordered[0].ID, ordered[1].ID})
}

// The lander takes the streams by their merging sets' levels and keeps each stream's batch
// as it is: a level reorders nothing within a stream (land.go landQueue reads the merge
// table's queued cell in score order; LandOrder orders streams only).
func TestLandOrderKeepsEachStreamsBatchAsItIs(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.s.Work.SetRows([]string{"s1", "s2"})
	w.s.Merge.SetRows([]string{"s1", "s2"})
	put := func(id, row string, score float64, fields map[string]string) {
		fields["kind"] = "primary"
		w.s.Work.Put(&Card{ID: id, Row: row, Col: Merging, Score: score, Rev: 1, Fields: fields})
		w.s.Merge.Put(&Card{ID: id, Row: row, Col: Queued, Score: score, Rev: 1, Fields: map[string]string{"kind": "merge"}})
	}
	put("s1-1", "s1", 1, map[string]string{})
	put("s1-2", "s1", 2, map[string]string{FieldPriority: PriorityHigh, FieldPriorityBy: PriorityByRule})
	put("s2-1", "s2", 1, map[string]string{})
	order := LandOrder(w.s, []string{"s1", "s2"})
	assert.Equal(t, []string{"s1", "s2"}, order, "the stream with the rule-raised card merging goes first")
	order = LandOrder(w.s, []string{"s2", "s1"})
	assert.Equal(t, []string{"s1", "s2"}, order)
	var batch []string
	for _, c := range w.s.Merge.Cell("s1", Queued) {
		batch = append(batch, c.ID)
	}
	assert.Equal(t, []string{"s1-1", "s1-2"}, batch, "within the stream the high card keeps its place behind its predecessor")
}

func TestWhereAndCardSayTheRulesLevel(t *testing.T) {
	t.Parallel()
	w := blockingWorld(t, 3, 3)
	rises(w)
	counts := PriorityCounts(w.s)
	assert.Equal(t, []string{"s1-root"}, counts[PriorityHigh])
	line := PriorityLine(counts, nil, RuleRaised(w.s))
	assert.Equal(t, "priority: high s1-root (behind=3)", line)
	assert.True(t, strings.HasSuffix(PriorityLine(counts, nil, nil), "high s1-root"), "without the raised map the ids alone")
}
