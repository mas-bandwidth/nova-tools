package sprintdash

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The existing clock/dot reflects the source alarm without redrawing an unchanged
// snapshot (SPEC-SPRINT-DASHBOARD, Page); recovery restores the live indicator.
func TestTheLiveIndicatorShowsStaleSourceAndRecovery(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("NOVA_CI") == "1" {
			require.NoError(t, err)
		}
		t.Skip("node is required for the dashboard JS behavioural test")
	}
	var data map[string]any
	require.NoError(t, json.Unmarshal(fixture(t), &data))
	shim, _, ok := strings.Cut(scrollShim, "// the viewer:")
	require.True(t, ok)
	input, err := json.Marshal(map[string]any{"appJS": string(file("app.js")), "data": data})
	require.NoError(t, err)
	driver := `
const results = [];
for (const stale of [false, true, true, false]) {
 const before = draws;
 context.apply({data:input.data,stale});
 results.push({cls:doc.getElementById('live').className,text:doc.getElementById('live-text').innerHTML,draws:draws-before});
}
process.stdout.write(JSON.stringify(results));
`
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := subproc.Context(ctx, node, "-e", shim+driver)
	cmd.Stdin = bytes.NewReader(input)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	require.NoError(t, cmd.Run(), stderr.String())
	require.Empty(t, stderr.String())
	var results []struct {
		Cls, Text string
		Draws     int
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &results))
	require.Len(t, results, 4)
	for i, result := range results {
		if i == 1 || i == 2 {
			assert.Equal(t, "live waiting", result.Cls)
			assert.Contains(t, result.Text, "Stale")
		} else {
			assert.Equal(t, "live ok", result.Cls)
			assert.Contains(t, result.Text, "Updated")
		}
		if i > 0 {
			assert.Zero(t, result.Draws, "an alarm does not redraw the same data")
		}
	}
}
