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
// spend an hour measured from the provider's count used between two reads; a balance over
// zero but not over one hour of that spend rests the provider LOW ON FUNDS before any take
// is refused, and its one judgment opens. The rest does not flap: the spend is the one
// measured before the rest began, so reads that show the resting provider spending nothing,
// and a payment smaller than that hour of spend, end nothing; a read over it (a payment)
// ends the rest and the judgment closes. A provider with no balance to read is unknown and
// rests nothing.
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
	restAt := s.Now
	rests := sprint.RouteRests(routes, s.Fleet)
	for _, name := range []string{"or-a", "or-b"} {
		require.Contains(t, rests, name, "every route of the provider rests")
		assert.Equal(t, sprint.RestBalance, rests[name].Cause)
		assert.False(t, rests[name].Out(), "low on funds is not out of credit")
		assert.Contains(t, rests[name].Why, "low on funds: provider openrouter balance $40.00 at")
		assert.Contains(t, rests[name].Why, "is not over one hour of its spend ($300.00 an hour)")
	}
	assert.NotContains(t, rests, "oc-a", "an unknown balance rests nothing")
	h.machine()
	assert.Empty(t, h.openOf(sprint.NProviderFunds), "not out of funds")
	open := h.openOf(sprint.NProviderLow)
	require.Len(t, open, 1)
	assert.Contains(t, open[0].Note.What, "provider openrouter is low on funds (balance $40.00 at ")
	assert.Contains(t, open[0].Note.What, "it is not out of credit, and the sprint does not stop for it")
	assert.Equal(t, []string{"funded openrouter", "ack", "wait"}, open[0].Note.Decisions)
	assert.True(t, rests["or-a"].Open(), "until a balance returns")
	for _, c := range h.snap().Fleet.Column(sprint.Ready) {
		assert.Equal(t, "oc-a", c.F(sprint.FieldRoute), "%s: dealt on the provider with funds", c.ID)
	}

	// the resting provider spends next to nothing, and a payment under an hour of the spend
	// measured before the rest is not enough: the rest holds, the same rest
	for _, read := range []sprint.ProviderRead{openrouter(1250, 1210), openrouter(1250, 1215), openrouter(1500, 1215)} {
		h.tick(10 * time.Minute)
		h.poll(read, opencode)
		s = h.snap()
		bal := sprint.ProviderBalances(s.Fleet)["openrouter"]
		assert.InDelta(t, 300, bal.SpendHour, 1e-9, "balance %v: the spend measured before the rest", bal.Balance)
		rest := sprint.ProviderRests(s.Fleet)["openrouter"]
		assert.True(t, rest.Resting(s.Now), "balance %v: not over $300, still resting", bal.Balance)
		assert.Equal(t, restAt, rest.At, "the same rest: it never flapped")
	}
	h.machine()
	assert.Len(t, h.openOf(sprint.NProviderLow), 1)
	assert.Equal(t, 1, h.written(sprint.NProviderLow), "written once")

	h.tick(10 * time.Minute)
	h.poll(openrouter(2250, 1220), opencode) // a payment: $1030 left, over the $300
	s = h.snap()
	for name, r := range sprint.RouteRests(routes, s.Fleet) {
		assert.False(t, r.Resting(s.Now), "%s serves again", name)
		assert.Contains(t, r.Why, "ended: balance $1030.00 at")
	}
	assert.Len(t, h.noteWhats(sprint.NProviderFunded), 1, "one note of the provider")
	h.machine()
	assert.Empty(t, h.openOf(sprint.NProviderLow), "the judgment closes with the rest")
	h.clean("a provider paid")

	h.tick(10 * time.Minute)
	h.poll(openrouter(2250, 1230), opencode) // spend measured again from the read that ended it
	assert.InDelta(t, 60, sprint.ProviderBalances(h.snap().Fleet)["openrouter"].SpendHour, 1e-9)

	rows := sprint.ProviderRows(routes, h.snap().Fleet, h.snap().Now)
	require.Len(t, rows, 2)
	assert.Equal(t, sprint.ProviderRow{Name: "opencode", Balance: "unknown", BalanceAt: rows[0].BalanceAt, State: "serving", Note: "opencode Zen publishes no balance endpoint"}, rows[0])
	assert.Equal(t, "openrouter", rows[1].Name)
	assert.Equal(t, "$1020.00", rows[1].Balance)
	assert.InDelta(t, 60, rows[1].SpendHour, 1e-9)
	assert.Equal(t, "serving", rows[1].State)
}

