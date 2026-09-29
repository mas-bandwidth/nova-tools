package sprint

import (
	"testing"
	"time"
)

// H1: a work card carries dealt (stamped when it is placed in a member's
// ready queue, again on every redeal, unset while withdrawn) and taken; a read
// card carries asked and begun. Every time is the step's clock.
func TestCardsCarryTheirStamps(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.must(Start(w.s, StartReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	card := w.s.Fleet.Card("s1-1.w1")
	if card.F("dealt") != stamp(w.s.Now) {
		t.Fatalf("dealt at start: %q", card.F("dealt"))
	}
	// A member going down deals the card again: dealt moves with it.
	w.tick(time.Minute)
	w.must(FleetStep(w.s, FleetReq{Op: "down", Member: card.Row}))
	if card.F("dealt") != stamp(w.s.Now) || card.Int("gen") != 2 {
		t.Fatalf("dealt on a redeal: %q gen %d", card.F("dealt"), card.Int("gen"))
	}
	// No member up: withdrawn, no dealt; dealt again by start.
	w.tick(time.Minute)
	w.must(FleetStep(w.s, FleetReq{Op: "down", Member: card.Row}))
	if card.Col != Withdrawn || card.F("dealt") != "" {
		t.Fatalf("withdrawn: %s dealt %q", card.Col, card.F("dealt"))
	}
	w.tick(time.Minute)
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	w.must(Start(w.s, StartReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	if card.F("dealt") != stamp(w.s.Now) {
		t.Fatalf("dealt again by start: %q", card.F("dealt"))
	}
	w.tick(time.Minute)
	w.must(Take(w.s, TakeReq{As: card.Row, Sel: Sel{IDs: []string{card.ID}}, Gens: gensOf(w.s, card.ID)}))
	if card.F("taken") != stamp(w.s.Now) {
		t.Fatalf("taken: %q", card.F("taken"))
	}
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{card.ID}}, Gens: gensOf(w.s, card.ID)}))
	w.tick(time.Minute)
	w.must(Ask(w.s, AskReq{}))
	reads := readsAt(w.s, w.s.Work.Card("s1-1"), 1)
	for _, rc := range reads {
		if rc.F("asked") != stamp(w.s.Now) || rc.F("begun") != "" {
			t.Fatalf("a read card asked: %v", rc.Fields)
		}
	}
	w.tick(time.Minute)
	w.must(Read(w.s, ReadReq{As: reads[0].F("reader"), Begin: true, Sel: Sel{IDs: []string{reads[0].ID}}}))
	if reads[0].F("begun") != stamp(w.s.Now) {
		t.Fatalf("begun: %q", reads[0].F("begun"))
	}
	w.clean("stamped")
}
