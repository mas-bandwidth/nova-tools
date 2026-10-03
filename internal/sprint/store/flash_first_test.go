package store

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// Flash first on every card (the owner, 2026-10-02, cost rule 1 of nova-tools#5174:
// "Flash first on every card; pro only on escalation"; route.go, tierLadder): the tier
// a brief's line 1 names is the card's ceiling, its first deal is on flash, and a card
// that reaches its bound below its ceiling is escalated by the machine and dealt a new
// attempt on the next tier, no judgment raised; at its ceiling the bound's judgment is
// the coordinator's as before. On the mem twin, injected clock, no socket.

// setPrimary writes fields onto a primary as a step that wrote them would leave it.
func (h *harness) setPrimary(id string, set map[string]string) {
	h.t.Helper()
	s := h.snap()
	c := s.Work.Card(id)
	require.NotNil(h.t, c)
	_, err := h.m.Apply(h.ctx, ntable.BatchManifest{Schema: 1, Table: "t-work", Epoch: "0", ExpectedTableRevision: fmt.Sprint(s.Work.Revision),
		OperationID: "set-primary-" + id, Members: []ntable.BatchMemberEntry{{ID: c.ID, Expect: &ntable.MemberExpect{Revision: fmt.Sprint(c.Rev)}, Set: set}}})
	require.NoError(h.t, err)
}

// boundOut ends every take of the primary's live work card without a finish, as a
// provider failing it does, until the deal would pass the redeal bound: the attempt
// reaches its bound, and the machine's next ticks act on it.
func (h *harness) boundOut(id string) {
	h.t.Helper()
	for i := 0; i <= sprint.MaxRedeals; i++ {
		wc := h.snap().Work.Card(id).F("work")
		h.failTake(wc, providerLine)
		h.machine()
	}
	h.machine()
}

// flashAndPro is a store with two flash routes and two pro routes.
func flashAndPro(t *testing.T) *harness {
	return routeHarness(t, route("flash-a", "flash"), route("flash-b", "flash"), route("pro-a", "pro"), route("pro-b", "pro"))
}

// tierOfRoute is the tier of a route of flashAndPro, by its name.
func tierOfRoute(name string) string {
	switch name {
	case "flash-a", "flash-b":
		return "flash"
	case "pro-a", "pro-b":
		return "pro"
	}
	return ""
}

// A pro card's first deal is on flash: its brief's tier is its ceiling, never its first
// deal. The work card records the tier it was drawn from, its packet hands it to the
// child, and `card` shows the tier now and the ceiling.
func TestAProCardsFirstDealIsFlash(t *testing.T) {
	t.Parallel()
	h := flashAndPro(t)
	h.addReady("s1", 1, briefOf("pro", ""))
	h.must(DealStep(sprint.DealReq{}))
	wc := h.workCards()["s1-1.w1"]
	require.NotNil(t, wc)
	assert.Equal(t, "flash", tierOfRoute(wc.F(sprint.FieldRoute)), "dealt on %s", wc.F(sprint.FieldRoute))
	assert.Equal(t, "flash", wc.F(sprint.FieldTier), "the work card records the tier it was drawn from")
	assert.Equal(t, "flash", h.packetOf(wc.ID).Tier, "the packet hands the child its tier")
	now, ceiling := sprint.CardTiers(h.snap().Work.Card("s1-1"))
	assert.Equal(t, []string{"flash", "pro"}, []string{now, ceiling})
	assert.Contains(t, sprint.AttemptLine(wc), " tier=flash ")
	h.clean("a pro card dealt on flash")
}

