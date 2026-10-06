package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// workRowsDrawn is the stream rows of the work table where draws, and the line under it.
func (ta *testApp) workRowsDrawn() (rows []string, under string) {
	ta.t.Helper()
	in := false
	for _, l := range strings.Split(ta.ok("where"), "\n") {
		f := strings.Split(l, "|")
		switch {
		case strings.HasPrefix(l, "work "):
			in = true
		case in && strings.TrimSpace(l) == "":
			return rows, under
		case in && strings.HasPrefix(strings.TrimSpace(l), "-"):
		case in && len(f) == 1:
			under = l
		case in && strings.TrimSpace(f[0]) != "":
			rows = append(rows, strings.TrimSpace(f[0]))
		}
	}
	return rows, under
}

// stream archive and unarchive end to end on the twin (the owner, 2026-10-05: "I would like
// you to remove all the already landed work streams"): on a RUNNING machine, refused for a
// stream with a card not landed, naming it; the archived stream leaves the drawn table and
// where --json's tables and rows, one line under the work table counts it, --archived keeps
// its row, and the summary leaves it, archived_cards and archived_landed carrying its cards
// (the owner, 2026-10-06: the headline counts only the streams on the table); unarchive
// draws it again.
func TestStreamArchiveTakesALandedStreamOffTheTable(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1:8")
	ta.m.SetRoutes(costRoutes())
	ta.readersReadPro() // a fleet row reads flash unless it says more (sprint fleetReadsFlashOnly)
	ta.ok("add --stream s1 --count 2 --brief-file " + proBriefFile(t))
	ta.ok("add --one --stream s2 --count 1 --brief-file " + writeBrief(t, "a flash card, tier: flash"))
	for _, id := range []string{"s1-1", "s1-2"} {
		ta.tierNow(id, "pro")
	}
	ta.ok("start")
	ta.landStream("s1", []string{"input=10 actual_usd=1 actual_by=harness", "input=10 actual_usd=2 actual_by=harness"}, []string{"", ""}, []string{"", ""})
	// the tick archived s1 as its last card landed; brought back by hand, it stays
	ta.ok("stream unarchive s1")
	ta.ok("tick")
	var before whereView
	ta.json("where --rows", &before)
	require.Nil(t, before.Archived)
	require.Contains(t, before.Tables["work"], "s1")
	footer := ta.costCells()[""]

	code, _, errs := ta.do("stream archive s1 s2")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "s2: stream s2 holds 1 card not landed (s2-1 ")
	assert.Contains(t, errs, "s1: not archived: the verb names several and applies to all or none")
	rows, _ := ta.workRowsDrawn()
	assert.Equal(t, []string{"s1", "s2"}, rows, "all or none: nothing changed")

	assert.Contains(t, ta.ok("stream archive s1"), "STREAM-ARCHIVE OK streams=s1")
	rows, under := ta.workRowsDrawn()
	assert.Equal(t, []string{"s2"}, rows, "the archived stream leaves the drawn table")
	assert.Equal(t, "1 archived stream, 2 cards landed, $3.00 (where --json --archived)", under)
	var v whereView
	ta.json("where --rows", &v)
	assert.NotContains(t, v.Tables["work"], "s1")
	assert.NotContains(t, v.Tables["merge"], "s1")
	for _, r := range v.Rows {
		assert.NotEqual(t, "s1", r.Stream, "an archived stream's primaries are in --rows only with --archived")
	}
	require.NotNil(t, v.Archived)
	assert.Equal(t, archivedView{Streams: []string{"s1"}, Cards: 2, Landed: 2, Cost: "$3.00"}, *v.Archived)
	assert.Equal(t, before.Landed-2, v.Landed, "the summary leaves it")
	assert.Equal(t, before.All-2, v.All)
	assert.Equal(t, [2]int64{2, 2}, [2]int64{v.ArchivedCards, v.ArchivedLanded})
	assert.NotEqual(t, before.Summary, v.Summary)
	assert.Equal(t, before.StreamCosts["s1"], v.StreamCosts["s1"], "its costs stay in stream_costs")
	var all whereView
	ta.json("where --rows --archived", &all)
	assert.Equal(t, before.Tables["work"]["s1"], all.Tables["work"]["s1"])
	assert.Equal(t, before.Rows, all.Rows, "--archived carries every row")
	code, _, errs = ta.do("where --archived")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--archived is a field of the JSON view")

	assert.Contains(t, ta.ok("stream unarchive s1"), "STREAM-UNARCHIVE OK streams=s1")
	rows, under = ta.workRowsDrawn()
	assert.Equal(t, []string{"s1", "s2"}, rows)
	assert.Empty(t, under)
	assert.Equal(t, footer, ta.costCells()[""], "the total is unchanged throughout")
	code, _, errs = ta.do("stream unarchive s1")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "stream s1 is not archived")
	ta.clean()
}
