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
	_, c := costStore(t, rows, cols, n)
	return c
}

// costStore is costTable with the store's address, for a second client.
func costStore(t *testing.T, rows, cols, n int) (string, *redis.Client) {
	t.Helper()
	addr, c := live(t)
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
	return addr, c
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

// The three ways T.place_index reads a cell answer the same: one id (ZSCORE
// per cell), two to T.place_index_direct ids (ZMSCORE per cell), and a longer
// list (ZCARD, then ZRANGE or ZMSCORE). A read set and a batch that writes are
// each run once per way, the id under test first and absent ids after it, and
// give the same member, the same refusal for a second placement, for an owned
// set no record places an id in, for a record whose cell does not hold it and
// for a cell of the wrong type, and rethrow an error that is not WRONGTYPE (a
// command the caller's ACL denies) rather than answer.
func TestReadSetDirectAndSizedPathsAnswerTheSame(t *testing.T) {
	t.Parallel()
	const direct = 8 // T.place_index_direct in table.lua
	ctx := context.Background()
	paths := []struct {
		name string
		n    int // ids in the call: the id under test and n-1 absent ones
		uses string
	}{{"one id", 1, "zscore"}, {"a few ids", direct, "zmscore"}, {"a long list", direct + 1, "zcard"}}
	ids := func(id string, n int) []string {
		out := []string{id}
		for i := 1; i < n; i++ {
			out = append(out, fmt.Sprintf("absent-%d", i))
		}
		return out
	}
	// a batch: the entry under test first, then a guard that each absent id
	// stays absent; an absent id is unplaced, so it joins the place index
	batch := func(c *redis.Client, e ntable.BatchMemberEntry, n int) error {
		m := ntable.BatchManifest{Schema: 1, Table: "cost", Epoch: "0", ExpectedTableRevision: c.HGet(ctx, ntable.DefKey("cost")+":revision", "n").Val(), OperationID: "paths", Actor: "p", Members: []ntable.BatchMemberEntry{e}}
		for _, id := range ids(e.ID, n)[1:] {
			m.Members = append(m.Members, ntable.BatchMemberEntry{ID: id, Expect: &ntable.MemberExpect{Absent: true}})
		}
		_, err := ntable.ApplyBatch(ctx, c, m)
		return err
	}
	set := func(id string) ntable.BatchMemberEntry {
		return ntable.BatchMemberEntry{ID: id, Expect: &ntable.MemberExpect{}, Set: map[string]string{"note": "y"}}
	}
	// refusals: each call, by read set and by batch, on each path, refuses
	// with the kind and with the same words as on the one-id path
	refuses := func(t *testing.T, c *redis.Client, id string, e ntable.BatchMemberEntry, kind error) {
		t.Helper()
		var first [2]string
		for i, p := range paths {
			_, rerr := ntable.ReadSetMembers(ctx, c, "cost", ids(id, p.n))
			berr := batch(c, e, p.n)
			for j, err := range []error{rerr, berr} {
				what := []string{"read set", "batch"}[j]
				require.Error(t, err, "%s, %s: accepted", what, p.name)
				require.ErrorIs(t, err, kind, "%s, %s", what, p.name)
				if i == 0 {
					first[j] = err.Error()
				} else {
					require.Equal(t, first[j], err.Error(), "%s, %s: refuses unlike the one-id path", what, p.name)
				}
			}
		}
	}

	t.Run("a placed member", func(t *testing.T) {
		c := costTable(t, 3, 3, 20)
		require.NoError(t, c.HSet(ctx, ntable.MemberKey("m4"), "note", "x").Err())
		var first ntable.ReadSetMember
		for i, p := range paths {
			rs, err := ntable.ReadSetMembers(ctx, c, "cost", ids("m4", p.n))
			require.NoError(t, err, p.name)
			got, ok := rs.Member("m4")
			require.True(t, ok, p.name)
			require.Len(t, rs.Missing, p.n-1, p.name)
			if i == 0 {
				first = got
			}
			require.Equal(t, first, got, p.name)
		}
	})
	t.Run("a placed member a batch writes", func(t *testing.T) {
		for _, p := range paths {
			c := costTable(t, 3, 3, 20)
			require.NoError(t, batch(c, set("m4"), p.n), p.name)
			require.Equal(t, "y", c.HGet(ctx, ntable.MemberKey("m4"), "note").Val(), p.name)
		}
	})
	t.Run("a missing id", func(t *testing.T) {
		c := costTable(t, 3, 3, 20)
		for _, p := range paths {
			rs, err := ntable.ReadSetMembers(ctx, c, "cost", ids("nobody", p.n))
			require.NoError(t, err, p.name)
			require.Equal(t, ids("nobody", p.n), rs.Missing, p.name)
		}
	})
	t.Run("a second placement, in the last cell", func(t *testing.T) {
		c := costTable(t, 3, 3, 20)
		require.NoError(t, c.ZAdd(ctx, ntable.CellKey("cost", "r2", "c2"), redis.Z{Score: 1, Member: "m0"}).Err())
		refuses(t, c, "m0", set("m0"), ntable.ErrDrift)
	})
	t.Run("a second placement, in a middle cell", func(t *testing.T) {
		c := costTable(t, 3, 3, 20)
		require.NoError(t, c.ZAdd(ctx, ntable.CellKey("cost", "r1", "c1"), redis.Z{Score: 1, Member: "m0"}).Err())
		refuses(t, c, "m0", set("m0"), ntable.ErrDrift)
	})
	t.Run("an owned set no record places an id in", func(t *testing.T) {
		c := costTable(t, 3, 3, 20)
		require.NoError(t, c.ZAdd(ctx, ntable.CellKey("cost", "r2", "c2"), redis.Z{Score: 1, Member: "ghost"}).Err())
		refuses(t, c, "ghost", ntable.BatchMemberEntry{ID: "ghost", Expect: &ntable.MemberExpect{Absent: true}, Create: &ntable.MemberCreateOp{Row: "r0", Col: "c0", Score: 1}}, ntable.ErrDrift)
	})
	t.Run("a record whose cell does not hold it", func(t *testing.T) {
		c := costTable(t, 3, 3, 20)
		require.NoError(t, c.ZRem(ctx, ntable.CellKey("cost", "r0", "c0"), "m0").Err())
		refuses(t, c, "m0", set("m0"), ntable.ErrDrift)
	})
	t.Run("a cell of the wrong type", func(t *testing.T) {
		c := costTable(t, 3, 3, 20)
		require.NoError(t, c.Del(ctx, ntable.CellKey("cost", "r2", "c2")).Err())
		require.NoError(t, c.Set(ctx, ntable.CellKey("cost", "r2", "c2"), "s", 0).Err())
		refuses(t, c, "m0", set("m0"), ntable.ErrWrongType)
	})
	t.Run("a wrong-type cell before a second placement", func(t *testing.T) {
		c := costTable(t, 3, 3, 20)
		require.NoError(t, c.Del(ctx, ntable.CellKey("cost", "r1", "c0")).Err())
		require.NoError(t, c.Set(ctx, ntable.CellKey("cost", "r1", "c0"), "s", 0).Err())
		require.NoError(t, c.ZAdd(ctx, ntable.CellKey("cost", "r2", "c2"), redis.Z{Score: 1, Member: "m0"}).Err())
		refuses(t, c, "m0", set("m0"), ntable.ErrWrongType)
	})
	t.Run("an error that is not WRONGTYPE is rethrown", func(t *testing.T) {
		for _, p := range paths {
			addr, c := costStore(t, 3, 3, 20)
			// a caller whose ACL denies the command this path reads a cell with
			require.NoError(t, c.Do(ctx, "ACL", "SETUSER", "denied", "on", "nopass", "~*", "&*", "+@all", "-"+p.uses).Err(), p.name)
			dc := redis.NewClient(&redis.Options{Addr: addr, Username: "denied", Password: "unused"})
			t.Cleanup(func() { _ = dc.Close() })
			_, rerr := ntable.ReadSetMembers(ctx, dc, "cost", ids("nobody", p.n))
			berr := batch(dc, ntable.BatchMemberEntry{ID: "nobody", Expect: &ntable.MemberExpect{Absent: true}}, p.n)
			for j, err := range []error{rerr, berr} {
				what := []string{"read set", "batch"}[j]
				require.Error(t, err, "%s, %s: answered past a denied %s", what, p.name, p.uses)
				require.False(t, ntable.IsRefusal(err), "%s, %s: a denied %s became a refusal: %v", what, p.name, p.uses, err)
				require.Contains(t, strings.ToLower(err.Error()), p.uses, "%s, %s: %v", what, p.name, err)
			}
		}
	})
}
