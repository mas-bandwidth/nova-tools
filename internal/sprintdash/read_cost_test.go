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
  unreconciled: txt('cost-unreconciled'), landed: txt('landed'), all: txt('all'), eta: txt('eta') }));
`

// The cost tile shows its recorded total and only the amount per landed card below it.
func TestTheCostTileShowsReadsBesideWork(t *testing.T) {
	t.Parallel()
	res := drawCostTile(t, nil)
	assert.Equal(t, "$3.00", res.Cost, "the total includes recorded work and reads")
	assert.Regexp(t, `^\$[0-9]+\.[0-9]{2} per card$`, res.Per)
	assert.Empty(t, res.Title)
	assert.Empty(t, res.Unreconciled)
	assert.NotContains(t, string(file("index.html")), `id="cost-unreconciled"`)
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
	assert.Equal(t, "$0.50 per card", res.Per)
	assert.Empty(t, res.Title)
}

// costTile is the cost tile and the hero as app.js draws them.
type costTile struct{ Cost, Per, Title, Unreconciled, Landed, All, Eta string }

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
