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

// barCountDriver draws a copy with app.js on the scroll test's DOM shim and reads back, for
// every friends row, the track's cell classes and the numerator of its "n / width" figure.
const barCountDriver = `
context.render(input.data);
const cls = n => n._classes.filter(c => c !== 'cell' && c !== 'flash').join(' ');
const out = {};
for (const r of doc.getElementById('friends').children) {
  if (r._classes.includes('total') || !r.children[3] || !r.children[4]) continue;
  const m = /class="fa"[^>]*>(\d+)</.exec(r.children[4].innerHTML);
  if (!m) continue;
  out[r.children[0].textContent] = { cells: r.children[3].children.map(cls), shown: Number(m[1]) };
}
process.stdout.write(JSON.stringify(out));
`

// A friend's bar is one lit cell per card or read on the row, coloured by its level, and the
// figure beside it is the number of lit cells. stella is the live row of 2026-10-09 that drew 24
// cells for "31 / 32" (11 critical, 5 high, 15 reads drawn two a cell); the others mix every
// level, an odd read count, and a working count over the width (clamped to it).
func TestFriendsBarSquaresEqualTheShownCount(t *testing.T) {
	t.Parallel()
	nodePath, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("NOVA_CI") == "1" {
			require.NoError(t, err, "node is required for the dashboard JS behavioural test")
		}
		t.Skip("node is not installed on this machine; the dashboard JS behavioural test needs it")
	}
	var d map[string]any
	require.NoError(t, json.Unmarshal(fixView(fixture(t)), &d))
	friends := map[string]any{
		"stella": map[string]any{"status": "up", "width": "32", "working": "31", "blocker_working": "0", "critical_working": "11",
			"fix_working": "0", "high_working": "5", "normal_working": "0", "low_working": "0", "reads_working": "15", "ready": "10", "done": "421", "ok": "163"},
		"mixer": map[string]any{"status": "up", "width": "16", "working": "12", "blocker_working": "1", "critical_working": "2",
			"fix_working": "3", "high_working": "1", "normal_working": "2", "reads_working": "3", "ready": "0", "done": "0", "ok": "0"},
		"over": map[string]any{"status": "up", "width": "8", "working": "10", "critical_working": "2", "reads_working": "5",
			"normal_working": "3", "ready": "0", "done": "0", "ok": "0"},
	}
	d["tables"].(map[string]any)["friends"] = friends

	shim, _, ok := strings.Cut(scrollShim, "// the viewer:")
	require.True(t, ok, "the scroll test's shim has its viewer")
	in, err := json.Marshal(map[string]any{"appJS": string(file("app.js")), "data": d})
	require.NoError(t, err)
	cmd := exec.Command(nodePath, "-e", shim+barCountDriver)
	cmd.Stdin = bytes.NewReader(in)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &outBuf, &errBuf
	require.NoError(t, cmd.Run(), "node runner failed: %s", errBuf.String())
	require.Empty(t, errBuf.String(), "app.js threw while drawing")
	var res map[string]struct {
		Cells []string
		Shown int
	}
	require.NoError(t, json.Unmarshal(outBuf.Bytes(), &res), outBuf.String())

	count := func(cells []string) map[string]int {
		c := map[string]int{}
		for _, x := range cells {
			if x != "" {
				c[x]++
				c["lit"]++
			}
		}
		return c
	}
	for name, want := range map[string]struct {
		shown  int
		levels map[string]int
	}{
		"stella": {31, map[string]int{"p-critical": 11, "p-reader": 15, "working": 5, "lit": 31}},
		"mixer":  {12, map[string]int{"p-blocker": 1, "p-critical": 2, "p-fix": 3, "p-reader": 3, "working": 3, "lit": 12}},
		"over":   {8, map[string]int{"p-critical": 2, "p-reader": 5, "working": 1, "lit": 8}},
	} {
		row, ok := res[name]
		require.True(t, ok, "the friends table draws %s: %s", name, outBuf.String())
		assert.Equal(t, want.shown, row.Shown, "%s's figure", name)
		assert.Equal(t, row.Shown, count(row.Cells)["lit"], "%s: lit squares == the shown count", name)
		assert.Equal(t, want.levels, count(row.Cells), "%s: one square per card or read, by level", name)
	}
}
