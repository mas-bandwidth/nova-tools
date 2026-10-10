package store

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
)

// noteWhats is the What of every note of a type the inbox holds.
func (h *harness) noteWhats(typ string) []string {
	h.t.Helper()
	notes, _, err := h.m.NotesSince(h.ctx, "", 100000)
	require.NoError(h.t, err)
	var out []string
	for _, n := range notes {
		if n.Type == typ {
			out = append(out, n.What)
		}
	}
	return out
}

// rateLimitLine is a member's report of a take the provider refused for its rate limit.
const rateLimitLine = cardhdr.EndProvider + ": provider: class=rate-limited status=429 msg=Rate limit exceeded: free-models-per-min"

// A route rests only on a provider-typed failure (tla/RouteRest.tla): three takes on one route
// the provider failed with a 429, of three cards, rest the route in the tick that sees the
// third; the rest names the cards, no work card is drawn on it while it rests (a redeal
// included, even when it is the tier's only route), and it ends by itself at RouteRestFor,
// its window begun again. Two such takes in a window with ok ends rest nothing.
func TestARouteWhoseProviderFailsThreeTakesRests(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"))
	h.addReady("s1", 4, briefOf("flash", ""))
	h.startMachine()
	h.machine()
	for _, id := range []string{"s1-1.w1", "s1-2.w1"} {
		h.failTake(id, rateLimitLine)
	}
	h.machine()
	assert.Empty(t, h.noteWhats(sprint.NRouteRested), "two 429s rest nothing")
	for _, id := range []string{"s1-1.w1", "s1-2.w1"} {
		require.Equal(t, sprint.Ready, h.snap().Fleet.Card(id).Col, "%s is dealt again on the tier's one route", id)
	}

	h.failTake("s1-3.w1", rateLimitLine)
	h.machine()
	s := h.snap()
	rests := sprint.RouteRests([]sprint.Route{route("flash-a", "flash")}, s.Fleet)
	rest, ok := rests["flash-a"]
	require.True(t, ok, "the route rests: %v", rests)
	assert.Equal(t, sprint.RestProvider, rest.Cause)
	assert.Equal(t, []string{"s1-1.w1", "s1-2.w1", "s1-3.w1"}, rest.Cards, "the rest names the three cards")
	assert.Equal(t, sprint.RouteRestFor, rest.Until.Sub(rest.At))
	whats := h.noteWhats(sprint.NRouteRested)
	require.Len(t, whats, 1, "one note, to the coordinator")
	assert.Contains(t, whats[0], "route flash-a rested until")
	assert.Contains(t, whats[0], "s1-1.w1, s1-2.w1, s1-3.w1")
	assert.Equal(t, sprint.Withdrawn, s.Fleet.Card("s1-3.w1").Col, "never redealt on the resting route, its tier's only one")
	open := h.openOf(sprint.NNoRoute)
	require.Len(t, open, 1, "the tier waits, once")
	assert.Contains(t, open[0].Note.What, "rests")
	assert.Contains(t, open[0].Note.What, "flash-a until "+rest.Until.UTC().Format(time.RFC3339), "it names when the rest ends")

	h.machine()
	assert.Len(t, h.noteWhats(sprint.NRouteRested), 1, "a rest is written once")
	assert.Equal(t, sprint.Withdrawn, h.snap().Fleet.Card("s1-3.w1").Col, "still resting")
	h.clean("a route resting")

	// the rest ends by itself; the window begins again after it, so the old ends rest nothing
	h.tick(sprint.RouteRestFor)
	h.machine()
	w := h.snap().Fleet.Card("s1-3.w1")
	assert.Equal(t, sprint.Ready, w.Col, "dealt again when the rest ends")
	assert.Equal(t, "flash-a", w.F(sprint.FieldRoute))
	assert.Empty(t, h.openOf(sprint.NNoRoute), "the tier is served again")
	assert.Len(t, h.noteWhats(sprint.NRouteRested), 1, "no second rest from the ends before the first")
	h.clean("a rest ended")
}

// A take that ended with no result is the model's output on that card, never the
// provider's (flash-deepseek41-direct, 2026-10-10, rested twenty minutes for "no-result:
// its children ended with no result"): five of them on the tier's one route rest nothing,
// and each card is dealt again on it.
func TestARouteWhoseChildrenEndWithNoResultNeverRests(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"))
	h.addReady("s1", 5, briefOf("flash", ""))
	h.startMachine()
	h.machine()
	ids := []string{"s1-1.w1", "s1-2.w1", "s1-3.w1", "s1-4.w1", "s1-5.w1"}
	for _, id := range ids {
		h.failTake(id, noResultLine)
	}
	h.machine()
	s := h.snap()
	assert.Empty(t, h.noteWhats(sprint.NRouteRested), "no-result ends rest nothing")
	assert.Empty(t, sprint.RouteRests([]sprint.Route{route("flash-a", "flash")}, s.Fleet))
	assert.Empty(t, h.openOf(sprint.NNoRoute), "the tier is served")
	for _, id := range ids {
		if c := s.Fleet.Card(id); c != nil && c.Col == sprint.Ready {
			assert.Equal(t, "flash-a", c.F(sprint.FieldRoute), "%s is dealt again on the route", id)
		}
	}
}

