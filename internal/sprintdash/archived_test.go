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

// archivedRun is the scenario of the archived streams over the scroll test's DOM shim: one
// draw, a click on the archived line, a second click.
const archivedRun = `
const rows = () => doc.getElementById('streams').children
  .filter(r => r._classes.includes('row') && !r._classes.includes('head') && !r._classes.includes('total'))
  .map(r => r.children[0].textContent);
const line = () => doc.getElementById('streams').children.find(r => r._classes.includes('archived'));
(async () => {
  nextBody = { build: 'b1', data: input.snap }; await tick();
  const out = { rows: rows(), line: line() ? line().textContent : null, cost: doc.getElementById('cost').textContent,
    landed: doc.getElementById('landed').textContent };
  const total = doc.getElementById('streams').children.find(r => r._classes.includes('total'));
  out.total = total ? total.children[8].textContent : null;
  line()._listeners.click.forEach(f => f());
  out.shown = rows(); out.shownLine = line().textContent;
  line()._listeners.click.forEach(f => f());
  out.hidden = rows();
  process.stdout.write(JSON.stringify(out));
})();
`

// TestTheWorkTableDrawsLiveStreamsAndOneLineForTheArchived is the dashboard's side of stream
// archive (the owner, 2026-10-05: "I would like you to remove all the already landed work
// streams"): the Work table draws the live streams only, one line under it says "N archived
// streams, M cards landed, $X", a click shows them and a second hides them, and the Total row
// and the hero cost count them always. app.js runs on node against the scroll test's shim.
func TestTheWorkTableDrawsLiveStreamsAndOneLineForTheArchived(t *testing.T) {
	t.Parallel()
	nodePath, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("NOVA_CI") == "1" {
			require.NoError(t, err, "node is required for the dashboard JS behavioural test")
		}
		t.Skip("node is not installed on this machine; the dashboard JS behavioural test needs it")
	}
	var snap map[string]any
	require.NoError(t, json.Unmarshal(fixture(t), &snap))
	work := map[string]any{
		"alpha": map[string]any{"cost": "$1.00", "landed": "1", "merging": "0", "ready": "1", "review": "0", "waiting": "1", "working": "0"},
		"beta":  map[string]any{"cost": "$4.00", "landed": "3", "merging": "0", "ready": "0", "review": "0", "waiting": "0", "working": "0"},
	}
	snap["tables"].(map[string]any)["work"] = work
	snap["stream_costs"] = map[string]any{"alpha": map[string]any{"total_cost": "$1.00"}, "beta": map[string]any{"total_cost": "$4.00"}}
	snap["archived"] = map[string]any{"streams": []string{"beta"}, "landed": 3, "cost": "$4.00"}
	snap["streams"] = []any{}
	in, err := json.Marshal(map[string]any{"appJS": string(file("app.js")), "snap": snap})
	require.NoError(t, err)

	shim := scrollShim[:strings.Index(scrollShim, "let draws = 0")]
	cmd := exec.Command(nodePath, "-e", shim+archivedRun)
	cmd.Stdin = bytes.NewReader(in)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &outBuf, &errBuf
	require.NoError(t, cmd.Run(), "node runner failed: %s", errBuf.String())
	require.Empty(t, errBuf.String(), "app.js threw while drawing")
	var res struct {
		Rows, Shown, Hidden          []string
		Line, ShownLine, Cost, Total string
	}
	require.NoError(t, json.Unmarshal(outBuf.Bytes(), &res), outBuf.String())

	assert.Equal(t, []string{"alpha"}, res.Rows, "the archived stream is not drawn")
	assert.Equal(t, "1 archived stream, 3 cards landed, $4.00 · show them", res.Line)
	assert.Equal(t, "$5.00", res.Total, "the Total row counts the archived")
	assert.Equal(t, "$5.00", res.Cost, "the hero cost counts the archived")
	assert.ElementsMatch(t, []string{"alpha", "beta"}, res.Shown, "a click shows them")
	assert.Equal(t, "1 archived stream, 3 cards landed, $4.00 · hide them", res.ShownLine)
	assert.Equal(t, []string{"alpha"}, res.Hidden, "a second click hides them")
}
