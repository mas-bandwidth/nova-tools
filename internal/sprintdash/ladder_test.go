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

// ladderShimTail renders the shipped app.js against one snapshot and reports each fleet row's
// cell classes, the Total row's working fraction and the Fleet and Friends subtitles.
const ladderShimTail = `
(async () => {
  nextBody = { build: 'b1', data: input.snap }; await tick();
  const box = doc.getElementById('fleet');
  const out = { rows: {}, total: '', fleetHead: doc.getElementById('fleet-head').textContent, friendsSub: doc.getElementById('friends-sub').textContent };
  box.children.filter(r => r._classes.includes('row') && !r._classes.includes('head')).forEach(r => {
    if (r._classes.includes('total')) { out.total = r.children[4].innerHTML; return; }
    out.rows[r.children[0].textContent] = r.children[3].children.map(c => c.className);
  });
  process.stdout.write(JSON.stringify(out));
})();
`

// TestFleetRowCellsRunInTheLadder: a row's lit cells run blocker, critical, reads (one orange
// cell per two reads, a lone read a whole cell), then the working blue; the Total row carries
// the working "x / y"; a side switched off or tier-limited says so in its subtitle
// (docs/SPEC-SPRINT-DASHBOARD.md, Fleet).
func TestFleetRowCellsRunInTheLadder(t *testing.T) {
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
	snap["tables"].(map[string]any)["fleet"] = map[string]any{
		"mixed": map[string]any{"status": "up", "width": "8", "working": "6", "blocker_working": "1", "critical_working": "1", "reads_working": "3", "normal_working": "2", "done": "10", "ok": "9"},
		"plain": map[string]any{"status": "up", "width": "8", "working": "2", "normal_working": "2", "done": "1000", "ok": "9"},
	}
	snap["fleet_work"], snap["fleet_tiers"] = "off", "all"
	snap["friends_work"], snap["friends_tiers"] = "on", []string{"flash", "pro"}

	in, err := json.Marshal(map[string]any{"appJS": string(file("app.js")), "snap": snap})
	require.NoError(t, err)
	cmd := exec.Command(nodePath, "-e", strings.SplitN(scrollShim, "let draws = 0;", 2)[0]+ladderShimTail)
	cmd.Stdin = bytes.NewReader(in)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &outBuf, &errBuf
	require.NoError(t, cmd.Run(), "node runner failed: %s", errBuf.String())
	var res struct {
		Rows       map[string][]string `json:"rows"`
		Total      string              `json:"total"`
		FleetHead  string              `json:"fleetHead"`
		FriendsSub string              `json:"friendsSub"`
	}
	require.NoError(t, json.Unmarshal(outBuf.Bytes(), &res), outBuf.String())

	assert.Equal(t, []string{"cell p-blocker", "cell p-critical", "cell p-reader", "cell p-reader", "cell working", "cell working", "cell", "cell"}, res.Rows["mixed"], "blocker, critical, two orange cells for three reads, then blue")
	assert.Equal(t, []string{"cell working", "cell working", "cell", "cell", "cell", "cell", "cell", "cell"}, res.Rows["plain"])
	assert.Contains(t, res.Total, `>8</span>`, "the Total's working is the sum of working")
	assert.Contains(t, res.Total, `style="width:2ch">16</span>`, "the Total's width is the sum of width, its denominator as wide as it needs")
	assert.True(t, strings.HasSuffix(res.FleetHead, " · off"), "an off side says so: %q", res.FleetHead)
	assert.True(t, strings.HasSuffix(res.FriendsSub, " · tiers flash, pro"), "a tier-limited side says so: %q", res.FriendsSub)
	assert.Contains(t, string(file("index.html")), ".panel.off { opacity: .45; }", "an off side's panel is dimmed")
}