// After its bound on flash a pro card is escalated by the machine: the attempt at its
// bound is retired, a new attempt is dealt on pro and told why, the primary records the
// tier, and no judgment is raised.
func TestAProCardAfterItsFlashBoundIsRedealtOnPro(t *testing.T) {
	t.Parallel()
	h := flashAndPro(t)
	h.addReady("s1", 1, briefOf("pro", ""))
	h.startMachine()
	h.machine()
	h.boundOut("s1-1")
	pr := h.snap().Work.Card("s1-1")
	require.Equal(t, 2, pr.Int("attempt"), "a new attempt")
	assert.Equal(t, "pro", pr.F(sprint.FieldTierNow), "the primary records the tier it escalated to")
	assert.Empty(t, pr.F(sprint.FieldTier), "the machine's escalation is no coordinator's pin")
	cards := h.workCards()
	assert.NotContains(t, cards, "s1-1.w1", "the attempt at its bound is retired")
	w2 := cards["s1-1.w2"]
	require.NotNil(t, w2)
	assert.Equal(t, "pro", tierOfRoute(w2.F(sprint.FieldRoute)), "dealt on %s", w2.F(sprint.FieldRoute))
	assert.Equal(t, "pro", w2.F(sprint.FieldTier))
	assert.Contains(t, w2.F("why"), "escalated from flash to pro: attempt 1 reached its bound on flash")
	assert.Zero(t, w2.Int("redeals"), "the new attempt starts its own bound")
	assert.Empty(t, h.openOf(sprint.NBound), "no judgment below the ceiling")
	assert.Zero(t, h.notesOf(sprint.NBound))
	now, ceiling := sprint.CardTiers(pr)
	assert.Equal(t, []string{"pro", "pro"}, []string{now, ceiling})
	h.clean("escalated to pro")
}

// At its ceiling the bound is the coordinator's: a pro card that reaches its bound on
// pro, and a flash card on flash, raise the judgment "a card reached its bound" and are
// dealt no more. A card the coordinator pinned to flash (rework --tier) never climbs.
func TestAtTheProBoundTheJudgmentIsRaised(t *testing.T) {
	t.Parallel()
	t.Run("a pro card at the pro bound", func(t *testing.T) {
		t.Parallel()
		h := flashAndPro(t)
		h.addReady("s1", 1, briefOf("pro", ""))
		h.startMachine()
		h.machine()
		h.boundOut("s1-1")
		require.Equal(t, "pro", h.snap().Work.Card("s1-1").F(sprint.FieldTierNow))
		h.boundOut("s1-1")
		pr := h.snap().Work.Card("s1-1")
		assert.Equal(t, 2, pr.Int("attempt"), "no third attempt: pro is its ceiling")
		assert.Equal(t, sprint.Ready, pr.Col)
		open := h.openOf(sprint.NBound)
		require.Len(t, open, 1, "the bound's judgment")
		assert.Contains(t, open[0].Note.What, "s1-1.w2: attempt 2 was redealt 3 times")
		h.clean("at the pro bound")
	})
	t.Run("a flash card at the flash bound", func(t *testing.T) {
		t.Parallel()
		h := flashAndPro(t)
		h.addReady("s1", 1, briefOf("flash", ""))
		h.startMachine()
		h.machine()
		h.boundOut("s1-1")
		assert.Equal(t, 1, h.snap().Work.Card("s1-1").Int("attempt"), "flash is its ceiling")
		assert.Len(t, h.openOf(sprint.NBound), 1)
	})
	t.Run("a pro card pinned to flash", func(t *testing.T) {
		t.Parallel()
		h := flashAndPro(t)
		h.addReady("s1", 1, briefOf("pro", ""))
		h.setPrimary("s1-1", map[string]string{sprint.FieldTier: "flash"})
		h.startMachine()
		h.machine()
		h.boundOut("s1-1")
		pr := h.snap().Work.Card("s1-1")
		assert.Equal(t, 1, pr.Int("attempt"), "the coordinator's tier is its ceiling")
		assert.Empty(t, pr.F(sprint.FieldTierNow))
		assert.Len(t, h.openOf(sprint.NBound), 1)
	})
}

