//go:build functional

package ntable_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// A replacement declaration must not silently erase stored text on an
// omitted row, just as it cannot erase that row's owned members.
func TestBindRefusesOmittedTextUntilCleared(t *testing.T) {
	t.Parallel()
	for _, standing := range []bool{false, true} {
		t.Run(fmt.Sprint(standing), func(t *testing.T) {
			t.Parallel()
			_, c := live(t)
			ctx := context.Background()
			orderTable(t, c)
			if standing {
				if _, err := ntable.Set(ctx, c, "t", ntable.SetOpts{RowSort: &ntable.Sort{By: "name", Keep: true}}); err != nil {
					t.Fatal(err)
				}
			}
			tab, err := ntable.Read(ctx, c, "t")
			if err != nil {
				t.Fatal(err)
			}
			rows := []ntable.Row{{Key: "fresh", Cells: make([]ntable.Cell, len(tab.Columns))}}
			for _, row := range tab.Rows {
				if row.Key != "m" {
					rows = append(rows, row)
				}
			}
			tab.Rows = rows
			before := storeImage(t, c)
			err = ntable.Bind(ctx, c, tab, now)
			if err == nil || !strings.Contains(err.Error(), "clear it with row set first") {
				t.Fatalf("Bind omitted row m holding note=alpha: %v", err)
			}
			if !reflect.DeepEqual(before, storeImage(t, c)) {
				t.Fatal("late text refusal changed the store")
			}
			if _, err := ntable.RowSet(ctx, c, "t", "m", map[string]string{"note": ""}); err != nil {
				t.Fatal(err)
			}
			heldBefore := held(t, c, "t")
			if err := ntable.Bind(ctx, c, tab, now); err != nil {
				t.Fatal(err)
			}
			after, _ := orderOf(t, c, "t")
			want := "fresh,z,c,k"
			if standing {
				want = "c,fresh,k,z"
			}
			if strings.Join(after, ",") != want {
				t.Fatalf("cleared text replacement: %v want %s", after, want)
			}
			if !reflect.DeepEqual(heldBefore, held(t, c, "t")) {
				t.Fatal("replacement lost retained cell contents")
			}
		})
	}
}
