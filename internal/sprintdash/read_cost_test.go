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
process.stdout.write(JSON.stringify({ cost: doc.getElementById('cost').textContent, per: doc.getElementById('cost-per').innerHTML }));
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
		"ci":  map[string]any{"total_cost": "$3.00", "work_cost": "$2.00", "read_cost": "$1.00"},
		"old": map[string]any{"total_cost": "$1.50", "work_cost": "$1.00", "read_cost": "$0.50"},
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
	var res struct{ Cost, Per string }
	require.NoError(t, json.Unmarshal(outBuf.Bytes(), &res), outBuf.String())

	assert.Contains(t, res.Cost, "$4.50", "the complete cost holds work and reads of every stream")
	assert.Contains(t, res.Per, "$3.00 work · $1.50 reads", "the reads beside the work, the archived stream's included")
}
