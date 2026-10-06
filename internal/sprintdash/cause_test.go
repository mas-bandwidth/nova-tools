package sprintdash

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A friend's row carries her ok% as the work-fault rate with the brief's and the machinery's
// faults beside it (docs/SPEC-SPRINT.md section 1, "The cause of a failed attempt"): the pull
// view's row and its text say them, a row with none says nothing more, and the page draws
// them as small numbers in the ok% cell and pools the footer over ok and the work's faults.
func TestTheOkCellCarriesTheBriefAndMachineryFaultsBesideIt(t *testing.T) {
	t.Parallel()
	c := copyOf(t, fixture(t))
	c.Tables["friends"]["amy"] = map[string]string{"status": "up", "ready": "0", "working": "1", "width": "8", "done": "10",
		"okpct": "100.0%", "ok": "7", "failed": "0", "brief": "2", "machinery": "1"}
	v, ok := pullView(c, KindFriend, "amy")
	require.True(t, ok)
	assert.Equal(t, "2", v.Row.Brief)
	assert.Equal(t, "1", v.Row.Machinery)
	assert.Contains(t, v.Text(), "done 10 ok 100.0% (brief 2 machinery 1)")
	c.Tables["friends"]["amy"]["brief"], c.Tables["friends"]["amy"]["machinery"] = "0", "0"
	v, _ = pullView(c, KindFriend, "amy")
	assert.NotContains(t, v.Text(), "brief", "no fault but the work's: nothing beside ok%")

	app, err := os.ReadFile("page/app.js")
	require.NoError(t, err)
	js := string(app)
	assert.Contains(t, js, `setOk(r.ok, pct(okv), done, int(m.brief), int(m.machinery));`)
	assert.Contains(t, js, `t.ok / (t.ok + t.failed) * 100`, "the footer pools ok% over ok and the work's faults, never over done")
	assert.False(t, strings.Contains(js, "t.ok / t.done"), "done counts brief and machinery faults: never ok%'s denominator")
}
