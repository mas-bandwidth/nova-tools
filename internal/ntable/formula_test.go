package ntable_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shareTable is a fleet-shaped table: ready drawn, ok and failed hidden
// counts, done the sum of the two and ok% the share of ok in them.
func shareTable(t *testing.T, spec string, rows map[string][]int64, order []string) ntable.Table {
	t.Helper()
	cols, err := ntable.ParseColumns(spec)
	require.NoError(t, err)
	tb := ntable.Table{Name: "fleet", Columns: cols, FooterLabel: "total", Hidden: []string{"ok", "failed"}}
	for _, key := range order {
		r := ntable.NewRow(tb, key)
		for j, n := range rows[key] {
			r.Cells[j].Count = n
		}
		tb.Rows = append(tb.Rows, r)
	}
	return tb
}

const shareSpec = "ready,ok,failed,done:sum(ok+failed),okpct:pct(ok/ok+failed):pooled:ok%"

// TestNamedShareAndSum: pct(<col>/<a>+<b>) divides by the named counts of the
// row only (ready is not in it), a zero denominator prints the known-empty
// 0.0%, sum(<a>+<b>) prints the named counts added, right-aligned as a count,
// and the footer pools the numerators over the denominators: 4 of 5 is 80.0%,
// never the mean of 75.0, 0.0 and 100.0.
func TestNamedShareAndSum(t *testing.T) {
	t.Parallel()
	tb := shareTable(t, shareSpec, map[string][]int64{
		"m1": {2, 3, 1},
		"m2": {0, 0, 0},
		"m3": {5, 1, 0},
	}, []string{"m1", "m2", "m3"})
	want := "fleet | ready | done | ok%\n" +
		"------+-------+------+-------\n" +
		"m1    |     2 |    4 | 75.0%\n" +
		"m2    |     0 |    0 | 0.0%\n" +
		"m3    |     5 |    1 | 100.0%\n" +
		"------+-------+------+-------\n" +
		"total |     7 |    5 | 80.0%\n"
	got := ntable.Render(tb, ntable.RenderOpts{Title: "fleet"})
	require.Equal(t, want, got, "named share and sum:\n%s\nwant:\n%s", got, want)
	done, okpct := tb.Column("done"), tb.Column("okpct")
	got = ntable.CellText(tb.Columns, tb.Rows[0], done)
	require.Equal(t, "4", got, "done cell text %q", got)
	got = ntable.CellText(tb.Columns, tb.Rows[1], okpct)
	require.Equal(t, "0.0%", got, "zero denominator %q, want the known-empty 0.0%%", got)
}

// TestNamedShareFixedWidths: a fixed width pads a sum column on the left, as
// a count, and a percentage on the right, as before.
func TestNamedShareFixedWidths(t *testing.T) {
	t.Parallel()
	tb := shareTable(t, shareSpec, map[string][]int64{"m1": {2, 3, 1}}, []string{"m1"})
	got := ntable.Render(tb, ntable.RenderOpts{Title: "fleet", Widths: map[string]int{"done": 6, "okpct": 8}})
	want := "fleet | ready |   done | ok%\n" +
		"------+-------+--------+---------\n" +
		"m1    |     2 |      4 | 75.0%\n" +
		"------+-------+--------+---------\n" +
		"total |     2 |      4 | 75.0%\n"
	require.Equal(t, want, got, "fixed widths:\n%s\nwant:\n%s", got, want)
}

// TestPctOfOneColumnKeepsItsMeaning: pct(<col>) still divides by every count
// column of the row, hidden ones included; a sum column is not a count column
// and does not enter it.
func TestPctOfOneColumnKeepsItsMeaning(t *testing.T) {
	t.Parallel()
	tb := shareTable(t, shareSpec+",all:pct(ok)", map[string][]int64{"m1": {2, 3, 1}}, []string{"m1"})
	got := ntable.CellText(tb.Columns, tb.Rows[0], tb.Column("all"))
	require.Equal(t, "50.0%", got, "pct(ok) over ready+ok+failed = %q, want 50.0%%", got)
}

