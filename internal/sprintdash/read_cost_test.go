package sprintdash

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readCostDriver draws a where --json snapshot with app.js on the scroll test's DOM shim
// and reads the cost tile.
const readCostDriver = `
context.render(input.data);
process.stdout.write(JSON.stringify({ cost: doc.getElementById('cost').textContent, per: doc.getElementById('cost-per').innerHTML, title: doc.getElementById('cost-per').title || "" }));
`

// The cost tile shows the reads as their own number beside the work (reads are priced like
// work; the owner, 2026-10-05: "do we have the cost for readers properly calculated yet in
// nova sprint?"), over every stream, the archived ones off the table included, as the
// total counts them.
func TestTheCostTileShowsReadsBesideWork(t *testing.T) {
	t.Parallel()
	nodePath, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("NOVA_CI") == "1" {
			require.NoError(t, err, "node is required for the dashboard JS behavioural test")
		}
		t.Skip("node is not installed on this machine; the dashboard JS behavioural test needs it")
	}
	var d map[string]any
	require.NoError(t, json.Unmarshal(fixture(t), &d))
	work := d["tables"].(map[string]any)["work"].(map[string]any)
	work["old"] = map[string]any{"cost": "$1.50", "landed": "4", "merging": "0", "ready": "0", "review": "0", "waiting": "0", "working": "0"}
	d["archived"] = map[string]any{"streams": []string{"old"}, "landed": 4, "cost": "$1.50"}
	d["stream_costs"] = map[string]any{
		"ci": map[string]any{"total_cost": "$3.00", "work_cost": "$2.00", "read_cost": "$1.00", "readers": map[string]any{
			"reader-a": map[string]any{"reads": 3, "priced": 3, "usd": "0.875", "hour_priced": 1, "hour_usd": "0.125"},
			"reader-b": map[string]any{"reads": 1, "priced": 1, "usd": "0.125"},
			"reader-c": map[string]any{"reads": 2, "tokens": 1500},
		}},
		"old": map[string]any{"total_cost": "$1.50", "work_cost": "$1.00", "read_cost": "$0.50", "readers": map[string]any{
			"reader-b": map[string]any{"reads": 1, "priced": 1, "usd": "0.5"},
		}},
	}

	shim, _, ok := strings.Cut(scrollShim, "// the viewer:")
	require.True(t, ok, "the scroll test's shim has its viewer")
	in, err := json.Marshal(map[string]any{"appJS": string(file("app.js")), "data": d})
	require.NoError(t, err)
	cmd := exec.Command(nodePath, "-e", shim+readCostDriver)
	cmd.Stdin = bytes.NewReader(in)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &outBuf, &errBuf
	require.NoError(t, cmd.Run(), "node runner failed: %s", errBuf.String())
	require.Empty(t, errBuf.String(), "app.js threw while drawing")
	var res struct{ Cost, Per, Title string }
	require.NoError(t, json.Unmarshal(outBuf.Bytes(), &res), outBuf.String())

	assert.Contains(t, res.Cost, "$4.50", "the complete cost holds work and reads of every stream")
	assert.Contains(t, res.Per, "$3.00 work · $1.50 reads", "the reads beside the work, the archived stream's included")
	assert.Contains(t, res.Per, "$1.50 reads (33%)", "and their share of work and reads together")
	assert.Equal(t, "reader-a $0.88 ($0.13 last hour)\nreader-b $0.63 ($0.00 last hour)", res.Title,
		"the tooltip names each reader's spend over every stream, most first; a subscription reader has no dollars")
}

// readHeadlinesDriver draws a where --json snapshot and reads the cost and ETA tiles.
const readHeadlinesDriver = `
context.render(input.data);
var t = function (id) { return doc.getElementById(id).textContent; };
process.stdout.write(JSON.stringify({ cost: t('cost'), per: doc.getElementById('cost-per').innerHTML, cover: t('cost-unreconciled'), eta: t('eta-basis') }));
`

// The headlines carry their completeness (docs/SPEC-SPRINT-DASHBOARD.md, the hero row): while
// a run is unpriced the cost is a floor and the cost per card is unknown, never a figure the
// unpriced run made smaller; the coverage is named; and the ETA tile states its rate's window
// and sample and shows the held cards apart from the executing ones.
func TestTheHeadlinesCarryTheirCompleteness(t *testing.T) {
	t.Parallel()
	nodePath, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("NOVA_CI") == "1" {
			require.NoError(t, err, "node is required for the dashboard JS behavioural test")
		}
		t.Skip("node is not installed on this machine; the dashboard JS behavioural test needs it")
	}
	draw := func(d map[string]any) (res struct{ Cost, Per, Cover, ETA string }) {
		t.Helper()
		shim, _, ok := strings.Cut(scrollShim, "// the viewer:")
		require.True(t, ok, "the scroll test's shim has its viewer")
		in, err := json.Marshal(map[string]any{"appJS": string(file("app.js")), "data": d})
		require.NoError(t, err)
		cmd := exec.Command(nodePath, "-e", shim+readHeadlinesDriver)
		cmd.Stdin = bytes.NewReader(in)
		var outBuf, errBuf bytes.Buffer
		cmd.Stdout, cmd.Stderr = &outBuf, &errBuf
		require.NoError(t, cmd.Run(), "node runner failed: %s", errBuf.String())
		require.Empty(t, errBuf.String(), "app.js threw while drawing")
		require.NoError(t, json.Unmarshal(outBuf.Bytes(), &res), outBuf.String())
		return res
	}
	base := func(unpriced int) map[string]any {
		var d map[string]any
		require.NoError(t, json.Unmarshal(fixture(t), &d))
		d["landed"], d["all"] = 6, 20
		d["stream_costs"] = map[string]any{
			"ci": map[string]any{"total_cost": "$7.18", "work_cost": "$7.18", "unpriced_runs": unpriced, "landed": 6, "landed_priced": 1,
				"coverage": map[string]any{"records": 21 + unpriced, "actual": 2, "estimated": 19, "unpriced": unpriced}, "dropped": map[string]any{"cards": 2}},
		}
		d["eta"] = map[string]any{"rate": map[string]any{"window": "last 1h of running time", "landings": 7, "hours": 1, "per_hour": 7},
			"work": map[string]any{"left": 14, "held": 9, "executing": 3, "queued": 2}}
		return d
	}

	res := draw(base(19))
	assert.Contains(t, res.Cost, "≥ $7.18", "a floor while runs are unpriced")
	assert.Contains(t, res.Per, "per card unknown", "never the priced spend over every landed card")
	assert.NotContains(t, res.Per, "$1.20")
	assert.Contains(t, res.Cover, "2 actual · 19 estimated · 19 unpriced · 2 dropped")
	assert.Equal(t, "7.0/h over last 1h of running time (7 landed) · 9 held · 3 executing · 2 queued", res.ETA)

	res = draw(base(0))
	assert.NotContains(t, res.Cost, "≥")
	assert.Contains(t, res.Per, "$1.20 per card of 6 landed", "every record priced: the spend over the landed cards, with its denominator")
}
