package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// collect is the coordinator's hand (the stopgap finish-loop.py, 92 finishes on the night
// of 2026-10-05): a report in any friend's tree finishes the working card it names as her
// row, once; a HOLD is failed; a lane her runner ENDed with no report is returned with
// --dead-lanes; a LAND whose Head is not on origin is refused naming the branch.

func TestCollectFinishesAReportInAnotherFriendsTreeOnce(t *testing.T) {
	t.Parallel()
	ta, root := friendCardApp(t, "friend amy", "amy", "bob")
	ta.ok("tick")
	ta.startFriend("amy", 1)
	// written ahead, in bob's tree: friend sync reads amy's alone
	outboxReport(t, root, "bob", "s1-1.w1", "Verdict: LAND\nHead: "+landHead+"\n\nDone in bob's tree.\n")
	out := ta.ok("collect --root " + root)
	assert.Contains(t, out, "COLLECT s1-1.w1 LAND "+landHead+"\n")
	assert.Contains(t, out, "COLLECT OK friends=2 working=1 landed=1 failed=0 returned=0 refused=0 left=0")
	ta.ok("tick")
	var c cardView
	ta.json("card s1-1", &c)
	assert.Equal(t, sprint.Review, c.Primary.Col)
	assert.Equal(t, landHead, c.Primary.F("head"))
	assert.Contains(t, ta.ok("collect --root "+root), "COLLECT OK friends=2 working=0 landed=0", "a report finished once is never finished twice")
	ta.clean()
}

func TestCollectFailsAReportedHoldAndReturnsANoReportHarnessFault(t *testing.T) {
	t.Parallel()
	ta, root := friendCardApp(t, "friend amy", "amy")
	ta.ok("tick")
	ta.startFriend("amy", 1)
	outboxReport(t, root, "amy", "s1-1.w1", "Verdict: HOLD\n\nThe gate is red.\n")
	out := ta.ok("collect amy --root " + root)
	assert.Contains(t, out, "COLLECT s1-1.w1 FAILED friend amy HOLD: Verdict: HOLD The gate is red.")
	ta.ok("tick")
	assert.Equal(t, []string{"s1-1"}, ta.group(sprint.NWorkFailed, "s1").Primaries)

	// A lane ended with no report, and no live run: --dead-lanes returns it through
	// FriendReturn. A missing harness report is not a worker's failed attempt.
	ta2, root2 := friendCardApp(t, "friend amy", "amy")
	ta2.ok("tick")
	ta2.startFriend("amy", 1)
	dir := filepath.Join(root2, "amy-working")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	end := "2026-10-06 07:10:00 AM END s1-1.w1 model=m exit=1 wall=600s report=no"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "runner.log"), []byte("2026-10-06 07:00:00 AM START s1-1.w1 tier=heavy model=m\n"+end+"\n"), 0o644))
	assert.Contains(t, ta2.ok("collect --root "+root2), "landed=0 failed=0", "dead lanes only when asked")
	assert.Contains(t, ta2.ok("collect --dead-lanes --dry-run --root "+root2), "COLLECT s1-1.w1 RETURNED harness-fault: no report; friend amy lane ended: "+end+" (dry run: not returned)")
	out = ta2.ok("collect --dead-lanes --root " + root2)
	assert.Contains(t, out, "COLLECT s1-1.w1 RETURNED harness-fault: no report; friend amy lane ended: "+end+"\n")
	assert.Contains(t, out, "landed=0 failed=0 returned=1")
	ta2.ok("tick") // the work table's queued return drains at the next tick
	var c cardView
	ta2.json("card s1-1", &c)
	assert.NotEqual(t, "s1-1.w1", c.Primary.F("work"), "the no-report work card was retired for a fresh deal")
	assert.Empty(t, c.Primary.F("result"), "the harness fault is not a failed result")
	for _, g := range ta2.inboxGroups() {
		assert.NotEqual(t, sprint.NWorkFailed, g.Type, "a harness fault must not ask for failed-work judgment")
	}
	assert.NotContains(t, out, "FAILED")
	assert.NotContains(t, ta2.ok("collect --dead-lanes --root "+root2), "RETURNED", "the retired job's END cannot return its next generation")
}

func TestCollectRefusesAHeadNotOnOrigin(t *testing.T) {
	t.Parallel()
	ta, root := friendCardApp(t, "friend amy", "amy")
	const tip = "fedcba9876543210fedcba9876543210fedcba98"
	ta.a.tip = tipIs(t, tip)
	ta.ok("tick")
	ta.startFriend("amy", 1)
	outboxReport(t, root, "amy", "s1-1.w1", "Verdict: LAND\nHead: "+landHead+"\n\nPushed.\n")
	out := ta.ok("collect --root " + root)
	assert.Contains(t, out, "COLLECT s1-1.w1 REFUSED Head "+landHead+" is not origin's tip of sprint/s1-1.w1.g1.e0, "+tip)
	assert.Contains(t, out, "refused=1")
	code, _, errs := ta.do("collect carol --root " + root)
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "friend carol is not on the roster")
}