// The cost history lists each attempt's tier: every take's record on the primary names
// the tier its route was drawn from, flash for the first attempt's takes and pro for the
// escalated attempt's, and `card` prints it on each COST line.
func TestTheCostHistoryListsTheTiers(t *testing.T) {
	t.Parallel()
	h := flashAndPro(t)
	h.addReady("s1", 1, briefOf("pro", ""))
	h.startMachine()
	h.machine()
	h.boundOut("s1-1")
	h.failTake("s1-1.w2", providerLine)
	h.machine()
	v := sprint.CardCostOf(h.snap().Work.Card("s1-1"))
	tiers := map[int][]string{}
	for _, c := range v.Consumers {
		require.Equal(t, "work", c.Kind)
		tiers[c.Attempt] = append(tiers[c.Attempt], c.Tier)
		assert.Equal(t, tierOfRoute(c.Route), c.Tier, "%s ran on %s", c.Key, c.Route)
	}
	assert.Equal(t, []string{"flash", "flash", "flash", "flash"}, tiers[1], "attempt 1's four takes on flash")
	assert.Equal(t, []string{"pro"}, tiers[2], "attempt 2's take on pro")
	lines := v.CostLines()
	assert.Contains(t, lines[0], " tier=flash ")
	assert.Contains(t, lines[len(lines)-2], " tier=pro ")
}

// A pro card at its flash bound in a store whose pro tier no route serves waits under
// that tier's judgment, never refused in silence tick after tick.
func TestAnEscalationNoRouteServesIsJudgedUnderItsTier(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"))
	h.addReady("s1", 1, briefOf("pro", ""))
	h.startMachine()
	h.machine()
	h.boundOut("s1-1")
	open := h.a2Open(sprint.NNoRoute)
	require.Len(t, open, 1)
	assert.Equal(t, sprint.StreamSubject(sprint.TierSubject("pro")), open[0].Subject())
	assert.Empty(t, h.openOf(sprint.NBound))
	h.m.SetRoutes([]sprint.Route{route("flash-a", "flash"), route("pro-a", "pro")})
	h.machine()
	assert.Equal(t, "pro-a", h.workCards()["s1-1.w2"].F(sprint.FieldRoute), "a pro route deals the escalation")
	assert.Empty(t, h.a2Open(sprint.NNoRoute))
}

// The reads follow the tier the work is on (route.go, readTierOf through cardTier): a pro
// card's first attempt, on flash, is read on flash routes; escalated to pro, on pro.
func TestAProCardsFlashAttemptIsReadOnFlash(t *testing.T) {
	t.Parallel()
	h := flashAndPro(t)
	require.NoError(t, h.st.BeatReaders(h.ctx))
	h.addReady("s1", 1, briefOf("pro", ""))
	h.startMachine()
	h.machine()
	require.Equal(t, "flash", h.workCards()["s1-1.w1"].F(sprint.FieldTier))
	h.work("m1")
	h.work("m2")
	h.machine()
	reads := h.snap().Readers.Of("s1-1")
	require.Len(t, reads, 2)
	for _, rc := range reads {
		assert.Equal(t, "flash", tierOfRoute(rc.F(sprint.FieldRoute)), "%s read on %s", rc.ID, rc.F(sprint.FieldRoute))
		assert.Equal(t, "flash", rc.F(sprint.FieldTier))
	}
	h.clean("a flash attempt read on flash")
}

// A store with no route escalates nothing (NextTier): its member runs its own model
// whatever the tier, so a pro card at its bound on a twin is the bound's judgment, never
// a silent second attempt.
func TestAStoreWithNoRouteEscalatesNothing(t *testing.T) {
	t.Parallel()
	h := routeHarness(t)
	h.addReady("s1", 1, briefOf("pro", ""))
	h.startMachine()
	h.machine()
	h.boundOut("s1-1")
	pr := h.snap().Work.Card("s1-1")
	assert.Equal(t, 1, pr.Int("attempt"), "no second attempt")
	assert.Empty(t, pr.F(sprint.FieldTierNow))
	assert.Len(t, h.openOf(sprint.NBound), 1, "the bound's judgment")
}

