package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stream archive end to end on the twin (the owner, 2026-10-05: "I would like you to remove
// all the already landed work streams"): the tick archives a stream whose last card
// landed; where leaves it out and says it in one line, where --archived shows it, and the
// summary counts it either way; the verbs archive and unarchive on a RUNNING machine,
// and a stream with a card not landed is refused naming it.
func TestStreamArchiveTakesLandedStreamsOffWhereAndKeepsTheirRecord(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1:8")
	ta.m.SetRoutes(costRoutes())
	ta.ok("add --stream s1 --count 2 --brief-file " + proBriefFile(t))
	ta.ok("add --one --stream s2 --count 1 --brief-file " + proBriefFile(t))
	for _, id := range []string{"s1-1", "s1-2", "s2-1"} {
		ta.tierNow(id, "pro")
	}
	ta.ok("start")
	ta.landStream("s1", []string{"input=10 actual_usd=1 actual_by=harness", "input=10 actual_usd=2 actual_by=harness"}, []string{"", ""}, []string{"", ""})

	// the tick took s1 off: where leaves it out, says it in one line, and counts it
	var live, all whereView
	ta.json("where", &live)
	ta.json("where --archived", &all)
	require.NotNil(t, live.Archived)
	assert.Equal(t, []string{"s1"}, live.Archived.Streams)
	assert.Equal(t, int64(2), live.Archived.Landed)
	assert.Equal(t, "$3.00", live.Archived.Cost)
	assert.NotContains(t, live.Tables["work"], "s1")
	assert.NotContains(t, live.Tables["merge"], "s1")
	assert.NotContains(t, live.StreamCosts, "s1")
	assert.Contains(t, live.Tables["work"], "s2")
	assert.Equal(t, "$3.00", all.Tables["work"]["s1"]["cost"])
	assert.Contains(t, all.Tables["merge"], "s1")
	assert.Equal(t, all.Landed, live.Landed, "the summary counts the archived stream")
	assert.Equal(t, all.All, live.All)
	assert.Equal(t, all.Summary, live.Summary)
	assert.Equal(t, int64(2), live.Landed)
	frame := ta.ok("where")
	assert.Contains(t, frame, "1 archived stream, 2 cards landed, $3.00 (where --archived)")
	assert.False(t, hasRow(frame, "s1"), "where's frame: %s", frame)
	assert.True(t, hasRow(ta.ok("where --archived"), "s1"))
	assert.NotContains(t, ta.ok("where --archived"), "archived stream,")

	// --rows: the archived stream's cards only with --archived
	var rows, allRows whereView
	ta.json("where --rows", &rows)
	ta.json("where --rows --archived", &allRows)
	streams := func(v whereView) map[string]int {
		out := map[string]int{}
		for _, r := range v.Rows {
			out[r.Stream]++
		}
		return out
	}
	assert.Equal(t, map[string]int{"s2": 1}, streams(rows))
	assert.Equal(t, map[string]int{"s1": 2, "s2": 1}, streams(allRows))

	// the verbs, on the RUNNING machine
	assert.Contains(t, ta.ok("stream unarchive s1"), "STREAM-UNARCHIVE OK streams=s1")
	assert.True(t, hasRow(ta.ok("where"), "s1"))
	ta.ok("tick")
	assert.True(t, hasRow(ta.ok("where"), "s1"), "the tick leaves a stream unarchived by hand")
	code, _, errs := ta.do("stream archive s1 s2")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "s2: stream s2 holds 1 card not landed: s2-1 (")
	assert.Contains(t, errs, "s1: not archived: the verb names several and applies to all or none")
	assert.True(t, hasRow(ta.ok("where"), "s1"), "refused: nothing changed")
	assert.Contains(t, ta.ok("stream archive s1"), "STREAM-ARCHIVE OK streams=s1")
	assert.False(t, hasRow(ta.ok("where"), "s1"))
	code, _, errs = ta.do("stream unarchive s2")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "stream s2 is not archived")

	// a clear keeps it archived
	ta.ok("stop --reason r --until 9999h")
	ta.ok("clear --confirm sprint")
	var cleared whereView
	ta.json("where", &cleared)
	require.NotNil(t, cleared.Archived)
	assert.Equal(t, []string{"s1"}, cleared.Archived.Streams)
	assert.False(t, hasRow(ta.ok("where"), "s1"))
	assert.True(t, hasRow(ta.ok("where"), "s2"))
}

// hasRow says where's frame draws a row of the stream.
func hasRow(frame, stream string) bool {
	for _, l := range strings.Split(frame, "\n") {
		if strings.HasPrefix(l, stream+" ") {
			return true
		}
	}
	return false
}
