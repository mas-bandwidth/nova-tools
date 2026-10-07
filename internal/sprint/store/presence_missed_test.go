package store

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// One missed beat window never downs a working member (tla/DirtyTick.tla,
// Lapse needs MissedBeatsDown misses): m1 goes a window and a second without
// a beat, the tick runs, and m1 is up and keeps its cards, none withdrawn;
func TestOneMissedBeatDownsNobodyAndWithdrawsNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(4)
	h.startMachine()
	h.machine()
	dealt := h.dealtTo()
	h.setLive("m2")
	h.tick(sprint.BeatDeadline + time.Second)
	h.machine()
	require.Equal(t, sprint.Up, h.snap().MemberCtl("m1").F("status"))
	assert.Equal(t, dealt, h.dealtTo(), "one missed beat moved cards")
	assert.Empty(t, h.snap().Fleet.Column(sprint.Withdrawn))
	assert.Empty(t, h.memberNotes(sprint.NMemberDown, "m1"))
}

// Three misses in a row down the member and take its cards back, and the
// fleet table says three.
func TestThreeMissedBeatsDownTheMemberAndWithdrawItsCards(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(4)
	h.startMachine()
	h.machine()
	h.setLive("m2")
	h.tick(downAfter)
	h.machine()
	require.Equal(t, sprint.Up, h.snap().MemberCtl("m1").F("status"), "at the third window's end it is still up")
	h.tick(time.Second)
	h.machine()
	assert.Equal(t, sprint.Down, h.snap().MemberCtl("m1").F("status"))
	assert.Zero(t, h.dealtTo()["m1"])
}

// A beat between misses resets the count: two missed windows, a beat, two more
// missed windows, and m1 was never down.
func TestABeatBetweenMissesResetsTheMemberCount(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(4)
	h.startMachine()
	h.machine()
	dealt := h.dealtTo()
	for range 2 {
		h.setLive("m2")
		h.tick(2*sprint.BeatDeadline + time.Second)
		h.machine()
		h.setLive("m1", "m2")
		h.tick(time.Second)
		h.machine()
	}
	assert.Equal(t, sprint.Up, h.snap().MemberCtl("m1").F("status"))
	assert.Equal(t, dealt, h.dealtTo())
	assert.Empty(t, h.memberNotes(sprint.NMemberDown, "m1"))
}
