package ntable_test

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// counts builds a count-only table of the given rows for the render tests.
func counts(cols []string, rows map[string][]int64, order []string) ntable.Table {
	t := ntable.Table{Name: "t", Columns: []ntable.Column{{Name: "row", Projection: ntable.Text, Fold: ntable.None}}}
	for _, c := range cols {
		t.Columns = append(t.Columns, ntable.Column{Name: c, Projection: ntable.Count, Fold: ntable.Sum})
	}
	for _, key := range order {
		r := ntable.NewRow(t, key)
		for j, n := range rows[key] {
			r.Cells[j+1].Count = n
		}
		t.Rows = append(t.Rows, r)
	}
	return t
}

// TestRenderEmptyTableIsTheEmptyString: no rows, and no visible row under
// HideZeroRows, print nothing at all: not a header, not a newline (Glenn
// 2026-09-26: the empty stream table is hidden with no extra newline).
func TestRenderEmptyTableIsTheEmptyString(t *testing.T) {
	t.Parallel()

	empty := counts([]string{"a"}, nil, nil)
	if got := ntable.Render(empty, ntable.RenderOpts{}); got != "" {
		t.Fatalf("empty table rendered %q, want \"\"", got)
	}
	zeros := counts([]string{"a", "b"}, map[string][]int64{"x": {0, 0}, "y": {0, 0}}, []string{"x", "y"})
	if got := ntable.Render(zeros, ntable.RenderOpts{HideZeroRows: true}); got != "" {
		t.Fatalf("all-zero rows hidden rendered %q, want \"\"", got)
	}
	// without HideZeroRows the zero rows show
	want := "row   | a | b\n" +
		"------+---+--\n" +
		"x     | 0 | 0\n" +
		"y     | 0 | 0\n" +
		"------+---+--\n" +
		"total | 0 | 0\n"
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
		"total        |     150 |     5\n"
	if got := ntable.Render(one, ntable.RenderOpts{}); got != want {
		t.Fatalf("one row:\n%s\nwant:\n%s", got, want)
	}
}

// TestRenderWidthsAndHiddenRows: a fixed width pads the label column; a
// hidden zero row still counts in the footer (it is the column's fold, not
// the screen's); an unread cell prints ? and so does its fold.
func TestRenderWidthsAndHiddenRows(t *testing.T) {
	t.Parallel()

	tb := counts([]string{"n"}, map[string][]int64{"a": {2}, "b": {0}, "c": {3}}, []string{"a", "b", "c"})
	tb.Rows[2].Cells[1].Unread = true
	want := "row        | n\n" +
		"-----------+--\n" +
		"a          | 2\n" +
		"c          | ?\n" +
		"-----------+--\n" +
		"total      | ?\n"
	got := ntable.Render(tb, ntable.RenderOpts{Widths: map[string]int{"row": 10}, HideZeroRows: true})
	if got != want {
		t.Fatalf("widths and hidden rows:\n%s\nwant:\n%s", got, want)
	}
	tb.Rows[2].Cells[1].Unread = false
	tb.Columns[1].Width = 4
	tb.FooterLabel = "all"
	want = "row        |    n\n" +
		"-----------+-----\n" +
		"a          |    2\n" +
		"b          |    0\n" +
		"c          |    3\n" +
		"-----------+-----\n" +
		"all        |    5\n"
	if got := ntable.Render(tb, ntable.RenderOpts{Widths: map[string]int{"row": 10}}); got != want {
		t.Fatalf("column width and footer label:\n%s\nwant:\n%s", got, want)
	}
}

// TestRenderProjectionsAndFolds: members, first and last cells, the union
// fold, a max fold, a none fold (blank), and no footer at all when every
// column folds none.
func TestRenderProjectionsAndFolds(t *testing.T) {
	t.Parallel()

	tb := ntable.Table{Name: "t", Columns: []ntable.Column{
		{Name: "row", Projection: ntable.Text, Fold: ntable.None},
		{Name: "who", Projection: ntable.Members, Fold: ntable.Union},
		{Name: "oldest", Projection: ntable.First, Fold: ntable.None},
		{Name: "newest", Projection: ntable.Last, Fold: ntable.None},
		{Name: "n", Projection: ntable.Count, Fold: ntable.Max},
	}}
	ms := []ntable.Member{{Member: "ann", Score: 1}, {Member: "bo", Score: 2}}
	a := ntable.NewRow(tb, "a")
	a.Cells[1].Members, a.Cells[2].Members, a.Cells[3].Members, a.Cells[4].Count = ms, ms, ms, 7
	b := ntable.NewRow(tb, "b")
	b.Label = "B row"
	b.Cells[1].Members = []ntable.Member{{Member: "bo", Score: 3}, {Member: "cy", Score: 4}}
	b.Cells[4].Count = 9
	tb.Rows = []ntable.Row{a, b}
	// the union is wider than the widest body cell only in the footer, and
	// the footer counts in the natural width; a blank footer cell leaves
	// no trailing space
	want := "row   | who       | oldest | newest | n\n" +
		"------+-----------+--------+--------+--\n" +
		"a     | ann,bo    | ann    | bo     | 7\n" +
		"B row | bo,cy     | -      | -      | 9\n" +
		"------+-----------+--------+--------+--\n" +
		"total | ann,bo,cy |        |        | 9\n"
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
	want := "row   | n | who\n" +
		"------+---+----\n" +
		"r     | 1 | x\n" +
		"------+---+----\n" +
		"total | 1 |\n"
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
	for _, bad := range []string{"", "a b", "a:rows", "a:count:union", "a:text:sum", "a:members:sum", "a,a", "-a"} {
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
// count column gets the row-label column in front, headed row, the footer
// label under it and every fold in its own column; a title prints above the
// grid; an empty table with a title prints the title and (no rows).
func TestRenderPutsTheRowLabelFirstAndTitles(t *testing.T) {
	t.Parallel()
	tb := counts([]string{"waiting", "ready"}, map[string][]int64{"alpha": {2, 1}, "beta": {0, 0}}, []string{"alpha", "beta"})
	tb.Columns = tb.Columns[1:] // no text column: as `nova-table create demo --columns waiting,ready` makes it
	for i := range tb.Rows {
		tb.Rows[i].Cells = tb.Rows[i].Cells[1:]
	}
	want := "demo\n" +
		"row   | waiting | ready\n" +
		"------+---------+------\n" +
		"alpha |       2 |     1\n" +
		"beta  |       0 |     0\n" +
		"------+---------+------\n" +
		"total |       2 |     1\n"
	if got := ntable.Render(tb, ntable.RenderOpts{Title: "demo"}); got != want {
		t.Fatalf("row label first and a title:\n%s\nwant:\n%s", got, want)
	}
	if got := ntable.Render(ntable.Table{Name: "empty"}, ntable.RenderOpts{Title: "empty"}); got != "empty\n(no rows)\n" {
		t.Fatalf("an empty table with a title: %q", got)
	}
	if got := ntable.Render(ntable.Table{Name: "empty"}, ntable.RenderOpts{}); got != "" {
		t.Fatalf("an empty table without a title stays hidden: %q", got)
	}
}
