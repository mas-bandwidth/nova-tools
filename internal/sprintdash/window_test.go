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

// windowShimTail renders the shipped app.js against one snapshot and reports each friends
// row's working / width cell as the page wrote it.
const windowShimTail = `
(async () => {
  nextBody = { build: 'b1', data: input.snap }; await tick();
  const box = doc.getElementById('friends');
  const out = {};
  box.children.filter(r => r._classes.includes('row') && !r._classes.includes('head') && !r._classes.includes('total')).forEach(r => {
    out[r.children[0].textContent] = r.children[4].innerHTML;
  });
  process.stdout.write(JSON.stringify(out));
})();
`

// TestFriendsRowShowsTheWindowUseBesideTheWidth: a subscription friend's row carries her
// windows' use (where --json's window cell, docs/SPEC-SPRINT.md, the friends table) and the
// page shows it beside her working / width, escaped; a row with none shows the width alone.
func TestFriendsRowShowsTheWindowUseBesideTheWidth(t *testing.T) {
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
	row := func(window string) map[string]any {
		m := map[string]any{"status": "up", "width": "4", "working": "1", "ready": "3", "done": "10", "ok": "9"}
		if window != "" {
			m["window"] = window
		}
		return m
	}
	snap["tables"].(map[string]any)["friends"] = map[string]any{"sub": row("5h 62% 7d 31%"), "metered": row(""), "odd": row("<b>"), "capacity": map[string]any{"status": "up", "width": "4", "working": "1", "jobs_bytes": "200", "free_bytes": "0", "free_inodes": "50", "capacity_error": "<blocked>"}}

	in, err := json.Marshal(map[string]any{"appJS": string(file("app.js")), "snap": snap})
	require.NoError(t, err)
	cmd := exec.Command(nodePath, "-e", strings.SplitN(scrollShim, "let draws = 0;", 2)[0]+windowShimTail)
	cmd.Stdin = bytes.NewReader(in)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &outBuf, &errBuf
	require.NoError(t, cmd.Run(), "node runner failed: %s", errBuf.String())
	var cells map[string]string
	require.NoError(t, json.Unmarshal(outBuf.Bytes(), &cells), outBuf.String())

	require.Contains(t, cells, "capacity")
	assert.Contains(t, cells["capacity"], "jobs 200 B · free 0 B · inodes 50")
	assert.Contains(t, cells["capacity"], "&#60;blocked&#62;")
	require.Contains(t, cells, "sub")
	assert.Contains(t, cells["sub"], `<span class="win"> · 5h 62% 7d 31%</span>`, "the window use beside the width")
	assert.Contains(t, cells["sub"], `class="fb"`, "the width is still there")
	assert.NotContains(t, cells["metered"], `class="win"`, "a row with no window shows the width alone")
	assert.NotContains(t, cells["odd"], "<b>", "the window text is escaped")
	assert.Contains(t, cells["odd"], "&#60;b&#62;")
	assert.Contains(t, string(file("index.html")), ".win {", "the page styles the window use")
}
