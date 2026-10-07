package store

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nova-tools/internal/sprint"
)

// A card's deadline by machine (docs/SPEC-SPRINT.md section 5; deadline.go; the owner,
// 2026-10-04). The route's deadline was one number for the fleet; superman's median run wall
// was 759 s against 380 to 435 s elsewhere, so it timed out twice as often and every timeout
// lost a whole attempt's spend. A card dealt to a member gets the larger of its own deadline
// and three times the member's median run wall over its last fifty ok attempts, and the fleet
// row may pin it (fleet up <m> --deadline <d>).

// finishWithWall has the member take and finish the primary's live card ok with the wall.
func (h *harness) finishWithWall(id, wall string) {
	h.t.Helper()
	wc := h.snap().Fleet.Card(h.snap().Work.Card(id).F("work"))
	g := map[string]int{wc.ID: wc.Int("gen")}
	h.must(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: g}))
	h.must(FinishStep(sprint.FinishReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: g, Head: "h-" + id, Usage: "wall=" + wall}))
}

// dealtDeadline deals the primary and says the member it went to and the deadline its work
// card and packet carry.
func (h *harness) dealtDeadline(id string) (member string, card, packet int) {
	h.t.Helper()
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{id}}}))
	s := h.snap()
	wc := s.Fleet.Card(s.Work.Card(id).F("work"))
	return wc.Row, wc.Int(sprint.FieldDeadline), h.packetOf(wc.ID).Deadline
}

func TestADealtCardsDeadlineIsThreeTimesItsMembersMedianWall(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash")) // the route's deadline: routeSeconds, 600
	h.addReady("s1", 7, briefOf("flash", ""))
	// the first card of each member is dealt with no ok attempt behind it: the route's deadline
	m1, d, p := h.dealtDeadline("s1-1")
	require.Equal(t, "m1", m1)
	assert.Equal(t, routeSeconds, d, "no ok attempt yet on m1: the route's deadline")
	assert.Equal(t, d, p, "the packet carries the deadline")
	m2, d, _ := h.dealtDeadline("s1-2")
	require.Equal(t, "m2", m2)
	assert.Equal(t, routeSeconds, d, "no ok attempt yet on m2: the route's deadline")
	// m1 runs long (759 s, as superman did), m2 short (150 s)
	h.finishWithWall("s1-1", "759.00s")
	h.finishWithWall("s1-2", "150s")
	median, n := sprint.MemberMedianWall(h.snap(), "m1")
	assert.Equal(t, 759.0, median)
	assert.Equal(t, 1, n)
	// the next deals: a card on m1 gets 3 x 759 = 2277; one on m2 keeps the route's 600 (3 x
	// 150 is under it); the packet carries the deadline
	want := map[string]int{"m1": 3 * 759, "m2": routeSeconds}
	for _, id := range []string{"s1-3", "s1-4", "s1-5", "s1-6"} {
		m, d, p := h.dealtDeadline(id)
		assert.Equal(t, want[m], d, "%s on %s", id, m)
		assert.Equal(t, d, p, "%s: the packet carries it", id)
	}
	// more ok attempts on m1 at 700 s and 800 s: the median of three is still 759
	for _, id := range []string{"s1-3", "s1-4", "s1-5", "s1-6"} {
		if h.snap().Fleet.Card(id+".w1").Row == "m1" {
			h.finishWithWall(id, "700s")
			break
		}
	}
	median, n = sprint.MemberMedianWall(h.snap(), "m1")
	assert.Equal(t, 2, n)
	assert.Equal(t, (759.0+700)/2, median, "the mean of the middle two of an even count")
	// the fleet row pins it, whatever the card's; default takes the pin off
	const pinned = 45 * 60 // seconds, as fleet up --deadline 45m writes it
	for _, m := range []string{"m1", "m2"} {
		res := h.must(FleetStep(sprint.FleetReq{Op: "up", Member: m, Deadline: pinned}))
		assert.Contains(t, res.Moved[0], m+" up deadline=2700s (pinned)")
	}
	_, d, p = h.dealtDeadline("s1-7")
	assert.Equal(t, 2700, d, "the pin, whatever the card's")
	assert.Equal(t, 2700, p)
	res := h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1", DeadlineOff: true}))
	assert.Contains(t, res.Moved[0], "the pin taken off")
	assert.Empty(t, h.snap().MemberCtl("m1").F(sprint.FieldMemberDeadline))
	assert.Equal(t, "2700", h.snap().MemberCtl("m2").F(sprint.FieldMemberDeadline), "m2's pin stays")
	h.clean("deadlines by machine")
}

func TestARedealtCardsDeadlineIsItsNewMembersToo(t *testing.T) {
	t.Parallel()
	h := routeHarness(t, route("flash-a", "flash"), route("flash-b", "flash"))
	h.addReady("s1", 3, briefOf("flash", ""))
	h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1", "s1-2"}}}))
	h.finishWithWall("s1-1", "1000s") // m1's median: 1000
	h.finishWithWall("s1-2", "100s")  // m2's: 100
	m, d, _ := h.dealtDeadline("s1-3")
	require.Equal(t, "m1", m)
	assert.Equal(t, 3000, d)
	// m1 goes down: the card is dealt again to m2, at m2's deadline
	h.must(FleetStep(sprint.FleetReq{Op: "down", Member: "m1"}))
	wc := h.snap().Fleet.Card("s1-3.w1")
	require.Equal(t, "m2", wc.Row, "dealt again to m2")
	assert.Equal(t, routeSeconds, wc.Int(sprint.FieldDeadline), fmt.Sprintf("m2's: three times 100 is under the route's %d; the card: %v", routeSeconds, wc.Fields))
	h.clean("redealt at the new member's deadline")
}
