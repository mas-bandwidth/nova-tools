package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A work table made before the cost column (a live store, until the column is added
// with nova-table col add) is left as it is: the display sync skips the cell it does not
// have, so no step that syncs the display fails on it.
func TestTheDisplaySyncSkipsAWorkTableWithoutTheCostColumn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMem()
	names := sprint.Names{Prefix: "old-"}
	for _, def := range names.Definitions() {
		if def.Name == names.Table(sprint.Work) {
			require.Equal(t, sprint.Cost, def.Columns[len(def.Columns)-1].Name, "cost is the work table's last column")
			def.Columns = def.Columns[:len(def.Columns)-1]
		}
		require.NoError(t, m.Create(ctx, def))
	}
	require.NoError(t, m.RowsAdd(ctx, names.Table(sprint.Work), []string{"s1"}))
	require.NoError(t, m.RowsAdd(ctx, names.Table(sprint.Merge), []string{"s1"}))
	st := &Store{B: m, Names: names, Actor: "tester"}
	require.NoError(t, st.SyncMirrors(ctx), "a table without the column is not written a cell")
}
