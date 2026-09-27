//go:build functional

package ntable_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	nsstore "github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
)

func largeOrderFixture(t *testing.T, n int) (*redis.Client, []string) {
	t.Helper()
	_, c := live(t)
	cols, err := ntable.ParseColumns("a")
	if err != nil {
		t.Fatal(err)
	}
	if err := ntable.Create(context.Background(), c, ntable.Table{Name: "large", Columns: cols}, now); err != nil {
		t.Fatal(err)
	}
	rows := make([]string, n)
	for i := range rows {
		rows[i] = fmt.Sprintf("r%05d", i)
	}
	if _, err := ntable.RowsAdd(context.Background(), c, "large", rows); err != nil {
		t.Fatal(err)
	}
	return c, rows
}

func TestLargeRowOrdersKeepRanksTripsAndReceipts(t *testing.T) {
	t.Parallel()
	for _, n := range []int{255, 256, 257, 4100} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			t.Parallel()
			c, rows := largeOrderFixture(t, n)
			ctx := context.Background()
			trips := nsstore.New(c).CountTrips()
			check := func(name string, want []string, write func(*ntable.Receipt) error) {
				t.Helper()
				events, err := c.XLen(ctx, ntable.ChangesKey("large")).Result()
				if err != nil {
					t.Fatal(err)
				}
				before := trips.N()
				var receipt ntable.Receipt
				if err := write(&receipt); err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				if got := trips.N() - before; got != 1 {
					t.Fatalf("%s took %d trips", name, got)
				}
				if receipt.After != receipt.Before+1 || receipt.Outcome != "changed" {
					t.Fatalf("%s receipt = %#v", name, receipt)
				}
				if got := c.XLen(ctx, ntable.ChangesKey("large")).Val(); got != events+1 {
					t.Fatalf("%s emitted %d events", name, got-events)
				}
				got, err := c.ZRangeWithScores(ctx, "table:large:rows", 0, -1).Result()
				if err != nil || len(got) != len(want) {
					t.Fatalf("%s: %d rows, want %d: %v", name, len(got), len(want), err)
				}
				for i, row := range got {
					if row.Member != want[i] || row.Score != float64(i+1) {
						t.Fatalf("%s row %d = %#v, want %s at rank %d", name, i, row, want[i], i+1)
					}
				}
				last, err := c.XRevRangeN(ctx, ntable.ChangesKey("large"), "+", "-", 1).Result()
				if err != nil || len(last) != 1 {
					t.Fatalf("%s last receipt: %v", name, err)
				}
				var cells []string
				if err := json.Unmarshal([]byte(last[0].Values["cells"].(string)), &cells); err != nil {
					t.Fatal(err)
				}
				wantCells := make([]string, len(want))
				for i, row := range want {
					wantCells[i] = row + ":a"
				}
				slices.Sort(wantCells)
				if !slices.Equal(cells, wantCells) {
					t.Fatalf("%s receipt misses ranked cells: got %d, want %d", name, len(cells), len(wantCells))
				}
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
	if err := c.Do(ctx, "ACL", "SETUSER", "rank-writer", "on", ">rank-test-only", "+@all", "~*", "-xadd").Err(); err != nil {
		t.Fatal(err)
	}
	writer := redis.NewClient(&redis.Options{Addr: c.Options().Addr, Username: "rank-writer", Password: "rank-test-only"})
	t.Cleanup(func() { _ = writer.Close() })
	before := memberStoreImage(t, c)
	_, err := ntable.Set(ctx, writer, "large", ntable.SetOpts{RowMove: &ntable.Reorder{Item: rows[len(rows)-1], Place: at("first", "")}})
	if err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Fatalf("rank write without receipt permission = %v; want NOPERM", err)
	}
	if !reflect.DeepEqual(before, memberStoreImage(t, c)) {
		t.Fatal("late receipt permission refusal wrote rank chunks or other state")
	}
}
