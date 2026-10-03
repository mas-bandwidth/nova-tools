package store

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// creditLine is the finish of a take the provider refused for want of credit, as the member
// writes it from native's PROVIDER-FAIL line (nova-tools#5199, slot ci-03.w246.g4).
const creditLine = cardhdr.EndProvider + ": provider: class=out-of-credit status=402 msg=Insufficient credits. Add more using https://openrouter.test/settings/credits"

// providerRoute is a route of the tier on the provider.
func providerRoute(name, tier, provider string) sprint.Route {
	r := route(name, tier)
	r.Provider = provider
	return r
}

// takeCard is a member taking the work card: its child launches now. It returns the gens a
// finish of it sends.
func (h *harness) takeCard(card string) map[string]int {
	h.t.Helper()
	wc := h.snap().Fleet.Card(card)
	require.NotNil(h.t, wc)
	gens := map[string]int{wc.ID: wc.Int("gen")}
	h.must(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: gens, Who: wc.Row}))
	return gens
}

// refuseTaken is the provider's refusal of a card taken earlier (takeCard) arriving now.
func (h *harness) refuseTaken(card string, gens map[string]int, report string) {
	h.t.Helper()
	wc := h.snap().Fleet.Card(card)
	require.NotNil(h.t, wc)
	h.must(FinishStep(sprint.FinishReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: gens, Failed: true,
		Report: report, Usage: "wall=1.50s budget=1/1000", Who: wc.Row}))
}

// onProvider is the ready work cards dealt on a route of the provider.
func (h *harness) onProvider(routes []sprint.Route, provider string) []string {
	h.t.Helper()
	of := map[string]string{}
	for _, r := range routes {
		of[r.Name] = r.Provider
	}
	var out []string
	for _, c := range h.snap().Fleet.Column(sprint.Ready) {
		if of[c.F(sprint.FieldRoute)] == provider {
			out = append(out, c.ID)
		}
	}
	return out
}

