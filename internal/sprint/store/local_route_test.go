package store

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The local tier on the mem twin (docs/SPEC-LOCAL.md, "Fleet"; internal/sprint/route.go,
// the lanes): a local route is a route the deal draws like any other, served on one fleet
// machine whose lanes bound the cards it takes at once; its price is 0 and its tokens are
// recorded.

// localRoute is a route of provider local served on machine, price 0.
func localRoute(name, machine string) sprint.Route {
	r := sprint.Route{Name: name, Tier: "flash", Provider: "local", Model: "gemma4-32k", Machine: machine, Deadline: routeSeconds, Enabled: true}
	r.Prices = cardcost.PricesOf(map[string]string{cardcost.FieldInput: "0", cardcost.FieldOutput: "0"})
	return r
}

// A card is dealt to a local route with its serving machine on the work card and in the
// packet; the machine's one lane takes one card at a time, the rest wait with no judgment;
// the finished take's usage is recorded at cost 0; and a freed lane takes the next card.
func TestALocalRouteTakesOneCardPerLaneAndRecordsItsUsageAtNoCost(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, localRoute("local-gemma4-32k-g1", "g1"))
	h.m.SetLanes(map[string]int{"g1": 1})
	h.addReady("s1", 3, briefOf("flash", ""))
	h.run(DealStep(sprint.DealReq{}))
	cards := h.workCards()
	require.Len(t, cards, 1, "one lane takes one card")
	var wc *sprint.Card
	for _, c := range cards {
		wc = c
	}
	assert.Equal(t, "local-gemma4-32k-g1", wc.F(sprint.FieldRoute))
	assert.Equal(t, "local/gemma4-32k", wc.F(sprint.FieldModel))
	assert.Equal(t, "g1", wc.F(sprint.FieldServe))
	ps, err := h.st.Packets(h.ctx, []*sprint.Card{wc})
	require.NoError(t, err)
	assert.Equal(t, "g1", ps[0].Serve, "the member points the harness at the serving machine")
	h.startMachine()
	h.machine()
	assert.Empty(t, h.a2Open(sprint.NNoRoute), "a full lane is no judgment: the tier is served")
	assert.Len(t, h.workCards(), 1, "the tick holds the lane too")

	gens := map[string]int{wc.ID: wc.Int("gen")}
	h.must(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: gens, Who: wc.Row}))
	h.run(DealStep(sprint.DealReq{}))
	assert.Len(t, h.workCards(), 1, "a working card holds the lane")
	h.must(FinishStep(sprint.FinishReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: gens, Report: "done",
		Usage: "wall=40.00s budget=1500/unmetered input=1200 output=300 model=local/gemma4-32k", Who: wc.Row}))
	done := h.snap().Fleet.Card(wc.ID)
	assert.Contains(t, done.F(sprint.FieldUsage), "input=1200")
	assert.Contains(t, done.F(sprint.FieldUsage), "price_route=local-gemma4-32k-g1")
	view := sprint.CardCostOf(h.snap().Work.Card(done.F("primary")))
	lines := view.CostLines()
	require.NotEmpty(t, lines)
	assert.Contains(t, lines[0], "route=local-gemma4-32k-g1")
	assert.Contains(t, lines[0], "predicted_usd=0 ")

	h.run(DealStep(sprint.DealReq{}))
	assert.Len(t, h.workCards(), 2, "the freed lane takes the next card")
}

// Beside an API route of the tier, a full lane sends the next card to the API route; a
// machine with no lanes serves nothing, and a tier only it would serve is judged unserved.
func TestAFullLaneDrawsTheNextRouteAndNoLaneServesNone(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, localRoute("local-gemma4-32k-g1", "g1"), route("flash-a", "flash"))
	h.m.SetLanes(map[string]int{"g1": 1})
	h.addReady("s1", 4, briefOf("flash", ""))
	h.run(DealStep(sprint.DealReq{}))
	on := map[string]int{}
	for _, c := range h.workCards() {
		on[c.F(sprint.FieldRoute)]++
	}
	assert.Equal(t, map[string]int{"local-gemma4-32k-g1": 1, "flash-a": 3}, on)

	h2 := routeHarness(t, localRoute("local-gemma4-32k-g1", "g1"))
	h2.addReady("s1", 1, briefOf("flash", ""))
	res := h2.run(DealStep(sprint.DealReq{}))
	assert.Contains(t, fmt.Sprint(res.Refused), "no enabled route serves tier flash")
	h2.startMachine()
	h2.machine()
	assert.Empty(t, h2.workCards(), "a machine with no lanes serves none")
	assert.Len(t, h2.a2Open(sprint.NNoRoute), 1, "the tier only a laneless machine would serve is judged unserved")
}
