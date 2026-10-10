package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTickPartsTakeUnderTwentyRoundTrips pins the tick's cost in store round
// trips: one tick over a snapshot of 2,000 cards with every tick part busy
// makes at most 20 exchanges with the store, whatever the card count. The
// store is the recording Mem, whose Trips counts every exchange it answers; a
// tick that batches its reads and writes into few flushes is what the bound
// wants. The tick plans on its twin, and the harness checks every part's read
// against a fresh read (Store.CheckTwin), so the part results are the same as
// a fresh read's, and the bound is met without changing what the tick does.
//
// The fixture drives the first tick of a 2,000-card sprint: the deal, the
// rebalance, the accept, the ask, the resume, the presence, the friend stall,
// the check, the deadlines, the overdue, the done, the where and the archive
// parts all have work to do (each is asserted busy below), so the count is the
// whole tick's, not a quiet one's.
func TestTickPartsTakeUnderTwentyRoundTrips(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2000)
	h.startMachine()
	before := h.m.Trips()
	res := h.machine()
	trips := h.m.Trips() - before
	for _, p := range res.Times {
		t.Logf("tick part %s/%s trips=%d", p.Table, p.Name, p.Trips)
	}
	// every part this fixture drives is busy: the count below is the whole
	// tick's, and a part passed over would be a hole in the measurement
	for _, name := range []string{
		"deal", "rebalance", "accept", "ask", "resume", "presence", "friend-stall",
		"check", "deadlines", "overdue", "done", "where", "archive",
	} {
		busy := false
		for _, p := range res.Times {
			if p.Name == name && p.Trips > 0 {
				busy = true
			}
		}
		require.True(t, busy, "part %s did not run: the tick was not busy; %+v", name, res.Times)
	}
	t.Logf("one tick over 2,000 cards: trips=%d took=%s", trips, res.Took)
	require.LessOrEqual(t, trips, int64(20),
		"one tick over 2,000 cards takes %d store round trips, over the bound of 20; the parts made: %+v", trips, res.Times)
}
