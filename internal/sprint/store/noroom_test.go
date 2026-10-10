package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A member's fleet beat and a reader's queue that carry --no-room put their word on the
// snapshot (Snapshot.NoRoom) while the beat is fresh; a beat without it clears it, and a
// stale beat's word counts for nothing. The reader stays up: it still finishes and returns.
func TestNoRoomOnTheSnapshotFollowsTheBeat(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	why := "free disk on the volume of /slots is 0.0 GiB, under the floor of 10 GiB"
	zero := 0.0
	noRoom := func() (map[string]string, map[string]string) {
		t.Helper()
		s, err := h.st.Load(h.ctx, All, nil)
		require.NoError(t, err)
		require.NoError(t, h.st.readerStatesInto(h.ctx, s))
		return s.NoRoom, s.ReaderStates
	}
	_, err := h.st.BeatOwing(h.ctx, "m1", &zero, hostload.Source{}, nil, why)
	require.NoError(t, err)
	wrote, err := h.st.ReaderBeat(h.ctx, "reader-a", why)
	require.NoError(t, err)
	require.True(t, wrote)
	nr, states := noRoom()
	assert.Equal(t, map[string]string{"m1": why, "reader-a": why}, nr)
	assert.Equal(t, sprint.ReaderUp, states["reader-a"])

	_, err = h.st.BeatOwing(h.ctx, "m1", &zero, hostload.Source{}, nil, "")
	require.NoError(t, err)
	nr, _ = noRoom()
	assert.Equal(t, map[string]string{"reader-a": why}, nr, "a beat without the word clears it")

	h.mu.Lock()
	h.now = h.now.Add(2 * sprint.BeatDeadline)
	h.mu.Unlock()
	nr, _ = noRoom()
	assert.Empty(t, nr, "a stale beat's word counts for nothing")
}

// The tick's ask asks no read of a reader whose fresh beat says it starts none: with two of
// the three readers so, no primary gets the two reads it needs; their word cleared, it does.
func TestTheTickAsksNoReadOfAReaderWithNoRoom(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.startMachine()
	h.machine()
	h.work("m1")
	h.work("m2")
	why := "free disk on the volume of /slots is 0.0 GiB, under the floor of 10 GiB"
	for _, r := range []string{"reader-a", "reader-b"} {
		_, err := h.st.ReaderBeat(h.ctx, r, why)
		require.NoError(t, err)
	}
	h.machine()
	for _, c := range h.snap().Readers.Column(sprint.Asked) {
		assert.Equal(t, "reader-c", c.Row, "a reader under its floor is asked nothing")
	}
	h.beat()
	h.machine()
	for _, id := range []string{"s1-1", "s1-2"} {
		assert.Len(t, h.snap().Readers.Of(id), 2, id+": its reads asked once the word is cleared")
	}
}

// The coordinator's fleet verbs, outside the tick, give a member whose fresh beat says it
// starts no card nothing: fleet down redeals the down member's cards to a member that came up
// beside it with no word, never to it.
func TestFleetVerbsLevelNothingToAMemberWithNoRoom(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(12)
	h.live = []string{"m1", "m2"}
	h.beat()
	h.startMachine()
	h.machine()
	why := "free disk on the volume of /slots is 0.0 GiB, under the floor of 10 GiB"
	zero := 0.0
	_, err := h.st.BeatOwing(h.ctx, "m3", &zero, hostload.Source{}, nil, why)
	require.NoError(t, err)
	_, err = h.st.BeatOwing(h.ctx, "m4", &zero, hostload.Source{}, nil, "")
	require.NoError(t, err)
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m3", Width: 12, Who: "coordinator"}))
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m4", Width: 12, Who: "coordinator"}))
	// m1 goes down: its cards are dealt round the members up, never to m3
	h.must(FleetStep(sprint.FleetReq{Op: "down", Member: "m1", Who: "coordinator"}))
	s := h.snap()
	assert.Empty(t, s.Fleet.Cell("m1", sprint.Ready), "the down member's cards are dealt away")
	assert.Empty(t, s.Fleet.Cell("m3", sprint.Ready), "dealt nothing under its floor")
	assert.NotEmpty(t, s.Fleet.Cell("m4", sprint.Ready), "the member beside it with no word is dealt cards")
}