// TestBarePctDeadSourceIsUnknown: a bare pct keeps its all-count denominator,
// but its numerator must still be a count column of this shape. A stale
// formula after a source is deleted, renamed or retyped cannot turn into 0%.
func TestBarePctDeadSourceIsUnknown(t *testing.T) {
	t.Parallel()
	newTable := func(t *testing.T) ntable.Table {
		t.Helper()
		cols, err := ntable.ParseColumns("ok,other,extra,bare:pct(ok):pooled,named:pct(ok/ok+other):pooled,total:sum(ok+other)")
		require.NoError(t, err)
		tb := ntable.Table{Name: "fleet", Columns: cols, FooterLabel: "total"}
		for _, key := range []string{"m1", "m2"} {
			tb.Rows = append(tb.Rows, ntable.NewRow(tb, key))
		}
		tb.Rows[0].Cells[0].Count = 2
		tb.Rows[0].Cells[1].Count = 3
		tb.Rows[0].Cells[2].Count = 5 // an unrelated count is in bare pct's denominator
		return tb
	}
	cell := func(t *testing.T, rendered, row string, column int) string {
		t.Helper()
		for _, line := range strings.Split(rendered, "\n") {
			parts := strings.Split(line, "|")
			if len(parts) > column && strings.TrimSpace(parts[0]) == row {
				return strings.TrimSpace(parts[column])
			}
		}
		t.Fatalf("row %q missing from render:\n%s", row, rendered)
		return ""
	}
	tb := newTable(t)
	valid := ntable.Render(tb, ntable.RenderOpts{Title: "fleet"})
	for row, want := range map[string][]string{
		"m1":    {"20.0%", "40.0%", "5"},
		"m2":    {"0.0%", "0.0%", "0"},
		"total": {"20.0%", "40.0%", "5"},
	} {
		for i, v := range want {
			got := cell(t, valid, row, len(tb.Columns)-2+i)
			assert.Equal(t, v, got, "valid %s formula %d = %q, want %q", row, i, got, v)
		}
	}
	for _, tc := range []struct {
		name   string
		damage func(*ntable.Table)
	}{
		{"deleted", func(tb *ntable.Table) {
			tb.Columns = tb.Columns[1:]
			for i := range tb.Rows {
				tb.Rows[i].Cells = tb.Rows[i].Cells[1:]
			}
		}},
		{"renamed", func(tb *ntable.Table) { tb.Columns[0].Name = "renamed" }},
		{"text", func(tb *ntable.Table) {
			tb.Columns[0].Projection, tb.Columns[0].Fold = ntable.Text, ntable.None
		}},
		{"members", func(tb *ntable.Table) {
			tb.Columns[0].Projection, tb.Columns[0].Fold = ntable.Members, ntable.None
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tb := newTable(t)
			tc.damage(&tb)
			got := ntable.Render(tb, ntable.RenderOpts{Title: "fleet"})
			for _, row := range []string{"m1", "m2", "total"} {
				for i := range 3 {
					v := cell(t, got, row, len(tb.Columns)-2+i)
					assert.Equal(t, "?", v, "%s %s formula %d = %q, want ?\n%s", tc.name, row, i, v, got)
				}
			}
		})
	}
}

// TestNamedShareUnread: a cell a formula reads that did not come back prints
// ?, in the body and in the fold; a count the formula does not name (ready)
// leaves it alone.
func TestNamedShareUnread(t *testing.T) {
	t.Parallel()
	tb := shareTable(t, shareSpec, map[string][]int64{"m1": {2, 3, 1}, "m2": {1, 1, 1}}, []string{"m1", "m2"})
	tb.Rows[0].Cells[tb.Column("ready")].Unread = true
	got := ntable.Render(tb, ntable.RenderOpts{Title: "fleet"})
	require.Contains(t, got, "m1    |     ? |    4 | 75.0%\n", "an unread count the formulas do not read:\n%s", got)
	require.Contains(t, got, "total |     ? |    6 | 66.7%\n", "an unread count the formulas do not read:\n%s", got)
	tb.Rows[0].Cells[tb.Column("ready")].Unread = false
	tb.Rows[0].Cells[tb.Column("failed")].Unread = true
	got = ntable.Render(tb, ntable.RenderOpts{Title: "fleet"})
	require.Contains(t, got, "m1    |     2 |    ? | ?\n", "an unread input:\n%s", got)
	require.Contains(t, got, "total |     3 |    ? | ?\n", "an unread input:\n%s", got)
	require.Contains(t, got, "m2    |     1 |    2 | 50.0%\n", "a read row beside an unread one:\n%s", got)
}

