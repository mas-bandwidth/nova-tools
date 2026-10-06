package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cardProbe counts a card read's log and row touches. LogSince from "" is the
// whole log; CellIDs is a table read whole.
type cardProbe struct {
	*store.Mem
	logFromStart int
	logLines     int
	readSetIDs   int
	cellIDs      int
}

func (p *cardProbe) LogSince(ctx context.Context, after string, max int) ([]sprint.Line, []string, error) {
	lines, ids, err := p.Mem.LogSince(ctx, after, max)
	if after == "" {
		p.logFromStart++
	}
	p.logLines += len(lines)
	return lines, ids, err
}

func (p *cardProbe) ReadSet(ctx context.Context, table string, ids []string) (ntable.ReadSetResult, error) {
	p.readSetIDs += len(ids)
	return p.Mem.ReadSet(ctx, table, ids)
}

func (p *cardProbe) CellIDs(ctx context.Context, shapes []ntable.Table) (map[string][]string, error) {
	p.cellIDs++
	return p.Mem.CellIDs(ctx, shapes)
}

// TestCardReadTouchesOnlyItsOwnLogAndRows fails if card <id> --json reads the
// epoch's log from the start or the work table's cells.
func TestCardReadTouchesOnlyItsOwnLogAndRows(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	const streams = 30
	for i := 1; i <= streams; i++ {
		ta.ok(fmt.Sprintf("add --stream s%d --count 1 --one", i))
	}
	ta.ok("tick")
	var total int
	after := ""
	for {
		lines, ids, err := ta.m.LogSince(context.Background(), after, 5000)
		require.NoError(t, err)
		total += len(lines)
		if len(ids) < 5000 {
			break
		}
		after = ids[len(ids)-1]
	}
	require.GreaterOrEqual(t, total, streams, "the log has %d lines; a whole-log read would not show", total)

	p := &cardProbe{Mem: ta.m}
	ta.a.backend = func(context.Context, string, sprint.Names) (store.Backend, error) { return p, nil }
	out := ta.ok("card s1-1 --json")
	var view cardView
	require.NoError(t, json.Unmarshal([]byte(out), &view), "card --json: %s", out)
	assert.NotNil(t, view.Primary, "card --json: %s", out)
	assert.NotEmpty(t, view.Timeline, "the card's own lines are in the story: %s", out)
	assert.Zero(t, p.logFromStart, "card read the log from the start (%d lines of %d)", p.logLines, total)
	assert.Less(t, p.logLines, total/2, "card read %d log lines of %d", p.logLines, total)
	assert.Zero(t, p.cellIDs, "card read a table's cells")
	assert.Greater(t, p.readSetIDs, 0, "card read no row")
	assert.Less(t, p.readSetIDs, 12, "card read %d rows; the sprint has %d cards", p.readSetIDs, streams)
	t.Logf("card s1-1 --json: log lines read %d of %d, from start %d, cell-id reads %d, read-set ids %d",
		p.logLines, total, p.logFromStart, p.cellIDs, p.readSetIDs)
}

// card --all --json and card --stream --json are one object a line.
func TestCardAllPrintsOneJSONObjectALine(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	brief := writeBrief(t, "handle the empty case")
	ta.ok("add --stream s1 --count 1 --one --brief-file " + brief)
	ta.ok("add --stream s2 b --one --needs s1-1 --brief-file " + brief)
	out := ta.ok("card --all --json")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.GreaterOrEqual(t, len(lines), 2, "card --all --json:\n%s", out)
	var sawS1, sawB bool
	for _, line := range lines {
		var row cardBulkLine
		require.NoError(t, json.Unmarshal([]byte(line), &row), "line: %s", line)
		assert.NotContains(t, line, "handle the empty case", "the brief body is in the line: %s", line)
		_, hasBrief := row.Fields["brief"]
		assert.False(t, hasBrief, "the brief field is in the line: %s", line)
		assert.Greater(t, row.BriefLen, 0, "brief_len: %s", line)
		if row.ID == "s1-1" {
			sawS1 = true
			assert.Equal(t, "s1", row.Stream, line)
			assert.NotEmpty(t, row.Column, line)
		}
		if row.ID == "b" {
			sawB = true
			assert.Equal(t, "s2", row.Stream, line)
			assert.Contains(t, row.Needs, "s1-1", line)
		}
	}
	assert.True(t, sawS1, "card --all --json has no s1-1:\n%s", out)
	assert.True(t, sawB, "card --all --json has no b:\n%s", out)

	one := strings.Split(strings.TrimSpace(ta.ok("card --stream s2 --json")), "\n")
	require.Len(t, one, 1, "card --stream s2 --json:\n%s", strings.Join(one, "\n"))
	var row cardBulkLine
	require.NoError(t, json.Unmarshal([]byte(one[0]), &row), one[0])
	assert.Equal(t, "b", row.ID, one[0])

	code, _, errs := ta.do("card --all")
	assert.Equal(t, 2, code, errs)
	assert.Contains(t, errs, "--json", errs)
	code, _, errs = ta.do("card --all s1-1 --json")
	assert.Equal(t, 2, code, errs)
	assert.Contains(t, errs, "not both", errs)
	code, _, errs = ta.do("card")
	assert.Equal(t, 2, code, errs)
	assert.Contains(t, errs, "wants one primary id, found none", errs)
}
