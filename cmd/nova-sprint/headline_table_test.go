package main

import (
	"strconv"
	"strings"
	"testing"
	"time"

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

// epochCounts is where --json's landed and all cards of the epoch: the headline's, the archived
// streams' added unless the sprint is done, when the headline is the epoch's already.
func epochCounts(w whereView) (landed, all int64) {
	if w.Done {
		return w.Landed, w.All
	}
	return w.Landed + w.ArchivedLanded, w.All + w.ArchivedCards
}

// headlineTwoStreams is a running twin with s1 landed (two cards, $3.00, archived by the
// tick as its last card landed) and s2 (three flash cards) on the table.
func headlineTwoStreams(t *testing.T) *testApp {
	t.Helper()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1:8")
	ta.m.SetRoutes(costRoutes())
	ta.readersReadPro() // a fleet row reads flash unless it says more (sprint fleetReadsFlashOnly)
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

// The ETA's guard of five landed cards counts the epoch's landings, an archived stream's too:
// with three cards on the table and five landed in an archived stream, the rate is known and
// the ETA is a number, not a dash. And an archive, which changes no card left to land, keeps
// the held estimate: the hold's key is the epoch's cards and the held ones.
func TestTheETAGuardAndHoldSeeTheEpochsLandings(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1:8")
	ta.ok("add --stream s1 --count 5")
	ta.ok("add --stream s2 --count 3 --held")
	ta.ok("start")
	five := []string{"", "", "", "", ""}
	ta.landStream("s1", five, five, five)
	ta.a.sleep(30 * time.Minute) // a rate: five landed over half an hour of running

	var v whereView
	ta.json("where", &v)
	require.NotNil(t, v.Archived, "the tick archived s1 as its last card landed")
	require.Equal(t, [2]int64{0, 3}, [2]int64{v.Landed, v.All})
	assert.True(t, strings.HasPrefix(v.Summary, "0/3 0.0% held=3 -> ETA "), v.Summary)
	assert.NotContains(t, v.Summary, "ETA -", "five landed in the epoch: the rate is known")

	ta.ok("stream unarchive s1")
	ta.ok("where")
	key := ta.a.etaKey
	ta.ok("stream archive s1")
	ta.ok("where")
	assert.Equal(t, key, ta.a.etaKey, "an archive changes no card left to land: the held estimate stays")
	ta.clean()
}

// An add to an archived stream mid-epoch, and the moment before the next tick: every number
// is backed by a row. On a RUNNING machine the add reaches the table at the next tick's drain,
// so until then nothing moves (s1 archived, 0/3); on a STOPPED one the add is placed at once,
// and s1 is back on the table at once, before any tick draws its row: where draws the row,
// its footer counts it, --json's tables and rows carry it and archived no longer names it.
// The tick changes none of it.
func TestAnAddToAnArchivedStreamIsBackedByItsRow(t *testing.T) {
	t.Parallel()
	check := func(t *testing.T, ta *testApp, when string, back bool) {
		t.Helper()
		var w whereView
		ta.json("where --rows", &w)
		rows, under := ta.workRowsDrawn()
		var s1 int
		for _, r := range w.Rows {
			if r.Stream == "s1" {
				s1++
			}
		}
		if !back {
			require.NotNil(t, w.Archived, "%s: s1 still archived", when)
			assert.Equal(t, [4]int64{0, 3, 2, 2}, [4]int64{w.Landed, w.All, w.ArchivedCards, w.ArchivedLanded}, when)
			assert.NotContains(t, w.Tables["work"], "s1", when)
			assert.Zero(t, s1, when)
			assert.Equal(t, []string{"s2"}, rows, when)
			assert.NotEmpty(t, under, when)
			assert.Equal(t, "0", ta.workFooter()["landed"], when)
			return
		}
		assert.Nil(t, w.Archived, "%s: s1 is archived no more", when)
		assert.Equal(t, [4]int64{2, 6, 0, 0}, [4]int64{w.Landed, w.All, w.ArchivedCards, w.ArchivedLanded}, when)
		assert.Contains(t, w.Tables["work"], "s1", "%s: the row backs the count", when)
		assert.Equal(t, 3, s1, "%s: --rows carries s1's cards", when)
		assert.Equal(t, []string{"s1", "s2"}, rows, "%s: the frame draws it", when)
		assert.Empty(t, under, when)
		assert.Equal(t, "2", ta.workFooter()["landed"], when)
	}
	t.Run("running", func(t *testing.T) {
		t.Parallel()
		ta := headlineTwoStreams(t)
		check(t, ta, "archived", false)
		ta.ok("add --stream s1 --count 1 --one")
		check(t, ta, "added, before the tick's drain", false)
		ta.ok("tick")
		check(t, ta, "after the tick", true)
		ta.clean()
	})
	t.Run("stopped", func(t *testing.T) {
		t.Parallel()
		ta := headlineTwoStreams(t)
		ta.ok("stop --reason r --until 9999h")
		check(t, ta, "archived", false)
		out := ta.ok("add --stream s1 --count 1 --one")
		assert.Contains(t, out, "2/6 33.3%", "the add's sprint line counts s1 again")
		check(t, ta, "added, before any tick", true)
		ta.ok("tick")
		check(t, ta, "after the tick", true)
		ta.clean()
	})
}

// A sprint done says the same in every source: the CLI's done line, where --json (done, and
// landed and all over the epoch's cards, as the done line counts them) and the summary.
func TestASprintDoneReadsTheSameEverywhere(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1:8")
	ta.ok("add --stream s1 --count 2")
	ta.ok("start")
	two := []string{"", ""}
	ta.landStream("s1", two, two, two)
	out := ta.ok("tick")
	var v whereView
	ta.json("where", &v)
	require.NotNil(t, v.Archived, "the tick archived every stream of the sprint done")
	assert.True(t, v.Done)
	assert.Equal(t, [2]int64{2, 2}, [2]int64{v.Landed, v.All}, "done: the epoch's cards")
	assert.Equal(t, "2/2 100.0% done", v.Summary)
	assert.Contains(t, out, "2/2 100.0% done", "the CLI's done line")
	ta.clean()
}
