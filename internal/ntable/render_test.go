package ntable_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// counts builds a count-only table of the given rows for the render tests.
func counts(cols []string, rows map[string][]int64, order []string) ntable.Table {
	t := ntable.Table{Name: "t"}
	for _, c := range cols {
		t.Columns = append(t.Columns, ntable.Column{Name: c, Projection: ntable.Count, Fold: ntable.Sum})
	}
	for _, key := range order {
		r := ntable.NewRow(t, key)
		for j, n := range rows[key] {
			r.Cells[j].Count = n
		}
		t.Rows = append(t.Rows, r)
	}
	return t
}

// TestRenderEmptyTableShowsItsHeaderAndFooter: a table with no row, and a
// table whose rows are all zero, render with their header and footer (the
// owner's ruling, 2026-09-30: tables and rows always show, empty or not).
func TestRenderEmptyTableShowsItsHeaderAndFooter(t *testing.T) {
	t.Parallel()

	empty := counts([]string{"a"}, nil, nil)
	want := "row | a\n" +
		"----+--\n" +
		"----+--\n" +
		"    | 0\n"
	got := ntable.Render(empty, ntable.RenderOpts{})
	require.Equal(t, want, got, "empty table rendered:\n%s\nwant:\n%s", got, want)
	zeros := counts([]string{"a", "b"}, map[string][]int64{"x": {0, 0}, "y": {0, 0}}, []string{"x", "y"})
	want = "row | a | b\n" +
		"----+---+--\n" +
		"x   | 0 | 0\n" +
		"y   | 0 | 0\n" +
		"----+---+--\n" +
		"    | 0 | 0\n"
	got = ntable.Render(zeros, ntable.RenderOpts{})
	require.Equal(t, want, got, "zero rows shown:\n%s\nwant:\n%s", got, want)
}

// TestRenderOneRowAndTotal: the header, the rule, the row, the rule and the
// footer, count cells right-aligned, the text cell left-aligned, the last
// column padded to its width.
func TestRenderOneRowAndTotal(t *testing.T) {
	t.Parallel()

	one := counts([]string{"waiting", "ready"}, map[string][]int64{"swarm: cards": {150, 5}}, []string{"swarm: cards"})
	want := "row          | waiting | ready\n" +
		"-------------+---------+------\n" +
		"swarm: cards |     150 |     5\n" +
		"-------------+---------+------\n" +
		"             |     150 |     5\n"
	got := ntable.Render(one, ntable.RenderOpts{})
	require.Equal(t, want, got, "one row:\n%s\nwant:\n%s", got, want)
}

// TestRenderWidthsAndHiddenRows: a fixed width pads the label column; a
// row hidden with row hide still counts in the footer (it is the column's fold, not
// the screen's); an unread cell prints ? and so does its fold.
func TestRenderWidthsAndHiddenRows(t *testing.T) {
	t.Parallel()

	tb := counts([]string{"n"}, map[string][]int64{"a": {2}, "b": {0}, "c": {3}}, []string{"a", "b", "c"})
	tb.Rows[2].Cells[0].Unread = true
	tb.Rows[1].Hidden = true
	want := "row        | n\n" +
		"-----------+--\n" +
		"a          | 2\n" +
		"c          | ?\n" +
		"-----------+--\n" +
		"           | ?\n"
	got := ntable.Render(tb, ntable.RenderOpts{LabelWidth: 10})
	require.Equal(t, want, got, "widths and hidden rows:\n%s\nwant:\n%s", got, want)
	tb.Rows[2].Cells[0].Unread = false
	tb.Rows[1].Hidden = false
	tb.Columns[0].Width = 4
	tb.FooterLabel = "all"
	want = "row        |    n\n" +
		"-----------+-----\n" +
		"a          |    2\n" +
		"b          |    0\n" +
		"c          |    3\n" +
		"-----------+-----\n" +
		"all        |    5\n"
	got = ntable.Render(tb, ntable.RenderOpts{LabelWidth: 10})
	require.Equal(t, want, got, "column width and footer label:\n%s\nwant:\n%s", got, want)
}

