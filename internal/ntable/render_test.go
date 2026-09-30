package ntable_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
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
	if got := ntable.Render(empty, ntable.RenderOpts{}); got != want {
		t.Fatalf("empty table rendered:\n%s\nwant:\n%s", got, want)
	}
	zeros := counts([]string{"a", "b"}, map[string][]int64{"x": {0, 0}, "y": {0, 0}}, []string{"x", "y"})
	want = "row | a | b\n" +
		"----+---+--\n" +
		"x   | 0 | 0\n" +
		"y   | 0 | 0\n" +
		"----+---+--\n" +
		"    | 0 | 0\n"
	if got := ntable.Render(zeros, ntable.RenderOpts{}); got != want {
		t.Fatalf("zero rows shown:\n%s\nwant:\n%s", got, want)
	}
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
	if got := ntable.Render(one, ntable.RenderOpts{}); got != want {
		t.Fatalf("one row:\n%s\nwant:\n%s", got, want)
	}
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
	if got != want {
		t.Fatalf("widths and hidden rows:\n%s\nwant:\n%s", got, want)
	}
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
	if got := ntable.Render(tb, ntable.RenderOpts{LabelWidth: 10}); got != want {
		t.Fatalf("column width and footer label:\n%s\nwant:\n%s", got, want)
	}
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
	if got := ntable.Render(tb, ntable.RenderOpts{}); got != want {
		t.Fatalf("projections and folds:\n%s\nwant:\n%s", got, want)
	}
	for i := range tb.Columns {
		tb.Columns[i].Fold = ntable.None
	}
	want = "row   | who    | oldest | newest | n\n" +
		"------+--------+--------+--------+--\n" +
		"a     | ann,bo | ann    | bo     | 7\n" +
		"B row | bo,cy  | -      | -      | 9\n"
	if got := ntable.Render(tb, ntable.RenderOpts{}); got != want {
		t.Fatalf("no fold, no footer:\n%s\nwant:\n%s", got, want)
	}
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
	if got != want {
		t.Fatalf("left last column:\n%q\nwant:\n%q", got, want)
	}
}

// TestParseColumnsAndWidths: the declaration grammar and its defaults.
func TestParseColumnsAndWidths(t *testing.T) {
	t.Parallel()

	cols, err := ntable.ParseColumns("stream:text:none:stream,waiting,ready:count:max:Ready,who:members:union")
	if err != nil {
		t.Fatal(err)
	}
	want := []ntable.Column{
		{Name: "stream", Projection: ntable.Text, Fold: ntable.None, Label: "stream"},
		{Name: "waiting", Projection: ntable.Count, Fold: ntable.Sum},
		{Name: "ready", Projection: ntable.Count, Fold: ntable.Max, Label: "Ready"},
		{Name: "who", Projection: ntable.Members, Fold: ntable.Union},
	}
	if len(cols) != len(want) {
		t.Fatalf("parsed %d columns, want %d", len(cols), len(want))
	}
	for i := range want {
		if cols[i] != want[i] {
			t.Errorf("column %d = %+v, want %+v", i, cols[i], want[i])
		}
	}
	for _, bad := range []string{"", "a b", "a:rows", "a:count:union", "a:text:avg", "a:members:sum", "a,a", "-a"} {
		if _, err := ntable.ParseColumns(bad); err == nil {
			t.Errorf("ParseColumns(%q) accepted", bad)
		}
	}
	w, err := ntable.ParseWidths("stream=25, n=3")
	if err != nil || w["stream"] != 25 || w["n"] != 3 {
		t.Fatalf("ParseWidths = %v %v", w, err)
	}
	if _, err := ntable.ParseWidths("stream=x"); err == nil {
		t.Error("ParseWidths(stream=x) accepted")
	}
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
	if got := ntable.Render(tb, ntable.RenderOpts{Title: "demo"}); got != want {
		t.Fatalf("row label first and a title:\n%s\nwant:\n%s", got, want)
	}
	if got := ntable.Render(ntable.Table{Name: "empty"}, ntable.RenderOpts{Title: "empty"}); !strings.HasPrefix(got, "empty\n") {
		t.Fatalf("an empty table with a title shows its header: %q", got)
	}
	if got := ntable.Render(ntable.Table{Name: "empty"}, ntable.RenderOpts{}); !strings.HasPrefix(got, "row\n") {
		t.Fatalf("an empty table without a title shows its header: %q", got)
	}
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
	if err != nil {
		t.Fatal(err)
	}
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
	if got := ntable.Render(tb, ntable.RenderOpts{Title: "streams"}); got != want {
		t.Fatalf("formula and text cells:\n%s\nwant:\n%s", got, want)
	}
	if _, err := ntable.ParseColumns("wpct:pct(nothere)"); err == nil || !strings.Contains(err.Error(), "count column named nothere") {
		t.Fatalf("a pct of a missing column: %v", err)
	}
	if _, err := ntable.ParseColumns("who:members:avg"); err == nil {
		t.Fatal("avg over members was accepted")
	}
	// the mean of percentages is refused; the pooled share is the fold (Glenn 2026-09-27)
	if _, err := ntable.ParseColumns("waiting,wpct:pct(waiting):avg"); err == nil || !strings.Contains(err.Error(), "not accurate") {
		t.Fatalf("avg over a pct column: %v", err)
	}
	if c, err := ntable.ParseColumn("wpct:pct(waiting)"); err != nil || c.Fold != ntable.Pooled {
		t.Fatalf("the default fold of a pct column: %+v %v", c, err)
	}
}

