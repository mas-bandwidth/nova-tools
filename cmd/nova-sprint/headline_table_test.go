package main

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// workFooter is the drawn work table's footer, column name -> cell: the line of the
// table whose label cell is blank.
func (ta *testApp) workFooter() map[string]string {
	ta.t.Helper()
	var head []string
	for _, l := range strings.Split(ta.ok("where"), "\n") {
		f := strings.Split(l, "|")
		switch {
		case strings.HasPrefix(l, "work "):
			for _, h := range f {
				head = append(head, strings.TrimSpace(h))
			}
		case head == nil || strings.HasPrefix(strings.TrimSpace(l), "-") || len(f) != len(head):
		case strings.TrimSpace(f[0]) == "":
			out := map[string]string{}
			for i, c := range f {
				out[head[i]] = strings.TrimSpace(c)
			}
			return out
		}
	}
	ta.t.Fatal("the work table has no footer")
	return nil
}

// headlineTwoStreams is a running twin with s1 landed (two cards, $3.00, archived by the
// tick as its last card landed) and s2 (three flash cards) on the table.
func headlineTwoStreams(t *testing.T) *testApp {
	t.Helper()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1:8")
	ta.m.SetRoutes(costRoutes())
	ta.ok("add --stream s1 --count 2 --brief-file " + proBriefFile(t))
	ta.ok("add --stream s2 --count 3 --brief-file " + writeBrief(t, "a flash card, tier: flash"))
	for _, id := range []string{"s1-1", "s1-2"} {
		ta.tierNow(id, "pro")
	}
	ta.ok("start")
	ta.landStream("s1", []string{"input=10 actual_usd=1 actual_by=harness", "input=10 actual_usd=2 actual_by=harness"}, []string{"", ""}, []string{"", ""})
	return ta
}

// The headline counts only the streams on the table (the owner, 2026-10-06 2:43 PM ET,
// after 69 landed streams were archived and the headline still read 1859/2874: "I really
// don't think we have 2.8k cards post-archive..."): landed N of M, the percent, the ETA and
// the work table's footer leave an archived stream's cards; where --json carries
// archived_cards and archived_landed beside them, so the whole epoch stays readable; a stream
// drawn again counts again.
func TestTheHeadlineCountsOnlyStreamsOnTheTable(t *testing.T) {
	t.Parallel()
	ta := headlineTwoStreams(t)

	var v whereView
	ta.json("where", &v)
	require.NotNil(t, v.Archived, "the tick archived s1 as its last card landed")
	require.Equal(t, []string{"s1"}, v.Archived.Streams)
	assert.Equal(t, int64(0), v.Landed, "the archived stream's landed cards leave the headline")
	assert.Equal(t, int64(3), v.All, "only s2's three cards are on the table")
	assert.True(t, strings.HasPrefix(v.Summary, "0/3 0.0%"), "the summary: %s", v.Summary)
	assert.Equal(t, int64(2), v.ArchivedCards)
	assert.Equal(t, int64(2), v.ArchivedLanded)
	assert.Contains(t, ta.ok("where"), "0/3 0.0%", "the text frame's headline")
	foot := ta.workFooter()
	assert.Equal(t, "0", foot["landed"], "the footer counts only the drawn streams")
	var open int
	for _, c := range []string{"waiting", "ready", "working", "review", "merging"} {
		n, err := strconv.Atoi(foot[c])
		require.NoError(t, err, "%s: %v", c, foot)
		open += n
	}
	assert.Equal(t, 3, open, "s2's three cards: %v", foot)
	assert.Equal(t, "-", foot["cost"], "the footer's cost leaves the archived stream's $3.00")

	ta.ok("stream unarchive s1")
	v = whereView{}
	ta.json("where", &v)
	assert.Nil(t, v.Archived)
	assert.Equal(t, int64(2), v.Landed, "drawn again, it counts again")
	assert.Equal(t, int64(5), v.All)
	assert.True(t, strings.HasPrefix(v.Summary, "2/5 40.0%"), "the summary: %s", v.Summary)
	assert.Zero(t, v.ArchivedCards)
	assert.Zero(t, v.ArchivedLanded)
	assert.Equal(t, "2", ta.workFooter()["landed"])
	assert.Contains(t, ta.ok("where --json"), `"archived_cards":0`, "the field is there at zero")

	ta.ok("stream archive s1")
	v = whereView{}
	ta.json("where", &v)
	assert.Equal(t, [4]int64{0, 3, 2, 2}, [4]int64{v.Landed, v.All, v.ArchivedCards, v.ArchivedLanded}, "archived by the verb, as by the tick")
	ta.clean()
}

// An archived stream's figures are kept, not lost: where --json's archived carries its
// cards, its landed cards and their cost, stream_costs keeps its spend, --archived its row
// as it was, and the headline and archived_* add up to the epoch's.
func TestArchiveKeepsTheStreamsFigures(t *testing.T) {
	t.Parallel()
	ta := headlineTwoStreams(t)
	ta.ok("stream unarchive s1")
	var before whereView
	ta.json("where --archived", &before)
	require.Nil(t, before.Archived)
	require.Contains(t, before.Tables["work"], "s1")

	ta.ok("stream archive s1")
	var v, all whereView
	ta.json("where", &v)
	require.NotNil(t, v.Archived)
	assert.Equal(t, archivedView{Streams: []string{"s1"}, Cards: 2, Landed: 2, Cost: "$3.00"}, *v.Archived)
	assert.Equal(t, before.All, v.All+v.ArchivedCards, "the epoch's cards: the table's and the archived")
	assert.Equal(t, before.Landed, v.Landed+v.ArchivedLanded, "the epoch's landed cards")
	assert.Equal(t, before.StreamCosts["s1"], v.StreamCosts["s1"], "its spend stays in stream_costs")
	ta.json("where --archived", &all)
	assert.Equal(t, before.Tables["work"]["s1"], all.Tables["work"]["s1"], "--archived carries its row as it was")
	assert.Equal(t, v.Summary, all.Summary, "--archived changes the rows carried, not the headline")
	ta.clean()
}
