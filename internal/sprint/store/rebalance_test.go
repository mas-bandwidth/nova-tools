package store

import (
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The owner, 2026-10-01: "The WHOLE POINT of nova-sprint is to feed the fleet
// at width and keep it working at that width until done." and "deal at most
// 2X width ahead per-machine in fleet". The machine deals each member
// DealAhead times its width, and rebalances the fleet and the readers once at
// the start of every tick.

// takeWidth has a member take its width of ready cards, as the member loop
// does (pkg/member: width less what it runs).
func (h *harness) takeWidth(m string) {
	h.t.Helper()
	s := h.snap()
	room := s.Width(m) - s.Fleet.Count(m, sprint.Working)
	if room > 0 {
		h.run(TakeStep(sprint.TakeReq{As: m, Sel: sprint.Sel{Limit: room}, Who: m}))
	}
}

// finishAll has a member finish every card it works, ok.
func (h *harness) finishAll(m string) {
	h.t.Helper()
	s := h.snap()
	var ids []string
	gens := map[string]int{}
	for _, c := range s.Fleet.Cell(m, sprint.Working) {
		ids = append(ids, c.ID)
		gens[c.ID] = c.Int("gen")
	}
	if len(ids) > 0 {
		h.must(FinishStep(sprint.FinishReq{As: m, Sel: sprint.Sel{IDs: ids}, Gens: gens, Who: m}))
	}
}

// (a) The feed: with more ready cards than the fleet's lanes, one tick deals
// every member up DealAhead times its width; once each works at its width,
// every member working at its width has a card ready behind it while undealt
// cards remain.
func TestTheTickFeedsEveryMemberDealAheadTimesItsWidth(t *testing.T) {
	t.Parallel()
	h, ms := fleetOf(t, 3, 4)
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 60}))
	h.startMachine()
	h.machine()
	s := h.snap()
	for _, m := range ms {
		require.Equal(t, sprint.DealAhead*4, heldBy(s, m), "%s after one tick", m)
	}
	for round := 0; round < 3; round++ {
		for _, m := range ms {
			h.takeWidth(m)
		}
		h.finishAll("m2") // m2 is fast: its lanes free every round
		h.takeWidth("m2")
		h.tick(time.Second)
		h.machine()
		s = h.snap()
		undealt := len(s.Work.Column(sprint.Ready))
		for _, m := range ms {
			assert.LessOrEqual(t, heldBy(s, m), sprint.DealAhead*4, "round %d: %s past DealAhead times its width", round, m)
			assert.LessOrEqual(t, s.Fleet.Count(m, sprint.Working), 4, "round %d: %s works past its width", round, m)
			if undealt > 0 && s.Fleet.Count(m, sprint.Working) == 4 {
				assert.GreaterOrEqual(t, s.Fleet.Count(m, sprint.Ready), 1, "round %d: %s works at its width with nothing ready behind it while %d wait to be dealt", round, m, undealt)
			}
		}
	}
}

// (c) A stalled member (it takes nothing more) holds ready cards it cannot
// start beside a member whose lanes are free and whose ready column is
// empty: within one tick the free member has them.
func TestAStalledMembersReadyCardsGoToAFreeMemberWithinOneTick(t *testing.T) {
	t.Parallel()
	h, _ := fleetOf(t, 2, 2)
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 8}))
	h.startMachine()
	h.machine()       // four each: two lanes and two ready behind them
	h.takeWidth("m1") // m1 works its width, two ready it cannot start, and stalls
	h.takeWidth("m2") // m2 works its two ...
	h.finishAll("m2") // ... finishes them,
	h.takeWidth("m2") // takes its other two
	h.finishAll("m2") // and finishes them: free lanes, nothing ready
	before := h.snap()
	require.Equal(t, 2, before.Fleet.Count("m1", sprint.Ready))
	require.Equal(t, 0, heldBy(before, "m2"))
	require.Empty(t, before.Work.Column(sprint.Ready), "nothing is left to deal: only the rebalance feeds m2")
	h.tick(time.Second)
	h.machine()
	s := h.snap()
	assert.Equal(t, 2, s.Fleet.Count("m2", sprint.Ready), "m2 has m1's two ready cards")
	assert.Equal(t, 0, s.Fleet.Count("m1", sprint.Ready), "m1 keeps the cards it works")
	assert.Equal(t, 2, s.Fleet.Count("m1", sprint.Working))
}

// (d) A member going down: its ready and working cards are on the members up
// within one tick, none past DealAhead times its width, none working past its
// width.
func TestADownMembersCardsAreOnTheOthersWithinOneTick(t *testing.T) {
	t.Parallel()
	h, _ := fleetOf(t, 3, 4)
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 6}))
	h.startMachine()
	h.machine() // two each
	h.run(TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 1}, Who: "m1"}))
	require.Equal(t, 1, h.snap().Fleet.Count("m1", sprint.Working))
	h.setLive("m2", "m3")
	h.tick(pastDown)
	h.machine()
	s := h.snap()
	assert.Equal(t, sprint.Down, s.MemberCtl("m1").F("status"))
	assert.Equal(t, 0, heldBy(s, "m1"), "the down member holds nothing")
	assert.Equal(t, 6, heldBy(s, "m2")+heldBy(s, "m3"), "every card is on the members up")
	for _, m := range []string{"m2", "m3"} {
		assert.LessOrEqual(t, heldBy(s, m), sprint.DealAhead*4, m)
		assert.LessOrEqual(t, s.Fleet.Count(m, sprint.Working), 4, m)
	}
}

// The rebalance runs once a tick, at its start, before every table's update:
// the tick's order starts with it, and its level part, when it moves, comes
// before the deal and is not run again in the tick.
func TestTheRebalanceRunsOnceAtTheStartOfEveryTick(t *testing.T) {
	t.Parallel()
	h, _ := fleetOf(t, 2, 2)
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 12}))
	h.startMachine()
	h.machine()
	h.takeWidth("m1")
	h.takeWidth("m2")
	h.finishAll("m2")
	h.takeWidth("m2")
	h.finishAll("m2")
	h.tick(time.Second)
	res := h.machine()
	require.Equal(t, "start", res.Order[0], "the tick's order: %v", res.Order)
	assert.Equal(t, 1, slices.Index(res.Order, sprint.Work), "the start is the only step before the first table's update: %v", res.Order)
	var names []string
	for _, p := range res.Parts {
		names = append(names, p.Name)
	}
	level := slices.Index(names, sprint.PartLevel)
	require.GreaterOrEqual(t, level, 0, "the level moved m1's ready cards to m2: parts %v", names)
	assert.Less(t, level, slices.Index(names, "deal"), "the level comes before the deal: %v", names)
	n := 0
	for _, x := range names {
		if x == sprint.PartLevel {
			n++
		}
	}
	assert.Equal(t, 1, n, "the level runs once a tick: %v", names)
}
