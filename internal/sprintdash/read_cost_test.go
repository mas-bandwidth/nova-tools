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
process.stdout.write(JSON.stringify({ cost: doc.getElementById('cost').textContent, per: doc.getElementById('cost-per').innerHTML,
  cover: doc.getElementById('cost-unreconciled').textContent, cards: doc.getElementById('eta-cards').textContent }));
`

// The cost tile shows the reads as their own number beside the work (reads are priced like
// work; the owner, 2026-10-05: "do we have the cost for readers properly calculated yet in
// nova sprint?"), over every stream, the archived ones off the table included, as the
// total counts them.
func TestTheCostTileShowsReadsBesideWork(t *testing.T) {
	t.Parallel()
	nodePath := nodeOrSkip(t)
	var d map[string]any
	require.NoError(t, json.Unmarshal(fixture(t), &d))
	work := d["tables"].(map[string]any)["work"].(map[string]any)
	work["old"] = map[string]any{"cost": "$1.50", "landed": "4", "merging": "0", "ready": "0", "review": "0", "waiting": "0", "working": "0"}
	d["archived"] = map[string]any{"streams": []string{"old"}, "landed": 4, "cost": "$1.50"}
	d["stream_costs"] = map[string]any{
		"ci":  map[string]any{"total_cost": "$3.00", "work_cost": "$2.00", "read_cost": "$1.00"},
		"old": map[string]any{"total_cost": "$1.50", "work_cost": "$1.00", "read_cost": "$0.50"},
	}

	res := drawCost(t, nodePath, d)
	assert.Contains(t, res.Cost, "$4.50", "the complete cost holds work and reads of every stream")
	assert.Contains(t, res.Per, "$3.00 work · $1.50 reads", "the reads beside the work, the archived stream's included")
}

// costTile is what drawCost reads off the page.
type costTile struct{ Cost, Per, Cover, Cards string }

// nodeOrSkip is node's path; with none the test is skipped, except on CI, where it fails.
func nodeOrSkip(t *testing.T) string {
	t.Helper()
	nodePath, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("NOVA_CI") == "1" {
			require.NoError(t, err, "node is required for the dashboard JS behavioural test")
		}
		t.Skip("node is not installed on this machine; the dashboard JS behavioural test needs it")
	}
	return nodePath
}

// drawCost draws d with app.js and reads the cost tile and the ETA's cards.
func drawCost(t *testing.T, nodePath string, d map[string]any) costTile {
	t.Helper()
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

// The cost tile's headlines carry their denominators and coverage (docs/SPEC-SPRINT.md
// section 1, cost visibility): with a run unpriced the total is a floor and the cost per
// card is unknown, never a figure the unpriced run made smaller; priced whole, the cost
// per card names the landed cards it is over. The ETA's cards show the held apart from
// the executing.
func TestTheCostTileNeverLetsAnUnpricedRunReadAsFree(t *testing.T) {
	t.Parallel()
	nodePath := nodeOrSkip(t)
	data := func(unpriced, landedPriced int) map[string]any {
		var d map[string]any
		require.NoError(t, json.Unmarshal(fixture(t), &d))
		work := d["tables"].(map[string]any)["work"].(map[string]any)
		d["stream_costs"] = map[string]any{}
		for k := range work {
			d["stream_costs"].(map[string]any)[k] = map[string]any{}
		}
		d["stream_costs"].(map[string]any)["ci"] = map[string]any{"total_cost": "$7.18", "work_cost": "$7.18", "landed": 6, "landed_priced": landedPriced,
			"unpriced_runs": unpriced, "coverage": map[string]any{"records": 21, "actual": 2, "estimated": 21 - 2 - unpriced, "unpriced": unpriced}}
		return d
	}
	t.Run("a run unpriced", func(t *testing.T) {
		t.Parallel()
		res := drawCost(t, nodePath, data(19, 1))
		assert.Contains(t, res.Cost, "\u2265 $7.18", "the recorded spend is a floor")
		assert.Contains(t, res.Per, "per card unknown")
		assert.NotContains(t, res.Per, "$1.20", "the depressed ratio is never shown")
		assert.Contains(t, res.Cover, "2 actual \u00b7 0 estimated \u00b7 19 unpriced")
		assert.Regexp(t, `^\d+ held · \d+ executing · \d+ queued$`, res.Cards)
	})
	t.Run("priced whole", func(t *testing.T) {
		t.Parallel()
		res := drawCost(t, nodePath, data(0, 6))
		assert.NotContains(t, res.Cost, "\u2265")
		assert.Contains(t, res.Per, "$1.20 per card of 6 landed", "every recorded take and read over the landed cards")
		assert.Contains(t, res.Cover, "2 actual \u00b7 19 estimated \u00b7 0 unpriced")
	})
}
