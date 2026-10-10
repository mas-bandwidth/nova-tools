package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/hostload"
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
