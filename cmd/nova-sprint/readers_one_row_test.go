package main

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The readers table of the where view is one row, all, the sum over every
// reader, with no per-reader row and no footer (the owner, 2026-10-01: "change
// the table to just be one row, sum of all"; "i just need to see reader
// *progress* overall"): docs/SPEC-SPRINT.md section 1. where --json keeps every
// reader's row.

// A sprint with three readers holding different counts prints one readers row
// whose four cells are the sums; --json still lists each reader.
func TestWhereReadersTableIsOneRowTheSumOfAllReaders(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1")
	ta.inReview(6)
	ta.ok("ask")
	ta.ok("read --as reader-a --ok --limit 2")
	ta.ok("read --as reader-b --begin --limit 1")
	ta.ok("read --as reader-c --broken --finding 'the empty case is not handled' --limit 1")

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
		var line []string
		for _, c := range cols {
			n, err := strconv.Atoi(row[c])
			require.NoError(t, err, "%s %s", rd, c)
			sums[c] += n
			line = append(line, row[c])
		}
		seen[strings.Join(line, ",")] = true
	}
	require.Len(t, seen, 3, "the three readers hold different counts: %v", readers)
	for _, c := range cols {
		require.Positive(t, sums[c], "every column has a card somewhere: %s", c)
	}

	block := tableOf(ta.ok("where"), "readers")
	lines := strings.Split(strings.TrimRight(block, "\n"), "\n")
	require.Len(t, lines, 3, "header, rule, one row; no footer:\n%s", block)
	assert.Equal(t, []string{"readers", "asked", "reading", "ok", "broken"}, cells(lines[0]))
	assert.True(t, strings.HasPrefix(lines[1], "--"), "the rule: %q", lines[1])
	want := []string{readersAllRow}
	for _, c := range cols {
		want = append(want, strconv.Itoa(sums[c]))
	}
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
		"all     |   111 |     222 | 333 |      ?\n", got)
	require.Len(t, tb.Rows, 3, "the table read is not changed")
	assert.NotEqual(t, ntable.None, tb.Columns[0].Fold, "the columns read are not changed")
}
