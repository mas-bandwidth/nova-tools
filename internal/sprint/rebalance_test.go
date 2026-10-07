package sprint

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The rebalance at the start of every tick (TickStart; the owner, 2026-10-01:
// "both for readers and fleet, there needs to be a rebalance step done at the
// start of each tick. it's simple. just once before tick, rebalance each
// table." and "and it's a safety, if ever there are cards on a held or down
// machine, rebalance moves them away.").

// heldCards is the work cards on a member, ready and working.
func heldCards(s *Snapshot, m string) int { return s.Fleet.Count(m, Ready) + s.Fleet.Count(m, Working) }

// A member that cannot start its ready cards (its lanes full) beside a member
// with free lanes and nothing ready: the level gives the idle member the
// cards, within one of each other in backlog, none past DealAhead times a
// width.
func TestTheLevelFeedsAnIdleMemberFromOneThatCannotStartItsCards(t *testing.T) {
	t.Parallel()
	w := fleetWorld(t, 0, 2, "m1", "m2")
	for i := 1; i <= 2; i++ {
		putWorkCard(w, fmt.Sprintf("w%d", i), "m1", Working, float64(i), nil)
	}
	for i := 3; i <= 4; i++ {
		putWorkCard(w, fmt.Sprintf("w%d", i), "m1", Ready, float64(i), nil)
	}
	w.part(TickLevel, TickReq{})
	assert.Equal(t, 2, w.s.Fleet.Count("m2", Ready), "m2, idle, has m1's two ready cards m1 cannot start")
	assert.Equal(t, 0, w.s.Fleet.Count("m1", Ready))
	assert.Equal(t, 2, w.s.Fleet.Count("m1", Working), "working cards on an up member stay")
	for _, m := range []string{"m1", "m2"} {
		assert.LessOrEqual(t, heldCards(w.s, m), DealAhead*2, m)
	}
}

// The safety: cards planted on a held member and on a down member, ready and
// working, are on the members up after the one rebalance, a working card
// dealt again at a new generation with its redeal counted, none past DealAhead
// times a width.
func TestTheRebalanceMovesEveryCardOffAHeldOrDownMember(t *testing.T) {
	t.Parallel()
	for _, op := range []string{"hold", "down"} {
		t.Run(op, func(t *testing.T) {
			t.Parallel()
			w := fleetWorld(t, 0, 4, "m1", "m2", "m3")
			w.must(FleetStep(w.s, FleetReq{Op: op, Member: "m1"}))
			require.NotEqual(t, Up, w.s.MemberCtl("m1").F("status"))
			putWorkCard(w, "a", "m1", Ready, 1, nil)
			putWorkCard(w, "b", "m1", Working, 2, nil)
			w.part(TickLevel, TickReq{})
			assert.Equal(t, 0, heldCards(w.s, "m1"), "no card is left on a %s member", op)
			for _, id := range []string{"a.w1", "b.w1"} {
				c := w.s.Fleet.Card(id)
				require.NotNil(t, c)
				assert.Contains(t, []string{"m2", "m3"}, c.Row, id)
				assert.Equal(t, Ready, c.Col, id)
				assert.Equal(t, 3, c.Int("gen"), "%s dealt again at a new generation", id)
			}
			assert.Equal(t, 1, w.s.Fleet.Card("b.w1").Int("redeals"), "the working card's take ended: its redeal counts")
			for _, m := range []string{"m2", "m3"} {
				assert.LessOrEqual(t, heldCards(w.s, m), DealAhead*4, m)
			}
		})
	}
}

// With no member up, the cards on a down member go back: withdrawn, their
// primaries ready again for the deal.
func TestTheRebalanceWithdrawsTheCardsOfADownMemberWhenNoneIsUp(t *testing.T) {
	t.Parallel()
	w := fleetWorld(t, 1, 4, "m1") // one primary: the stream s1 has a row
	w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m1"}))
	putWorkCard(w, "a", "m1", Ready, 1, nil)
	putWorkCard(w, "b", "m1", Working, 2, nil)
	w.part(TickLevel, TickReq{})
	assert.Equal(t, 0, heldCards(w.s, "m1"))
	assert.Equal(t, Withdrawn, w.s.Fleet.Card("a.w1").Col)
	assert.Equal(t, Withdrawn, w.s.Fleet.Card("b.w1").Col)
	assert.Equal(t, Ready, w.state("b"), "the working card's primary is ready again")
}
