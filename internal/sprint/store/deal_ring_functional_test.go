//go:build functional

package store

import "testing"

// The deal's and the ask's rolling indexes through the machine's tick on a
// real Redis with the table layer's functions loaded (deal_ring_test.go has
// the drivers and runs them on the Mem backend): the index is read from the
// table's property by each tick, not only written, and eight members that
// beat before start are dealt round, m1..m8, m1..m8.

func TestRedisTheDealGoesRoundTheFleetThroughTheTick(t *testing.T) {
	t.Parallel()
	h, _ := liveHarness(t)
	dealRingOwnersRun(t, h)
}

func TestRedisTheIndexesGoOnFromTickToTick(t *testing.T) {
	t.Parallel()
	h, _ := liveHarness(t)
	dealRingAcrossTicks(t, h)
}

func TestRedisTheRedealsGoRoundTheFleetInARunWithFailures(t *testing.T) {
	t.Parallel()
	h, _ := liveHarness(t)
	dealRingWithFailures(t, h)
}

func TestRedisMemberDownRedealsAndLevelGoRoundTheFleet(t *testing.T) {
	t.Parallel()
	h, _ := liveHarness(t)
	dealRingMemberDownAndLevel(t, h)
}

// Dealt on the real table layer: the ready and working cells' ids through the cell reader
// cut to those columns, never a done card (dealt_test.go has the driver).
func TestRedisDealtReadsTheCardsInFlightAlone(t *testing.T) {
	t.Parallel()
	h, _ := liveHarness(t)
	dealtLife(t, h)
}
