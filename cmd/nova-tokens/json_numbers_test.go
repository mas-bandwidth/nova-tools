package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// itemFields is the fields of the first item of a kind, or a failure when the object
// carries none: a type assertion over a missing row would pass by checking nothing.
func itemFields(t *testing.T, j jsonResult, kind string) map[string]any {
	t.Helper()
	for _, it := range j.Items {
		if it.Kind == kind {
			return it.Fields
		}
	}
	require.FailNow(t, "no item of the kind", "the --json object carries no %q item: %+v", kind, j.Items)
	return nil
}

// TestJSONCountsAreNumbersAndAbsentCountsAreNull pins the two type rules docs/STANDARD.md
// section 2 gives one value's two renderings: a count is a JSON number, and a count the
// source never measured is JSON null -- the dash stays the TEXT rendering (rule 15: a type
// the source did not report is a dash, never 0), and it must not become the string "-" or
// the string "1" in the object a program reads (sum's input and turns were strings beside
// days as a number; fold's SOURCE item carried files as a string).
func TestJSONCountsAreNumbersAndAbsentCountsAreNull(t *testing.T) {
	t.Parallel()

	dir := foldedBench(t)
	out, repos, tr := filepath.Join(dir, "out"), filepath.Join(dir, "repos.tsv"), filepath.Join(dir, "transcripts")

	// The text line keeps the dash: this step changes the object, not the line.
	text := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--claude", "bench="+tr, "--dry-run")
	wantExit(t, text, 0)
	wantContains(t, text.stdout, "nousage=-")

	_, got := asJSON(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--claude", "bench="+tr, "--dry-run", "--json")
	src := itemFields(t, got, "source")
	assert.Equal(t, 1.0, src["files"], "files is a number")
	assert.Equal(t, 0.0, src["unreadable"], "unreadable is a number")
	assert.Equal(t, 3.0, src["messages"], "messages is a number")
	assert.Equal(t, 2.0, src["rows"], "rows is a number")
	for _, absent := range []string{"nousage", "unparsed", "comments", "redated", "superseded"} {
		assert.Nil(t, src[absent], "%s is a count this kind never measured, and an absent count is null", absent)
	}
	day := itemFields(t, got, "day")
	assert.Equal(t, 3.0, day["turns"], "turns is a number")

	// sum: a cell a row measured is a number, a cell no row measured is null.
	_, sum := asJSON(t, "sum", "--out", out, "--month", "2026-09", "--json")
	pairs := 0
	for _, it := range sum.Items {
		if it.Kind != "pair" {
			continue
		}
		pairs++
		_, isNumber := it.Fields["input"].(float64)
		assert.True(t, isNumber, "sum's pair input is a number, got %T (%v)", it.Fields["input"], it.Fields["input"])
		assert.Nil(t, it.Fields["reasoning"], "a cell no row of the pair measured is null")
	}
	require.Greater(t, pairs, 0, "the sum object carries no pair item; the fixture checked nothing")

	// sources: the tally's total is a number when the flag asked for it and null when not.
	_, plain := asJSON(t, "sources", "--repos", repos, "--all", "--claude", "bench="+tr, "--json")
	assert.Nil(t, plain.Facts["unattributed"], "a tally not asked for is null")
	_, asked := asJSON(t, "sources", "--repos", repos, "--all", "--claude", "bench="+tr, "--unattributed", "--json")
	assert.Equal(t, 0.0, asked.Facts["unattributed"], "a tally asked for is a number")
}
