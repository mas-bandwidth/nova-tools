package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// poll is one balance poll's step, as the run loop writes what it read.
func (h *harness) poll(reads ...sprint.ProviderRead) {
	h.t.Helper()
	h.must(BalanceStep(sprint.BalanceReq{Reads: reads, Who: sprint.MachineActor}))
}

// openrouter is a read of openrouter's credits: bought and used.
func openrouter(credits, used float64) sprint.ProviderRead {
	return sprint.ProviderRead{Provider: "openrouter", Known: true, Balance: credits - used, HasUsed: true, Used: used}
}

// The balance poll (nova-tools#5199): each read is written to its provider's property, the
// spend an hour measured from the provider's count used between two reads; a balance under
// one hour of that spend rests every route of the provider before any take is refused, and
// the provider's one judgment opens; a later read over it (a payment) ends the rest and the
// judgment closes; a provider with no balance to read is unknown and rests nothing.
func TestTheBalancePollRestsAProviderUnderAnHourOfItsSpend(t *testing.T) {
	t.Parallel()
	routes := []sprint.Route{providerRoute("or-a", "flash", "openrouter"), providerRoute("or-b", "flash", "openrouter"), providerRoute("oc-a", "flash", "opencode")}
	h := routeHarness(t, routes...)
	h.addReady("s1", 2, briefOf("flash", ""))
	h.startMachine()
	opencode := sprint.ProviderRead{Provider: "opencode", Note: "opencode Zen publishes no balance endpoint"}

	h.poll(openrouter(1250, 1150), opencode) // $100 left, no spend measured yet
	b := sprint.ProviderBalances(h.snap().Fleet)
	require.Contains(t, b, "openrouter")
	assert.InDelta(t, 100, b["openrouter"].Balance, 1e-9)
	assert.Zero(t, b["openrouter"].SpendHour, "one read measures no spend")
	assert.False(t, b["opencode"].Known)
	assert.Equal(t, "unknown: opencode Zen publishes no balance endpoint", b["opencode"].Said())
	assert.Empty(t, sprint.RouteRests(routes, h.snap().Fleet), "nothing rests")

	h.tick(10 * time.Minute)
	h.poll(openrouter(1250, 1160), opencode) // $10 in ten minutes: $60 an hour, $90 left
	assert.InDelta(t, 60, sprint.ProviderBalances(h.snap().Fleet)["openrouter"].SpendHour, 1e-9)
	assert.Empty(t, sprint.RouteRests(routes, h.snap().Fleet), "$90 is over an hour at $60")

	h.tick(10 * time.Minute)
	h.poll(openrouter(1250, 1210), opencode) // $50 in ten minutes: $300 an hour, $40 left
	s := h.snap()
	rests := sprint.RouteRests(routes, s.Fleet)
	for _, name := range []string{"or-a", "or-b"} {
		require.Contains(t, rests, name, "every route of the provider rests")
		assert.Equal(t, sprint.RestBalance, rests[name].Cause)
		assert.Contains(t, rests[name].Why, "provider openrouter balance $40.00 at")
		assert.Contains(t, rests[name].Why, "is under one hour of its spend ($300.00 an hour)")
	}
	assert.NotContains(t, rests, "oc-a", "an unknown balance rests nothing")
	h.machine()
	open := h.openOf(sprint.NProviderFunds)
	require.Len(t, open, 1)
	assert.Contains(t, open[0].Note.What, "provider openrouter is out of funds (balance $40.00 at ")
	for _, c := range h.snap().Fleet.Column(sprint.Ready) {
		assert.Equal(t, "oc-a", c.F(sprint.FieldRoute), "%s: dealt on the provider with funds", c.ID)
	}

	h.tick(10 * time.Minute)
	h.poll(openrouter(2250, 1220), opencode) // a payment: $1030 left at $60 an hour
	s = h.snap()
	for name, r := range sprint.RouteRests(routes, s.Fleet) {
		assert.False(t, r.Resting(s.Now), "%s serves again", name)
		assert.Contains(t, r.Why, "ended: balance $1030.00 at")
	}
	assert.Len(t, h.noteWhats(sprint.NProviderFunded), 2, "one note a route")
	h.machine()
	assert.Empty(t, h.openOf(sprint.NProviderFunds), "the judgment closes with the rest")
	h.clean("a provider paid")

	rows := sprint.ProviderRows(routes, h.snap().Fleet, h.snap().Now)
	require.Len(t, rows, 2)
	assert.Equal(t, sprint.ProviderRow{Name: "opencode", Balance: "unknown", BalanceAt: rows[0].BalanceAt, State: "serving", Note: "opencode Zen publishes no balance endpoint"}, rows[0])
	assert.Equal(t, "openrouter", rows[1].Name)
	assert.Equal(t, "$1030.00", rows[1].Balance)
	assert.InDelta(t, 60, rows[1].SpendHour, 1e-9)
	assert.Equal(t, "serving", rows[1].State)
}
