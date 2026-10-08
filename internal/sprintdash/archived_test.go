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

// archivedDriver renders an archived snapshot and reads the visible Work rows and total.
const archivedDriver = `
const box = doc.getElementById('streams');
context.render(input.data);
const names = box.children.filter(r => !r._classes.includes('head') && !r._classes.includes('total'))
  .map(r => r.children[0].children[0].textContent);
process.stdout.write(JSON.stringify({ live: names, total: box._total._c[8].innerHTML + ' ' + box._total._c[9].textContent }));
`

// Archived streams remain absent from Work while the live total remains correct.
func TestTheWorkPanelHidesArchivedStreams(t *testing.T) {
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
	work["old"] = map[string]any{"cost": "$1.00", "landed": "4", "merging": "0", "ready": "0", "review": "0", "waiting": "0", "working": "0"}
	d["archived"] = map[string]any{"streams": []string{"old"}, "landed": 4, "cost": "$1.00"}

	shim, _, ok := strings.Cut(scrollShim, "// the viewer:")
	require.True(t, ok, "the scroll test's shim has its viewer")
	in, err := json.Marshal(map[string]any{"appJS": string(file("app.js")), "data": d})
	require.NoError(t, err)
	cmd := exec.Command(nodePath, "-e", shim+archivedDriver)
	cmd.Stdin = bytes.NewReader(in)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &outBuf, &errBuf
	require.NoError(t, cmd.Run(), "node runner failed: %s", errBuf.String())
	require.Empty(t, errBuf.String(), "app.js threw while drawing")
	var res struct {
		Live  []string
		Total string
	}
	require.NoError(t, json.Unmarshal(outBuf.Bytes(), &res), outBuf.String())
	assert.Equal(t, []string{"ci"}, res.Live)
	assert.Contains(t, res.Total, "$2.15", "the total row excludes the archived stream's $1.00")
	assert.NotContains(t, string(file("index.html")), `id="streams-archived"`)
	assert.NotContains(t, string(file("app.js")), "renderArchived")
}
