package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// store-latency-alarm-bb (docs/SPEC-SPRINT.md section 14): the tick's store slow
// alarm on the server's measured p50 store round trip (TickReq.StoreRTTP50MS,
// TickReq.StoreRTTFresh). Over StoreSlowBar it says the store is slow once per
// episode, under half the bar the episode ends with one note more, and with no
// fresh record it does nothing. The episode is kept as two properties of the
// fleet table, as the idle alarm's is (TickIdle); the test is on the in-memory
// world, no store, no socket and no wall clock.
func TestStoreRoundTripOverFiveMillisecondsRaisesOneAlarm(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	r := func(p50 float64) TickReq {
		return TickReq{IdleAlarm: true, StoreRTTP50MS: p50, StoreRTTFresh: true}
	}
	tick := func(p50 float64) Plan {
		p, _ := TickStoreSlow(w.s, r(p50))
		return w.must(p)
	}
	prop := func(name string) string {
		v, _ := w.s.Fleet.Prop(name)
		return v
	}

	// 9 ms: the episode begins and the one note says the store is slow
	tick(9)
	notes := w.notesOf(NStoreSlow)
	require.Len(t, notes, 1, "9 ms: one note")
	assert.Contains(t, notes[0].What, "9 ms", "the note names the p50")
	assert.Equal(t, "coordinator", notes[0].To, "the note is addressed to the coordinator")
	require.NotEmpty(t, prop(PropStoreSlowSince), "9 ms: the episode began")
	require.NotEmpty(t, prop(PropStoreSlowSaid), "9 ms: the note was pushed")

	// a second tick at 9 ms: none again
	tick(9)
	require.Len(t, w.notesOf(NStoreSlow), 1, "a second 9 ms: no note again")

	// 4 ms: the episode is kept, and no note is written
	tick(4)
	require.Len(t, w.notesOf(NStoreSlow), 1, "4 ms: no note")
	require.Empty(t, w.notesOf(NStoreSlowCleared), "4 ms: the episode is kept")
	require.NotEmpty(t, prop(PropStoreSlowSince), "4 ms: the episode is kept")

	// 2 ms: the episode ends with one cleared note
	tick(2)
	require.Len(t, w.notesOf(NStoreSlowCleared), 1, "2 ms: one cleared note")
	assert.Empty(t, prop(PropStoreSlowSince), "2 ms: the episode ended")
	assert.Empty(t, prop(PropStoreSlowSaid), "2 ms: the episode ended")

	// no fresh record: nothing, though the p50 is over the bar
	p, _ := TickStoreSlow(w.s, TickReq{IdleAlarm: true, StoreRTTP50MS: 99})
	assert.Empty(t, p.Units, "no fresh record: nothing")
	assert.Empty(t, p.Notes, "no fresh record: nothing")
}
