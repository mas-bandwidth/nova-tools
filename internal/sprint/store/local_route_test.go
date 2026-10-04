package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The local tier on the mem twin (docs/SPEC-LOCAL.md, "Fleet"; internal/sprint/route.go,
// the cap): a local route is a route the deal draws like any other, served at its endpoint,
// whose concurrency bounds the cards that use it at once; its price is 0 and its tokens
// are recorded.

// localRoute is a route of provider local served at the endpoint of host, at most
// concurrency cards at once, price 0.
func localRoute(name, host string, concurrency int) sprint.Route {
	r := sprint.Route{Name: name, Tier: "flash", Provider: "local", Model: "gemma4-32k", Endpoint: "http://" + host + ".test:11434/v1", Concurrency: concurrency, Deadline: routeSeconds, Enabled: true}
	r.Prices = cardcost.PricesOf(map[string]string{cardcost.FieldInput: "0", cardcost.FieldOutput: "0"})
	return r
}

// A card is dealt to a local route with its endpoint on the work card and in the packet;
// the route's concurrency of one takes one card at a time, the rest wait with no judgment;
// the finished take's usage is recorded at cost 0; and a freed slot takes the next card.
func TestALocalRouteTakesItsConcurrencyOfCardsAndRecordsItsUsageAtNoCost(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, localRoute("local-gemma4-32k-g1", "g1", 1))
	h.addReady("s1", 3, briefOf("flash", ""))
	h.run(DealStep(sprint.DealReq{}))
	cards := h.workCards()
	require.Len(t, cards, 1, "a concurrency of one takes one card")
	var wc *sprint.Card
	for _, c := range cards {
		wc = c
	}
	assert.Equal(t, "local-gemma4-32k-g1", wc.F(sprint.FieldRoute))
	assert.Equal(t, "local/gemma4-32k", wc.F(sprint.FieldModel))
	assert.Equal(t, "http://g1.test:11434/v1", wc.F(sprint.FieldServe))
	ps, err := h.st.Packets(h.ctx, []*sprint.Card{wc})
	require.NoError(t, err)
	assert.Equal(t, "http://g1.test:11434/v1", ps[0].Serve, "the member points the harness at the route's endpoint")
	h.startMachine()
	h.machine()
	assert.Empty(t, h.a2Open(sprint.NNoRoute), "a route at its concurrency is no judgment: the tier is served")
	assert.Len(t, h.workCards(), 1, "the tick holds the slot too")

	gens := map[string]int{wc.ID: wc.Int("gen")}
	h.must(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: gens, Who: wc.Row}))
	h.run(DealStep(sprint.DealReq{}))
	assert.Len(t, h.workCards(), 1, "a working card holds the slot")
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
	assert.Len(t, h.workCards(), 2, "the freed slot takes the next card")
}

// Beside an API route of the tier, a route at its concurrency sends the next card to the API
// route; one with no cap takes every card, the same field on a local and a metered route
// (a machine's lanes are its width; which model serves them is the route's).
func TestARouteAtItsConcurrencyDrawsTheNextRoute(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, localRoute("local-gemma4-32k-g1", "g1", 1), route("flash-a", "flash"))
	h.addReady("s1", 4, briefOf("flash", ""))
	h.run(DealStep(sprint.DealReq{}))
	on := map[string]int{}
	for _, c := range h.workCards() {
		on[c.F(sprint.FieldRoute)]++
	}
	assert.Equal(t, map[string]int{"local-gemma4-32k-g1": 1, "flash-a": 3}, on)

	capped := route("flash-a", "flash")
	capped.Concurrency = 2
	h2 := routeHarness(t, capped, localRoute("local-gemma4-32k-g1", "g1", 1))
	h2.addReady("s1", 6, briefOf("flash", ""))
	h2.run(DealStep(sprint.DealReq{}))
	on = map[string]int{}
	for _, c := range h2.workCards() {
		on[c.F(sprint.FieldRoute)]++
	}
	assert.Equal(t, map[string]int{"local-gemma4-32k-g1": 1, "flash-a": 2}, on, "a metered route is held to its concurrency as a local one is")

	h3 := routeHarness(t, localRoute("local-gemma4-32k-g1", "g1", 1))
	h3.addReady("s1", 2, briefOf("flash", ""))
	h3.run(DealStep(sprint.DealReq{}))
	h3.startMachine()
	h3.machine()
	assert.Len(t, h3.workCards(), 1, "the one slot is taken")
	assert.Empty(t, h3.a2Open(sprint.NNoRoute), "the tier is served; the second card waits for the slot")
}
