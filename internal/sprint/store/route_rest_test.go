package store

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
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

// Rule 3 (nova-tools#5174, the owner, 2026-10-02: "A route whose children end without a
// result three times is rested by the machine, never redealt on."): three takes on one route
// that left no result, of three cards, rest the route in the tick that sees the third; the
// rest names the cards, no work card is drawn on it while it rests (a redeal included, even
// when it is the tier's only route), and it ends by itself at RouteRestFor, its window begun
// again. Two such takes in a window with ok ends rest nothing.
func TestARouteWhoseChildrenEndWithNoResultThreeTimesRests(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("pro-a", "pro"))
	h.addReady("s1", 4, briefOf("pro", ""))
	h.startMachine()
	h.machine()
	for _, id := range []string{"s1-1.w1", "s1-2.w1"} {
		h.failTake(id, noResultLine)
	}
	h.machine()
	assert.Empty(t, h.noteWhats(sprint.NRouteRested), "two no-result ends rest nothing")
	for _, id := range []string{"s1-1.w1", "s1-2.w1"} {
		require.Equal(t, sprint.Ready, h.snap().Fleet.Card(id).Col, "%s is dealt again on the tier's one route", id)
	}

	h.failTake("s1-3.w1", noResultLine)
	h.machine()
	s := h.snap()
	rests := sprint.RouteRests([]sprint.Route{route("pro-a", "pro")}, s.Fleet)
	rest, ok := rests["pro-a"]
	require.True(t, ok, "the route rests: %v", rests)
	assert.Equal(t, []string{"s1-1.w1", "s1-2.w1", "s1-3.w1"}, rest.Cards, "the rest names the three cards")
	assert.Equal(t, sprint.RouteRestFor, rest.Until.Sub(rest.At))
	whats := h.noteWhats(sprint.NRouteRested)
	require.Len(t, whats, 1, "one note, to the coordinator")
	assert.Contains(t, whats[0], "route pro-a rested until")
	assert.Contains(t, whats[0], "s1-1.w1, s1-2.w1, s1-3.w1")
	assert.Equal(t, sprint.Withdrawn, s.Fleet.Card("s1-3.w1").Col, "never redealt on the resting route, its tier's only one")
	open := h.openOf(sprint.NNoRoute)
	require.Len(t, open, 1, "the tier waits, once")
	assert.Contains(t, open[0].Note.What, "rests")
	assert.Contains(t, open[0].Note.What, "pro-a until "+rest.Until.UTC().Format(time.RFC3339), "it names when the rest ends")

	h.machine()
	assert.Len(t, h.noteWhats(sprint.NRouteRested), 1, "a rest is written once")
	assert.Equal(t, sprint.Withdrawn, h.snap().Fleet.Card("s1-3.w1").Col, "still resting")
	h.clean("a route resting")

	// the rest ends by itself; the window begins again after it, so the old ends rest nothing
	h.tick(sprint.RouteRestFor)
	h.machine()
	w := h.snap().Fleet.Card("s1-3.w1")
	assert.Equal(t, sprint.Ready, w.Col, "dealt again when the rest ends")
	assert.Equal(t, "pro-a", w.F(sprint.FieldRoute))
	assert.Empty(t, h.openOf(sprint.NNoRoute), "the tier is served again")
	assert.Len(t, h.noteWhats(sprint.NRouteRested), 1, "no second rest from the ends before the first")
	h.clean("a rest ended")
}

// A rest leaves the other routes of the tier serving: every card the tick draws after it,
// a first deal or a redeal, is on another route.
func TestARestingRouteServesNoDealWhileAnotherServes(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("pro-a", "pro"), route("pro-b", "pro"))
	h.addReady("s1", 6, briefOf("pro", ""))
	h.startMachine()
	h.machine()
	var onA []string
	for _, c := range h.snap().Fleet.Column(sprint.Ready) {
		if c.F(sprint.FieldRoute) == "pro-a" {
			onA = append(onA, c.ID)
		}
	}
	require.Len(t, onA, 3, "the deal alternates the two routes")
	for _, id := range onA {
		h.failTake(id, noResultLine)
	}
	h.addReady("s1", 4, briefOf("pro", ""))
	h.machine()
	require.Len(t, h.noteWhats(sprint.NRouteRested), 1)
	for _, c := range h.snap().Fleet.Column(sprint.Ready, sprint.Working) {
		assert.Equal(t, "pro-b", c.F(sprint.FieldRoute), "%s: nothing is drawn on the resting route", c.ID)
	}
	for _, id := range onA {
		assert.True(t, strings.HasSuffix(id, ".w1"))
		assert.Equal(t, sprint.Ready, h.snap().Fleet.Card(id).Col, "%s is redealt, on the other route", id)
	}
	h.clean("one route resting of two")
}
