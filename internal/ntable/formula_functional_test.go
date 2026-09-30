//go:build functional

package ntable_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
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
	if err != nil {
		t.Fatal(err)
	}
	if err := ntable.Create(ctx, c, ntable.Table{Name: "fleet", Columns: cols, FooterLabel: "total", Hidden: []string{"ok", "failed"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowsAdd(ctx, c, "fleet", []string{"m1", "m2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.CellsAdd(ctx, c, "fleet", "m1", "ok", 1, []string{"a", "b", "c"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.CellAdd(ctx, c, "fleet", "m1", "failed", "d", 1); err != nil {
		t.Fatal(err)
	}
	tb, err := ntable.Read(ctx, c, "fleet")
	if err != nil {
		t.Fatal(err)
	}
	want := "fleet | ready | done | ok%\n" +
		"------+-------+------+------\n" +
		"m1    |     0 |    4 | 75.0%\n" +
		"m2    |     0 |    0 | 0.0%\n" +
		"------+-------+------+------\n" +
		"total |     0 |    4 | 75.0%\n"
	if got := ntable.Render(tb, ntable.RenderOpts{Title: "fleet"}); got != want {
		t.Fatalf("read back:\n%s\nwant:\n%s", got, want)
	}
	for _, key := range []string{ntable.CellKey("fleet", "m1", "done"), ntable.CellKey("fleet", "m1", "okpct")} {
		if n, err := c.Exists(ctx, key).Result(); err != nil || n != 0 {
			t.Fatalf("a formula cell has a set %s: %d %v", key, n, err)
		}
	}
	if _, err := ntable.CellAdd(ctx, c, "fleet", "m1", "done", "e", 1); err == nil {
		t.Fatal("a member was added to a sum column")
	}
	share, _ := ntable.ParseColumn("readypct:pct(ready/ready+ok+failed)")
	if _, err := ntable.Set(ctx, c, "fleet", ntable.SetOpts{ColAdd: &share}); err != nil {
		t.Fatalf("col add of a named share: %v", err)
	}
	bad, _ := ntable.ParseColumn("x:sum(ok+nope)")
	if _, err := ntable.Set(ctx, c, "fleet", ntable.SetOpts{ColAdd: &bad}); err == nil || !strings.Contains(err.Error(), `col add 'fleet' 'nope'`) {
		t.Fatalf("col add of a sum over a missing column: %v", err)
	}
	if _, err := ntable.Set(ctx, c, "fleet", ntable.SetOpts{ColDel: "readypct"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.Set(ctx, c, "fleet", ntable.SetOpts{ColDel: "ready"}); err != nil {
		t.Fatalf("ready is read by no formula now: %v", err)
	}
	if _, err := ntable.Set(ctx, c, "fleet", ntable.SetOpts{ColDel: "failed"}); err == nil || !strings.Contains(err.Error(), `col del 'fleet' 'done'`) {
		t.Fatalf("col del of a column a sum reads: %v", err)
	}
}
