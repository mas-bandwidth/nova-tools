package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// capSpend puts on the work table a primary holding one consumer record of cost usd
// (actual) on route, ended at at: what the tick's clock-hour sums read (hourSpendOf).
func capSpend(w *world, id, route, usd string, at time.Time) {
	u := cardcost.NoUsage()
	u.Actual, u.ActualBy = usd, cardcost.ActualByHarness
	c := Consumer{Kind: "work", Card: id + ".w1", Attempt: 1, Gen: 1, Who: "m1", Route: route, End: "finished", At: stamp(at), Key: id + ".w1#g1", Usage: u}
	w.s.Work.Put(&Card{ID: id, Row: "s1", Col: DoneOK, Fields: map[string]string{FieldCostRecord + c.Key: c.line()}})
}

// spendCapDealtOn is the work cards on the fleet table, by the route each was drawn on.
func spendCapDealtOn(w *world) map[string]int {
	out := map[string]int{}
	for _, c := range w.s.Fleet.Column(Ready, Working) {
		out[c.F(FieldRoute)]++
	}
	return out
}

// spendCapJudgments is the cap judgments written so far on the subject.
func spendCapJudgments(w *world, subject string) []Note {
	var out []Note
	for _, n := range w.notes {
		if n.Type == NRouteCap && n.Kind == Judgment && n.Stream == subject {
			out = append(out, n)
		}
	}
	return out
}

// docs/SPEC-SPRINT.md, spend-circuit-breakerb-bb.w8: a route whose cost over the current
// clock hour has reached its cap rests with the reason `cap reached: $x of $y this hour`,
// one judgment goes to the coordinator for the episode, the deal draws no card on it, and
// it re-opens at the next hour by itself. The clock is the snapshot's, injected.
func TestARoutePastItsHourlyCapRestsWithOneJudgment(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a")
	w.s.Routes = []Route{
		{Name: "flash-a", Tier: "flash", Provider: "pa", Model: "a", Enabled: true, CapUSDHour: "5"},
		{Name: "flash-b", Tier: "flash", Provider: "pb", Model: "b", Enabled: true}, // no cap named here: the store resolves the tier's default (TierCapUSDHour) off the row
	}
	assert.Equal(t, "5", TierCapUSDHour["flash"], "the tier's default cap")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1", Width: 8}))
	w.must(Add(w.s, AddReq{Stream: "s1", Count: 4, Brief: "c: the work tier: flash\nThe task.\n"}))
	hour := t0.Truncate(time.Hour)
	capSpend(w, "old-1", "flash-a", "6.001", hour.Add(time.Minute)) // past $5 this hour
	capSpend(w, "old-2", "flash-b", "4.99", hour.Add(time.Minute))  // under it
	capSpend(w, "old-3", "flash-b", "100", hour.Add(-time.Minute))  // the hour before: not summed

	p, _ := TickDeal(w.s, TickReq{})
	w.must(p)
	on := spendCapDealtOn(w)
	assert.Zero(t, on["flash-a"], "no card is drawn on the route past its cap: %v", on)
	assert.Equal(t, 4, on["flash-b"], "the deal goes to the route that serves: %v", on)
	j := spendCapJudgments(w, RouteSubject("flash-a"))
	require.Len(t, j, 1, "one judgment of the episode: %v", w.notes)
	assert.Contains(t, j[0].What, "cap reached: $6.01 of $5.00 this hour", "money to the cent, rounded up")
	assert.Empty(t, spendCapJudgments(w, RouteSubject("flash-b")), "the route under its cap is not judged")
	rest := RouteRests(w.s.Routes, w.s.Fleet)["flash-a"]
	assert.Equal(t, RestCap, rest.Cause)
	assert.True(t, rest.Resting(w.s.Now))
	assert.Equal(t, hour.Add(time.Hour), rest.Until, "it rests to the next clock hour")
	assert.Equal(t, "cap reached: $6.01 of $5.00 this hour", rest.Why)

	// later in the same hour: still resting, no second judgment
	w.tick(20 * time.Minute)
	w.must(Add(w.s, AddReq{Stream: "s1", Count: 2, Brief: "c: the work tier: flash\nThe task.\n"}))
	p, _ = TickDeal(w.s, TickReq{})
	w.must(p)
	assert.Len(t, spendCapJudgments(w, RouteSubject("flash-a")), 1, "one judgment per episode, not one per tick")
	assert.Zero(t, spendCapDealtOn(w)["flash-a"])

	// the next hour: the sum starts again, the rest has ended by itself, the judgment closes
	w.s.Now = hour.Add(time.Hour + time.Minute)
	assert.False(t, RouteRests(w.s.Routes, w.s.Fleet)["flash-a"].Resting(w.s.Now), "the rest ends at its time")
	p, _ = TickDeal(w.s, TickReq{})
	w.must(p)
	for _, o := range w.s.Open {
		assert.NotEqual(t, NRouteCap, o.Note.Type, "the judgment closes when the route re-opens")
	}
	w.must(Add(w.s, AddReq{Stream: "s1", Count: 4, Brief: "c: the work tier: flash\nThe task.\n"}))
	p, _ = TickDeal(w.s, TickReq{})
	w.must(p)
	assert.Positive(t, spendCapDealtOn(w)["flash-a"], "the route is drawn again: %v", spendCapDealtOn(w))
}

