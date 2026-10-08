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

// archivedDriver draws a where --json --archived snapshot with app.js on the scroll test's
// DOM shim, reads the Work rows, clicks the archived line twice, and reads them again.
const archivedDriver = `
const names = () => doc.getElementById('streams').children
  .filter(r => !r._classes.includes('head') && !r._classes.includes('total')).map(r => r.children[0].children[0].textContent);
context.render(input.data);
process.stdout.write(JSON.stringify({live:names(),total:doc.getElementById('streams')._total._c[9].textContent}));
`

// The Work panel shows only the live streams by default (stream archive; the owner,
// 2026-10-05: "I would like you to remove all the already landed work streams"), with one
// line "N archived streams, M cards landed, $X" that shows them, and hides them again, when
// clicked; the total row counts only the streams on the table, shown or not (the owner,
// 2026-10-06: "I really don't think we have 2.8k cards post-archive...").
func TestTheWorkPanelHidesArchivedStreamsBehindOneLine(t *testing.T) {
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
		Live, Shown, Again []string
		ShownTags          []string
		Line, ShownLine    string
		Hidden             bool
		Total, ShownTotal  string
	}
	require.NoError(t, json.Unmarshal(outBuf.Bytes(), &res), outBuf.String())

	assert.Equal(t, []string{"ci"}, res.Live, "only the live streams by default")
	assert.Contains(t, res.Total, "$2.15", "the total excludes archived streams")
	assert.NotContains(t, string(file("index.html")), `id="streams-archived"`)
}
