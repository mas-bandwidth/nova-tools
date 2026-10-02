//go:build functional

package ntable_test

// A create over a dropped table whose saved definition differs is refused
// naming both ways out, and each one runs: the create of the saved
// definition brings the table back, drop --definition forgets it and the new
// create succeeds (USE defect 3). The Lua is T.exists in the nova_sprint
// library; the words are droppedDefinition.

import (
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestACreateOverADroppedTableNamesTheWaysOutAndEachRuns(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()
	saved := demo()
	saved.FooterLabel = "total"
	newTable(t, c, saved)
	_, err := ntable.Drop(ctx, c, saved.Name)
	require.NoError(t, err, "drop")
	other := demo()
	other.Columns = other.Columns[:1]
	err = ntable.Create(ctx, c, other, now)
	require.ErrorIs(t, err, ntable.ErrExists, "create over the dropped definition")
	assert.Contains(t, err.Error(), "nova-table create 'demo' --columns 'ready:count:sum,working:count:sum,done:count:sum,who:members:union' --footer 'total'", "the create that brings it back")
	assert.Contains(t, err.Error(), "; run: nova-table drop 'demo' --definition", "the drop that forgets it")
	require.NoError(t, ntable.Create(ctx, c, saved, now), "the saved definition brings it back")
	_, err = ntable.Drop(ctx, c, saved.Name)
	require.NoError(t, err, "drop again")
	_, err = ntable.DropDefinition(ctx, c, saved.Name)
	require.NoError(t, err, "drop --definition of the dropped table")
	require.NoError(t, ntable.Create(ctx, c, other, now), "the new definition after drop --definition")
	// a present table with another definition keeps set as its way out
	err = ntable.Create(ctx, c, saved, now)
	require.ErrorIs(t, err, ntable.ErrExists, "create over a present table")
	assert.Contains(t, err.Error(), "; run: nova-table set 'demo' --columns <columns>", "a present table's remedy")
}
