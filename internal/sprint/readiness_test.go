package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// headlineWorld is a world with the machines given up at width 2 and n cards of the brief in
// stream s1, ready.
func headlineWorld(t *testing.T, brief string, n int, machines ...string) *world {
	w := newWorld(t)
	for _, m := range machines {
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: m, Width: 2}))
	}
	if n > 0 {
		var cards []CardAdd
		for i := range n {
			cards = append(cards, CardAdd{ID: "s1-" + itoa(i+1), Brief: brief})
		}
		w.must(Add(w.s, AddReq{Stream: "s1", Cards: cards}))
	}
	return w
}

// rowsAgree says the headline's sums are its rows' sums, whatever the fleet's shape.
func rowsAgree(t *testing.T, c Capacity) {
	t.Helper()
	free, eligible := 0, 0
	for _, r := range c.Rows {
		assert.LessOrEqual(t, r.Eligible, r.Free, "row %s", r.Row)
		free += r.Free
		eligible += r.Eligible
	}
	assert.Equal(t, free, c.Free, "the headline's free lanes are the rows'")
	assert.Equal(t, eligible, c.Eligible, "the headline's eligible lanes are the rows'")
	assert.Equal(t, c.Free-c.Eligible, c.Unused)
	lanes := 0
	for _, x := range c.Reasons {
		lanes += x.Lanes
		assert.NotEmpty(t, x.Say)
		assert.NotEmpty(t, x.Owner, "every unused lane has an owner")
		assert.NotEmpty(t, x.Next, "every unused lane has a next action")
	}
	assert.Equal(t, c.Unused, lanes, "every unused lane carries a reason")
	assert.Equal(t, c.Unused, c.Held+c.Starved)
}

// rawIdle is what the headline said before: the up machines' width less their cards
// working, whatever the cards can run on.
func rawIdle(s *Snapshot) int {
	working, width := FleetWorking(s)
	return width - working
}

