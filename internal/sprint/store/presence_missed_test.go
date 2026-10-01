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
// the fleet table's load cell says it missed one beat.
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
	assert.Equal(t, "missed 1", h.fleetRow("m1")[sprint.Load])
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
	assert.Equal(t, "missed 3", h.fleetRow("m1")[sprint.Load])
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
		require.Equal(t, "missed 2", h.fleetRow("m1")[sprint.Load])
		h.setLive("m1", "m2")
		h.tick(time.Second)
		h.machine()
	}
	assert.Equal(t, sprint.Up, h.snap().MemberCtl("m1").F("status"))
	assert.Equal(t, dealt, h.dealtTo())
	assert.Empty(t, h.memberNotes(sprint.NMemberDown, "m1"))
}

// The tick measures the store round trip (the time of its one read of the
// heartbeat record) and keeps the last TripWindow of them; where prints the
// last and the greatest. The mem twin answers at the speed the test's hook
// sets by stepping the harness clock.
func TestTheTickMeasuresTheStoreRoundTrip(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.startMachine()
	var delay time.Duration
	h.m.Fail = func(p string) error {
		if p == "kv" {
			h.mu.Lock()
			h.now = h.now.Add(delay)
			h.mu.Unlock()
		}
		return nil
	}
	for _, d := range []time.Duration{120 * time.Millisecond, 450 * time.Millisecond, 30 * time.Millisecond} {
		delay = d
		h.tick(6 * time.Second) // past the idle heartbeat's every
		h.machine()
	}
	_, hb, err := h.st.Machine(h.ctx)
	require.NoError(t, err)
	assert.Equal(t, "store round trip: last 30ms, max 450ms over the last 3 ticks", hb.RoundTrips())
	for range 2 * TripWindow {
		h.tick(6 * time.Second)
		h.machine()
	}
	_, hb, err = h.st.Machine(h.ctx)
	require.NoError(t, err)
	assert.Len(t, hb.Trips, TripWindow, "the window is bounded")
	assert.Equal(t, "store round trip: last 30ms, max 30ms over the last 10 ticks", hb.RoundTrips(), "the 450ms left the window")
}
