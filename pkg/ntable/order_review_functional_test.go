//go:build functional

package ntable_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/stretchr/testify/require"
)

// Regression witnesses from the independent review of the ordering kernel.
// A standing sort is a stored invariant across every row writer, including
// Bind, and a combined Set must use the definition that it will leave.
func TestStandingSortSurvivesBind(t *testing.T) {
	t.Parallel()
	for _, by := range []string{"name", "label"} {
		t.Run(by, func(t *testing.T) {
			t.Parallel()
			_, c := live(t)
			ctx := context.Background()
			orderTable(t, c)
			_, err := ntable.RowsAdd(ctx, c, "t", []string{"unused"})
			require.NoError(t, err)
			_, err = ntable.Set(ctx, c, "t", ntable.SetOpts{RowSort: &ntable.Sort{By: by, Keep: true}})
			require.NoError(t, err)
			before := held(t, c, "t")
			tab, err := ntable.Read(ctx, c, "t")
			require.NoError(t, err)
			// Reversing all input rows must not replace a standing sort with input
			// order, even when the desired order equals the existing stored order.
			for l, r := 0, len(tab.Rows)-1; l < r; l, r = l+1, r-1 {
				tab.Rows[l], tab.Rows[r] = tab.Rows[r], tab.Rows[l]
			}
			var receipt ntable.Receipt
			require.NoError(t, ntable.Bind(ctx, c, tab, now, ntable.WriteOptions{Receipt: &receipt}))
			rows, _ := orderOf(t, c, "t")
			require.Equal(t, "c,k,m,unused,z", strings.Join(rows, ","), "standing sort violated after reversed Bind: %v", rows)
			require.Equal(t, receipt.Before, tab.Revision, "Bind receipt: %+v", receipt)
			require.Equal(t, tab.Revision+1, receipt.After, "Bind receipt: %+v", receipt)
			// A replacement removes an empty row, adds another, and relabels an
			// existing row. Removed rows must not be resurrected by final ranking.
			kept := tab.Rows[:0]
			for _, row := range tab.Rows {
				if row.Key != "unused" {
					if row.Key == "z" {
						row.Label = "A team"
					}
					kept = append(kept, row)
				}
			}
			tab.Rows = append(kept, ntable.Row{Key: "b", Cells: make([]ntable.Cell, len(tab.Columns))})
			require.NoError(t, ntable.Bind(ctx, c, tab, now))
			rows, _ = orderOf(t, c, "t")
			want := "b,c,k,m,z"
			if by == "label" {
				want = "z,b,c,k,m"
			}
			require.Equal(t, want, strings.Join(rows, ","), "standing %s sort after replacement: %v want %s", by, rows, want)
			after := held(t, c, "t")
			require.Equal(t, before, after, "Bind changed cell contents: %v -> %v", before, after)
		})
	}
}

func TestStandingSortCombinedManualEditsRefuseAtomically(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"move", "order"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			_, c := live(t)
			ctx := context.Background()
			orderTable(t, c)
			change := ntable.SetOpts{RowSort: &ntable.Sort{By: "name", Keep: true}, Footer: new("should not write")}
			if verb == "move" {
				change.RowMove = &ntable.Reorder{Item: "z", Place: at("first", "")}
			} else {
				change.RowOrder = []string{"z"}
			}
			before := storeImage(t, c)
			_, err := ntable.Set(ctx, c, "t", change)
			require.ErrorContains(t, err, "--manual", "standing sort plus manual %s: %v", verb, err)
			require.Equal(t, before, storeImage(t, c), "refused combined edit wrote")
			// Clearing a standing sort and moving in the same call remains valid.
			_, err = ntable.Set(ctx, c, "t", ntable.SetOpts{RowSort: change.RowSort})
			require.NoError(t, err)
			change.RowSort = &ntable.Sort{Manual: true}
			_, err = ntable.Set(ctx, c, "t", change)
			require.NoError(t, err)
			rows, _ := orderOf(t, c, "t")
			require.Equal(t, "z", rows[0], "manual edit after clearing standing sort: %v", rows)
		})
	}
}

func TestRowSortRejectsUnsupportedProjections(t *testing.T) {
	t.Parallel()
	for _, projection := range []string{"members", "first", "last"} {
		t.Run(projection, func(t *testing.T) {
			t.Parallel()
			_, c := live(t)
			ctx := context.Background()
			orderTable(t, c)
			col, err := ntable.ParseColumn("names:" + projection + ":union")
			require.NoError(t, err)
			_, err = ntable.Set(ctx, c, "t", ntable.SetOpts{ColAdd: &col})
			require.NoError(t, err)
			before := storeImage(t, c)
			_, err = ntable.Set(ctx, c, "t", ntable.SetOpts{RowSort: &ntable.Sort{By: "names"}})
			require.ErrorContains(t, err, "a count column or a text column", "unsupported projection sort")
			require.Equal(t, before, storeImage(t, c), "refused projection sort wrote")
		})
	}
}