// TestRenderProjectionsAndFolds: members, first and last cells, the union
// fold, a max fold, a none fold (blank), and no footer at all when every
// column folds none.
func TestRenderProjectionsAndFolds(t *testing.T) {
	t.Parallel()

	tb := ntable.Table{Name: "t", Columns: []ntable.Column{
		{Name: "who", Projection: ntable.Members, Fold: ntable.Union},
		{Name: "oldest", Projection: ntable.First, Fold: ntable.None},
		{Name: "newest", Projection: ntable.Last, Fold: ntable.None},
		{Name: "n", Projection: ntable.Count, Fold: ntable.Max},
	}}
	ms := []ntable.Member{{Member: "ann", Score: 1}, {Member: "bo", Score: 2}}
	a := ntable.NewRow(tb, "a")
	a.Cells[0].Members, a.Cells[1].Members, a.Cells[2].Members, a.Cells[3].Count = ms, ms, ms, 7
	b := ntable.NewRow(tb, "b")
	b.Label = "B row"
	b.Cells[0].Members = []ntable.Member{{Member: "bo", Score: 3}, {Member: "cy", Score: 4}}
	b.Cells[3].Count = 9
	tb.Rows = []ntable.Row{a, b}
	// the union is wider than the widest body cell only in the footer, and
	// the footer counts in the natural width; a blank footer cell leaves
	// no trailing space
	want := "row   | who       | oldest | newest | n\n" +
		"------+-----------+--------+--------+--\n" +
		"a     | ann,bo    | ann    | bo     | 7\n" +
		"B row | bo,cy     | -      | -      | 9\n" +
		"------+-----------+--------+--------+--\n" +
		"      | ann,bo,cy |        |        | 9\n"
	got := ntable.Render(tb, ntable.RenderOpts{})
	require.Equal(t, want, got, "projections and folds:\n%s\nwant:\n%s", got, want)
	for i := range tb.Columns {
		tb.Columns[i].Fold = ntable.None
	}
	want = "row   | who    | oldest | newest | n\n" +
		"------+--------+--------+--------+--\n" +
		"a     | ann,bo | ann    | bo     | 7\n" +
		"B row | bo,cy  | -      | -      | 9\n"
	got = ntable.Render(tb, ntable.RenderOpts{})
	require.Equal(t, want, got, "no fold, no footer:\n%s\nwant:\n%s", got, want)
}

// TestRenderLastLeftColumnIsNotPadded: a left-aligned last column is
// written as it is, so no line ends in a space.
func TestRenderLastLeftColumnIsNotPadded(t *testing.T) {
	t.Parallel()

	tb := ntable.Table{Name: "t", Columns: []ntable.Column{
		{Name: "n", Projection: ntable.Count, Fold: ntable.Sum},
		{Name: "who", Projection: ntable.Members, Fold: ntable.None},
	}}
	r := ntable.NewRow(tb, "r")
	r.Cells[0].Count = 1
	r.Cells[1].Members = []ntable.Member{{Member: "x"}}
	tb.Rows = []ntable.Row{r}
	// no text column first: the row-label column is put in front and the
	// footer label sits under it (the n column keeps its fold)
	want := "row | n | who\n" +
		"----+---+----\n" +
		"r   | 1 | x\n" +
		"----+---+----\n" +
		"    | 1 |\n"
	got := ntable.Render(tb, ntable.RenderOpts{})
	require.Equal(t, want, got, "left last column:\n%q\nwant:\n%q", got, want)
}

// TestParseColumnsAndWidths: the declaration grammar and its defaults.
func TestParseColumnsAndWidths(t *testing.T) {
	t.Parallel()

	cols, err := ntable.ParseColumns("stream:text:none:stream,waiting,ready:count:max:Ready,who:members:union")
	require.NoError(t, err)
	want := []ntable.Column{
		{Name: "stream", Projection: ntable.Text, Fold: ntable.None, Label: "stream"},
		{Name: "waiting", Projection: ntable.Count, Fold: ntable.Sum},
		{Name: "ready", Projection: ntable.Count, Fold: ntable.Max, Label: "Ready"},
		{Name: "who", Projection: ntable.Members, Fold: ntable.Union},
	}
	assert.Equal(t, want, cols, "parsed columns")
	for _, bad := range []string{"", "a b", "a:rows", "a:count:union", "a:text:avg", "a:members:sum", "a,a", "-a"} {
		_, err := ntable.ParseColumns(bad)
		assert.Error(t, err, "ParseColumns(%q) accepted", bad)
	}
	w, err := ntable.ParseWidths("stream=25, n=3")
	require.NoError(t, err)
	require.Equal(t, 25, w["stream"], "ParseWidths = %v", w)
	require.Equal(t, 3, w["n"], "ParseWidths = %v", w)
	_, err = ntable.ParseWidths("stream=x")
	assert.Error(t, err, "ParseWidths(stream=x) accepted")
}

