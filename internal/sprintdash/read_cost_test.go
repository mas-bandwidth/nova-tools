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
const txt = id => doc.getElementById(id).textContent;
process.stdout.write(JSON.stringify({ cost: txt('cost'), per: doc.getElementById('cost-per').innerHTML, title: doc.getElementById('cost-per').title || "",
  unreconciled: txt('cost-unreconciled'), landed: txt('landed'), all: txt('all'), eta: txt('eta'), basis: txt('eta-basis') }));
`

// The cost tile shows the reads as their own number beside the work (reads are priced like
// work; the owner, 2026-10-05: "do we have the cost for readers properly calculated yet in
// nova sprint?"), over one scope, the streams on the table, as the total counts them, the
// readers' tooltip too: an archived stream's spend is on its archived line (the owner,
// 2026-10-06: "I really don't think we have 2.8k cards post-archive..."). The providers'
// unreconciled spend is the epoch's (sprint.UnreconciledSpend), so it is never added into the
// tile: it has its own line, its scope named.
func TestTheCostTileShowsReadsBesideWork(t *testing.T) {
	t.Parallel()
	res := drawCostTile(t, nil)
	assert.Equal(t, "$3.00", res.Cost, "the complete cost holds work and reads of the streams on the table, and no unreconciled spend")
	assert.Contains(t, res.Per, "$2.00 work · $1.00 reads", "the reads beside the work, the archived stream's left out")
	assert.Contains(t, res.Per, "$1.00 reads (33%)", "and their share of work and reads together")
	assert.Equal(t, "reader-a $0.88 ($0.13 last hour)\nreader-b $0.13 ($0.00 last hour)", res.Title,
		"the tooltip names each reader's spend over the streams on the table, most first; a subscription reader has no dollars")
	assert.Contains(t, res.Per, "2 runs unpriced", "the runs unpriced of the tile's scope")
	assert.Equal(t, "$0.40 unreconciled since 2026-10-03", res.Unreconciled,
		"the unreconciled spend on its own line, its scope the epoch's, never in the tile")
}

// A sprint done (where --json's done: every stream archived by the tick) reads the same on the
// hero as on the CLI's done line: the epoch's N/N, done, and the epoch's cost, every stream's.
func TestTheHeroOfASprintDoneIsTheEpochs(t *testing.T) {
	t.Parallel()
	res := drawCostTile(t, func(d map[string]any) {
		d["done"] = true
		d["landed"], d["all"] = 9, 9
		d["summary"] = "9/9 100.0% done"
	})
	assert.Equal(t, [3]string{"9", "9", "done"}, [3]string{res.Landed, res.All, res.Eta})
	assert.Equal(t, "$4.50", res.Cost, "the epoch's cost: the table's and the archived streams'")
	assert.Contains(t, res.Per, "$3.00 work · $1.50 reads")
	assert.Equal(t, "reader-a $0.88 ($0.13 last hour)\nreader-b $0.63 ($0.00 last hour)", res.Title, "the tooltip over the same scope")
}

// The headlines carry their denominators. While a run of the tile's scope is unpriced the
// per-card phrase is unknown, and coverage is named only when the frame carries it. Held
// work is drawn apart from executing work. The rate window is drawn only when the frame
// carries eta.
func TestTheHeadlinesCarryCoverageAndKeepHeldApart(t *testing.T) {
	t.Parallel()
	res := drawCostTile(t, func(d map[string]any) {
		d["held"] = 2
		sc := d["stream_costs"].(map[string]any)
		ci := sc["ci"].(map[string]any)
		ci["coverage"] = map[string]any{"records": 4, "actual": 1, "estimated": 1, "tokens": 0, "unpriced": 2}
		ci["dropped"] = map[string]any{"cards": 1}
	})
	assert.Contains(t, res.Per, "per card unknown")
	assert.Contains(t, res.Per, "2 runs unpriced")
	assert.Contains(t, res.Unreconciled, "unreconciled since 2026-10-03")
	assert.Contains(t, res.Unreconciled, "1 actual · 1 estimated · 0 tokens · 2 unpriced of 4 records · 1 dropped")
	assert.NotContains(t, res.Basis, "/h over")
	assert.Regexp(t, `^\d+ held · \d+ executing · \d+ queued$`, res.Basis)

	withRate := drawCostTile(t, func(d map[string]any) {
		d["eta"] = map[string]any{
			"rate": map[string]any{"window": "last 1h of running time", "landings": 5, "hours": 1, "per_hour": 5},
			"work": map[string]any{"held": 2, "executing": 1, "queued": 3},
		}
	})
	assert.Equal(t, "5.0/h over last 1h of running time (5 landings) · 2 held · 1 executing · 3 queued", withRate.Basis)
}

// costTile is the cost tile and the hero as app.js draws them.
type costTile struct{ Cost, Per, Title, Unreconciled, Landed, All, Eta, Basis string }

// drawCostTile draws the fixture with an archived stream old ($1.50: $1.00 work, $0.50 reads)
// beside ci ($3.00: $2.00 work, $1.00 reads) and $0.40 unreconciled, edited by edit, and reads
// the cost tile and the hero.
func drawCostTile(t *testing.T, edit func(map[string]any)) costTile {
	t.Helper()
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
	d["archived"] = map[string]any{"streams": []string{"old"}, "cards": 4, "landed": 4, "cost": "$1.50"}
	d["cleared"] = "2026-10-03T08:00:00Z"
	d["stream_costs"] = map[string]any{
		"ci": map[string]any{"total_cost": "$3.00", "work_cost": "$2.00", "read_cost": "$1.00", "unreconciled": "$0.40", "unpriced_runs": 2, "readers": map[string]any{
			"reader-a": map[string]any{"reads": 3, "priced": 3, "usd": "0.875", "hour_priced": 1, "hour_usd": "0.125"},
			"reader-b": map[string]any{"reads": 1, "priced": 1, "usd": "0.125"},
			"reader-c": map[string]any{"reads": 2, "tokens": 1500},
		}},
		"old": map[string]any{"total_cost": "$1.50", "work_cost": "$1.00", "read_cost": "$0.50", "unreconciled": "$0.40", "readers": map[string]any{
			"reader-b": map[string]any{"reads": 1, "priced": 1, "usd": "0.5"},
		}},
	}
	if edit != nil {
		edit(d)
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
	var res costTile
	require.NoError(t, json.Unmarshal(outBuf.Bytes(), &res), outBuf.String())
	return res
}
