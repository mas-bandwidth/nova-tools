//go:build functional

package ntable_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// commandCalls is the server's count of the commands it has run, by name
// (INFO commandstats; a function's own redis.call counts as its command), so a
// test of what a call costs holds no clock.
func commandCalls(t *testing.T, c *redis.Client) map[string]int64 {
	t.Helper()
	info, err := c.Info(context.Background(), "commandstats").Result()
	require.NoError(t, err)
	out := map[string]int64{}
	for _, line := range strings.Split(info, "\n") {
		name, rest, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		name, ok = strings.CutPrefix(name, "cmdstat_")
		if !ok {
			continue
		}
		for _, kv := range strings.Split(rest, ",") {
			if v, ok := strings.CutPrefix(kv, "calls="); ok {
				n, err := strconv.ParseInt(v, 10, 64)
				require.NoError(t, err)
				out[name] = n
			}
		}
	}
	return out
}

// costTable is a table of rows x cols cells holding n placed members spread
// over the cells in turn, on its own throwaway store.
func costTable(t *testing.T, rows, cols, n int) *redis.Client {
	t.Helper()
	_, c := live(t)
	ctx := context.Background()
	names := make([]string, cols)
	for i := range names {
		names[i] = fmt.Sprintf("c%d", i)
	}
	cs, err := ntable.ParseColumns(strings.Join(names, ","))
	require.NoError(t, err)
	require.NoError(t, ntable.Create(ctx, c, ntable.Table{Name: "cost", Columns: cs}, now))
	for r := 0; r < rows; r++ {
		_, err := ntable.RowAdd(ctx, c, "cost", fmt.Sprintf("r%d", r), ntable.RowSpec{})
		require.NoError(t, err)
	}
	for i := 0; i < n; i++ {
		cell := i % (rows * cols)
		_, err := ntable.CellAdd(ctx, c, "cost", fmt.Sprintf("r%d", cell/cols), names[cell%cols], fmt.Sprintf("m%d", i), float64(i))
		require.NoError(t, err)
	}
	return c
}

// A read set of one id reads each cell of the table once, with one ZSCORE,
// and nothing else per cell: no ZCARD first (T.place_index_direct); one of
// eight ids does the same with one ZMSCORE. The reverse check still reads
// every cell, so the count grows with the cells and with nothing else: what it
// costs beyond the cells is the same at R x C x N and at 10R x 10C x 10N.
func TestAOneIDReadSetCostsOneCommandPerCell(t *testing.T) {
	t.Parallel()
	type size struct{ rows, cols, n int }
	var beyond []int64
	for _, sz := range []size{{4, 3, 24}, {40, 30, 240}} {
		c := costTable(t, sz.rows, sz.cols, sz.n)
		ctx := context.Background()
		cells := int64(sz.rows * sz.cols)
		for _, k := range []int{1, 8} {
			ids := make([]string, k)
			for i := range ids {
				ids[i] = fmt.Sprintf("m%d", i)
			}
			before := commandCalls(t, c)
			rs, err := ntable.ReadSetMembers(ctx, c, "cost", ids)
			after := commandCalls(t, c)
			require.NoError(t, err)
			require.Len(t, rs.Members, k)
			delta := func(name string) int64 { return after[name] - before[name] }
			var total int64
			for name := range after {
				if name != "info" {
					total += delta(name)
				}
			}
			t.Logf("%d rows x %d columns (%d cells), %d members: a %d-id read set ran %d commands (zcard %d, zrange %d, zscore %d, zmscore %d)",
				sz.rows, sz.cols, cells, sz.n, k, total, delta("zcard"), delta("zrange"), delta("zscore"), delta("zmscore"))
			require.Zero(t, delta("zcard"), "a %d-id read set asked a cell's size", k)
			require.Equal(t, int64(1), delta("zrange"), "a %d-id read set lists the rows and no cell", k)
			if k == 1 {
				// one ZSCORE per cell, with the recorded cell's row looked up
				// and the member scored there (T.cell, the read set)
				require.Zero(t, delta("zmscore"))
				require.Equal(t, cells+2, delta("zscore"), "a 1-id read set scores each cell once")
				beyond = append(beyond, total-cells)
			} else {
				require.Equal(t, cells, delta("zmscore"), "an %d-id read set scores each cell once", k)
				require.LessOrEqual(t, delta("zscore"), int64(2*k), "an %d-id read set looks up each recorded cell's row and scores the member there", k)
			}
		}
	}
	require.Equal(t, beyond[0], beyond[1], "what a 1-id read set costs beyond one command per cell grew with the table")
}