// TestRenderPutsTheRowLabelFirstAndTitles (Glenn 2026-09-27, the live
// session: "these tables are hard to interpret. the 'total' under waiting is
// strange"; "tables need a title"): a definition whose first column is a
// count column gets the row-label column in front, the footer label under
// it and every fold in its own column; the title is the top-left cell, the
// header of that column; an empty table shows, title or not.
func TestRenderPutsTheRowLabelFirstAndTitles(t *testing.T) {
	t.Parallel()
	tb := counts([]string{"waiting", "ready"}, map[string][]int64{"alpha": {2, 1}, "beta": {0, 0}}, []string{"alpha", "beta"})
	want := "demo  | waiting | ready\n" +
		"------+---------+------\n" +
		"alpha |       2 |     1\n" +
		"beta  |       0 |     0\n" +
		"------+---------+------\n" +
		"      |       2 |     1\n"
	got := ntable.Render(tb, ntable.RenderOpts{Title: "demo"})
	require.Equal(t, want, got, "row label first and a title:\n%s\nwant:\n%s", got, want)
	got = ntable.Render(ntable.Table{Name: "empty"}, ntable.RenderOpts{Title: "empty"})
	require.True(t, strings.HasPrefix(got, "empty\n"), "an empty table with a title shows its header: %q", got)
	got = ntable.Render(ntable.Table{Name: "empty"}, ntable.RenderOpts{})
	require.True(t, strings.HasPrefix(got, "row\n"), "an empty table without a title shows its header: %q", got)
}

// TestRenderFormulaAndTextCells (Glenn 2026-09-27, the live session: "a new
// column ... 'waiting%' ... the % of waiting tasks as a % of all tasks in
// that row ... bottom summary cell the average"; "a new column 'status'
// which is either up or down"): a pct(<col>) column is computed per row
// over the row's count cells, folds avg over the rows that have tasks, and
// prints "-" for a row with none; a text column prints the row's value set
// by row set, else blank (the row's identity is the label column in front;
// Stella's read of #4456).
func TestRenderFormulaAndTextCells(t *testing.T) {
	t.Parallel()
	cols, err := ntable.ParseColumns("waiting,ready,working,done,wpct:pct(waiting):pooled:waiting%,status:text:none")
	require.NoError(t, err)
	tb := ntable.Table{Name: "streams", Columns: cols, FooterLabel: "total"}
	mk := func(key string, n ...int64) ntable.Row {
		r := ntable.NewRow(tb, key)
		for i, v := range n {
			r.Cells[i].Count = v
		}
		return r
	}
	a := mk("alpha", 1, 1, 1, 0)
	a.Texts = map[string]string{"status": "up"}
	b := mk("beta", 5, 2, 3, 0)
	e := mk("empty", 0, 0, 0, 0)
	tb.Rows = []ntable.Row{a, b, e}
	want := "streams | waiting | ready | working | done | waiting% | status\n" +
		"--------+---------+-------+---------+------+----------+-------\n" +
		"alpha   |       1 |     1 |       1 |    0 | 33.3%    | up\n" +
		"beta    |       5 |     2 |       3 |    0 | 50.0%    |\n" +
		"empty   |       0 |     0 |       0 |    0 | 0.0%     |\n" +
		"--------+---------+-------+---------+------+----------+-------\n" +
		"total   |       6 |     3 |       4 |    0 | 46.2%    |\n"
	got := ntable.Render(tb, ntable.RenderOpts{Title: "streams"})
	require.Equal(t, want, got, "formula and text cells:\n%s\nwant:\n%s", got, want)
	_, err = ntable.ParseColumns("wpct:pct(nothere)")
	require.ErrorContains(t, err, "count column named nothere", "a pct of a missing column")
	_, err = ntable.ParseColumns("who:members:avg")
	require.Error(t, err, "avg over members was accepted")
	// the mean of percentages is refused; the pooled share is the fold (Glenn 2026-09-27)
	_, err = ntable.ParseColumns("waiting,wpct:pct(waiting):avg")
	require.ErrorContains(t, err, "not accurate", "avg over a pct column")
	c, err := ntable.ParseColumn("wpct:pct(waiting)")
	require.NoError(t, err)
	require.Equal(t, ntable.Pooled, c.Fold, "the default fold of a pct column: %+v", c)
}

