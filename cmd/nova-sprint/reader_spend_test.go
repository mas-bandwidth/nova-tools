package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The readers table carries each reader's spend, and where --json each reader's row the same
// cells (the owner, 2026-10-05: "I would ask that you need to track spend on readers, can you
// do this before we start?"): a pro card read by two readers with their harnesses' costs, one
// of them a return first, shows each reader's sum all time and in the last hour, per priced
// read and how many were priced, from the tick's where record; the one text row sums them.
func TestWhereReadersCarryEachReadersSpend(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1:8")
	ta.ok("add --stream s1 --count 1 --one --brief-file " + proBriefFile(t))
	ta.ok("start")
	ta.ok("tick")
	ta.ok("take --as m1 s1-1.w1@1")
	ta.ok("finish --as m1 s1-1.w1@1 --usage 'input=1 actual_usd=0.1 actual_by=harness'")
	ta.ok("ask")
	ta.ok("ask s1-1 --another")
	var asked []string
	for _, rd := range []string{"reader-a", "reader-b", "reader-c"} {
		if code, _, _ := ta.do("read --as " + rd + " --begin s1-1.r1." + rd); code == 0 {
			asked = append(asked, rd)
		}
	}
	require.Len(t, asked, 2)
	first := asked[0]
	ta.ok("read --as " + first + " --return s1-1.r1." + first + " --reason 'no verdict' --usage 'input=1 actual_usd=0.02 actual_by=harness'")
	ta.ok("tick")
	var read []string
	for _, rd := range []string{"reader-a", "reader-b", "reader-c"} {
		if rd == first {
			continue
		}
		ta.ok("read --as " + rd + " --ok s1-1.r1." + rd + " --usage 'input=1 actual_usd=0.003 actual_by=harness'")
		read = append(read, rd)
	}
	ta.ok("tick") // the where record is the tick's

	var v struct {
		Tables map[string]map[string]map[string]string
	}
	ta.json("where", &v)
	rows := v.Tables[sprint.Readers]
	want := map[string]map[string]string{
		first:   {sprint.ReaderSpendCol: "$0.02", sprint.ReaderSpendHourCol: "$0.02", sprint.ReaderPerReadCol: "$0.02", sprint.ReaderPricedCol: "1"},
		read[0]: {sprint.ReaderSpendCol: "$0.01", sprint.ReaderSpendHourCol: "$0.01", sprint.ReaderPerReadCol: "$0.01", sprint.ReaderPricedCol: "1"},
		read[1]: {sprint.ReaderSpendCol: "$0.01", sprint.ReaderSpendHourCol: "$0.01", sprint.ReaderPerReadCol: "$0.01", sprint.ReaderPricedCol: "1"},
	}
	for rd, cells := range want {
		for col, cell := range cells {
			assert.Equal(t, cell, rows[rd][col], "where --json: %s's %s", rd, col)
		}
	}

	block := tableOf(ta.ok("where --all"), "readers")
	lines := strings.Split(strings.TrimRight(block, "\n"), "\n")
	require.Len(t, lines, 3, block)
	head, row := cells(lines[0]), cells(lines[2])
	at := func(col string) string { return row[indexOf(head, col)] }
	assert.Equal(t, "$0.03", at(sprint.ReaderSpendCol), "0.02 + 0.003 + 0.003, rounded up once:\n%s", block)
	assert.Equal(t, "$0.03", at(sprint.ReaderSpendHourCol), block)
	assert.Equal(t, "$0.01", at(sprint.ReaderPerReadCol), "0.026 over three priced reads:\n%s", block)
	assert.Equal(t, "3", at(sprint.ReaderPricedCol), block)
}

// indexOf is the index of a cell in a line's cells, -1 when absent.
func indexOf(cells []string, cell string) int {
	for i, c := range cells {
		if c == cell {
			return i
		}
	}
	return -1
}
