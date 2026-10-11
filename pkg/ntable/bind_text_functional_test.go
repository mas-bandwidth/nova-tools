//go:build functional

package ntable_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/stretchr/testify/require"
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
				_, err := ntable.Set(ctx, c, "t", ntable.SetOpts{RowSort: &ntable.Sort{By: "name", Keep: true}})
				require.NoError(t, err)
			}
			tab, err := ntable.Read(ctx, c, "t")
			require.NoError(t, err)
			rows := []ntable.Row{{Key: "fresh", Cells: make([]ntable.Cell, len(tab.Columns))}}
			for _, row := range tab.Rows {
				if row.Key != "m" {
					rows = append(rows, row)
				}
			}
			tab.Rows = rows
			before := storeImage(t, c)
			err = ntable.Bind(ctx, c, tab, now)
			require.ErrorContains(t, err, "clear it with row set first", "Bind omitted row m holding note=alpha")
			require.Equal(t, before, storeImage(t, c), "late text refusal changed the store")
			_, err = ntable.RowSet(ctx, c, "t", "m", map[string]string{"note": ""})
			require.NoError(t, err)
			heldBefore := held(t, c, "t")
			require.NoError(t, ntable.Bind(ctx, c, tab, now))
			after, _ := orderOf(t, c, "t")
			want := "fresh,z,c,k"
			if standing {
				want = "c,fresh,k,z"
			}
			require.Equal(t, want, strings.Join(after, ","), "cleared text replacement: %v want %s", after, want)
			require.Equal(t, held(t, c, "t"), heldBefore, "replacement lost retained cell contents")
		})
	}
}
