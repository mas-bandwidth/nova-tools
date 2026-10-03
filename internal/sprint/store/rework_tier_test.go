package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A card rises a tier by rework (nova-tools#5090; route.go, cardTier): `rework <id>
// --tier pro` writes the tier on the primary, the card is the persistent store, and
// every later deal of the card draws from that tier, the attempt the rework deals at
// once and every attempt after it, whatever its brief's line 1 says; a rework that
// finds no member with room leaves the primary ready with the tier, and the tick's deal
// draws from it. A pinned card runs on its pin whatever its tier, so --tier on it is
// refused, naming the pin.
func TestAReworkRaisesTheTierEveryLaterDealDrawsFrom(t *testing.T) {
	t.Parallel()
	routes := []sprint.Route{route("flash-a", "flash"), route("flash-b", "flash"), route("pro-a", "pro"), route("pro-b", "pro")}
	tierOf := func(name string) string {
		for _, r := range routes {
			if r.Name == name {
				return r.Tier
			}
		}
		return ""
	}
	rework := func(h *harness, tier string) []sprint.Refusal {
		h.t.Helper()
		return h.run(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Fix: "again", Tier: tier, Who: "tester"})).Refused
	}
	t.Run("dealt at once", func(t *testing.T) {
		t.Parallel()
		h := routeHarness(t, routes...)
		h.addReady("s1", 1, briefOf("flash", ""))
		h.must(DealStep(sprint.DealReq{}))
		var got []string
		for a := 1; a <= 4; a++ {
			pr := h.snap().Work.Card("s1-1")
			got = append(got, tierOf(h.workCards()[pr.F("work")].F(sprint.FieldRoute)))
			if a == 4 {
				break
			}
			h.finishAttempt("s1-1", true, "")
			tier := ""
			if a == 2 {
				tier = "pro" // a flash card that failed twice rises to pro
			}
			require.Empty(t, rework(h, tier))
		}
		assert.Equal(t, []string{"flash", "flash", "pro", "pro"}, got, "attempts 3 and 4 are drawn from pro, the tier the card keeps")
		assert.Equal(t, "pro", h.snap().Work.Card("s1-1").F(sprint.FieldTier), "the primary records its tier")
		h.clean("risen to pro")
	})
	t.Run("dealt by the tick", func(t *testing.T) {
		t.Parallel()
		h := routeHarness(t, routes...)
		h.addReady("s1", 1, briefOf("flash", ""))
		h.must(DealStep(sprint.DealReq{}))
		h.finishAttempt("s1-1", true, "")
		h.must(FleetStep(sprint.FleetReq{Op: "hold", Member: "m1"}))
		h.must(FleetStep(sprint.FleetReq{Op: "hold", Member: "m2"}))
		require.Empty(t, rework(h, "pro"))
		pr := h.snap().Work.Card("s1-1")
		require.Equal(t, sprint.Ready, pr.Col, "no member up: the rework leaves it ready")
		require.Equal(t, "pro", pr.F(sprint.FieldTier))
		h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
		h.must(DealStep(sprint.DealReq{}))
		wc := h.workCards()[h.snap().Work.Card("s1-1").F("work")]
		require.NotNil(t, wc)
		assert.Equal(t, "pro", tierOf(wc.F(sprint.FieldRoute)), "the deal reads the tier the card records: %s", wc.F(sprint.FieldRoute))
		h.clean("dealt on pro by the deal")
	})
	t.Run("a pinned card is refused", func(t *testing.T) {
		t.Parallel()
		h := routeHarness(t, routes...)
		h.addReady("s1", 1, briefOf("flash", "model: x/y\ntokens: 100\ndeadline: 60"))
		h.must(DealStep(sprint.DealReq{}))
		h.finishAttempt("s1-1", true, "")
		refused := rework(h, "pro")
		require.Len(t, refused, 1)
		assert.Contains(t, refused[0].Why, "pins model x/y")
		pr := h.snap().Work.Card("s1-1")
		assert.Equal(t, sprint.Review, pr.Col, "nothing moved")
		assert.Empty(t, pr.F(sprint.FieldTier), "nothing written")
	})
}
