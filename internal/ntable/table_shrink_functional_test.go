//go:build functional

package ntable_test

// The column bound is checked when a change grows a table's column count, so a
// table already over it can shrink.

import (
	"reflect"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// A table already over the column bound (one written under an older rule, or by
// hand) can shrink: the bound is checked when a change grows the count.
func TestTableOverTheColumnBoundCanShrink(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := t.Context()
	if err := ntable.Create(ctx, c, ntable.Table{Name: "wide", Columns: wideColumns(ntable.LimitColumns)}, now); err != nil {
		t.Fatal(err)
	}
	order := c.HGet(ctx, ntable.DefKey("wide"), "order").Val()
	if err := c.HSet(ctx, ntable.DefKey("wide"), "col:x1", "count:sum:0:", "col:x2", "count:sum:0:", "order", order+",x1,x2").Err(); err != nil {
		t.Fatal(err)
	}
	// growing is refused, by name, and writes nothing
	before := storeImage(t, c)
	_, err := ntable.Set(ctx, c, "wide", ntable.SetOpts{ColAdd: &ntable.Column{Name: "x3", Projection: "count", Fold: "sum"}})
	requireLimit(t, "col add to a table over the bound", err, "columns per table", ntable.LimitColumns, ntable.LimitColumns+3)
	if !reflect.DeepEqual(before, storeImage(t, c)) {
		t.Errorf("a refused col add changed the store")
	}
	// shrinking works, twice, down to the bound
	for _, name := range []string{"x1", "x2"} {
		if _, err := ntable.Set(ctx, c, "wide", ntable.SetOpts{ColDel: name}); err != nil {
			t.Fatalf("col del %s on a table over the bound: %v", name, err)
		}
	}
	tb, err := ntable.Read(ctx, c, "wide")
	if err != nil || len(tb.Columns) != ntable.LimitColumns {
		t.Errorf("after two deletes: %d columns, %v; want %d", len(tb.Columns), err, ntable.LimitColumns)
	}
	// and at the bound it grows no more
	_, err = ntable.Set(ctx, c, "wide", ntable.SetOpts{ColAdd: &ntable.Column{Name: "x3", Projection: "count", Fold: "sum"}})
	requireLimit(t, "col add at the bound", err, "columns per table", ntable.LimitColumns, ntable.LimitColumns+1)
}