// Low on funds never stops the sprint; out of credit does (the owner, 2026-10-03: "if all
// providers are out, then you stop the sprint."). With one provider: a balance not over an
// hour of its spend rests it low, and the machine runs on, the tier's judgment saying why
// nothing deals; a balance at zero is out of credit and the tick stops the machine; a small
// payment (over zero, not over the hour) makes it low again, and a start is no longer
// refused.
func TestLowOnFundsNeverStopsTheSprintAndOutOfCreditDoes(t *testing.T) {
	t.Parallel()
	routes := []sprint.Route{providerRoute("or-a", "flash", "openrouter")}
	h := routeHarness(t, routes...)
	h.addReady("s1", 2, briefOf("flash", ""))
	h.startMachine()
	h.poll(openrouter(148, 100)) // $48
	h.tick(10 * time.Minute)
	h.poll(openrouter(148, 108)) // $40 at $48 an hour: low
	h.machine()
	h.tick(10 * time.Second)
	h.machine()
	assert.True(t, h.machineRecord().Running(), "a provider with $40 is not out: the sprint runs")
	assert.Empty(t, h.openOf(sprint.NAllOutOfCredit))
	assert.Len(t, h.openOf(sprint.NProviderLow), 1)
	why, err := h.st.OutOfCredit(h.ctx)
	require.NoError(t, err)
	assert.Empty(t, why, "a start is not refused")

	h.tick(10 * time.Minute)
	h.poll(openrouter(148, 108)) // nothing spent while it rests: still low, never lifted
	assert.Equal(t, sprint.RestBalance, sprint.ProviderRests(h.snap().Fleet)["openrouter"].Cause)

	h.tick(10 * time.Minute)
	h.poll(openrouter(148, 148)) // $0: out of credit
	assert.Equal(t, sprint.RestCredit, sprint.ProviderRests(h.snap().Fleet)["openrouter"].Cause)
	h.machine()
	assert.False(t, h.machineRecord().Running(), "every provider is out: the tick stopped the machine")
	assert.Len(t, h.openOf(sprint.NProviderFunds), 1)

	h.tick(10 * time.Minute)
	h.poll(openrouter(168, 148)) // $20 paid, under the $48 an hour measured before the rest
	rest := sprint.ProviderRests(h.snap().Fleet)["openrouter"]
	assert.Equal(t, sprint.RestBalance, rest.Cause, "money again: low, not out")
	assert.True(t, rest.Resting(h.snap().Now))
	why, err = h.st.OutOfCredit(h.ctx)
	require.NoError(t, err)
	assert.Empty(t, why, "a provider with money: the start is the coordinator's")
	h.clean("low after out")
}

// An unknown balance changes no rest (balance.go: an unknown read writes and ends none): a
// provider that serves is not rested by it, and one resting out of credit for a refused take
// still rests after it.
func TestAnUnknownBalanceChangesNoRest(t *testing.T) {
	t.Parallel()
	routes := []sprint.Route{providerRoute("or-a", "flash", "openrouter"), providerRoute("oc-a", "flash", "opencode")}
	h := routeHarness(t, routes...)
	h.addReady("s1", 4, briefOf("flash", ""))
	h.startMachine()
	h.machine()
	unknown := func(p string) sprint.ProviderRead {
		return sprint.ProviderRead{Provider: p, Note: "no balance endpoint"}
	}
	h.poll(unknown("opencode"), unknown("openrouter"))
	assert.Empty(t, sprint.ProviderRests(h.snap().Fleet), "a provider that serves is not rested by an unknown balance")

	onOC := h.onProvider(routes, "opencode")
	require.NotEmpty(t, onOC)
	h.failTake(onOC[0], creditLine)
	h.machine()
	require.True(t, sprint.ProviderRests(h.snap().Fleet)["opencode"].Resting(h.snap().Now))
	for range 3 {
		h.tick(sprint.BalancePollEvery)
		h.poll(unknown("opencode"), unknown("openrouter"))
	}
	s := h.snap()
	rest := sprint.ProviderRests(s.Fleet)["opencode"]
	assert.True(t, rest.Resting(s.Now), "an unknown balance ends no rest")
	assert.Equal(t, sprint.RestCredit, rest.Cause)
	assert.NotContains(t, sprint.ProviderRests(s.Fleet), "openrouter")
	assert.Empty(t, h.noteWhats(sprint.NProviderFunded))
}
