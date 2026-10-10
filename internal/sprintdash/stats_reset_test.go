package sprintdash

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A stats reset on the page (nova-sprint stats reset): the cost tile counts from the mark,
// and so must the cards its cost per card is over, in the release the page shows by default
// as in the whole sprint; the pie reads the spend by tier where where --json carries it.

// resetCopy is where --json after a reset: two streams of release v1 and v2, the mark's
// cards landed since by stream (2 and 3, 5 in all), and the spend since the mark.
const resetCopy = `{"landed":100,"all":120,
"streams":[{"Stream":"a","Release":"v1"},{"Stream":"b","Release":"v2"}],
"releases":{"v1":10,"v2":10},
"tables":{"work":{"a":{"landed":"60","waiting":"10","cost":"$2.00","per_landed":"$1.00"},"b":{"landed":"40","waiting":"10","cost":"$9.00","per_landed":"$3.00"}}},
"stream_costs":{"a":{"total_cost":"$2.00","cost_by_tier":{"flash":"$2.00"}},"b":{"total_cost":"$9.00","cost_by_tier":{"pro":"$9.00"}}},
"stats_reset":{"at":"2026-10-09T21:30:00Z","reason":"count from now","landed":5,"streams":{"a":2,"b":3}}}`

// The release the page shows by default (the earliest with cards left, v1 here) narrows
// stats_reset to its streams: its landed is the release's cards since the mark.
func TestAReleaseViewCountsTheResetsLandedOverItsStreams(t *testing.T) {
	t.Parallel()
	v := viewOf(json.RawMessage(resetCopy), "")
	require.Equal(t, "v1", v.Release, "the current release is shown by default")
	var d struct {
		Landed     int64 `json:"landed"`
		StatsReset struct {
			Landed  int64            `json:"landed"`
			Streams map[string]int64 `json:"streams"`
			Reason  string           `json:"reason"`
		} `json:"stats_reset"`
	}
	require.NoError(t, json.Unmarshal(v.Data, &d))
	assert.Equal(t, int64(60), d.Landed)
	assert.Equal(t, int64(2), d.StatsReset.Landed, "the release's cards since the mark, not the sprint's 5")
	assert.Equal(t, map[string]int64{"a": 2}, d.StatsReset.Streams)
	assert.Equal(t, "count from now", d.StatsReset.Reason, "the mark's other fields pass through")

	all := viewOf(json.RawMessage(resetCopy), AllReleases)
	require.NoError(t, json.Unmarshal(all.Data, &d))
	assert.Equal(t, int64(5), d.StatsReset.Landed, "all: the sprint's")

	// a copy with no reset is left as it was
	none := viewOf(json.RawMessage(`{"streams":[{"Stream":"a","Release":"v1"}],"tables":{"work":{"a":{"landed":"1"}}}}`), "v1")
	assert.NotContains(t, string(none.Data), "stats_reset")
}

// The page itself, run by node on the shipped app.js: the cost per card is the cost since
// the mark over stats_reset.landed, in the release view as well; the pie and its legend read
// cost_by_tier from stream_costs, where where --json carries it.
func TestThePageCountsCostPerCardAndTiersFromTheMark(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("NOVA_CI") == "1" {
			require.NoError(t, err, "node is required for the dashboard JS behavioural test")
		}
		t.Skip("node is not installed on this machine; the dashboard JS behavioural test needs it")
	}
	appJS, err := os.ReadFile("page/app.js")
	require.NoError(t, err)
	release := viewOf(json.RawMessage(resetCopy), "")
	script := jsDOMPrelude + `
const text = id => { const e = getEl(id); return e._val != null ? String(e._val) : e.textContent; };
const out = {};
for (const [name, d] of Object.entries(input.copies)) {
  // the hero over the copy's streams: the cost tile's sum is the streams' total_cost
  let total = 0;
  for (const k of Object.keys(d.stream_costs || {})) total += context.cents(d.stream_costs[k].total_cost) || 0;
  const sum = { totalCost: total, working: 0, review: 0, merging: 0, fix: 0, epoch: { totalCost: total } };
  context.renderHero(d, { sum: sum }, {});
  context.renderPie(d);
  context.renderTopStreams(d);
  out[name] = {
    cost: text("cost"), per: text("cost-per"),
    tiers: getEl("tier-sub").children.map(sp => sp.children[1] && (sp.children[1]._val != null ? sp.children[1]._val : sp.children[1].textContent)),
    paths: getEl("pie").children.filter(c => c.tagName === "PATH").length
  };
}
process.stdout.write(JSON.stringify(out));
`
	var copies = map[string]json.RawMessage{"sprint": json.RawMessage(resetCopy), "release": release.Data}
	in, err := json.Marshal(map[string]any{"appJS": string(appJS), "copies": copies})
	require.NoError(t, err)
	cmd := exec.Command(node, "-e", script)
	cmd.Stdin = bytes.NewReader(in)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	require.NoError(t, cmd.Run(), "node: %s", stderr.String())
	var got map[string]struct {
		Cost  string   `json:"cost"`
		Per   string   `json:"per"`
		Tiers []string `json:"tiers"`
		Paths int      `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &got), stdout.String())
	assert.Equal(t, "$11.00", got["sprint"].Cost)
	assert.Equal(t, "$2.20 per card", got["sprint"].Per, "$11.00 since the mark over the 5 cards since it, not the 100 of the epoch")
	assert.ElementsMatch(t, []string{"pro", "flash"}, got["sprint"].Tiers, "the pie's legend reads stream_costs' tiers")
	assert.Equal(t, 2, got["sprint"].Paths)
	assert.Equal(t, "$2.00", got["release"].Cost)
	assert.Equal(t, "$1.00 per card", got["release"].Per, "the release's $2.00 over its 2 cards since the mark")
	assert.Equal(t, []string{"flash"}, got["release"].Tiers)
}
