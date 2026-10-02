package main

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// statsSprint plays s1-1 and s1-2 to landed on the twin, the clock stepped by hand:
// both dealt at 0; s1-1 taken at 2 and finished at 12 (wall 8); s1-2 taken at 14,
// failed by its provider at 16 (wall 1) and dealt again, taken at 20 and finished at
// 30 (wall 6); asked at 30; reader-a begins both at 33 and reads them at 53 (wall
// 15), reader-b reads both at 60 with no begin (wall 5); accepted at 70, landed at 75.
func statsSprint(t *testing.T) *testApp {
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.m.SetRoutes(costRoutes())
	ta.ok("add --stream s1 --count 2")
	ta.deal(2)
	usage := func(wall, model string) string {
		return " --usage 'wall=" + wall + " model=opencode/" + model + " actual_usd=0.001 actual_by=harness'"
	}
	step := func(s int) { ta.a.sleep(time.Duration(s) * time.Second) }
	step(2)
	ta.ok("take --as m1 s1-1.w1@1")
	step(10)
	ta.ok("finish --as m1 s1-1.w1@1" + usage("8.00s", "deepseek-v4-flash"))
	step(2)
	ta.ok("take --as m1 s1-2.w1@1")
	step(2)
	ta.ok("finish --as m1 s1-2.w1@1 --failed --report 'provider failure: 529 overloaded'" + usage("1.00s", "deepseek-v4-flash"))
	ta.deal(1)
	gen := ta.workGen("m1", "s1-2.w1")
	step(4)
	ta.ok("take --as m1 s1-2.w1@" + gen)
	step(10)
	ta.ok("finish --as m1 s1-2.w1@" + gen + usage("6.00s", "deepseek-v4-flash"))
	ta.ok("ask")
	step(3)
	ta.ok("read --as reader-a --begin --limit 2")
	step(20)
	ta.ok("read --as reader-a --ok --limit 2" + usage("15.00s", "deepseek-v4-pro"))
	step(7)
	ta.ok("read --as reader-b --ok --limit 2" + usage("5.00s", "deepseek-v4-pro"))
	step(10)
	ta.ok("accept --stream s1")
	step(5)
	ta.ok("merge --stream s1")
	ta.ok("tick")
	return ta
}

// stats prints each stage, member, reader and route with its median, max and count
// in seconds, from the cards' stamps and usage (the owner, 2026-10-01: the
// coordinator's per-pass script "should be a verb").
func TestStatsPrintsThePassFromTheCards(t *testing.T) {
	t.Parallel()
	ta := statsSprint(t)
	out := ta.ok("stats")
	rows := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		k, v, _ := strings.Cut(l, " | ")
		rows[strings.TrimSpace(k)] = strings.Join(strings.Fields(v), " ")
	}
	want := map[string]string{
		"deal wait":           "0.0 0.0 n=2",
		"finish to two reads": "49.0 58.0 n=2", // accepted at 70: 70-12, 70-30
		"accept to land":      "5.0 5.0 n=2",
		"total":               "75.0 75.0 n=2",
		"m1":                  "2 | 0 | 3.0 4.0 n=2 | 7.0 8.0 n=2 | 3.0 4.0 n=2",
		"reader-a":            "2 | 3.0 3.0 n=2 | 15.0 15.0 n=2 | 5.0 5.0 n=2",
		"reader-b":            "2 | 30.0 30.0 n=2 | 5.0 5.0 n=2 | -5.0 -5.0 n=2", // a report with no begin is its begin
		// a read runs on its card's tier, so the flash cards' four reads are flash-a's
		// takes too; the provider's take is a take of the route
		"flash-a": "7 | 6 | 0 | 1 | 6.0 15.0 n=7",
	}
	for k, v := range want {
		assert.Equal(t, v, rows[k], "row %s\n%s", k, out)
	}
	assert.Contains(t, out, "STATS OK epoch=0 primaries=2 members=1 readers=2 routes=1\n")

	var ps sprint.PassStats
	ta.json("stats", &ps)
	require.Len(t, ps.Work, 1)
	assert.Equal(t, sprint.Measure{Median: 7, Max: 8, N: 2}, ps.Work[0].RunWall)
	assert.Equal(t, sprint.Measure{Median: 49, Max: 58, N: 2}, ps.Stages.FinishToReads)
	require.Len(t, ps.Routes, 1)
	assert.Equal(t, sprint.RouteTakes{Route: "flash-a", Takes: 7, OK: 6, Provider: 1, RunWall: sprint.Measure{Median: 6, Max: 15, N: 7}}, ps.Routes[0])

}

// An empty sprint prints its tables with no row and every stage "-".
func TestStatsOfAnEmptySprint(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	out := ta.ok("stats")
	assert.Contains(t, out, "deal wait           | -\n")
	assert.Contains(t, out, "STATS OK epoch=0 primaries=0 members=0 readers=0 routes=0\n")
	code, _, errs := ta.do("stats extra")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "takes no words")
}