// The direct path (an id list up to T.place_index_direct) and the sized path
// (one longer) answer the same: the same member, and the same refusal for a
// second placement, for an owned set no record places an id in, and for a
// cell of the wrong type, whichever comes first in cell order.
func TestReadSetDirectAndSizedPathsAnswerTheSame(t *testing.T) {
	t.Parallel()
	const direct = 8 // T.place_index_direct in table.lua
	pad := func(ids ...string) []string {
		for i := 0; len(ids) <= direct; i++ {
			ids = append(ids, fmt.Sprintf("absent-%d", i))
		}
		return ids
	}
	ctx := context.Background()
	both := func(t *testing.T, c *redis.Client, id string) (ntable.ReadSetResult, error, ntable.ReadSetResult, error) {
		t.Helper()
		small, serr := ntable.ReadSetMembers(ctx, c, "cost", []string{id})
		large, lerr := ntable.ReadSetMembers(ctx, c, "cost", pad(id))
		return small, serr, large, lerr
	}
	sameRefusal := func(t *testing.T, what string, serr, lerr error, kind error) {
		t.Helper()
		require.Error(t, serr, "%s: the direct path accepted", what)
		require.ErrorIs(t, serr, kind, "%s: the direct path", what)
		require.Error(t, lerr, "%s: the sized path accepted", what)
		require.Equal(t, lerr.Error(), serr.Error(), "%s: the two paths refuse differently", what)
	}

	t.Run("a placed member", func(t *testing.T) {
		c := costTable(t, 3, 3, 20)
		require.NoError(t, c.HSet(ctx, ntable.MemberKey("m4"), "note", "x").Err())
		small, serr, large, lerr := both(t, c, "m4")
		require.NoError(t, serr)
		require.NoError(t, lerr)
		require.Len(t, small.Members, 1)
		got, ok := large.Member("m4")
		require.True(t, ok)
		require.Equal(t, got, small.Members[0])
		require.Equal(t, large.Revision, small.Revision)
	})
	t.Run("a missing id", func(t *testing.T) {
		c := costTable(t, 3, 3, 20)
		small, serr, _, lerr := both(t, c, "nobody")
		require.NoError(t, serr)
		require.NoError(t, lerr)
		require.Equal(t, []string{"nobody"}, small.Missing)
	})
	t.Run("a second placement", func(t *testing.T) {
		c := costTable(t, 3, 3, 20)
		require.NoError(t, c.ZAdd(ctx, ntable.CellKey("cost", "r2", "c2"), redis.Z{Score: 1, Member: "m0"}).Err())
		_, serr, _, lerr := both(t, c, "m0")
		sameRefusal(t, "a second placement", serr, lerr, ntable.ErrDrift)
	})
	t.Run("an owned set no record places an id in", func(t *testing.T) {
		c := costTable(t, 3, 3, 20)
		require.NoError(t, c.ZAdd(ctx, ntable.CellKey("cost", "r1", "c1"), redis.Z{Score: 1, Member: "ghost"}).Err())
		_, serr, _, lerr := both(t, c, "ghost")
		sameRefusal(t, "a set-only placement", serr, lerr, ntable.ErrDrift)
	})
	t.Run("a record whose cell does not hold it", func(t *testing.T) {
		c := costTable(t, 3, 3, 20)
		require.NoError(t, c.ZRem(ctx, ntable.CellKey("cost", "r0", "c0"), "m0").Err())
		_, serr, _, lerr := both(t, c, "m0")
		sameRefusal(t, "a record with no set entry", serr, lerr, ntable.ErrDrift)
	})
	t.Run("a cell of the wrong type", func(t *testing.T) {
		c := costTable(t, 3, 3, 20)
		require.NoError(t, c.Del(ctx, ntable.CellKey("cost", "r2", "c1")).Err())
		require.NoError(t, c.Set(ctx, ntable.CellKey("cost", "r2", "c1"), "s", 0).Err())
		_, serr, _, lerr := both(t, c, "m0")
		sameRefusal(t, "a wrong-type cell", serr, lerr, ntable.ErrWrongType)
	})
	t.Run("a wrong-type cell before a second placement", func(t *testing.T) {
		c := costTable(t, 3, 3, 20)
		require.NoError(t, c.Del(ctx, ntable.CellKey("cost", "r1", "c0")).Err())
		require.NoError(t, c.Set(ctx, ntable.CellKey("cost", "r1", "c0"), "s", 0).Err())
		require.NoError(t, c.ZAdd(ctx, ntable.CellKey("cost", "r2", "c2"), redis.Z{Score: 1, Member: "m0"}).Err())
		_, serr, _, lerr := both(t, c, "m0")
		sameRefusal(t, "a wrong-type cell before a drift", serr, lerr, ntable.ErrWrongType)
	})
}
