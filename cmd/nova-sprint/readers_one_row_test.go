package main

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
)

// The readers table of the where view is one row, all, the sum over every
// reader, with no per-reader row and no footer (the owner, 2026-10-01: "change
// the table to just be one row, sum of all"; "i just need to see reader
// *progress* overall"): docs/SPEC-SPRINT.md section 1. where --json keeps every
// reader's row.

// A sprint with three readers holding different counts prints one readers row
// whose count cells are the sums, with the readers' width beside reading ("-":
// none is named for a fleet row); --json still lists each reader, its width
// with it.
func TestWhereReadersTableIsOneRowTheSumOfAllReaders(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1")
	ta.inReview(6)
	ta.ok("ask")
	ta.ok("read --as reader-a --ok --limit 2")
	ta.ok("read --as reader-b --begin --limit 1")
	ta.ok("read --as reader-c --broken --finding 'line 3: the empty case is not handled' --limit 1")

	var v struct {
		Tables map[string]map[string]map[string]string
	}
	ta.json("where", &v)
	readers := v.Tables[sprint.Readers]
	require.Len(t, readers, 3, "--json lists each reader")
	cols := []string{sprint.Asked, sprint.Reading, sprint.OK, sprint.Broken}
	sums := map[string]int{}
	seen := map[string]bool{}
	for _, rd := range []string{"reader-a", "reader-b", "reader-c"} {
		row, ok := readers[rd]
		require.True(t, ok, "--json has %s's row: %v", rd, readers)
		assert.Equal(t, "-", row[sprint.FieldWidth], "%s is named for no fleet row: no width", rd)
		var line []string
		for _, c := range cols {
			n, err := strconv.Atoi(cellText(row[c]))
			require.NoError(t, err, "%s %s", rd, c)
			sums[c] += n
			line = append(line, cellText(row[c]))
		}
		seen[strings.Join(line, ",")] = true
	}
	require.Len(t, seen, 3, "the three readers hold different counts: %v", readers)
	for _, c := range cols {
		require.Positive(t, sums[c], "every column has a card somewhere: %s", c)
	}

	block := tableOf(ta.ok("where --all"), "readers")
	lines := strings.Split(strings.TrimRight(block, "\n"), "\n")
	require.Len(t, lines, 3, "header, rule, one row; no footer:\n%s", block)
	assert.Equal(t, []string{"readers", "asked", "reading", "width", "ok", "broken", "tiers"}, cells(lines[0]))
	assert.True(t, strings.HasPrefix(lines[1], "--"), "the rule: %q", lines[1])
	want := []string{allRow}
	for _, c := range cols {
		want = append(want, strconv.Itoa(sums[c]))
	}
	want = slices.Insert(want, 3, "-")
	want = append(want, "default")
	assert.Equal(t, want, cells(lines[2]))
	for _, rd := range []string{"reader-a", "reader-b", "reader-c"} {
		assert.NotContains(t, block, rd, "no per-reader row")
	}
}