// TestNamedFormulaFolds: a sum column folds sum (the default), max or avg;
// avg over a named percentage is refused like any percentage.
func TestNamedFormulaFolds(t *testing.T) {
	t.Parallel()
	for spec, want := range map[string]string{
		"ok,failed,done:sum(ok+failed)":     "total |  4 |      1 |    5\n",
		"ok,failed,done:sum(ok+failed):max": "total |  4 |      1 |    4\n",
		"ok,failed,done:sum(ok+failed):avg": "total |  4 |      1 |  2.5\n",
	} {
		cols, err := ntable.ParseColumns(spec)
		require.NoError(t, err)
		tb := ntable.Table{Name: "t", Columns: cols, FooterLabel: "total"}
		for _, n := range [][]int64{{3, 1}, {1, 0}} {
			r := ntable.NewRow(tb, "r")
			r.Cells[0].Count, r.Cells[1].Count = n[0], n[1]
			tb.Rows = append(tb.Rows, r)
		}
		got := ntable.Render(tb, ntable.RenderOpts{})
		require.True(t, strings.HasSuffix(got, want), "%s:\n%s\nwant the footer %q", spec, got, want)
	}
	c, err := ntable.ParseColumn("done:sum(ok+failed)")
	require.NoError(t, err)
	require.Equal(t, ntable.Sum, c.Fold, "the default fold of a sum column: %+v", c)
	c, err = ntable.ParseColumn("okpct:pct(ok/ok+failed)")
	require.NoError(t, err)
	require.Equal(t, ntable.Pooled, c.Fold, "the default fold of a named percentage: %+v", c)
}

// TestFormulaRefusals: every named column must be a count column of the same
// table; the grammar is refused before the table is looked at.
func TestFormulaRefusals(t *testing.T) {
	t.Parallel()
	for spec, want := range map[string]string{
		"ok,okpct:pct(ok/ok+failed)":                   "wants a count column named failed in the same table; declare failed as a count column",
		"ok,done:sum(ok+failed)":                       "wants a count column named failed",
		"failed,okpct:pct(ok/ok+failed)":               "wants a count column named ok",
		"ok,note:text,okpct:pct(ok/ok+note)":           "reads note, a text column; a formula reads count columns only",
		"ok,note:text,done:sum(ok+note)":               "reads note, a text column",
		"ok,who:members,done:sum(ok+who)":              "reads who, a members column",
		"ok,failed,done:sum(ok+failed),p:pct(ok/done)": "reads done, a sum(ok+failed) column",
		"ok,failed,p:pct(ok/)":                         `names "", which is not a column name`,
		"ok,failed,p:pct(/ok)":                         "wants a count column before the /",
		"ok,failed,p:pct(ok/ok+ok)":                    "names ok twice",
		"ok,failed,done:sum()":                         `names "", which is not a column name`,
		"ok,failed,done:sum(ok++failed)":               `names "", which is not a column name`,
		"ok,failed,done:sum(ok/failed)":                "not a column name",
		"ok,failed,p:pct(ok/ok+failed):avg":            "not accurate",
		"ok,failed,done:sum(ok+failed):pooled":         "folds pooled, which wants a pct column",
		"ok,failed,done:sum(ok+failed):union":          "folds union",
		"ok,failed,p:pct(ok/ok+failed):sum":            "folds sum, which wants the count projection or sum(...)",
		"ok,failed,p:ratio(ok/failed)":                 "wants a projection of count, members, first, last, text, pct(",
	} {
		_, err := ntable.ParseColumns(spec)
		assert.ErrorContains(t, err, want, "%s: %v, want %q", spec, err, want)
	}
	// the numerator need not be in the denominator
	_, err := ntable.ParseColumns("ok,failed,okpct:pct(ok/failed)")
	require.NoError(t, err, "a numerator outside its denominator is allowed")
}

// TestParseFormula: the three forms read into their parts.
func TestParseFormula(t *testing.T) {
	t.Parallel()
	for proj, want := range map[string]ntable.Formula{
		"pct(ok)":           {Part: "ok"},
		"pct(ok/ok+failed)": {Part: "ok", Over: []string{"ok", "failed"}},
		"pct(a.b/c-d+e_f)":  {Part: "a.b", Over: []string{"c-d", "e_f"}},
		"sum(ok+failed)":    {Sum: true, Over: []string{"ok", "failed"}},
		"sum(ok)":           {Sum: true, Over: []string{"ok"}},
	} {
		got, err := ntable.ParseFormula(proj)
		assert.NoError(t, err, proj)
		assert.Equal(t, want, got, proj)
	}
	f, _ := ntable.ParseFormula("pct(ok/ok+failed)")
	got := f.Inputs()
	require.Equal(t, []string{"ok", "failed"}, got, "inputs %v", got)
	require.False(t, ntable.IsSum("pct(ok)"), "IsSum/IsPct")
	require.True(t, ntable.IsSum("sum(ok)"), "IsSum/IsPct")
	require.True(t, ntable.IsPct("pct(ok/ok)"), "IsSum/IsPct")
	require.False(t, ntable.IsPct("sum(ok)"), "IsSum/IsPct")
	require.False(t, (ntable.Column{Projection: "sum(ok)"}).HasSet(), "a formula column holds no set")
	require.False(t, (ntable.Column{Projection: "pct(ok/ok)"}).HasSet(), "a formula column holds no set")
}
