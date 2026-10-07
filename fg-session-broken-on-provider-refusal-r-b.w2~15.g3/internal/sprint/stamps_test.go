package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// H1: a work card carries dealt (stamped when it is placed in a member's
// ready queue, again on every redeal, unset while withdrawn) and taken; a read
// card carries asked and begun. Every time is the step's clock.
func TestCardsCarryTheirStamps(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	card := w.s.Fleet.Card("s1-1.w1")
	require.Equal(t, stamp(w.s.Now), card.F("dealt"), "dealt at start: %q", card.F("dealt"))
	// A member going down deals the card again: dealt moves with it.
	w.tick(time.Minute)
	w.must(FleetStep(w.s, FleetReq{Op: "down", Member: card.Row}))
	require.Equal(t, stamp(w.s.Now), card.F("dealt"), "dealt on a redeal: %q gen %d", card.F("dealt"), card.Int("gen"))
	require.Equal(t, 2, card.Int("gen"), "dealt on a redeal: %q gen %d", card.F("dealt"), card.Int("gen"))
	// No member up: withdrawn, no dealt; dealt again by start.
	w.tick(time.Minute)
	w.must(FleetStep(w.s, FleetReq{Op: "down", Member: card.Row}))
	require.Equal(t, Withdrawn, card.Col, "withdrawn: %s dealt %q", card.Col, card.F("dealt"))
	require.Empty(t, card.F("dealt"), "withdrawn: %s dealt %q", card.Col, card.F("dealt"))
	w.tick(time.Minute)
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	require.Equal(t, stamp(w.s.Now), card.F("dealt"), "dealt again by start: %q", card.F("dealt"))
	w.tick(time.Minute)
	w.must(Take(w.s, TakeReq{As: card.Row, Sel: Sel{IDs: []string{card.ID}}, Gens: gensOf(w.s, card.ID)}))
	require.Equal(t, stamp(w.s.Now), card.F("taken"), "taken: %q", card.F("taken"))
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{card.ID}}, Gens: gensOf(w.s, card.ID)}))
	w.tick(time.Minute)
	w.must(Ask(w.s, AskReq{}))
	reads := readsAt(w.s, w.s.Work.Card("s1-1"), 1)
	for _, rc := range reads {
		require.Equal(t, stamp(w.s.Now), rc.F("asked"), "a read card asked: %v", rc.Fields)
		require.Empty(t, rc.F("begun"), "a read card asked: %v", rc.Fields)
	}
	w.tick(time.Minute)
	w.must(Read(w.s, ReadReq{As: reads[0].F("reader"), Begin: true, Sel: Sel{IDs: []string{reads[0].ID}}}))
	require.Equal(t, stamp(w.s.Now), reads[0].F("begun"), "begun: %q", reads[0].F("begun"))
	w.clean("stamped")
}
