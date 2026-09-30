//go:build functional

package ntable_test

import (
	"context"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/stretchr/testify/require"
)

// TestNamedFormulasOnAStore: a table with a named share and a sum over hidden
// count columns is created, read back and rendered from a real store; the
// formulas hold no set; col add takes a formula over existing counts and
// refuses one over a missing column with the col add remedy; col del refuses
// a column a sum reads, naming the sum.
func TestNamedFormulasOnAStore(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()
	cols, err := ntable.ParseColumns("ready,ok,failed,done:sum(ok+failed),okpct:pct(ok/ok+failed):pooled:ok%")
	require.NoError(t, err)
	require.NoError(t, ntable.Create(ctx, c, ntable.Table{Name: "fleet", Columns: cols, FooterLabel: "total", Hidden: []string{"ok", "failed"}}, time.Now()))
	_, err = ntable.RowsAdd(ctx, c, "fleet", []string{"m1", "m2"})
	require.NoError(t, err)
	_, err = ntable.CellsAdd(ctx, c, "fleet", "m1", "ok", 1, []string{"a", "b", "c"})
	require.NoError(t, err)
	_, err = ntable.CellAdd(ctx, c, "fleet", "m1", "failed", "d", 1)
	require.NoError(t, err)
	tb, err := ntable.Read(ctx, c, "fleet")
	require.NoError(t, err)
	want := "fleet | ready | done | ok%\n" +
		"------+-------+------+------\n" +
		"m1    |     0 |    4 | 75.0%\n" +
		"m2    |     0 |    0 | 0.0%\n" +
		"------+-------+------+------\n" +
		"total |     0 |    4 | 75.0%\n"
	got := ntable.Render(tb, ntable.RenderOpts{Title: "fleet"})
	require.Equal(t, want, got, "read back:\n%s\nwant:\n%s", got, want)
	for _, key := range []string{ntable.CellKey("fleet", "m1", "done"), ntable.CellKey("fleet", "m1", "okpct")} {
		if n, err := c.Exists(ctx, key).Result(); err != nil || n != 0 {
			t.Fatalf("a formula cell has a set %s: %d %v", key, n, err)
		}
	}
	_, err = ntable.CellAdd(ctx, c, "fleet", "m1", "done", "e", 1)
	require.Error(t, err, "a member was added to a sum column")
	share, _ := ntable.ParseColumn("readypct:pct(ready/ready+ok+failed)")
	_, err = ntable.Set(ctx, c, "fleet", ntable.SetOpts{ColAdd: &share})
	require.NoError(t, err, "col add of a named share")
	bad, _ := ntable.ParseColumn("x:sum(ok+nope)")
	_, err = ntable.Set(ctx, c, "fleet", ntable.SetOpts{ColAdd: &bad})
	require.ErrorContains(t, err, `col add 'fleet' 'nope'`, "col add of a sum over a missing column")
	_, err = ntable.Set(ctx, c, "fleet", ntable.SetOpts{ColDel: "readypct"})
	require.NoError(t, err)
	_, err = ntable.Set(ctx, c, "fleet", ntable.SetOpts{ColDel: "ready"})
	require.NoError(t, err, "ready is read by no formula now")
	_, err = ntable.Set(ctx, c, "fleet", ntable.SetOpts{ColDel: "failed"})
	require.ErrorContains(t, err, `col del 'fleet' 'done'`, "col del of a column a sum reads")
}
