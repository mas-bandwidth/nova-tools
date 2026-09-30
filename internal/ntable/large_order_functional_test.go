//go:build functional

package ntable_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	nsstore "github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func largeOrderFixture(t *testing.T, n int) (*redis.Client, []string) {
	t.Helper()
	_, c := live(t)
	cols, err := ntable.ParseColumns("a")
	require.NoError(t, err)
	require.NoError(t, ntable.Create(context.Background(), c, ntable.Table{Name: "large", Columns: cols}, now))
	rows := make([]string, n)
	for i := range rows {
		rows[i] = fmt.Sprintf("r%05d", i)
	}
	_, err = ntable.RowsAdd(context.Background(), c, "large", rows)
	require.NoError(t, err)
	return c, rows
}

func TestLargeRowOrdersKeepRanksTripsAndReceipts(t *testing.T) {
	t.Parallel()
	for _, n := range []int{255, 256, 257, 4100, 4500, 5000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			t.Parallel()
			c, rows := largeOrderFixture(t, n)
			ctx := context.Background()
			trips := nsstore.New(c).CountTrips()
			check := func(name string, want []string, write func(*ntable.Receipt) error) {
				t.Helper()
				events, err := c.XLen(ctx, ntable.ChangesKey("large")).Result()
				require.NoError(t, err)
				before := trips.N()
				var receipt ntable.Receipt
				err = write(&receipt)
				require.NoError(t, err, "%s: %v", name, err)
				if got := trips.N() - before; got != 1 {
					t.Fatalf("%s took %d trips", name, got)
				}
				require.Equal(t, receipt.Before+1, receipt.After, "%s receipt = %#v", name, receipt)
				require.Equal(t, "changed", receipt.Outcome, "%s receipt = %#v", name, receipt)
				if got := c.XLen(ctx, ntable.ChangesKey("large")).Val(); got != events+1 {
					t.Fatalf("%s emitted %d events", name, got-events)
				}
				got, err := c.ZRangeWithScores(ctx, "table:large:rows", 0, -1).Result()
				require.NoError(t, err, "%s: %d rows, want %d: %v", name, len(got), len(want), err)
				require.Len(t, got, len(want), "%s: %d rows, want %d: %v", name, len(got), len(want), err)
				for i, row := range got {
					if row.Member != want[i] || row.Score != float64(i+1) {
						t.Fatalf("%s row %d = %#v, want %s at rank %d", name, i, row, want[i], i+1)
					}
				}
				last, err := c.XRevRangeN(ctx, ntable.ChangesKey("large"), "+", "-", 1).Result()
				require.NoError(t, err, "%s last receipt: %v", name, err)
				require.Len(t, last, 1, "%s last receipt: %v", name, err)
				var cells []string
				require.NoError(t, json.Unmarshal([]byte(last[0].Values["cells"].(string)), &cells))
				wantCells := make([]string, len(want))
				for i, row := range want {
					wantCells[i] = row + ":a"
				}
				slices.Sort(wantCells)
				require.True(t, slices.Equal(cells, wantCells), "%s receipt misses ranked cells: got %d, want %d", name, len(cells), len(wantCells))
			}
			set := func(change ntable.SetOpts) func(*ntable.Receipt) error {
				return func(r *ntable.Receipt) error {
					_, err := ntable.Set(ctx, c, "large", change, ntable.WriteOptions{Receipt: r})
					return err
				}
			}
			moved := append([]string{rows[n-1]}, rows[:n-1]...)
			check("move", moved, set(ntable.SetOpts{RowMove: &ntable.Reorder{Item: rows[n-1], Place: at("first", "")}}))
			partial := append([]string{rows[n-2], rows[n-1]}, rows[:n-2]...)
			check("partial order", partial, set(ntable.SetOpts{RowOrder: []string{rows[n-2], rows[n-1]}}))
			check("sort", rows, set(ntable.SetOpts{RowSort: &ntable.Sort{By: "name"}}))
			desc := slices.Clone(rows)
			slices.Reverse(desc)
			check("standing sort", desc, set(ntable.SetOpts{RowSort: &ntable.Sort{By: "name", Desc: true, Keep: true}}))
			grown := append([]string{"zz-last"}, desc...)
			grown = append(grown, "aa-first")
			check("add into standing sort", grown, func(r *ntable.Receipt) error {
				_, err := ntable.RowsAdd(ctx, c, "large", []string{"aa-first", "zz-last"}, ntable.WriteOptions{Receipt: r})
				return err
			})
		})
	}
}

func TestLargeRankChunksAllPreflightBeforeWrites(t *testing.T) {
	t.Parallel()
	c, rows := largeOrderFixture(t, 4100)
	ctx := context.Background()
	require.NoError(t, c.Do(ctx, "ACL", "SETUSER", "rank-writer", "on", ">rank-test-only", "+@all", "~*", "-xadd").Err())
	writer := redis.NewClient(&redis.Options{Addr: c.Options().Addr, Username: "rank-writer", Password: "rank-test-only"})
	t.Cleanup(func() { _ = writer.Close() })
	before := storeImage(t, c)
	_, err := ntable.Set(ctx, writer, "large", ntable.SetOpts{RowMove: &ntable.Reorder{Item: rows[len(rows)-1], Place: at("first", "")}})
	require.ErrorContains(t, err, "NOPERM", "rank write without receipt permission = %v; want NOPERM", err)
	require.Equal(t, before, storeImage(t, c), "late receipt permission refusal wrote rank chunks or other state")
}