// TestRenderHidesAColumnButKeepsIt (Glenn 2026-09-27: "I no longer wish to
// see the waiting column ... Keep it, since the calculations depend on it,
// but hide that column"): a hidden column is not drawn; the formula that
// reads it still computes; the row-label column still comes first.
func TestRenderHidesAColumnButKeepsIt(t *testing.T) {
	t.Parallel()
	cols, err := ntable.ParseColumns("waiting,ready,wpct:pct(waiting):pooled:waiting%")
	if err != nil {
		t.Fatal(err)
	}
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
	if got := ntable.Render(tb, ntable.RenderOpts{Title: "s"}); got != want {
		t.Fatalf("hidden column:\n%s\nwant:\n%s", got, want)
	}
}

// TestRenderFormulaPropagatesUnread (Stella's read of #4456): a pct cell
// whose count did not come back prints ?, and so does its pooled fold.
func TestRenderFormulaPropagatesUnread(t *testing.T) {
	t.Parallel()
	cols, err := ntable.ParseColumns("waiting,ready,wpct:pct(waiting)")
	if err != nil {
		t.Fatal(err)
	}
	tb := ntable.Table{Name: "s", Columns: cols}
	r := ntable.NewRow(tb, "a")
	r.Cells[0].Count = 3
	r.Cells[1].Unread = true
	tb.Rows = []ntable.Row{r}
	got := ntable.Render(tb, ntable.RenderOpts{Title: "s"})
	if !strings.Contains(got, "a |       3 |     ? | ?\n") || !strings.Contains(got, "  |       3 |     ? | ?\n") {
		t.Fatalf("unread propagation:\n%s", got)
	}
}

func TestKnownEmptyPercentageBodyAndFooter(t *testing.T) {
	t.Parallel()
	cols, err := ntable.ParseColumns("ready,done,progress:pct(done)")
	if err != nil {
		t.Fatal(err)
	}
	table := ntable.Table{Name: "empty", Columns: cols, FooterLabel: "total"}
	table.Rows = []ntable.Row{ntable.NewRow(table, "r")}
	got := ntable.Render(table, ntable.RenderOpts{})
	if strings.Count(got, "0.0%") != 2 || strings.Contains(got, "?") {
		t.Fatalf("known empty row/footer: %s", got)
	}
	table.Rows[0].Cells[0].Unread = true
	got = ntable.Render(table, ntable.RenderOpts{})
	if strings.Contains(got, "0.0%") || strings.Count(got, "?") != 4 {
		t.Fatalf("unread row/footer: %s", got)
	}
}

// TestSummaryLineIsTheStateAloneWhileThereIsOne: a view's state is its summary
// line, alone, whatever the counts; without one, the counts; with neither, no
// line. A state is one line of at most MaxViewState bytes.
func TestSummaryLineIsTheStateAloneWhileThereIsOne(t *testing.T) {
	t.Parallel()
	cols, err := ntable.ParseColumns("ready,done")
	if err != nil {
		t.Fatal(err)
	}
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
		if got := ntable.SummaryLine(c.v, tb); got != c.want {
			t.Errorf("%+v: %q, want %q", c.v, got, c.want)
		}
	}
	for text, ok := range map[string]bool{"": true, "STOPPED": true, "a\nb": false, "\x1b[2J": false, strings.Repeat("x", ntable.MaxViewState): true, strings.Repeat("x", ntable.MaxViewState+1): false} {
		if ntable.ValidViewState(text) != ok {
			t.Errorf("ValidViewState(%q) = %v", text, !ok)
		}
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
	if !strings.Contains(got, "\na ") || !strings.Contains(got, "\nb ") || !strings.Contains(got, "\nr ") {
		t.Fatalf("a, b and r all show:\n%s", got)
	}
	work.Rows = nil
	got = ntable.RenderTables("", []ntable.Table{work, readers}, ntable.RenderOpts{})
	if !strings.HasPrefix(got, "work ") || !strings.Contains(got, "\n\nreaders ") {
		t.Fatalf("work has no row: it shows its header and footer, then a blank line, then readers:\n%s", got)
	}
	work.HiddenTable = true
	if got := ntable.RenderTables("", []ntable.Table{work, readers}, ntable.RenderOpts{}); !strings.HasPrefix(got, "readers ") {
		t.Fatalf("a table set hidden is not drawn and leaves no gap:\n%s", got)
	}

}

// TestATextColumnOfWholeNumbersFoldsSumAndMax: a text column with a sum or
// max fold prints right-aligned and totals its cells in the footer; a blank
// cell is 0 and a cell that is no whole number makes the fold "?".
func TestATextColumnOfWholeNumbersFoldsSumAndMax(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ fold, want string }{{ntable.Sum, "72"}, {ntable.Max, "64"}} {
		cols, err := ntable.ParseColumns("n:count,w:text:" + tc.fold)
		if err != nil {
			t.Fatal(err)
		}
		tab := ntable.Table{Name: "t", Columns: cols}
		for k, v := range map[string]string{"a": "64", "b": "8", "c": ""} {
			r := ntable.NewRow(tab, k)
			r.Texts = map[string]string{"w": v}
			tab.Rows = append(tab.Rows, r)
		}
		out := ntable.Render(tab, ntable.RenderOpts{})
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if last := lines[len(lines)-1]; !strings.HasSuffix(last, "| "+tc.want) {
			t.Errorf("fold %s: footer %q wants %s", tc.fold, last, tc.want)
		}
		if !strings.Contains(out, "| 64\n") || !strings.Contains(out, "|  8\n") {
			t.Errorf("fold %s: cells are not right-aligned:\n%s", tc.fold, out)
		}
		tab.Rows[0].Texts["w"] = "many"
		if out := ntable.Render(tab, ntable.RenderOpts{}); !strings.Contains(out, "?") {
			t.Errorf("fold %s: a cell that is no number leaves the fold known:\n%s", tc.fold, out)
		}
	}
}