// A take the provider refused for want of credit rests the PROVIDER, every route of it, in
// the tick that sees it (nova-tools#5199; the owner, 2026-10-03: "provider out of funds
// should never be a mystery failure."): one fleet table property, the reason on it; the
// card is dealt again on another provider's route; ONE judgment of the provider is open
// while it rests, never one per card, decided by funded, ack or wait and never by a rework.
// A take in flight when the rest began, refused a minute after it, writes nothing more. The
// provider is excluded until a balance returns (the owner, 2026-10-03, 8:18 AM ET: "exclude
// that provider moving forward"): no clock ends the rest; the coordinator's funded does (a
// poll's balance is TestTheBalancePoll...), and the judgment closes with it. The refusal
// that arrived after the rest began was launched before it ended, so funded is not undone
// at the next tick; a take launched after funded and refused rests the provider again.
func TestAnOutOfCreditTakeRestsEveryRouteOfItsProvider(t *testing.T) {
	t.Parallel()
	routes := []sprint.Route{providerRoute("or-a", "flash", "openrouter"), providerRoute("or-b", "flash", "openrouter"), providerRoute("oc-a", "flash", "opencode")}
	h := routeHarness(t, routes...)
	h.addReady("s1", 6, briefOf("flash", ""))
	h.startMachine()
	h.machine()
	onOR := h.onProvider(routes, "openrouter")
	require.GreaterOrEqual(t, len(onOR), 2, "the deal draws the openrouter routes")

	inFlight := h.takeCard(onOR[1]) // launched before the rest
	h.tick(30 * time.Second)
	h.failTake(onOR[0], creditLine)
	h.machine()
	s := h.snap()
	restAt := s.Now
	rests := sprint.RouteRests(routes, s.Fleet)
	for _, name := range []string{"or-a", "or-b"} {
		rest, ok := rests[name]
		require.True(t, ok, "%s rests: every route of the provider, not the one that failed", name)
		assert.Equal(t, "openrouter", rest.Provider, "the provider's rest")
		assert.Equal(t, sprint.RestCredit, rest.Cause)
		assert.True(t, rest.Out())
		assert.Equal(t, []string{onOR[0]}, rest.Cards)
		assert.Contains(t, rest.Why, "out of credit: provider openrouter refused card "+onOR[0])
		assert.Contains(t, rest.Why, "class=out-of-credit status=402 msg=Insufficient credits.")
		assert.True(t, rest.Open(), "until a balance returns, never for a time")
	}
	props := s.Fleet.Props()
	assert.Contains(t, props, sprint.PropProviderRest("openrouter"), "one property of the provider")
	for _, name := range []string{"or-a", "or-b", "oc-a"} {
		assert.NotContains(t, props, sprint.PropRouteRest(name), "no copy on the route")
	}
	_, ocRests := rests["oc-a"]
	assert.False(t, ocRests, "another provider's route serves on")
	assert.Equal(t, "oc-a", s.Fleet.Card(onOR[0]).F(sprint.FieldRoute), "the card is dealt again on the provider that has funds")
	assert.Equal(t, sprint.Ready, s.Fleet.Card(onOR[0]).Col)

	open := h.openOf(sprint.NProviderFunds)
	require.Len(t, open, 1, "one judgment of the provider")
	n := open[0].Note
	assert.Equal(t, sprint.ProviderSubject("openrouter"), n.Stream)
	assert.Contains(t, n.What, "provider openrouter is out of funds (balance unknown: not polled yet): a payment is the owner's")
	assert.Contains(t, n.What, "it is excluded: its routes or-a, or-b rest until a balance returns")
	assert.Equal(t, []string{"funded openrouter", "ack", "wait"}, n.Decisions, "a payment is the owner's: no rework is offered")
	assert.Empty(t, h.openOf(sprint.NBound), "no card's judgment")
	rested := h.noteWhats(sprint.NProviderRested)
	require.Len(t, rested, 1, "one note of the provider's rest")
	assert.Contains(t, rested[0], "provider openrouter rested until a balance returns, its routes or-a, or-b: ")
	assert.Contains(t, rested[0], "class=out-of-credit status=402", "the rest's note names the reason")

	// the card in flight when the rest began is refused a minute after it: nothing more
	h.tick(time.Minute)
	h.refuseTaken(onOR[1], inFlight, creditLine)
	h.machine()
	h.machine()
	assert.Len(t, h.openOf(sprint.NProviderFunds), 1, "never one per card")
	assert.Equal(t, 1, h.written(sprint.NProviderFunds), "written once")
	assert.Equal(t, "oc-a", h.snap().Fleet.Card(onOR[1]).F(sprint.FieldRoute))
	assert.Equal(t, restAt, sprint.ProviderRests(h.snap().Fleet)["openrouter"].At, "the same rest")
	h.clean("a provider resting for its funds")

	// no clock ends it: the provider stays excluded
	h.tick(4 * sprint.RouteRestFor)
	h.machine()
	assert.Len(t, h.openOf(sprint.NProviderFunds), 1, "still out of credit hours later")
	assert.True(t, sprint.RouteRests(routes, h.snap().Fleet)["or-a"].Resting(h.snap().Now))

	// the coordinator's word that it was paid ends the rest, and the judgment closes
	res, err := h.st.Run(h.ctx, FundedStep(sprint.FundedReq{Provider: "opencode", Reason: "paid", Who: "coordinator"}))
	require.NoError(t, err)
	require.Len(t, res.Refused, 1, "a provider with no rest of its funds: nothing to end")
	assert.Contains(t, res.Refused[0].Why, "provider opencode does not rest for its funds")
	h.must(FundedStep(sprint.FundedReq{Provider: "openrouter", Reason: "paid $100 in the console", Who: "coordinator"}))
	s = h.snap()
	for _, name := range []string{"or-a", "or-b"} {
		rest := sprint.RouteRests(routes, s.Fleet)[name]
		assert.False(t, rest.Resting(s.Now), "%s serves again", name)
		assert.Contains(t, rest.Why, "ended: funded by coordinator: paid $100 in the console")
	}
	funded := h.noteWhats(sprint.NProviderFunded)
	require.Len(t, funded, 1, "one note of the provider")
	assert.Equal(t, "provider openrouter serves again, its routes or-a, or-b: it was paid (paid $100 in the console, by coordinator)", funded[0])

	// the refusal that arrived after the rest began was launched before funded: the next
	// ticks rest nothing, and the judgment closes
	h.tick(time.Minute)
	h.machine()
	h.tick(time.Minute)
	h.machine()
	assert.False(t, sprint.RouteRests(routes, h.snap().Fleet)["or-a"].Resting(h.snap().Now), "funded is not undone by a refusal launched before it")
	assert.Empty(t, h.openOf(sprint.NProviderFunds), "the provider serves again: the judgment closes")
	assert.Equal(t, 1, h.written(sprint.NProviderFunds))
	assert.Len(t, h.noteWhats(sprint.NProviderRested), 1, "no second rest")
	h.clean("the provider funded")

	// a take launched after funded and refused is new evidence: the provider rests again
	h.addReady("s2", 6, briefOf("flash", ""))
	h.machine()
	again := h.onProvider(routes, "openrouter")
	require.NotEmpty(t, again, "the deal draws the openrouter routes again")
	h.tick(30 * time.Second)
	h.failTake(again[0], creditLine)
	h.machine()
	rest := sprint.ProviderRests(h.snap().Fleet)["openrouter"]
	assert.True(t, rest.Resting(h.snap().Now), "a refusal launched after the end rests the provider again")
	assert.Equal(t, []string{again[0]}, rest.Cards)
	assert.Len(t, h.openOf(sprint.NProviderFunds), 1)
	h.clean("the provider out again")
}

