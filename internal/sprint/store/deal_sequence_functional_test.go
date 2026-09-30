//go:build functional

package store

import "testing"

// The rolling indexes continue across plans on a real Redis with the table
// layer's functions loaded (deal_sequence_test.go has the drivers and runs
// them on the Mem backend).

func TestRedisTheDealContinuesAcrossTicksInExactSequence(t *testing.T) {
	t.Parallel()
	h, _ := liveHarness(t)
	dealSequenceAcrossTicks(t, h, 3, 20)
}

func TestRedisTheDealContinuesAcrossTheManifestsOfAPlan(t *testing.T) {
	t.Parallel()
	h, _ := liveHarness(t)
	dealSequenceAcrossTicks(t, h, 3, 150)
}

func TestRedisTheDealsStreamIndexContinuesAcrossTicks(t *testing.T) {
	t.Parallel()
	h, _ := liveHarness(t)
	dealStreamsAcrossTicks(t, h)
}

func TestRedisTheAsksIndexesContinueAcrossTicks(t *testing.T) {
	t.Parallel()
	h, _ := liveHarness(t)
	askAcrossTicks(t, h)
}

func TestRedisTheAcceptsStreamIndexContinuesAcrossSteps(t *testing.T) {
	t.Parallel()
	h, _ := liveHarness(t)
	acceptAcrossTicks(t, h)
}
