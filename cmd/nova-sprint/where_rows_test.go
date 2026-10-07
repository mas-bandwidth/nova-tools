package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// where --json --rows carries every primary's row of the work table in one call,
// its fields but the brief, so a child never loops card calls (the comfort list of
// 2026-10-03, item 8); the text frame takes no --rows.
func TestWhereRowsCarriesEveryPrimary(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --one --stream b --count 1 --brief-file " + writeBrief(t, "the work"))
	ta.ok("add --stream a a-1 a-2")
	ta.ok("add --one --stream a a-3 --needs a-1")

	var v struct{ Rows []primaryRow }
	ta.json("where --rows", &v)
	require.Len(t, v.Rows, 4)
	ids := []string{}
	for _, r := range v.Rows {
		ids = append(ids, r.ID)
	}
	assert.Equal(t, []string{"a-1", "a-2", "a-3", "b-1"}, ids, "work order: stream, then score")
	assert.Equal(t, "waiting", v.Rows[2].State)
	assert.Equal(t, "a-1", v.Rows[2].Fields["needs"])
	assert.Equal(t, "a", v.Rows[2].Stream)
	assert.Equal(t, "ready", v.Rows[3].State)
	assert.NotContains(t, v.Rows[3].Fields, "brief", "the brief is card --brief's")
	assert.Equal(t, "primary", v.Rows[3].Fields["kind"])

	var plain struct{ Rows []primaryRow }
	ta.json("where", &plain)
	assert.Empty(t, plain.Rows, "the rows are --rows's alone")
	code, _, errs := ta.do("where --rows")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--rows is a field of the JSON view: give --json with it")
}

// A stream the coordinator holds is marked on its work row, in the frame and in
// where --json's table row: the fix of 2026-10-07, when a card's story said
// "stream X is held" and where marked no stream.
func TestWhereMarksAHeldStreamRow(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream a --count 2")
	ta.ok("hold a --reason red")

	out := ta.ok("where")
	assert.Contains(t, out, "held red", "the work row carries the hold and its reason")

	var v struct {
		Tables map[string]map[string]map[string]any `json:"tables"`
	}
	ta.json("where", &v)
	assert.Equal(t, "red", v.Tables["work"]["a"]["held"])
}
