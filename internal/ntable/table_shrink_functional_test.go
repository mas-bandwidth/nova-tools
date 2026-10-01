//go:build functional

package ntable_test

// The column bound is checked when a change grows a table's column count, so a
// table already over it can shrink.

import (
	"fmt"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A table already over the column bound (one written under an older rule, or by
// hand) can shrink: the bound is checked when a change grows the count.
func TestTableOverTheColumnBoundCanShrink(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := t.Context()
	require.NoError(t, ntable.Create(ctx, c, ntable.Table{Name: "wide", Columns: wideColumns(ntable.LimitColumns)}, now))
	order := c.HGet(ctx, ntable.DefKey("wide"), "order").Val()
	require.NoError(t, c.HSet(ctx, ntable.DefKey("wide"), "col:x1", "count:sum:0:", "col:x2", "count:sum:0:", "order", order+",x1,x2").Err())
	// growing is refused, by name, and writes nothing
	before := storeImage(t, c)
	_, err := ntable.Set(ctx, c, "wide", ntable.SetOpts{ColAdd: &ntable.Column{Name: "x3", Projection: "count", Fold: "sum"}})
	requireLimit(t, "col add to a table over the bound", err, "columns per table", ntable.LimitColumns, ntable.LimitColumns+3)
	assert.Equal(t, before, storeImage(t, c), "a refused col add changed the store")
	// shrinking works, twice, down to the bound
	for _, name := range []string{"x1", "x2"} {
		_, err := ntable.Set(ctx, c, "wide", ntable.SetOpts{ColDel: name})
		require.NoError(t, err, "col del %s on a table over the bound", name)
	}
	tb, err := ntable.Read(ctx, c, "wide")
	assert.NoError(t, err, "after two deletes")
	assert.Len(t, tb.Columns, ntable.LimitColumns, "after two deletes")
	// and at the bound it grows no more
	_, err = ntable.Set(ctx, c, "wide", ntable.SetOpts{ColAdd: &ntable.Column{Name: "x3", Projection: "count", Fold: "sum"}})
	requireLimit(t, "col add at the bound", err, "columns per table", ntable.LimitColumns, ntable.LimitColumns+1)
}

// A table over the row bound (100,001 rows, written by hand) stays and shrinks
// and never grows: row add and rows add of a new row are refused, row del works,
// and once a row is gone the bound still refuses the row that would put it back.
func TestTableOverTheRowBoundStaysAndShrinks(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := t.Context()
	cols, err := ntable.ParseColumns("a")
	require.NoError(t, err)
	require.NoError(t, ntable.Create(ctx, c, ntable.Table{Name: "tall", Columns: cols}, now))
	rows := ntable.DefKey("tall") + ":rows"
	pipe := c.Pipeline()
	for i := 0; i <= ntable.LimitRows; i++ {
		pipe.ZAdd(ctx, rows, redis.Z{Score: float64(i + 1), Member: fmt.Sprintf("r%d", i)})
	}
	_, err = pipe.Exec(ctx)
	require.NoError(t, err)
	_, err = ntable.RowAdd(ctx, c, "tall", "new", ntable.RowSpec{})
	requireLimit(t, "row add on 100,001 rows", err, "rows per table", ntable.LimitRows, ntable.LimitRows+2)
	_, err = ntable.RowsAdd(ctx, c, "tall", []string{"new1", "new2"})
	requireLimit(t, "rows add on 100,001 rows", err, "rows per table", ntable.LimitRows, ntable.LimitRows+3)
	ok, err := ntable.RowDel(ctx, c, "tall", "r0")
	require.NoError(t, err, "row del on a table over the bound")
	require.True(t, ok, "row del on a table over the bound")
	n := c.ZCard(ctx, rows).Val()
	require.Equal(t, int64(ntable.LimitRows), n, "%d rows after a delete", n)
	_, err = ntable.RowAdd(ctx, c, "tall", "r0", ntable.RowSpec{})
	requireLimit(t, "row add at the bound", err, "rows per table", ntable.LimitRows, ntable.LimitRows+1)
}
