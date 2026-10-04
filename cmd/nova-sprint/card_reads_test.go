package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// cardReads counts whole reads of the work table and reads of the epoch log.
type cardReads struct {
	*store.Mem
	work int
	log  int
}

func (c *cardReads) CellIDs(ctx context.Context, shapes []ntable.Table) (map[string][]string, error) {
	if wholeWork(shapes) {
		c.work++
	}
	return c.Mem.CellIDs(ctx, shapes)
}

func (c *cardReads) LogSince(ctx context.Context, after string, max int) ([]sprint.Line, []string, error) {
	c.log++
	return c.Mem.LogSince(ctx, after, max)
}

// wholeWork says this exchange reads the work table's set columns, not one
// column of it.
func wholeWork(shapes []ntable.Table) bool {
	for _, s := range shapes {
		if s.Name != sprint.Work {
			continue
		}
		n := 0
		whole := true
		for _, col := range s.Columns {
			if !col.HasSet() {
				continue
			}
			if col.Projection == ntable.Text {
				whole = false
				break
			}
			n++
		}
		if whole && n >= 2 {
			return true
		}
	}
	return false
}

// card reads the work table whole once and does not read the epoch log. The
// story, the place, the needs and what holds the card still come from that read.
func TestCardReadsTheWorkTableOnceAndNotTheWholeLog(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1,m2")
	ta.ok("add --stream s1 --count 3")
	ta.ok("add --stream s1 b --needs s1-1")
	ta.ok("add --stream s2 --count 2")
	gate := &cardReads{Mem: ta.m}
	ta.a.backend = func(context.Context, string, sprint.Names) (store.Backend, error) { return gate, nil }

	run := func(line string) string {
		t.Helper()
		gate.work, gate.log = 0, 0
		var out, errb bytes.Buffer
		code := ta.a.run(split(line), &out, &errb)
		require.Equal(t, 0, code, "%s\n%s%s", line, out.String(), errb.String())
		assert.Equal(t, 1, gate.work, "whole work-table reads of %s", line)
		assert.Zero(t, gate.log, "epoch-log reads of %s", line)
		return out.String()
	}

	out := run("card b")
	assert.Contains(t, out, "added to")
	assert.Contains(t, out, "needs s1-1")
	assert.Contains(t, out, "in line")
	assert.Contains(t, out, "CARD OK")
	assert.Contains(t, out, "\nnow:\n")

	out = run("card s1-1")
	assert.Contains(t, out, "needed by b")
	assert.Contains(t, out, "added to")
	assert.Contains(t, out, "CARD OK")

	out = run("card b")
	assert.True(t, strings.Contains(out, "CARD OK"), "the second call still tells the card")

	out = run("card b --json")
	assert.Contains(t, out, "added to")
	assert.Contains(t, out, `"needs"`)

	out = run("card b --fields")
	assert.Contains(t, out, "NEEDS s1-1")
	assert.Contains(t, out, "CARD OK")
	assert.Contains(t, out, "HELD ")
}
