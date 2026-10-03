package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
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

// A take the provider refused for want of credit rests EVERY route of that provider in the
// tick that sees it (nova-tools#5199; the owner, 2026-10-03: "provider out of funds should
// never be a mystery failure."), with the reason on each rest; the card is dealt again on
// another provider's route; ONE judgment of the provider is open while its routes rest,
// never one per card, decided by funded, ack or wait and never by a rework; another refused
// take while they rest writes nothing more. The provider is excluded until a balance returns
// (the owner, 2026-10-03, 8:18 AM ET: "exclude that provider moving forward"): no clock ends
// the rest; the coordinator's funded does (a poll's balance is TestTheBalancePoll...), and
// the judgment closes with it.
func TestAnOutOfCreditTakeRestsEveryRouteOfItsProvider(t *testing.T) {
	t.Parallel()
	routes := []sprint.Route{providerRoute("or-a", "flash", "openrouter"), providerRoute("or-b", "flash", "openrouter"), providerRoute("oc-a", "flash", "opencode")}
	h := routeHarness(t, routes...)
	h.addReady("s1", 6, briefOf("flash", ""))
	h.startMachine()
	h.machine()
	var onOR []string
	for _, c := range h.snap().Fleet.Column(sprint.Ready) {
		if c.F(sprint.FieldRoute) != "oc-a" {
			onOR = append(onOR, c.ID)
		}
	}
	require.GreaterOrEqual(t, len(onOR), 2, "the deal draws the openrouter routes")

	h.failTake(onOR[0], creditLine)
	h.machine()
	s := h.snap()
	rests := sprint.RouteRests(routes, s.Fleet)
	for _, name := range []string{"or-a", "or-b"} {
		rest, ok := rests[name]
		require.True(t, ok, "%s rests: every route of the provider, not the one that failed", name)
		assert.Equal(t, sprint.RestCredit, rest.Cause)
		assert.True(t, rest.Funds())
		assert.Equal(t, []string{onOR[0]}, rest.Cards)
		assert.Contains(t, rest.Why, "out of credit: provider openrouter refused card "+onOR[0])
		assert.Contains(t, rest.Why, "class=out-of-credit status=402 msg=Insufficient credits.")
		assert.True(t, rest.Open(), "until a balance returns, never for a time")
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
	assert.Contains(t, h.noteWhats(sprint.NRouteRested)[0], "class=out-of-credit status=402", "the rest's note names the reason")

	// another card on the provider, dealt before the rest, is refused too: nothing more
	h.tick(1)
	h.failTake(onOR[1], creditLine)
	h.machine()
	h.machine()
	assert.Len(t, h.openOf(sprint.NProviderFunds), 1, "never one per card")
	assert.Equal(t, 1, h.written(sprint.NProviderFunds), "written once")
	assert.Equal(t, "oc-a", h.snap().Fleet.Card(onOR[1]).F(sprint.FieldRoute))
	h.clean("a provider resting for its funds")

	// no clock ends it: the provider stays excluded
	h.tick(4 * sprint.RouteRestFor)
	h.machine()
	assert.Len(t, h.openOf(sprint.NProviderFunds), 1, "still out of credit hours later")
	assert.True(t, sprint.RouteRests(routes, h.snap().Fleet)["or-a"].Resting(h.snap().Now))

	// the coordinator's word that it was paid ends the rests, and the judgment closes
	res, err := h.st.Run(h.ctx, FundedStep(sprint.FundedReq{Provider: "opencode", Reason: "paid", Who: "coordinator"}))
	require.NoError(t, err)
	require.Len(t, res.Refused, 1, "a provider with no rest of its funds: nothing to end")
	assert.Contains(t, res.Refused[0].Why, "provider opencode has no route resting for its funds")
	h.must(FundedStep(sprint.FundedReq{Provider: "openrouter", Reason: "paid $100 in the console", Who: "coordinator"}))
	s = h.snap()
	for _, name := range []string{"or-a", "or-b"} {
		rest := sprint.RouteRests(routes, s.Fleet)[name]
		assert.False(t, rest.Resting(s.Now), "%s serves again", name)
		assert.Contains(t, rest.Why, "ended: funded by coordinator: paid $100 in the console")
	}
	assert.Len(t, h.noteWhats(sprint.NProviderFunded), 2, "one note a route")
	h.machine()
	assert.Empty(t, h.openOf(sprint.NProviderFunds), "the routes serve again: the judgment closes")
	assert.Equal(t, 1, h.written(sprint.NProviderFunds))
	h.clean("the provider funded")
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
