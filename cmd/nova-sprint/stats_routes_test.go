package main

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStatsRoutesPrintsTheRouteTable feeds a recorded log to stats --routes
// (docs/SPEC-SPRINT.md, the verb stats) and pins every column of the rows.
// A provider take whose error begins "no result:" is a no-result even when
// its token counts are 0; a provider line with no token counts is a provider
// failure, and its actual_usd still counts. A deal, a take, an accept and a
// read before the window stay. An accept is the verb accept or tick accept.
func TestStatsRoutesPrintsTheRouteTable(t *testing.T) {
	t.Parallel()
	since := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	at := since.Add(time.Hour)
	stamp := func(t time.Time) string { return t.UTC().Format(time.RFC3339) }
	usage := func(route string, wall int, usd string) string {
		return "price_route=" + route + " wall=" + strconv.Itoa(wall) + "s run=" + strconv.Itoa(wall) + "s actual_usd=" + usd + " input=10 output=1"
	}
	noResult := sprint.ProviderTake{
		Route: "flash-a", Model: "p/m", Member: "m1", Finished: stamp(at),
		Usage: "wall=30s run=30s actual_usd=0.50 input=0 output=0 price_route=flash-a",
		Error: cardhdr.EndNoResult + ": the child wrote nothing",
	}.String()
	serverError := sprint.ProviderTake{
		Route: "flash-a", Model: "p/m", Member: "m1", Finished: stamp(at),
		Usage: "wall=2s run=2s actual_usd=0.25 price_route=flash-a",
		Error: "server_error",
	}.String()
	lines := []sprint.Line{
		// before the window: a priced ok finish, and a provider take on it, neither counts
		{At: since.Add(-time.Hour), Table: "fleet", Verb: "finish", Card: "old.w1", Gen: 1,
			Set: map[string]string{
				"ok": "yes", "usage": usage("flash-a", 999, "99.00"),
				sprint.FieldProviderTake + "1": sprint.ProviderTake{
					Route: "flash-a", Error: "server_error", Usage: "actual_usd=9.00 price_route=flash-a",
				}.String(),
			}},
		// a deal and a take before the window are the route and the wall of a finish inside it
		{At: since.Add(-2 * time.Hour), Table: "fleet", Verb: "tick deal", Card: "bare.w1",
			Set: map[string]string{"route": "flash-b", "gen": "1"}},
		{At: since.Add(-2 * time.Hour), Table: "fleet", Verb: "take", Card: "bare.w1", Gen: 1,
			Set: map[string]string{"taken": stamp(since.Add(-2 * time.Hour))}},
		// a read before the window still scores the ok take inside it
		{At: since.Add(-time.Minute), Table: "readers", Verb: "read", Card: "prim.r1.reader-a",
			Set: map[string]string{"verdict": "broken"}},
		// a tick accept before the window still bounds the landing
		{At: since.Add(-time.Hour), Table: "work", Verb: "tick accept", Primary: "early"},
		{At: since.Add(time.Minute), Table: "fleet", Verb: "finish", Card: "early.w1", Gen: 1,
			Set: map[string]string{"ok": "yes", "usage": "price_route=flash-c wall=10s run=10s input=1 output=1"}},
		{At: since.Add(2 * time.Minute), Table: "work", To: "s1:landed", Primary: "early"},
		{At: at, Table: "fleet", Verb: "finish", Card: "prim.w1", Gen: 1,
			Set: map[string]string{"ok": "yes", "usage": usage("flash-a", 100, "2.00")}},
		// the coordinator's accept, not the machine's tick accept
		{At: at.Add(time.Minute), Table: "work", Verb: "accept", Primary: "prim"},
		// a later ok take, after that accept and before the land, is not the landing
		{At: at.Add(10 * time.Minute), Table: "fleet", Verb: "finish", Card: "prim.w2", Gen: 2,
			Set: map[string]string{"ok": "yes", "usage": "price_route=flash-b wall=80s run=80s input=10 output=1"}},
		{At: at.Add(30 * time.Minute), Table: "work", To: "s1:landed", Primary: "prim"},
		{At: at.Add(4 * time.Minute), Table: "fleet", Verb: "finish", Card: "other.w1", Gen: 1,
			Set: map[string]string{
				"ok":                           "no",
				"usage":                        usage("flash-a", 40, "1.00"),
				sprint.FieldProviderTake + "1": noResult,
				sprint.FieldProviderTake + "2": serverError,
				sprint.FieldProviderTake + "3": serverError,
			}},
		// no usage and no price_route: the pre-window deal names the route, the pre-window take the wall
		{At: at, Table: "fleet", Verb: "finish", Card: "bare.w1", Gen: 1,
			Set: map[string]string{"ok": "no", "finished": stamp(at)}},
		{At: at.Add(5 * time.Minute), Table: "fleet", Verb: "finish", Card: "script.w1", Gen: 1,
			Set: map[string]string{"ok": "yes", "usage": "wall=1s run=1s"}},
	}
	out := sprint.RouteTable(lines, since)
	assert.Contains(t, out, "ROUTE TABLE since 2026-10-04T00:00:00Z")
	for _, col := range []string{"takes", "ok", "failed", "noRes", "prov", "landed", "1stT", "wrong", "$/take", "$/land", "medWall", "imp"} {
		assert.Contains(t, out, col, "column %s", col)
	}
	row := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		name, _, _ := strings.Cut(line, " ")
		if name != "" {
			row[name] = line
		}
	}
	require.Contains(t, row, "flash-a", out)
	assert.Equal(t, "flash-a                            3    1      1      1     2      1  100%    1/1    1.33    4.00     40s    0", row["flash-a"])
	assert.Equal(t, "flash-b                            2    1      1      0     0      0     -      -    0.00       -  10800s    0", row["flash-b"])
	assert.Equal(t, "flash-c                            1    1      0      0     0      0     -      -    0.00       -     10s    0", row["flash-c"])
	assert.Contains(t, out, "finishes ran no model")
	assert.NotContains(t, out, "99.00", "a finish before --since is outside the window")
	assert.NotContains(t, out, "9.00", "a provider take on a finish before --since is outside the window")
}