// A friend's takes are summed by her row; past her cap (10 unless her row says, 0 is none)
// she is capped for the hour, and the hour before is not summed.
func TestAFriendPastHerHourlyCapIsCapped(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	u := cardcost.NoUsage()
	u.Predicted = "10.5"
	c := Consumer{Kind: "work", Card: "f1.w1", Attempt: 1, Gen: 1, Who: FriendRow("fay"), End: "finished", At: stamp(t0), Key: "f1.w1#g1", Usage: u}
	w.s.Work.Put(&Card{ID: "f1", Row: "s1", Col: DoneOK, Fields: map[string]string{FieldCostRecord + c.Key: c.line()}})
	seats := []FriendSeat{{Name: "fay", Status: Up}, {Name: "gil", Status: Up}}
	for _, tc := range []struct {
		name string
		caps map[string]string
		now  time.Time
		want []string
	}{
		{"no cap named is uncapped", nil, t0, nil},
		{"an empty value takes the default of 10", map[string]string{"fay": ""}, t0, []string{"fay"}},
		{"her own cap above the spend", map[string]string{"fay": "11"}, t0, nil},
		{"0 is no cap", map[string]string{"fay": "0"}, t0, nil},
		{"the next hour sums again", map[string]string{"fay": ""}, t0.Add(time.Hour), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := *w.s
			s.Now = tc.now
			got := friendCaps(&s, seats, tc.caps)
			assert.ElementsMatch(t, tc.want, mapKeys(got))
			if len(tc.want) > 0 {
				assert.Equal(t, "cap reached: $10.50 of $10.00 this hour", got["fay"])
			}
		})
	}
}

// docs/SPEC-SPRINT.md, spend-circuit-breakerb-bb.w8: a friend whose cost over the current
// clock hour has reached her cap is dealt no card until the next hour, and one judgment per
// episode goes to the coordinator, filed under friend-cap:<name>. The clock is the
// snapshot's, injected.
func TestAFriendPastHerHourlyCapIsDealtNoCardAndJudged(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("only friend fay"))
	u := cardcost.NoUsage()
	u.Predicted = "10.5"
	c := Consumer{Kind: "work", Card: "fay.w1", Attempt: 1, Gen: 1, Who: FriendRow("fay"), End: "finished", At: stamp(t0), Key: "fay.w1#g1", Usage: u}
	w.s.Work.Put(&Card{ID: "fay", Row: "s1", Col: DoneOK, Fields: map[string]string{FieldCostRecord + c.Key: c.line()}})
	seats := []FriendSeat{{Name: "fay", Width: 2, Status: Up, Class: "flash,pro"}}
	p, _ := TickDeal(w.s, TickReq{Friends: seats, FriendCaps: map[string]string{"fay": ""}})
	w.must(p)
	require.Nil(t, w.s.Fleet.Card("s1-1.w1"), "no work card is dealt to a friend past her cap")
	require.Equal(t, Ready, w.s.StateOf("s1-1"), "the card waits ready for her, dealt no machine")
	js := w.notesOf(NFriendCap)
	require.Len(t, js, 1, "one judgment of the episode: %v", w.notes)
	assert.Equal(t, FriendCapSubject("fay"), js[0].Stream)
	assert.Contains(t, js[0].What, "cap reached: $10.50 of $10.00 this hour", "money to the cent, rounded up")
}

func mapKeys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
