package store

import (
	"context"
	"slices"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An old fleet table, created before the causes of a failed attempt (no brief and machinery
// columns, done the sum of ok and failed), is brought to the shape schema.go defines: the two
// columns added hidden at the end and done redefined over all four. A second ensure changes
// nothing, and init matches the table after it.
func TestEnsureDoneCausesBringsAnOldFleetTableToTheCauses(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMem()
	names := sprint.Names{}
	def, ok := fleetDefinition(names)
	require.True(t, ok)
	old := def
	old.Columns = slices.DeleteFunc(slices.Clone(def.Columns), func(c ntable.Column) bool {
		return c.Name == sprint.DoneBrief || c.Name == sprint.DoneMachinery
	})
	old.Hidden = slices.DeleteFunc(slices.Clone(def.Hidden), func(h string) bool {
		return h == sprint.DoneBrief || h == sprint.DoneMachinery
	})
	done := old.Column(sprint.Done)
	require.GreaterOrEqual(t, done, 0)
	old.Columns[done].Projection = "sum(ok+failed)"
	require.NoError(t, m.Create(ctx, old))
	st := &Store{B: m, Names: names, Actor: "coordinator"}
	shape := func() ntable.Table {
		shapes, err := m.Shapes(ctx, []string{def.Name})
		require.NoError(t, err)
		return shapes[0]
	}
	require.Less(t, shape().Column(sprint.DoneBrief), 0, "the old table has no brief column")

	require.NoError(t, st.EnsureDoneCauses(ctx))
	got := shape()
	var gotNames, wantNames []string
	for _, c := range got.Columns {
		gotNames = append(gotNames, c.Name)
	}
	for _, c := range def.Columns {
		wantNames = append(wantNames, c.Name)
	}
	assert.Equal(t, wantNames, gotNames, "the columns in Init's order")
	assert.Equal(t, "sum(ok+failed+brief+machinery)", got.Columns[got.Column(sprint.Done)].Projection)
	assert.True(t, got.IsHidden(sprint.DoneBrief))
	assert.True(t, got.IsHidden(sprint.DoneMachinery))

	require.NoError(t, st.EnsureDoneCauses(ctx), "a second ensure leaves it")
	assert.Equal(t, len(def.Columns), len(shape().Columns))
	require.NoError(t, st.Init(ctx), "init matches a table that now has the causes")
	require.NoError(t, (&Store{B: NewMem(), Names: names}).EnsureDoneCauses(ctx), "no table yet: nothing to do")
}
