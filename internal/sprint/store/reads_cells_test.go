package store

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/require"
)

// TestReadCellsReadsOnlyTheNamedCells pins ReadCells (reads.go) to the cells it is
// asked for: a member's queue (ready, working, ctl) reads those records alone, never
// its row's ok and failed cells, which hold every card it ever finished (2026-10-10:
// every worker's queue poll read them, and the fleet table's read sets were 62% of
// the store's one thread). What it returns is what a whole read of the table holds in
// those cells, column by column.
func TestReadCellsReadsOnlyTheNamedCells(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(20)
	h.startMachine()
	h.machine()
	h.work("m1")
	h.machine()

	whole, err := h.st.Load(h.ctx, []string{sprint.Fleet}, nil)
	require.NoError(t, err)
	require.NotEmpty(t, whole.Fleet.Cell("m1", sprint.DoneOK), "the member finished cards: its ok cell holds them")
	cols := []string{sprint.Ready, sprint.Working, sprint.Ctl}
	var want []string
	for _, col := range cols {
		for _, c := range whole.Fleet.Cell("m1", col) {
			want = append(want, c.ID)
		}
	}

	stats := h.st.stats()
	before := stats.rows.Load()
	got, err := h.st.ReadCells(h.ctx, sprint.Fleet, "m1", cols...)
	require.NoError(t, err)
	read := stats.rows.Load() - before
	var ids []string
	for _, c := range got {
		ids = append(ids, c.ID)
	}
	require.Equal(t, want, ids, "the named cells, column by column, as a whole read holds them")
	require.Equal(t, int64(len(want)), read, "ReadCells read %d records for %d cards in the named cells: the finished cells were read too", read, len(want))
}
