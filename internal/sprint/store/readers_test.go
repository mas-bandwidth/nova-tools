package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A reader's state is derived from two records outside the tables: its beat,
// written by its own queue, and the coordinator's hold (readers.go).
func TestReaderStatesFollowTheBeatAndTheHold(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rows := []string{"reader-a", "reader-b", "reader-c"}
	state := func() map[string]string {
		t.Helper()
		got, err := h.st.ReaderStates(h.ctx, rows, h.st.now())
		require.NoError(t, err)
		return got
	}
	// the harness beats every reader: all up
	assert.Equal(t, map[string]string{"reader-a": "up", "reader-b": "up", "reader-c": "up"}, state())
	// a name that is no row writes no beat
	wrote, err := h.st.ReaderBeat(h.ctx, "reader-z")
	require.NoError(t, err)
	assert.False(t, wrote)
	// the hold is away whatever it beats, and up releases it
	require.NoError(t, h.st.SetReaderAway(h.ctx, "reader-b", true, "coordinator"))
	h.beat()
	assert.Equal(t, "away", state()["reader-b"])
	require.NoError(t, h.st.SetReaderAway(h.ctx, "reader-b", false, "coordinator"))
	assert.Equal(t, "up", state()["reader-b"])
	require.Error(t, h.st.SetReaderAway(h.ctx, "reader-z", true, "coordinator"), "a hold names a row")
	// no beat past the bound is away; a reader that never beat is down
	h.mu.Lock()
	h.now = h.now.Add(sprint.ReaderBeatBound + time.Second)
	h.mu.Unlock()
	assert.Equal(t, "away", state()["reader-a"])
	got, err := h.st.ReaderStates(h.ctx, []string{"reader-q"}, h.st.now())
	require.NoError(t, err)
	assert.Equal(t, "down", got["reader-q"])
}

// The tick's ask reads the states: an away reader gets no read, and the
// judgment for fewer than two up is written once and closes.
func TestTheTickAsksReadersUpAndSaysWhenFewerThanTwoAre(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.startMachine()
	h.machine()
	h.work("m1")
	h.work("m2")
	require.NoError(t, h.st.SetReaderAway(h.ctx, "reader-a", true, "coordinator"))
	require.NoError(t, h.st.SetReaderAway(h.ctx, "reader-b", true, "coordinator"))
	h.machine()
	assert.Empty(t, h.snap().Readers.Column(sprint.Asked), "one reader up: none asked")
	assert.Len(t, h.openOf(sprint.NFewReaders), 1)
	h.machine()
	assert.Equal(t, 1, h.written(sprint.NFewReaders), "written once")
	require.NoError(t, h.st.SetReaderAway(h.ctx, "reader-a", false, "coordinator"))
	h.machine()
	for _, id := range []string{"s1-1", "s1-2"} {
		assert.Len(t, h.snap().Readers.Of(id), 2, id)
	}
	assert.Empty(t, h.openOf(sprint.NFewReaders))
}

// The table layer's row delete, in memory: a row goes, its cards are unplaced,
// and a store pinned to another epoch is refused as RowsAdd is.
func TestMemRowsDelRemovesTheRowsAndUnplacesTheirCards(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	h.work("m1")
	h.machine()
	require.NotEmpty(t, h.snap().Readers.Column(sprint.Asked))
	rows, err := h.st.ReaderRows(h.ctx)
	require.NoError(t, err)
	require.Len(t, rows, 3)
	require.NoError(t, h.m.RowsDel(h.ctx, "t-readers", []string{"reader-c", "reader-nobody"}))
	rows, err = h.st.ReaderRows(h.ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"reader-a", "reader-b"}, rows)
	for _, c := range h.snap().Readers.Column(sprint.Asked) {
		assert.NotEqual(t, "reader-c", c.Row, "the removed row's cards are not placed")
	}
	pinned := h.m.AtEpoch(7, false)
	require.Error(t, pinned.RowsDel(h.ctx, "t-readers", []string{"reader-a"}), "a stale epoch is refused")
}