func TestRowSortUsesFinalShape(t *testing.T) {
	t.Parallel()
	for _, projection := range []string{"count", "text"} {
		t.Run(projection, func(t *testing.T) {
			t.Parallel()
			_, c := live(t)
			ctx := context.Background()
			orderTable(t, c)
			// Orphan metadata is cleared when a fresh column is declared. The sort
			// must not use values/bindings that this same call will discard.
			require.NoError(t, c.HSet(ctx, ntable.RowKey("t", "c"), "key:fresh", "outside", "text:fresh", "zz").Err())
			require.NoError(t, c.Set(ctx, "outside", "not a set", 0).Err())
			col, err := ntable.ParseColumn("fresh:" + projection)
			require.NoError(t, err)
			var receipt ntable.Receipt
			_, err = ntable.Set(ctx, c, "t", ntable.SetOpts{ColAdd: &col, RowSort: &ntable.Sort{By: "fresh"}}, ntable.WriteOptions{Receipt: &receipt})
			require.NoError(t, err, "combined add and sort")
			rows, _ := orderOf(t, c, "t")
			require.Equal(t, "c,k,m,z", strings.Join(rows, ","), "sort used discarded metadata: %v", rows)
			require.Equal(t, receipt.Before+1, receipt.After, "combined edit receipt: %+v", receipt)
			before := storeImage(t, c)
			_, err = ntable.Set(ctx, c, "t", ntable.SetOpts{ColDel: "fresh", RowSort: &ntable.Sort{By: "fresh"}})
			require.ErrorContains(t, err, "no such column", "sort accepted removed column")
			require.Equal(t, before, storeImage(t, c), "refused removed-column sort wrote")
		})
	}
}

func TestOrderReceiptsNameEveryRankedRow(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"row", "column"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			_, c := live(t)
			ctx := context.Background()
			orderTable(t, c)
			change := ntable.SetOpts{}
			if verb == "row" {
				change.RowMove = &ntable.Reorder{Item: "k", Place: at("first", "")}
			} else {
				change.ColMove = &ntable.Reorder{Item: "note", Place: at("first", "")}
			}
			_, err := ntable.Set(ctx, c, "t", change)
			require.NoError(t, err)
			events, err := c.XRevRangeN(ctx, "table:t:changes", "+", "-", 1).Result()
			require.NoError(t, err)
			var cells []string
			require.NoError(t, json.Unmarshal([]byte(events[0].Values["cells"].(string)), &cells))
			want := []string{"c:a", "c:b", "c:note", "c:p", "k:a", "k:b", "k:note", "k:p", "m:a", "m:b", "m:note", "m:p", "z:a", "z:b", "z:note", "z:p"}
			require.Equal(t, want, cells, "ordering receipt does not name all affected row cells: %v", cells)
		})
	}
}

func TestOrderDefinitionOnlyReceiptsReportChange(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()
	orderTable(t, c)
	// Put rows in name order before enabling a standing sort: the definition
	// changes even when no rank needs to be written.
	_, err := ntable.Set(ctx, c, "t", ntable.SetOpts{RowSort: &ntable.Sort{By: "name"}})
	require.NoError(t, err)
	for _, step := range []struct {
		change ntable.SetOpts
		want   string
	}{
		{ntable.SetOpts{RowSort: &ntable.Sort{By: "name", Keep: true}}, "changed"},
		{ntable.SetOpts{RowSort: &ntable.Sort{By: "name", Keep: true}}, "noop"},
		{ntable.SetOpts{RowSort: &ntable.Sort{Manual: true}}, "changed"},
		{ntable.SetOpts{RowSort: &ntable.Sort{Manual: true}}, "noop"},
		{ntable.SetOpts{ColMove: &ntable.Reorder{Item: "note", Place: at("first", "")}}, "changed"},
		{ntable.SetOpts{ColMove: &ntable.Reorder{Item: "note", Place: at("first", "")}}, "noop"},
		{ntable.SetOpts{Footer: new("total")}, "changed"},
	} {
		var receipt ntable.Receipt
		_, err = ntable.Set(ctx, c, "t", step.change, ntable.WriteOptions{Receipt: &receipt})
		require.NoError(t, err)
		require.Equal(t, step.want, receipt.Outcome, "definition edit %+v: receipt %+v, want %s", step.change, receipt, step.want)
		require.Equal(t, receipt.Before+1, receipt.After, "definition edit %+v: receipt %+v, want %s", step.change, receipt, step.want)
		events, err := c.XRevRangeN(ctx, "table:t:changes", "+", "-", 1).Result()
		require.NoError(t, err)
		require.Equal(t, step.want, events[0].Values["outcome"], "durable receipt: %v, want %s", events[0], step.want)
	}
}