// A rest leaves the other routes of the tier serving: every card the tick draws after it,
// a first deal or a redeal, is on another route.
func TestARestingRouteServesNoDealWhileAnotherServes(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"), route("flash-b", "flash"))
	h.addReady("s1", 6, briefOf("flash", ""))
	h.startMachine()
	h.machine()
	var onA []string
	for _, c := range h.snap().Fleet.Column(sprint.Ready) {
		if c.F(sprint.FieldRoute) == "flash-a" {
			onA = append(onA, c.ID)
		}
	}
	require.Len(t, onA, 3, "the deal alternates the two routes")
	for _, id := range onA {
		h.failTake(id, rateLimitLine)
	}
	h.addReady("s1", 4, briefOf("flash", ""))
	h.machine()
	require.Len(t, h.noteWhats(sprint.NRouteRested), 1)
	for _, c := range h.snap().Fleet.Column(sprint.Ready, sprint.Working) {
		assert.Equal(t, "flash-b", c.F(sprint.FieldRoute), "%s: nothing is drawn on the resting route", c.ID)
	}
	for _, id := range onA {
		assert.True(t, strings.HasSuffix(id, ".w1"))
		assert.Equal(t, sprint.Ready, h.snap().Fleet.Card(id).Col, "%s is redealt, on the other route", id)
	}
	h.clean("one route resting of two")
}

// A route's own rests are one fleet property per provider (nova-tools#5210). At 100 routes
// over two providers, every route rested, the fleet table stays under the 64-property
// cap with the count named here, and no route_rest_<route> property is written.
func TestAHundredRoutesRestedByRule3StayUnderThePropertyCap(t *testing.T) {
	t.Parallel()
	var routes []sprint.Route
	for i := range 100 {
		provider := "openrouter"
		if i%2 == 1 {
			provider = "opencode"
		}
		routes = append(routes, providerRoute(fmt.Sprintf("r%03d", i), "flash", provider))
	}
	h := routeHarness(t, routes...)
	h.addReady("s1", 300, briefOf("flash", ""))
	h.startMachine()
	h.machine()
	// One read of the fleet names the 300 dealt cards and their gens; the 300 takes
	// then go as batched verbs (steps_work.go, takeOne and finishPlan: every named
	// card keeps its own take and failed finish, the members' width is MaxWidth, and
	// a set over one manifest is chunked by the store): three real failed takes per
	// route, in four steps, not 600 one-card steps each re-reading the whole table.
	s := h.snap()
	byRoute := map[string][]string{}
	byMember := map[string][]string{}
	gens := map[string]int{}
	for _, c := range s.Fleet.Column(sprint.Ready) {
		byRoute[c.F(sprint.FieldRoute)] = append(byRoute[c.F(sprint.FieldRoute)], c.ID)
		byMember[c.Row] = append(byMember[c.Row], c.ID)
		gens[c.ID] = c.Int("gen")
	}
	require.Len(t, byRoute, 100, "every route was dealt")
	for name, ids := range byRoute {
		require.Len(t, ids, 3, "%s: three takes, so the one tick rests it", name)
	}
	require.Equal(t, 300, len(gens), "every dealt card is taken and failed")
	for member, ids := range byMember {
		h.must(TakeStep(sprint.TakeReq{As: member, Sel: sprint.Sel{IDs: ids}, Gens: gens, Who: member}))
		h.must(FinishStep(sprint.FinishReq{As: member, Sel: sprint.Sel{IDs: ids}, Gens: gens, Failed: true,
			Report: rateLimitLine, Usage: "wall=450.00s budget=1/1000", Who: member}))
	}
	h.machine()
	s = h.snap()
	props := s.Fleet.Props()
	names := make([]string, 0, len(props))
	rule3 := 0
	for name, v := range props {
		names = append(names, name)
		assert.False(t, strings.HasPrefix(name, "route_rest_"), "%s: a rule-3 rest is never its own property", name)
		if strings.HasPrefix(name, sprint.PropRule3Rest("")) {
			rule3++
			assert.Less(t, len(v), ntable.LimitFieldValueBytes, name)
		}
	}
	assert.Equal(t, 2, rule3, "one property per provider: %v", names)
	// Five: the deal's index, the tier's route index, one rule-3 property per provider,
	// and the status transitions' one record (sprint.PropStatusSeen). 5 under the cap of
	// 64 is the headroom.
	assert.Equal(t, 5, len(props), "100 routes rested use %d properties, headroom under %d: %v", len(props), ntable.LimitTableProps, names)
	assert.Less(t, len(props), ntable.LimitTableProps)
	now := s.Now
	rests := sprint.RouteRests(routes, s.Fleet)
	for _, r := range routes {
		assert.True(t, rests[r.Name].Resting(now), "%s rests from the provider's one property", r.Name)
	}
}
