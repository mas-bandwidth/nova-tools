package store

import (
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/stretchr/testify/require"
)

// An old readers table, created with no tiers column, gains it and then takes
// a row set. Empty stays empty (every tier). Init after that matches.
func TestEnsureReaderTiersAddsTheColumnAnOldTableLacks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMem()
	names := sprint.Names{}
	var old ntable.Table
	for _, def := range names.Definitions() {
		if def.Name == names.Table(sprint.Readers) {
			old = def
		}
	}
	require.NotEmpty(t, old.Columns)
	old.Columns = old.Columns[:len(old.Columns)-1]
	require.NoError(t, m.Create(ctx, old))
	require.NoError(t, m.RowsAdd(ctx, old.Name, []string{"reader-a"}))
	require.Error(t, m.RowSet(ctx, old.Name, "reader-a", map[string]string{sprint.ReaderTiers: "flash"}), "the old table has no tiers column")
	st := &Store{B: m, Names: names, Actor: "coordinator"}
	require.NoError(t, st.EnsureReaderTiers(ctx))
	require.NoError(t, m.RowSet(ctx, old.Name, "reader-a", map[string]string{sprint.ReaderTiers: "flash"}))
	require.NoError(t, st.EnsureReaderTiers(ctx), "a second ensure leaves the column")
	require.NoError(t, st.Init(ctx), "init matches a table that now has the column")
	shapes, err := m.Shapes(ctx, []string{old.Name})
	require.NoError(t, err)
	require.GreaterOrEqual(t, shapes[0].Column(sprint.ReaderTiers), 0)
}
