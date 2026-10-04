package store

import (
	"fmt"
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
	assert.True(t, rests["or-a"].Open(), "until paid")
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

// A provider out of credit by a refused take whose balance reads small and over zero stays
// out of credit until a payment is seen (nova-tools#5199, the second cold read's probe B):
// OpenRouter refuses with 402 a request whose estimated cost the balance cannot cover, so a
// provider that refuses still reads $0.30. Beside opencode serving, over 40 minutes of polls
// at that unchanged balance it rests ONCE, its judgment written once, and no poll lifts it:
// nothing is dealt on it again, so nothing is refused again. The providers table shows it
// out of credit with its balance beside it.
func TestARefusedProviderReadingASmallBalanceRestsOnceBesideAServingOne(t *testing.T) {
	t.Parallel()
	routes := []sprint.Route{providerRoute("or-a", "flash", "openrouter"), providerRoute("or-b", "flash", "openrouter"), providerRoute("oc-a", "flash", "opencode")}
	h := routeHarness(t, routes...)
	h.addReady("s1", 8, briefOf("flash", ""))
	h.startMachine()
	opencode := sprint.ProviderRead{Provider: "opencode", Note: "opencode Zen publishes no balance endpoint"}
	h.poll(openrouter(0.30, 0), opencode)
	assert.Empty(t, sprint.ProviderRests(h.snap().Fleet), "$0.30 with no spend measured rests nothing by itself")
	h.machine()
	onOR := h.onProvider(routes, "openrouter")
	require.NotEmpty(t, onOR, "the deal draws the openrouter routes")
	h.tick(30 * time.Second)
	h.failTake(onOR[0], creditLine)
	h.machine()
	s := h.snap()
	restAt := s.Now
	rest := sprint.ProviderRests(s.Fleet)["openrouter"]
	require.True(t, rest.Resting(s.Now))
	assert.True(t, rest.Refused())
	assert.True(t, rest.HasBalance)
	assert.InDelta(t, 0.30, rest.Balance, 1e-9, "the rest keeps the balance at the refusal")

	for i := range 4 { // 40 minutes of polls at the unchanged balance, each followed by ticks
		h.tick(sprint.BalancePollEvery)
		h.poll(openrouter(0.30, 0), opencode)
		h.machine()
		for _, card := range h.onProvider(routes, "openrouter") { // any deal on it is refused
			h.tick(30 * time.Second)
			h.failTake(card, creditLine)
		}
		h.machine()
		s = h.snap()
		rest = sprint.ProviderRests(s.Fleet)["openrouter"]
		assert.True(t, rest.Resting(s.Now), "poll %d: $0.30 is no payment: the rest holds", i+1)
		assert.Equal(t, restAt, rest.At, "poll %d: the same rest, never lifted", i+1)
		assert.Equal(t, sprint.RestCredit, rest.Cause, "poll %d: out of credit, not low on funds", i+1)
	}
	assert.Len(t, h.noteWhats(sprint.NProviderRested), 1, "rested once")
	assert.Empty(t, h.noteWhats(sprint.NProviderFunded), "never lifted")
	assert.Equal(t, 1, h.written(sprint.NProviderFunds), "one judgment, written once")
	assert.Len(t, h.openOf(sprint.NProviderFunds), 1)
	assert.Contains(t, h.openOf(sprint.NProviderFunds)[0].Note.What, "the balance poll ends the rest when it sees a payment (a balance over zero read higher than the read before it, or than the balance at the refusal)")
	assert.True(t, h.machineRecord().Running(), "opencode serves: the sprint runs")
	assert.Empty(t, h.openOf(sprint.NAllOutOfCredit))
	assert.Empty(t, h.onProvider(routes, "openrouter"), "nothing dealt on the resting provider")

	rows := sprint.ProviderRows(routes, s.Fleet, s.Now)
	require.Len(t, rows, 2)
	assert.Equal(t, "openrouter", rows[1].Name)
	assert.Equal(t, "$0.30", rows[1].Balance, "its balance beside it")
	assert.Contains(t, rows[1].State, "resting until paid (out-of-credit: out of credit: provider openrouter refused card "+onOR[0])
	assert.Equal(t, "serving", rows[0].State)
	h.clean("a refused provider reading a small balance")
}

// A provider out of credit by a refused take, alone, stops the machine once, and it stays
// stopped until the provider is paid (nova-tools#5199, the second cold read's probe A): polls
// at an unchanged $0.30 lift nothing and a start stays refused; a read not higher than the
// balance at the refusal after an unknown one is no payment either; a read over it after an
// unknown one (no read before it to compare) is a payment: the rest ends, and the start is
// the coordinator's.
func TestARefusedProviderAloneStopsTheMachineOnceUntilAPayment(t *testing.T) {
	t.Parallel()
	routes := []sprint.Route{providerRoute("or-a", "flash", "openrouter"), providerRoute("or-b", "flash", "openrouter")}
	h := routeHarness(t, routes...)
	h.addReady("s1", 3, briefOf("flash", ""))
	h.startMachine()
	h.poll(openrouter(0.30, 0))
	h.machine()
	onOR := h.onProvider(routes, "openrouter")
	require.NotEmpty(t, onOR)
	h.tick(30 * time.Second)
	h.failTake(onOR[0], creditLine)
	h.machine()
	require.False(t, h.machineRecord().Running(), "every provider is out: the tick stopped the machine")

	stays := func(when string) {
		t.Helper()
		h.machine()
		s := h.snap()
		assert.True(t, sprint.ProviderRests(s.Fleet)["openrouter"].Resting(s.Now), "%s: the rest holds", when)
		assert.False(t, h.machineRecord().Running(), "%s: stopped", when)
		why, err := h.st.OutOfCredit(h.ctx)
		require.NoError(t, err)
		assert.Contains(t, why, sprint.FundsCause, "%s: a start is refused", when)
	}
	for i := range 4 {
		h.tick(sprint.BalancePollEvery)
		h.poll(openrouter(0.30, 0))
		stays(fmt.Sprintf("poll %d at $0.30", i+1))
	}
	unknown := sprint.ProviderRead{Provider: "openrouter", Note: "credits endpoint answered 503"}
	h.tick(sprint.BalancePollEvery)
	h.poll(unknown)
	stays("an unknown read")
	h.tick(sprint.BalancePollEvery)
	h.poll(openrouter(0.30, 0))
	stays("$0.30 after an unknown read: not higher than at the refusal")
	assert.Equal(t, 1, h.written(sprint.NAllOutOfCredit), "stopped once")
	assert.Equal(t, 1, h.written(sprint.NProviderFunds), "one judgment")
	assert.Empty(t, h.noteWhats(sprint.NProviderFunded))

	h.tick(sprint.BalancePollEvery)
	h.poll(unknown)
	h.tick(sprint.BalancePollEvery)
	h.poll(openrouter(5.30, 0)) // a payment: over the balance at the refusal
	s := h.snap()
	assert.False(t, sprint.ProviderRests(s.Fleet)["openrouter"].Resting(s.Now), "a payment seen ends the rest")
	assert.Len(t, h.noteWhats(sprint.NProviderFunded), 1)
	why, err := h.st.OutOfCredit(h.ctx)
	require.NoError(t, err)
	assert.Empty(t, why, "paid: the start is the coordinator's")
	assert.False(t, h.machineRecord().Running(), "the machine waits for the coordinator's start")
	h.startMachine()
	h.machine()
	assert.True(t, h.machineRecord().Running())
	assert.Empty(t, h.openOf(sprint.NAllOutOfCredit), "the judgment closes")
	assert.Equal(t, 1, h.written(sprint.NAllOutOfCredit), "stopped once")
	h.clean("a refused provider paid")
}

// A payment is a read higher than the read BEFORE it, not only than the balance at the
// refusal (nova-tools#5205, the third cold read's blocker): the rest keeps the poll's last
// read, which can be up to BalancePollEvery stale and higher than the balance at the moment
// of the refusal. A poll reads $5.00; a take is refused (the rest keeps $5.00); a poll reads
// $0.30, no payment, the rest holds; a poll reads $3.00, higher than the $0.30 before it: a
// payment, though under the $5.00 the rest keeps, and the rest ends, as the judgment told the
// owner it would.
func TestAReadHigherThanTheReadBeforeItEndsARefusedTakesRest(t *testing.T) {
	t.Parallel()
	routes := []sprint.Route{providerRoute("or-a", "flash", "openrouter"), providerRoute("oc-a", "flash", "opencode")}
	h := routeHarness(t, routes...)
	h.addReady("s1", 4, briefOf("flash", ""))
	h.startMachine()
	opencode := sprint.ProviderRead{Provider: "opencode", Note: "opencode Zen publishes no balance endpoint"}
	h.poll(openrouter(5, 0), opencode)
	h.machine()
	onOR := h.onProvider(routes, "openrouter")
	require.NotEmpty(t, onOR)
	h.tick(30 * time.Second)
	h.failTake(onOR[0], creditLine)
	h.machine()
	s := h.snap()
	rest := sprint.ProviderRests(s.Fleet)["openrouter"]
	require.True(t, rest.Resting(s.Now))
	require.True(t, rest.Refused())
	assert.InDelta(t, 5, rest.Balance, 1e-9, "the rest keeps the poll's last read")

	h.tick(sprint.BalancePollEvery)
	h.poll(openrouter(0.30, 0), opencode)
	s = h.snap()
	assert.True(t, sprint.ProviderRests(s.Fleet)["openrouter"].Resting(s.Now), "$0.30 after $5.00: no payment, the rest holds")
	assert.Empty(t, h.noteWhats(sprint.NProviderFunded))

	h.tick(sprint.BalancePollEvery)
	h.poll(openrouter(3, 0), opencode)
	s = h.snap()
	rest = sprint.ProviderRests(s.Fleet)["openrouter"]
	assert.False(t, rest.Resting(s.Now), "$3.00 after $0.30 is a payment, though under the $5.00 at the refusal: the rest ends")
	assert.Contains(t, rest.Why, "ended: balance $3.00 at")
	assert.Len(t, h.noteWhats(sprint.NProviderFunded), 1)
	h.machine()
	assert.Empty(t, h.openOf(sprint.NProviderFunds), "the judgment closes with the rest")
	h.clean("a payment over the read before it")
}

// A rest a balance at zero began names no card, so it is not a refused take's
// (RouteRest.Refused reads the cards) and a read over zero ends it whatever came before
// (nova-tools#5205, the third cold read's probe): -$0.51, then an unknown read, then $0.30.
// Were it held as a refused take's, no read before the $0.30 is known and no balance was kept
// at a refusal, and it would never end.
func TestABalanceAtZerosRestEndsOnAReadOverZeroAfterAnUnknownOne(t *testing.T) {
	t.Parallel()
	routes := []sprint.Route{providerRoute("or-a", "flash", "openrouter"), providerRoute("oc-a", "flash", "opencode")}
	h := routeHarness(t, routes...)
	h.startMachine()
	h.poll(sprint.ProviderRead{Provider: "openrouter", Known: true, Balance: -0.51})
	s := h.snap()
	rest := sprint.ProviderRests(s.Fleet)["openrouter"]
	require.True(t, rest.Resting(s.Now))
	assert.True(t, rest.Out(), "out of credit")
	assert.False(t, rest.Refused(), "no card: not a refused take's rest")

	h.tick(sprint.BalancePollEvery)
	h.poll(sprint.ProviderRead{Provider: "openrouter", Note: "credits endpoint answered 503"})
	s = h.snap()
	assert.True(t, sprint.ProviderRests(s.Fleet)["openrouter"].Resting(s.Now), "an unknown read ends no rest")

	h.tick(sprint.BalancePollEvery)
	h.poll(sprint.ProviderRead{Provider: "openrouter", Known: true, Balance: 0.30})
	s = h.snap()
	assert.False(t, sprint.ProviderRests(s.Fleet)["openrouter"].Resting(s.Now), "a read over zero ends a balance's rest")
	h.clean("a balance at zero, paid")
}