// TestRenderHidesAColumnButKeepsIt (Glenn 2026-09-27: "I no longer wish to
// see the waiting column ... Keep it, since the calculations depend on it,
// but hide that column"): a hidden column is not drawn; the formula that
// reads it still computes; the row-label column still comes first.
func TestRenderHidesAColumnButKeepsIt(t *testing.T) {
	t.Parallel()
	cols, err := ntable.ParseColumns("waiting,ready,wpct:pct(waiting):pooled:waiting%")
	require.NoError(t, err)
	tb := ntable.Table{Name: "s", Columns: cols, Hidden: []string{"waiting"}}
	r := ntable.NewRow(tb, "a")
	r.Cells[0].Count = 3
	r.Cells[1].Count = 1
	tb.Rows = []ntable.Row{r}
	want := "s | ready | waiting%\n" +
		"--+-------+---------\n" +
		"a |     1 | 75.0%\n" +
		"--+-------+---------\n" +
		"  |     1 | 75.0%\n"
	got := ntable.Render(tb, ntable.RenderOpts{Title: "s"})
	require.Equal(t, want, got, "hidden column:\n%s\nwant:\n%s", got, want)
}

// TestRenderFormulaPropagatesUnread (Stella's read of #4456): a pct cell
// whose count did not come back prints ?, and so does its pooled fold.
func TestRenderFormulaPropagatesUnread(t *testing.T) {
	t.Parallel()
	cols, err := ntable.ParseColumns("waiting,ready,wpct:pct(waiting)")
	require.NoError(t, err)
	tb := ntable.Table{Name: "s", Columns: cols}
	r := ntable.NewRow(tb, "a")
	r.Cells[0].Count = 3
	r.Cells[1].Unread = true
	tb.Rows = []ntable.Row{r}
	got := ntable.Render(tb, ntable.RenderOpts{Title: "s"})
	require.Contains(t, got, "a |       3 |     ? | ?\n", "unread propagation:\n%s", got)
	require.Contains(t, got, "  |       3 |     ? | ?\n", "unread propagation:\n%s", got)
}

func TestKnownEmptyPercentageBodyAndFooter(t *testing.T) {
	t.Parallel()
	cols, err := ntable.ParseColumns("ready,done,progress:pct(done)")
	require.NoError(t, err)
	table := ntable.Table{Name: "empty", Columns: cols, FooterLabel: "total"}
	table.Rows = []ntable.Row{ntable.NewRow(table, "r")}
	got := ntable.Render(table, ntable.RenderOpts{})
	require.Equal(t, 2, strings.Count(got, "0.0%"), "known empty row/footer: %s", got)
	require.NotContains(t, got, "?", "known empty row/footer: %s", got)
	table.Rows[0].Cells[0].Unread = true
	got = ntable.Render(table, ntable.RenderOpts{})
	require.NotContains(t, got, "0.0%", "unread row/footer: %s", got)
	require.Equal(t, 4, strings.Count(got, "?"), "unread row/footer: %s", got)
}

// TestSummaryLineIsTheStateAloneWhileThereIsOne: a view's state is its summary
// line, alone, whatever the counts; without one, the counts; with neither, no
// line. A state is one line of at most MaxViewState bytes.
func TestSummaryLineIsTheStateAloneWhileThereIsOne(t *testing.T) {
	t.Parallel()
	cols, err := ntable.ParseColumns("ready,done")
	require.NoError(t, err)
	tb := ntable.Table{Name: "w", Columns: cols}
	r := ntable.NewRow(tb, "s")
	r.Cells[0].Count, r.Cells[1].Count = 3, 1
	tb.Rows = []ntable.Row{r}
	for _, c := range []struct {
		v    ntable.View
		want string
	}{
		{ntable.View{Summary: "done", State: "STOPPED"}, "STOPPED"},
		{ntable.View{State: "STOPPED"}, "STOPPED"},
		{ntable.View{Summary: "done"}, "1/4 25.0% -> ETA"},
		{ntable.View{}, ""},
	} {
		got := ntable.SummaryLine(c.v, tb)
		assert.Equal(t, c.want, got, "%+v: %q, want %q", c.v, got, c.want)
	}
	for text, ok := range map[string]bool{"": true, "STOPPED": true, "a\nb": false, "\x1b[2J": false, strings.Repeat("x", ntable.MaxViewState): true, strings.Repeat("x", ntable.MaxViewState+1): false} {
		assert.Equal(t, ok, ntable.ValidViewState(text), "ValidViewState(%q) = %v", text, !ok)
	}
}