// The fleet table's properties grow with the providers, never with the routes
// (ntable.LimitTableProps, the 64 a table holds): at 100 routes over two providers and two
// tiers, both providers out of credit and both balances polled, the count is the deal's
// indexes, one rest and one balance a provider, far under the bound, and the ticks write.
func TestTheFleetPropertiesAtAHundredRoutesStayUnderTheBound(t *testing.T) {
	t.Parallel()
	var routes []sprint.Route
	for i := range 100 {
		provider, tier := "openrouter", "flash"
		if i%2 == 1 {
			provider = "opencode"
		}
		if i%4 >= 2 {
			tier = "pro"
		}
		routes = append(routes, providerRoute(fmt.Sprintf("r%03d", i), tier, provider))
	}
	h := routeHarness(t, routes...)
	h.addReady("s1", 20, briefOf("flash", ""))
	h.startMachine()
	h.machine()
	or, oc := h.onProvider(routes, "openrouter"), h.onProvider(routes, "opencode")
	require.NotEmpty(t, or)
	require.NotEmpty(t, oc)
	h.tick(30 * time.Second)
	h.failTake(or[0], creditLine)
	h.failTake(oc[0], creditLine)
	h.poll(openrouter(1250, 1250.51), sprint.ProviderRead{Provider: "opencode", Note: "opencode Zen publishes no balance endpoint"})
	h.machine()
	h.machine()
	props := h.snap().Fleet.Props()
	var names []string
	for name := range props {
		names = append(names, name)
		assert.False(t, strings.HasPrefix(name, "route_rest_"), "%s: a provider's rest is never a copy per route", name)
	}
	assert.Contains(t, props, sprint.PropProviderRest("openrouter"))
	assert.Contains(t, props, sprint.PropProviderRest("opencode"))
	assert.LessOrEqual(t, len(props), 2*2+2*2+2, "the deal's indexes of two tiers, a rest and a balance of two providers, and room for two more: %v", names)
	assert.Less(t, len(props), ntable.LimitTableProps)
	assert.False(t, h.machineRecord().Running(), "every provider out: the tick stopped the machine, and wrote")
}

// When every enabled route is the refused provider's, the tick STOPS the machine itself
// (the owner, 2026-10-03, 8:18 AM ET: "if all providers are out, then you stop the
// sprint."): STOPPED with the cause, one judgment, the tier's judgment naming the reason; a
// start is refused while they stay out; the coordinator's funded ends the rests, and a start
// runs the machine again.
func TestEveryProviderOutOfCreditStopsTheMachine(t *testing.T) {
	t.Parallel()
	routes := []sprint.Route{providerRoute("or-a", "flash", "openrouter"), providerRoute("or-b", "flash", "openrouter")}
	h := routeHarness(t, routes...)
	h.addReady("s1", 3, briefOf("flash", ""))
	h.startMachine()
	h.machine()
	h.failTake("s1-1.w1", creditLine)
	res := h.machine()
	m := h.machineRecord()
	assert.False(t, m.Running(), "the tick stopped the machine")
	assert.Equal(t, sprint.FundsCause, m.Cause)
	assert.Equal(t, "machine: STOPPED (every provider is out of credit)", MachineLine(h.now, m, Heartbeat{}))
	assert.Contains(t, res.Halted, "every provider is out of credit (openrouter): a payment is the owner's")
	all := h.openOf(sprint.NAllOutOfCredit)
	require.Len(t, all, 1, "one judgment")
	assert.Contains(t, all[0].Note.What, "every provider is out of credit (openrouter)")
	assert.Len(t, h.openOf(sprint.NProviderFunds), 1)
	open := h.openOf(sprint.NNoRoute)
	require.Len(t, open, 1, "the tier waits, once")
	assert.Contains(t, open[0].Note.What, "class=out-of-credit status=402")
	assert.Equal(t, sprint.Withdrawn, h.snap().Fleet.Card("s1-1.w1").Col, "not dealt on a resting provider")

	why, err := h.st.OutOfCredit(h.ctx)
	require.NoError(t, err)
	assert.Contains(t, why, sprint.FundsCause, "a start is refused with this line")

	h.must(FundedStep(sprint.FundedReq{Provider: "openrouter", Reason: "paid", Who: "coordinator"}))
	why, err = h.st.OutOfCredit(h.ctx)
	require.NoError(t, err)
	assert.Empty(t, why, "a provider in funds: the start is the coordinator's")
	h.startMachine()
	h.machine()
	assert.True(t, h.machineRecord().Running())
	assert.Empty(t, h.openOf(sprint.NAllOutOfCredit), "the judgment closes")
	assert.Equal(t, sprint.Ready, h.snap().Fleet.Card("s1-1.w1").Col, "dealt again")
	h.clean("a provider funded after every one was out")
}
