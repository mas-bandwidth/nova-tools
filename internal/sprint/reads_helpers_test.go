package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireNothingPlanned is a refused plan that changes nothing.
func requireNothingPlanned(t *testing.T, p Plan, why string) {
	t.Helper()
	require.Len(t, p.Refused, 1, "%+v", p)
	assert.Contains(t, p.Refused[0].Why, why)
	assert.Empty(t, p.Units)
	assert.Empty(t, p.Props)
	assert.Empty(t, p.Notes)
	assert.Empty(t, p.Closes)
}

// toReview takes primaries to review, unasked.
func toReview(w *world, ids ...string) {
	w.t.Helper()
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: ids}}))
	for _, id := range ids {
		c := w.s.Fleet.Card(w.s.Work.Card(id).F("work"))
		w.must(Take(w.s, TakeReq{As: c.Row, Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
		w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
	}
}

// proBrief is a pro card's brief: line 1 names tier pro, so its reads are two,
// from two different readers (ReadsNeeded). The tests of the two-reader
// machinery admit
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