// TestRenderTablesShowsEveryTableAndEveryRow: every table of a frame shows,
// empty or not, and every row in it, zero or not; a table drawn never (set
// --hidden) leaves no gap.
func TestRenderTablesShowsEveryTableAndEveryRow(t *testing.T) {
	t.Parallel()
	work := counts([]string{"waiting", "landed"}, map[string][]int64{"a": {0, 0}, "b": {0, 1}}, []string{"a", "b"})
	work.Name = "work"
	readers := counts([]string{"asked"}, map[string][]int64{"r": {0}}, []string{"r"})
	readers.Name = "readers"
	got := ntable.RenderTables("", []ntable.Table{work, readers}, ntable.RenderOpts{})
	for _, row := range []string{"\na ", "\nb ", "\nr "} {
		require.Contains(t, got, row, "a, b and r all show")
	}
	work.Rows = nil
	got = ntable.RenderTables("", []ntable.Table{work, readers}, ntable.RenderOpts{})
	require.True(t, strings.HasPrefix(got, "work "), "work has no row: it shows its header and footer, then a blank line, then readers:\n%s", got)
	require.Contains(t, got, "\n\nreaders ", "work has no row: it shows its header and footer, then a blank line, then readers:\n%s", got)
	work.HiddenTable = true
	got = ntable.RenderTables("", []ntable.Table{work, readers}, ntable.RenderOpts{})
	require.True(t, strings.HasPrefix(got, "readers "), "a table set hidden is not drawn and leaves no gap:\n%s", got)

}

// TestATextColumnOfWholeNumbersFoldsSumAndMax: a text column with a sum or
// max fold prints right-aligned and totals its cells in the footer; a blank
// cell is 0 and a cell that is no whole number makes the fold "?".
func TestATextColumnOfWholeNumbersFoldsSumAndMax(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ fold, want string }{{ntable.Sum, "72"}, {ntable.Max, "64"}} {
		cols, err := ntable.ParseColumns("n:count,w:text:" + tc.fold)
		require.NoError(t, err)
		tab := ntable.Table{Name: "t", Columns: cols}
		for k, v := range map[string]string{"a": "64", "b": "8", "c": ""} {
			r := ntable.NewRow(tab, k)
			r.Texts = map[string]string{"w": v}
			tab.Rows = append(tab.Rows, r)
		}
		out := ntable.Render(tab, ntable.RenderOpts{})
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		last := lines[len(lines)-1]
		assert.True(t, strings.HasSuffix(last, "| "+tc.want), "fold %s: footer %q wants %s", tc.fold, last, tc.want)
		assert.Contains(t, out, "| 64\n", "fold %s: cells are not right-aligned", tc.fold)
		assert.Contains(t, out, "|  8\n", "fold %s: cells are not right-aligned", tc.fold)
		tab.Rows[0].Texts["w"] = "many"
		out = ntable.Render(tab, ntable.RenderOpts{})
		assert.Contains(t, out, "?", "fold %s: a cell that is no number leaves the fold known:\n%s", tc.fold, out)
	}
}

