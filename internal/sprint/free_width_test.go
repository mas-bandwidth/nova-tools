package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A member holds at most DealAhead times its width of dealt-or-working work cards (its width
// working and as many ready behind them), whoever deals them: the tick, the deal verb and the
// coordinator's rework (tla/DirtyTick.tla, Room and
// WidthRespected, with the reversed witness "nowidth"; docs/SPEC-SPRINT.md, the deal).

// widthHeld is the work cards a member holds, ready and working.
func widthHeld(w *world, m string) int {
	return w.s.Fleet.Count(m, Ready) + w.s.Fleet.Count(m, Working)
}

// takeCard takes a dealt work card.
func takeCard(w *world, wc string) {
	w.t.Helper()
	w.must(Take(w.s, TakeReq{As: w.s.Fleet.Card(wc).Row, Sel: Sel{IDs: []string{wc}}, Gens: gensOf(w.s, wc)}))
}

// failedFirst is one member m1 of width 1 at its room, DealAhead times its width: s1-2's first
// attempt working and s1-3's ready behind it, while s1-1's came back failed and waits for its
// rework.
func failedFirst(t *testing.T) *world {
	t.Helper()
	w := fleetWorld(t, 3, 1, "m1")
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	takeCard(w, "s1-1.w1")
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, Gens: gensOf(w.s, "s1-1.w1"), Failed: true, Report: "red"}))
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-2"}}}))
	takeCard(w, "s1-2.w1")
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-3"}}}))
	require.Equal(t, DealAhead*1, widthHeld(w, "m1"))
	return w
}

// TestAReworkIsNotDealtToAMemberAtDealAheadTimesItsWidth pins that a rework with every up member
// at its room (DealAhead times its width) cuts no work card: the primary goes ready with its fix,
// and the tick deals it when a member has room. Passing the room (dealing to the first up when
// none has room) fails it: a width-1 member holding two cards is handed a third.
func TestAReworkIsNotDealtToAMemberAtDealAheadTimesItsWidth(t *testing.T) {
	t.Parallel()
	w := failedFirst(t)
	w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "make the test pass"}))
	assert.Nil(t, w.s.Fleet.Card("s1-1.w2"), "a width-1 member holding DealAhead cards was dealt another")
	assert.Equal(t, DealAhead*1, widthHeld(w, "m1"))
	assert.Equal(t, Ready, w.state("s1-1"))
	assert.Equal(t, "make the test pass", w.s.Work.Card("s1-1").F("fix"), "the fix rides on the primary until the tick deals it")

	// the tick deals nothing while the member is full ...
	w.part(TickDeal, TickReq{})
	assert.Nil(t, w.s.Fleet.Card("s1-1.w2"))
	// ... and deals the rework when the member has room
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-2.w1"}}, Gens: gensOf(w.s, "s1-2.w1"), Failed: true, Report: "red"}))
	w.part(TickDeal, TickReq{})
	wc := w.s.Fleet.Card("s1-1.w2")
	require.NotNil(t, wc)
	assert.Equal(t, "m1", wc.Row)
	assert.Equal(t, "make the test pass", wc.F("fix"))
	assert.Equal(t, DealAhead*1, widthHeld(w, "m1"))
}

// TestTheDealVerbRefusesAMemberAtDealAheadTimesItsWidth pins the deal verb's own limit: with
// every up member at its room it deals nothing and says why.
func TestTheDealVerbRefusesAMemberAtDealAheadTimesItsWidth(t *testing.T) {
	t.Parallel()
	w := fleetWorld(t, 3, 1, "m1")
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	takeCard(w, "s1-1.w1")
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-2"}}}))
	p := Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-3"}}})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "at its room (DealAhead times its width)")
	assert.Empty(t, p.Units)
}

// TestTheTickDealsOnlyWhatIsFree pins the free room of each member, DealAhead times its width
// less what it holds: width 1 with one working, one more ready behind it; width 2 with one
// working, three more; width 2 idle, four in one tick. The tick takes nothing: a member works
// at most its width.
func TestTheTickDealsOnlyWhatIsFree(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		width, working int
		wantHeld       int
	}{
		{"width 1, one working", 1, 1, DealAhead * 1},
		{"width 2, one working", 2, 1, DealAhead * 2},
		{"width 2, idle", 2, 0, DealAhead * 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := fleetWorld(t, 4, tc.width, "m1")
			for i := 1; i <= tc.working; i++ {
				id := WorkCardID("s1-"+itoa(i), 1)
				w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-" + itoa(i)}}}))
				takeCard(w, id)
			}
			w.part(TickDeal, TickReq{})
			assert.Equal(t, tc.wantHeld, widthHeld(w, "m1"))
			assert.Equal(t, tc.working, w.s.Fleet.Count("m1", Working), "the tick takes nothing")
			assert.LessOrEqual(t, w.s.Fleet.Count("m1", Working), tc.width, "a member works at most its width")
		})
	}
}