// The second identical failure across attempts below the ceiling escalates (rules 1 and 2
// of nova-tools#5174): a pro card whose second flash attempt fails the way its first did
// raises no judgment; the finish writes tier_now and the why on the primary and sends it
// back to ready, as a rework with no member up does, and the tick deals its next attempt
// on pro. Pro counts its own failures: its first failure is failed work, and its second
// identical one, at the ceiling, is the bound's judgment.
func TestASecondIdenticalFailedAttemptBelowTheCeilingEscalates(t *testing.T) {
	t.Parallel()
	h := flashAndPro(t)
	h.addReady("s1", 1, briefOf("pro", ""))
	h.startMachine()
	h.machine()
	rework := func() {
		h.t.Helper()
		h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Fix: "make them pass", Who: "tester"}))
		h.machine()
	}
	h.failTake("s1-1.w1", "verdict not-done; tests red in x")
	assert.Equal(t, 1, h.notesOf(sprint.NWorkFailed), "the first is failed work")
	rework()
	require.Equal(t, "flash", h.workCards()["s1-1.w2"].F(sprint.FieldTier))
	h.failTake("s1-1.w2", "verdict not-done; tests red in y")
	pr := h.snap().Work.Card("s1-1")
	assert.Equal(t, sprint.Ready, pr.Col, "back to ready, as a rework with no member up leaves it")
	assert.Equal(t, "pro", pr.F(sprint.FieldTierNow))
	assert.Contains(t, pr.F("why"), "escalated from flash to pro: attempts 1 and 2 failed the same way (verdict not-done; tests red in)")
	assert.Empty(t, h.openOf(sprint.NBound), "no judgment below the ceiling")
	assert.Equal(t, 1, h.notesOf(sprint.NWorkFailed), "and no failed-work one")
	h.machine()
	w3 := h.workCards()["s1-1.w3"]
	require.NotNil(t, w3, "the tick deals the next attempt")
	assert.Equal(t, "pro", tierOfRoute(w3.F(sprint.FieldRoute)), "on pro: %s", w3.F(sprint.FieldRoute))
	assert.Contains(t, w3.F("why"), "escalated from flash to pro")
	h.clean("escalated by the failed finish")

	h.failTake("s1-1.w3", "verdict not-done; tests red in z")
	assert.Equal(t, 2, h.notesOf(sprint.NWorkFailed), "pro's first failure is failed work")
	assert.Empty(t, h.openOf(sprint.NBound))
	rework()
	h.failTake("s1-1.w4", "verdict not-done; tests red in w")
	h.machine()
	open := h.openOf(sprint.NBound)
	require.Len(t, open, 1, "at the ceiling the second identical failure is the bound's judgment")
	assert.Contains(t, open[0].Note.What, "attempts 3 and 4 failed the same way")
}

// The second identical failure inside one attempt below the ceiling escalates through the
// deal (redealBound counts the two identical ends), and the new attempt's why names the
// class, not a redeal count.
func TestASecondIdenticalTakeBelowTheCeilingEscalatesNamingTheClass(t *testing.T) {
	t.Parallel()
	h := flashAndPro(t)
	h.addReady("s1", 1, briefOf("pro", ""))
	h.startMachine()
	h.machine()
	h.failTake("s1-1.w1", noResultLine)
	h.machine()
	h.failTake("s1-1.w1", noResultLine)
	h.machine()
	h.machine()
	w2 := h.workCards()["s1-1.w2"]
	require.NotNil(t, w2, "escalated: a new attempt")
	assert.Equal(t, "pro", tierOfRoute(w2.F(sprint.FieldRoute)))
	assert.Contains(t, w2.F("why"), "escalated from flash to pro: attempt 1 reached its bound on flash (its last two takes ended the same way: no result)")
	assert.NotContains(t, w2.F("why"), "redealt")
	assert.Empty(t, h.openOf(sprint.NBound))
	h.clean("escalated at the second identical take")
}