// cells is a rendered line's cells, trimmed.
func cells(line string) []string {
	parts := strings.Split(line, " | ")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// readersAll sums every row, hidden ones too, into one row and folds nothing;
// a set that did not come back prints "?" in its column, as the footer did.
func TestReadersAllSumsEveryRowHiddenToo(t *testing.T) {
	t.Parallel()
	cols, err := ntable.ParseColumns("asked,reading,ok,broken")
	require.NoError(t, err)
	row := func(key string, hidden bool, n ...int64) ntable.Row {
		r := ntable.Row{Key: key, Hidden: hidden}
		for _, c := range n {
			r.Cells = append(r.Cells, ntable.Cell{Count: c})
		}
		return r
	}
	tb := ntable.Table{Name: sprint.Readers, Columns: cols, Rows: []ntable.Row{
		row("reader-a", false, 1, 2, 3, 4),
		row("reader-b", true, 10, 20, 30, 40), // away or down: hidden, still counted
		row("reader-c", false, 100, 200, 300, 400),
	}}
	tb.Rows[2].Cells[3].Unread = true
	got := ntable.Render(readersAll(tb), ntable.RenderOpts{Title: sprint.Readers})
	assert.Equal(t, "readers | asked | reading |  ok | broken\n"+
		"--------+-------+---------+-----+-------\n"+
		"        |   111 |     222 | 333 |      ?\n", got)
	require.Len(t, tb.Rows, 3, "the table read is not changed")
	assert.NotEqual(t, ntable.None, tb.Columns[0].Fold, "the columns read are not changed")
}

// The merge table of the where view is one row the same way (the owner,
// 2026-10-01: "Can we please (for next sprint) do the same for merge"): three
// streams with different counts, one stopped red, print one row whose queued,
// merged and stuck are the sums and whose ci and state are the worst across the
// streams; --json still lists each stream.
func TestWhereMergeTableIsOneRowTheSumsAndTheWorstCIAndState(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.toMerging("s1", "s2", "s3")
	ta.ok("merge --stream s1 --red --suspect s1-2") // s1 stopped, ci red
	ta.ok("merge --stream s2 --batch 1")            // s2 lands one, ci green

	var v struct {
		Tables map[string]map[string]map[string]string
	}
	ta.json("where", &v)
	merge := v.Tables[sprint.Merge]
	require.Len(t, merge, 3, "--json lists each stream")
	cols := []string{sprint.Queued, sprint.Merged, sprint.Stuck}
	sums := map[string]int{}
	seen := map[string]bool{}
	for _, st := range []string{"s1", "s2", "s3"} {
		row, ok := merge[st]
		require.True(t, ok, "--json has %s's row: %v", st, merge)
		line := []string{cellText(row[sprint.CI]), cellText(row[sprint.StateCol])}
		for _, c := range cols {
			n, err := strconv.Atoi(cellText(row[c]))
			require.NoError(t, err, "%s %s", st, c)
			sums[c] += n
			line = append(line, cellText(row[c]))
		}
		seen[strings.Join(line, ",")] = true
	}
	require.Len(t, seen, 3, "the three streams differ: %v", merge)
	require.Equal(t, "red", merge["s1"][sprint.CI], "s1 is red: %v", merge)
	require.Equal(t, sprint.StreamStopped, merge["s1"][sprint.StateCol], "s1 is stopped: %v", merge)
	require.Equal(t, "green", merge["s2"][sprint.CI], "s2 is green: %v", merge)
	require.NotEqual(t, sprint.StreamStopped, merge["s3"][sprint.StateCol], "s3 is not stopped: %v", merge)

	block := tableOf(ta.ok("where --all"), "merge")
	lines := strings.Split(strings.TrimRight(block, "\n"), "\n")
	require.Len(t, lines, 3, "header, rule, one row; no footer:\n%s", block)
	assert.Equal(t, []string{"merge", "queued", "merged", "stuck", "ci", "state"}, cells(lines[0]))
	want := []string{allRow}
	for _, c := range cols {
		want = append(want, strconv.Itoa(sums[c]))
	}
	want = append(want, "red", "stopped 1")
	assert.Equal(t, want, cells(lines[2]), "\n%s", block)
	for _, st := range []string{"s1", "s2", "s3"} {
		assert.NotContains(t, cells(lines[2])[0], st, "no per-stream row")
	}
	assert.NotContains(t, block, "\ns1 ", "no per-stream row")
}

// worst is the value most in need of the coordinator's eye, and how many rows
// hold it: the order's first, then a value it does not name, then "-" or blank.
func TestWorstIsTheFirstInOrderAndItsCount(t *testing.T) {
	t.Parallel()
	rows := func(vs ...string) []ntable.Row {
		var rs []ntable.Row
		for _, v := range vs {
			rs = append(rs, ntable.Row{Texts: map[string]string{"state": v}})
		}
		return rs
	}
	order := []string{"stopped", "merging", "waiting", "landed"}
	for _, c := range []struct {
		in   []string
		want string
		n    int
	}{
		{nil, "-", 0},
		{[]string{"-", ""}, "-", 1},
		{[]string{"waiting", "merging", "waiting"}, "merging", 1},
		{[]string{"landed", "stopped", "merging", "stopped"}, "stopped", 2},
		{[]string{"landed", "-", "landed"}, "landed", 2},
		{[]string{"waiting", "odd"}, "waiting", 1},
		{[]string{"-", "odd"}, "odd", 1},
	} {
		w, n := worst(rows(c.in...), "state", order...)
		assert.Equal(t, c.want, w, "%v", c.in)
		assert.Equal(t, c.n, n, "%v", c.in)
	}
}

// mergeAll puts the count beside a stopped state, and red before green.
func TestMergeAllNamesHowManyStreamsAreStopped(t *testing.T) {
	t.Parallel()
	cols, err := ntable.ParseColumns("queued,merged,stuck,ci:text,state:text")
	require.NoError(t, err)
	row := func(key, ci, state string, n ...int64) ntable.Row {
		r := ntable.Row{Key: key, Texts: map[string]string{"ci": ci, "state": state}, Cells: make([]ntable.Cell, len(cols))}
		for j, c := range n {
			r.Cells[j].Count = c
		}
		return r
	}
	tb := ntable.Table{Name: sprint.Merge, Columns: cols, Rows: []ntable.Row{
		row("s1", "green", "stopped", 1, 2, 3),
		row("s2", "-", "merging", 10, 20, 30),
		row("s3", "red", "stopped", 100, 200, 300),
		row("s4", "green", "waiting", 0, 0, 0),
	}}
	got := ntable.Render(mergeAll(tb), ntable.RenderOpts{Title: sprint.Merge})
	assert.Equal(t, "merge | queued | merged | stuck | ci  | state\n"+
		"------+--------+--------+-------+-----+----------\n"+
		"      |    111 |    222 |   333 | red | stopped 2\n", got)
}
