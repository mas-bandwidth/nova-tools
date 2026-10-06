package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stream archive end to end on the twin (the owner, 2026-10-05: "I would like you to
// remove all the already landed work streams"): the tick archives a stream whose last
// card landed; where draws the live streams and one line for the archived; where --json
// drops their rows unless --archived and counts them in landed and all; stream unarchive
// and stream archive move them on a RUNNING machine; a stream with a card not landed is
// refused, named.
func TestStreamArchiveTakesLandedStreamsOffWhereAndKeepsTheirCount(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1:8")
	ta.m.SetRoutes(costRoutes())
	ta.ok("add --stream s1 --count 2 --brief-file " + proBriefFile(t))
	ta.ok("add --stream s2 --count 1 --one --brief-file " + proBriefFile(t))
	for _, id := range []string{"s1-1", "s1-2", "s2-1"} {
		ta.tierNow(id, "pro")
	}
	ta.ok("start")
	ta.landStream("s1",
		[]string{"input=10 actual_usd=0.5 actual_by=harness", "input=10 actual_usd=0.25 actual_by=harness"},
		[]string{"", ""}, []string{"", ""})

	type view struct {
		Landed, All int64
		Tables      map[string]map[string]map[string]any
		StreamCosts map[string]any `json:"stream_costs"`
		Streams     []struct{ Stream string }
		Rows        []struct{ ID, Stream string }
		Archived    *struct {
			Streams []string
			Landed  int64
			Cost    string
		}
	}
	streamsOf := func(v view) []string {
		var out []string
		for _, s := range v.Streams {
			out = append(out, s.Stream)
		}
		return out
	}
	var v view
	ta.json("where --rows", &v)
	require.NotNil(t, v.Archived, "the tick archived s1, its last card landed")
	assert.Equal(t, []string{"s1"}, v.Archived.Streams)
	assert.Equal(t, int64(2), v.Archived.Landed)
	assert.Equal(t, "$0.75", v.Archived.Cost)
	assert.Equal(t, int64(2), v.Landed, "landed counts the archived")
	assert.Equal(t, int64(3), v.All)
	assert.NotContains(t, v.Tables["work"], "s1")
	assert.NotContains(t, v.Tables["merge"], "s1")
	assert.Contains(t, v.Tables["work"], "s2")
	assert.NotContains(t, v.StreamCosts, "s1")
	assert.NotContains(t, streamsOf(v), "s1")
	for _, r := range v.Rows {
		assert.NotEqual(t, "s1", r.Stream, r.ID)
	}

	var all view
	ta.json("where --rows --archived", &all)
	assert.Contains(t, all.Tables["work"], "s1", "--archived keeps them")
	assert.Contains(t, all.Tables["merge"], "s1")
	assert.Contains(t, all.StreamCosts, "s1")
	assert.Contains(t, streamsOf(all), "s1")
	var rows []string
	for _, r := range all.Rows {
		if r.Stream == "s1" {
			rows = append(rows, r.ID)
		}
	}
	assert.Equal(t, []string{"s1-1", "s1-2"}, rows)
	assert.Equal(t, v.Landed, all.Landed)

	text := ta.ok("where")
	assert.Contains(t, text, "1 archived stream, 2 cards landed, $0.75\n")
	for _, l := range strings.Split(text, "\n") {
		assert.False(t, strings.HasPrefix(l, "s1 "), "the work table draws no s1: %q", l)
	}
	code, _, errs := ta.do("where --archived")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--archived is a field of the JSON view")

	// a stream with a card not landed is refused, named; all or none
	code, _, errs = ta.do("stream archive s2")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "s2-1 (")
	code, _, errs = ta.do("stream unarchive s9")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "no stream s9")

	// unarchive and archive on the RUNNING machine
	assert.Equal(t, "STREAM-UNARCHIVE OK streams=s1\n", ta.ok("stream unarchive s1"))
	ta.ok("tick")
	var shown view
	ta.json("where", &shown)
	assert.Nil(t, shown.Archived, "unarchived, and the tick leaves it drawn")
	assert.Contains(t, shown.Tables["work"], "s1")
	assert.Equal(t, "STREAM-ARCHIVE OK streams=s1\n", ta.ok("stream archive s1"))
	var again view
	ta.json("where", &again)
	require.NotNil(t, again.Archived)
	assert.Equal(t, []string{"s1"}, again.Archived.Streams)
	assert.Equal(t, int64(2), again.Landed)
}