// TestATextColumnOfMoneyFoldsExactly: a text sum column of money amounts ("$" and a
// decimal, "-" for none) totals them exactly to "$" and four places, never "?"; a column
// of dashes only folds to "-"; an amount that is not a decimal is "?"; and max keeps the
// largest.
func TestATextColumnOfMoneyFoldsExactly(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, fold string
		cells      []string
		want       string
	}{
		{name: "amounts and dashes", fold: ntable.Sum, cells: []string{"$0.0046", "$0.0001", "-"}, want: "$0.0047"},
		{name: "past a float's digits", fold: ntable.Sum, cells: []string{"$0.1", "$0.2", "-"}, want: "$0.3000"},
		{name: "a blank cell is none", fold: ntable.Sum, cells: []string{"$1.5", "", "-"}, want: "$1.5000"},
		{name: "nothing priced", fold: ntable.Sum, cells: []string{"-", "-", ""}, want: "-"},
		{name: "an amount that is no decimal", fold: ntable.Sum, cells: []string{"$1e-3", "-", "-"}, want: "?"},
		{name: "max", fold: ntable.Max, cells: []string{"$0.5", "$2.25", "-"}, want: "$2.2500"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cols, err := ntable.ParseColumns("n:count,cost:text:" + tc.fold)
			require.NoError(t, err)
			tab := ntable.Table{Name: "t", Columns: cols}
			for i, v := range tc.cells {
				r := ntable.NewRow(tab, string(rune('a'+i)))
				r.Texts = map[string]string{"cost": v}
				tab.Rows = append(tab.Rows, r)
			}
			lines := strings.Split(strings.TrimRight(ntable.Render(tab, ntable.RenderOpts{}), "\n"), "\n")
			last := lines[len(lines)-1]
			assert.True(t, strings.HasSuffix(last, " "+tc.want), "footer %q wants %s", last, tc.want)
		})
	}
}

// TestRenderCellsAlignByTerminalColumns: cell width is counted in terminal columns, so a
// wide East Asian row key (2 columns a rune) and one with a combining mark (0 columns) line
// up with their ASCII neighbours and the separators stay in one column.
func TestRenderCellsAlignByTerminalColumns(t *testing.T) {
	t.Parallel()

	keys := []string{"cafe\u0301", "abcd", "日本語"}
	tbl := counts([]string{"a", "b"}, map[string][]int64{keys[0]: {1, 2}, keys[1]: {3, 4}, keys[2]: {5, 6}}, keys)
	want := "row    | a |  b\n" +
		"-------+---+---\n" +
		"cafe\u0301   | 1 |  2\n" +
		"abcd   | 3 |  4\n" +
		"日本語 | 5 |  6\n" +
		"-------+---+---\n" +
		"       | 9 | 12\n"
	got := ntable.Render(tbl, ntable.RenderOpts{})
	require.Equal(t, want, got, "wide and combining rows rendered:\n%s\nwant:\n%s", got, want)
}

// A union footer is the members of every row together, "-" for none, and "?"
// when a row's set did not come back.
func TestRenderUnionFooterIsUnknownWhenARowIsUnread(t *testing.T) {
	t.Parallel()

	tb := ntable.Table{Name: "t", Columns: []ntable.Column{{Name: "who", Projection: ntable.Members, Fold: ntable.Union}}}
	a, b := ntable.NewRow(tb, "a"), ntable.NewRow(tb, "b")
	a.Cells[0].Members = []ntable.Member{{Member: "ann"}}
	for _, c := range []struct {
		name string
		rows func() []ntable.Row
		want string
	}{
		{"both rows read", func() []ntable.Row { return []ntable.Row{a, b} }, "  | ann\n"},
		{"no member", func() []ntable.Row { return []ntable.Row{b} }, "  | -\n"},
		{"a row unread", func() []ntable.Row {
			u := b
			u.Cells = []ntable.Cell{{Unread: true}}
			return []ntable.Row{a, u}
		}, "  | ?\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			tb.Rows = c.rows()
			got := ntable.Render(tb, ntable.RenderOpts{})
			assert.True(t, strings.HasSuffix(got, c.want), "%s: footer of\n%s\nwant it to end %q", c.name, got, c.want)
		})
	}
}

// A column declaration with an empty projection takes the default one, the
// count.
func TestParseColumnEmptyProjectionIsTheCount(t *testing.T) {
	t.Parallel()

	for _, spec := range []string{"n", "n:", "n::sum", "n::sum:Label"} {
		t.Run(spec, func(t *testing.T) {
			c, err := ntable.ParseColumn(spec)
			require.NoError(t, err, "ParseColumn(%q)", spec)
			assert.Equal(t, ntable.Count, c.Projection, "ParseColumn(%q) = %+v; want the count folded by sum", spec, c)
			assert.Equal(t, ntable.Sum, c.Fold, "ParseColumn(%q) = %+v; want the count folded by sum", spec, c)
		})
	}
}