func TestHeadlineCapacityIsEligibleExecution(t *testing.T) {
	t.Parallel()
	plain := "c: a card\nREPO: mas-bandwidth/nova-tools\n\nThe task."

	t.Run("machine-only: ready cards that fit fill every free lane", func(t *testing.T) {
		w := headlineWorld(t, plain, 3, "m1")
		c := HeadlineCapacity(w.s, CapacityReq{})
		rowsAgree(t, c)
		assert.Equal(t, 2, c.Free)
		assert.Equal(t, 2, c.Eligible)
		assert.Zero(t, c.Unused)
		assert.Nil(t, c.Refill)
		assert.Equal(t, "slots 2/2 free", c.Line())
	})

	t.Run("a held stream is a hold, not starvation, and is never lifted", func(t *testing.T) {
		w := headlineWorld(t, plain, 2, "m1")
		w.must(HoldNames(w.s, HoldReq{Names: []string{"s1"}, Reason: "waiting on the owner", Who: "coordinator"}))
		require.True(t, StreamHeld(w.s, "s1"))
		require.Equal(t, 2, rawIdle(w.s), "the raw counts read two idle lanes with two cards ready")
		c := HeadlineCapacity(w.s, CapacityReq{})
		rowsAgree(t, c)
		assert.Equal(t, 2, c.Free)
		assert.Zero(t, c.Eligible, "no ready card is eligible for a lane while its stream is held")
		assert.Equal(t, 2, c.Held)
		assert.Zero(t, c.Starved, "a deliberate hold is not starvation")
		require.NotEmpty(t, c.Reasons)
		assert.Equal(t, CapStreamHeld, c.Reasons[0].Kind)
		assert.True(t, c.Reasons[0].Hold)
		assert.Equal(t, OwnerCoordinator, c.Reasons[0].Owner)
		assert.Equal(t, "nova-sprint unhold s1", c.Reasons[0].Next)
		assert.True(t, StreamHeld(w.s, "s1"), "the headline lifts no hold")
		assert.Equal(t, Ready, w.s.StateOf("s1-1"), "the headline deals nothing")
		assert.Nil(t, w.s.Fleet.Card("s1-1.w1"))
	})

	t.Run("a tier no route serves leaves the lanes starved with its reason", func(t *testing.T) {
		w := headlineWorld(t, plain, 2, "m1")
		w.s.Routes = []Route{{Name: "r-pro", Tier: "pro", Enabled: true}}
		c := HeadlineCapacity(w.s, CapacityReq{})
		rowsAgree(t, c)
		assert.Zero(t, c.Eligible)
		assert.Equal(t, 2, c.Starved)
		require.NotEmpty(t, c.Reasons)
		assert.Equal(t, CapNoRoute, c.Reasons[0].Kind)
		assert.False(t, c.Reasons[0].Hold)
		assert.Contains(t, c.Reasons[0].Say, "no route serves it")
		assert.ElementsMatch(t, []string{"s1-1", "s1-2"}, c.Reasons[0].Cards)
	})

	t.Run("friend-only: the friends' lanes are the capacity", func(t *testing.T) {
		w := headlineWorld(t, friendBrief("friend"), 3)
		seats := []FriendSeat{{Name: "amy", Width: 2, Status: Up, Class: "flash,pro"}}
		c := HeadlineCapacity(w.s, CapacityReq{Friends: seats})
		rowsAgree(t, c)
		require.Len(t, c.Rows, 1)
		assert.True(t, c.Rows[0].Friend)
		assert.Equal(t, 2, c.Free)
		assert.Equal(t, 2, c.Eligible)
		assert.Zero(t, c.Unused)
	})

	t.Run("mixed: a pin to a friend who is down leaves the machine's lanes with its reason", func(t *testing.T) {
		w := headlineWorld(t, friendBrief("only friend bob"), 1, "m1")
		seats := []FriendSeat{{Name: "amy", Width: 1, Status: Up, Class: "flash"}, {Name: "bob", Width: 1, Status: Down, Why: "no session"}}
		c := HeadlineCapacity(w.s, CapacityReq{Friends: seats})
		rowsAgree(t, c)
		assert.Equal(t, 3, c.Free, "two machine lanes and amy's one")
		assert.Zero(t, c.Eligible)
		require.NotEmpty(t, c.Reasons)
		assert.Equal(t, CapPinned, c.Reasons[0].Kind)
		assert.Equal(t, 1, c.Reasons[0].Lanes)
		assert.Equal(t, "friend bob", c.Reasons[0].Owner)
		assert.Contains(t, c.Reasons[0].Say, "no session")
		assert.Equal(t, CapNoWork, c.Reasons[len(c.Reasons)-1].Kind, "the lanes no card could fill say so")
	})

	t.Run("zero ready work with free lanes is visible with a reason", func(t *testing.T) {
		w := headlineWorld(t, plain, 0, "m1")
		c := HeadlineCapacity(w.s, CapacityReq{})
		rowsAgree(t, c)
		assert.Equal(t, 2, c.Starved)
		require.Len(t, c.Reasons, 1)
		assert.Equal(t, CapNoWork, c.Reasons[0].Kind)
		assert.Equal(t, "nova-sprint add", c.Reasons[0].Next)
		require.NotNil(t, c.Refill)
		assert.True(t, c.Refill.Advise, "with nothing downstream, refilling runs")
	})

	t.Run("cards admitted held are a hold, named by NeedsRank", func(t *testing.T) {
		w := headlineWorld(t, plain, 0, "m1")
		w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-1", "s1-2"}, Brief: plain, Held: true}))
		c := HeadlineCapacity(w.s, CapacityReq{})
		rowsAgree(t, c)
		assert.Equal(t, 2, c.Held)
		assert.Zero(t, c.Starved)
		require.NotEmpty(t, c.Reasons)
		assert.Equal(t, CapNeed, c.Reasons[0].Kind)
		assert.True(t, c.Reasons[0].Hold)
		assert.Contains(t, c.Reasons[0].Next, "nova-sprint release s1-")
		assert.True(t, IsHeld(w.s.Work.Card("s1-1")), "the headline releases nothing")
	})

	t.Run("full build lanes make a machine's ready cards ineligible", func(t *testing.T) {
		w := headlineWorld(t, plain, 2, "m1")
		lanes := []LaneRow{{Kind: LaneGo, Machine: "m1", Width: 1, Held: []string{"a"}, Waiting: []string{"b"}}}
		c := HeadlineCapacity(w.s, CapacityReq{Lanes: lanes})
		rowsAgree(t, c)
		assert.Zero(t, c.Eligible)
		require.NotEmpty(t, c.Reasons)
		assert.Equal(t, CapBuildLanes, c.Reasons[0].Kind)
		assert.Equal(t, OwnerMachine, c.Reasons[0].Owner)
		assert.NotEmpty(t, c.Rows[0].Lanes)
	})

	t.Run("refill advice is checked against the readers downstream", func(t *testing.T) {
		w := headlineWorld(t, plain, 1, "m1")
		w.s.Readers.SetRows([]string{"reader-m1"})
		w.s.ReaderStates = map[string]string{} // no reader up
		w.place(w.s.Work, "s1-1", "s1", Review)
		c := HeadlineCapacity(w.s, CapacityReq{})
		rowsAgree(t, c)
		assert.Equal(t, 2, c.Starved)
		require.NotNil(t, c.Refill)
		assert.False(t, c.Refill.Advise, "refilling ready only moves the queue to review")
		assert.Contains(t, c.Refill.Say, "no reader is up")
		assert.Equal(t, "nova-sprint ask --stream s1", c.Refill.Next)
	})
}
