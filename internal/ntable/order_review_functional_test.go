//go:build functional

package ntable_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
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
			if _, err := ntable.RowsAdd(ctx, c, "t", []string{"unused"}); err != nil {
				t.Fatal(err)
			}
			if _, err := ntable.Set(ctx, c, "t", ntable.SetOpts{RowSort: &ntable.Sort{By: by, Keep: true}}); err != nil {
				t.Fatal(err)
			}
			before := held(t, c, "t")
			tab, err := ntable.Read(ctx, c, "t")
			if err != nil {
				t.Fatal(err)
			}
			// Reversing all input rows must not replace a standing sort with input
			// order, even when the desired order equals the existing stored order.
			for l, r := 0, len(tab.Rows)-1; l < r; l, r = l+1, r-1 {
				tab.Rows[l], tab.Rows[r] = tab.Rows[r], tab.Rows[l]
			}
			var receipt ntable.Receipt
			if err := ntable.Bind(ctx, c, tab, now, ntable.WriteOptions{Receipt: &receipt}); err != nil {
				t.Fatal(err)
			}
			rows, _ := orderOf(t, c, "t")
			if strings.Join(rows, ",") != "c,k,m,unused,z" {
				t.Fatalf("standing sort violated after reversed Bind: %v", rows)
			}
			if receipt.Before != tab.Revision || receipt.After != tab.Revision+1 {
				t.Fatalf("Bind receipt: %+v", receipt)
			}
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
			if err := ntable.Bind(ctx, c, tab, now); err != nil {
				t.Fatal(err)
			}
			rows, _ = orderOf(t, c, "t")
			want := "b,c,k,m,z"
			if by == "label" {
				want = "z,b,c,k,m"
			}
			if strings.Join(rows, ",") != want {
				t.Fatalf("standing %s sort after replacement: %v want %s", by, rows, want)
			}
			if after := held(t, c, "t"); !reflect.DeepEqual(before, after) {
				t.Fatalf("Bind changed cell contents: %v -> %v", before, after)
			}
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
			change := ntable.SetOpts{RowSort: &ntable.Sort{By: "name", Keep: true}, Footer: ptr("should not write")}
			if verb == "move" {
				change.RowMove = &ntable.Reorder{Item: "z", Place: at("first", "")}
			} else {
				change.RowOrder = []string{"z"}
			}
			before := storeImage(t, c)
			_, err := ntable.Set(ctx, c, "t", change)
			if err == nil || !strings.Contains(err.Error(), "--manual") {
				t.Fatalf("standing sort plus manual %s: %v", verb, err)
			}
			if !reflect.DeepEqual(before, storeImage(t, c)) {
				t.Fatal("refused combined edit wrote")
			}
			// Clearing a standing sort and moving in the same call remains valid.
			if _, err := ntable.Set(ctx, c, "t", ntable.SetOpts{RowSort: change.RowSort}); err != nil {
				t.Fatal(err)
			}
			change.RowSort = &ntable.Sort{Manual: true}
			if _, err := ntable.Set(ctx, c, "t", change); err != nil {
				t.Fatal(err)
			}
			rows, _ := orderOf(t, c, "t")
			if rows[0] != "z" {
				t.Fatalf("manual edit after clearing standing sort: %v", rows)
			}
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
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ntable.Set(ctx, c, "t", ntable.SetOpts{ColAdd: &col}); err != nil {
				t.Fatal(err)
			}
			before := storeImage(t, c)
			_, err = ntable.Set(ctx, c, "t", ntable.SetOpts{RowSort: &ntable.Sort{By: "names"}})
			if err == nil || !strings.Contains(err.Error(), "a count column or a text column") {
				t.Fatalf("unsupported projection sort: %v", err)
			}
			if !reflect.DeepEqual(before, storeImage(t, c)) {
				t.Fatal("refused projection sort wrote")
			}
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
			if err := c.HSet(ctx, ntable.RowKey("t", "c"), "key:fresh", "outside", "text:fresh", "zz").Err(); err != nil {
				t.Fatal(err)
			}
			if err := c.Set(ctx, "outside", "not a set", 0).Err(); err != nil {
				t.Fatal(err)
			}
			col, err := ntable.ParseColumn("fresh:" + projection)
			if err != nil {
				t.Fatal(err)
			}
			var receipt ntable.Receipt
			if _, err := ntable.Set(ctx, c, "t", ntable.SetOpts{ColAdd: &col, RowSort: &ntable.Sort{By: "fresh"}}, ntable.WriteOptions{Receipt: &receipt}); err != nil {
				t.Fatalf("combined add and sort: %v", err)
			}
			rows, _ := orderOf(t, c, "t")
			if strings.Join(rows, ",") != "c,k,m,z" {
				t.Fatalf("sort used discarded metadata: %v", rows)
			}
			if receipt.After != receipt.Before+1 {
				t.Fatalf("combined edit receipt: %+v", receipt)
			}
			before := storeImage(t, c)
			_, err = ntable.Set(ctx, c, "t", ntable.SetOpts{ColDel: "fresh", RowSort: &ntable.Sort{By: "fresh"}})
			if err == nil || !strings.Contains(err.Error(), "no such column") {
				t.Fatalf("sort accepted removed column: %v", err)
			}
			if !reflect.DeepEqual(before, storeImage(t, c)) {
				t.Fatal("refused removed-column sort wrote")
			}
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
			if _, err := ntable.Set(ctx, c, "t", change); err != nil {
				t.Fatal(err)
			}
			events, err := c.XRevRangeN(ctx, "table:t:changes", "+", "-", 1).Result()
			if err != nil {
				t.Fatal(err)
			}
			var cells []string
			if err := json.Unmarshal([]byte(events[0].Values["cells"].(string)), &cells); err != nil {
				t.Fatal(err)
			}
			want := []string{"c:a", "c:b", "c:note", "c:p", "k:a", "k:b", "k:note", "k:p", "m:a", "m:b", "m:note", "m:p", "z:a", "z:b", "z:note", "z:p"}
			if !reflect.DeepEqual(cells, want) {
				t.Fatalf("ordering receipt does not name all affected row cells: %v", cells)
			}
		})
	}
}
