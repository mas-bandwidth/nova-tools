package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// TestFriendCardLifecycleEndToEnd verifies the complete friend-card lifecycle:
// setup friend amy (width 1), add dependent cards s1-1 and s1-2, obey dependencies
// and friend width on deal, deliver once to inbox and verify idempotent repeated sync,
// test negative reports (wrong tip, missing branch, invalid head, HOLD/FAIL verdict raising
// work came back failed judgment), positive report finishing s1-1.w1 with result=ok,
// review, reader reads, accept, merge, landing, and chaining s1-2 when s1-1 lands,
// delivering s1-2, finishing it, and landing it.
func TestFriendCardLifecycleEndToEnd(t *testing.T) {
	t.Parallel()

	// 1. Setup friend app with friend "amy" (width 1) and reader rows (reader-a, reader-b).
	ta, cfg := friendApp(t, "amy")
	_, _, err := cfg.Update(context.Background(), config.KindFriend, "amy", map[string]string{"width": "1"}, "t")
	require.NoError(t, err)
	ta.a.tip = tipIs(t, landHead)
	root := t.TempDir()
	ta.ok("friend sync --root " + root)
	ta.ok("friend beat amy")

	var w whereView
	ta.json("where", &w)
	assert.Equal(t, "1", w.Tables[sprint.Friends]["amy"]["width"], "amy width is configured to 1")
	assert.Equal(t, "up", w.Tables[sprint.Friends]["amy"]["status"], "amy is up")

	// 2. Add two named-friend cards with dependency: s1-1 (WHO: friend amy) and s1-2 (WHO: friend amy, needs: s1-1).
	briefDir := t.TempDir()
	b1 := passingBrief("s1-1: first card for friend amy\nREPO: mas-bandwidth/nova-tools\nWHO: friend amy")
	require.NoError(t, os.WriteFile(filepath.Join(briefDir, "s1-1.md"), []byte(b1), 0o644))

	b2 := passingBrief("s1-2: second card for friend amy\nREPO: mas-bandwidth/nova-tools\nWHO: friend amy\nNeeds: s1-1")
	require.NoError(t, os.WriteFile(filepath.Join(briefDir, "s1-2.md"), []byte(b2), 0o644))

	ta.ok("add --stream s1 --brief-dir " + briefDir)
	ta.ok("start")

	var c1Init, c2Init cardView
	ta.json("card s1-1", &c1Init)
	assert.Equal(t, sprint.Ready, c1Init.Primary.Col, "s1-1 has no dependencies: ready")
	ta.json("card s1-2", &c2Init)
	assert.Equal(t, sprint.Waiting, c2Init.Primary.Col, "s1-2 needs s1-1: waiting")
	assert.Contains(t, ta.ok("queue --stream s1 --col waiting"), "s1-2 work:s1:waiting waits for: s1-1")

	// 3. Obey dependencies and width:
	//    - On tick, only s1-1 is dealt (working on amy's row); s1-2 remains waiting/unmet.
	//    - Friend width is 1, so amy takes at most 1 card.
	ta.ok("tick")

	var c1 cardView
	ta.json("card s1-1", &c1)
	require.Len(t, c1.Work, 1, "s1-1 has 1 work card")
	assert.Equal(t, sprint.FriendRow("amy"), c1.Work[0].Row, "s1-1 is dealt to amy's row")
	assert.Equal(t, sprint.Working, c1.Work[0].Col)
	assert.Equal(t, sprint.Working, c1.Primary.Col)
	assert.Equal(t, "friend.amy", c1.Who)

	var c2 cardView
	ta.json("card s1-2", &c2)
	assert.Equal(t, sprint.Waiting, c2.Primary.Col, "s1-2 remains waiting while s1-1 has not landed")
	assert.Empty(t, c2.Work, "s1-2 is not dealt")

	ta.json("where", &w)
	assert.Equal(t, "1", w.Tables[sprint.Friends]["amy"]["working"], "amy takes at most 1 card (width 1)")
	assert.Equal(t, "0", w.Tables[sprint.Friends]["amy"]["ready"])

	// 4. Deliver it once via friend sync --root <root>:
	//    - BRIEF.md is written into amy's inbox (inbox/s1-1.w1/BRIEF.md).
	//    - A repeated sync reports "nothing to do" and does not re-deliver.
	out := ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD DELIVERED friend=amy card=s1-1.w1 job=s1-1.w1 branch=sprint/s1-1.w1.g1.e0")
	assert.Contains(t, out, "delivered=1 finished=0")

	brief1Text, err := os.ReadFile(filepath.Join(root, "amy-working", "inbox", "s1-1.w1", "BRIEF.md"))
	require.NoError(t, err)
	assert.Contains(t, string(brief1Text), "STATUS: nova-sprint card s1-1.w1, epoch 0, attempt 1")
	assert.Contains(t, string(brief1Text), "s1-1: first card for friend amy")

	assert.Contains(t, ta.ok("friend sync --root "+root), "nothing to do", "repeated sync does not re-deliver")

	// 5. Negative report tests:
	//    - Reject missing/wrong/stale head (tip mismatch, missing branch, invalid head format).
	//    - Test HOLD / FAIL verdict: raises work came back failed judgment.

	// Case 5a: Reject tip mismatch (wrong/stale head)
	const forgedTip = "fedcba9876543210fedcba9876543210fedcba98"
	ta.a.tip = func(_ context.Context, repo, branch string) (string, error) {
		return forgedTip, nil
	}
	outboxReport(t, root, "amy", "s1-1.w1", "Verdict: LAND\nHead: "+landHead+"\n\nPushed commit.\n")
	out = ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD REFUSED friend=amy card=s1-1.w1: Head "+landHead+" is not origin's tip of sprint/s1-1.w1.g1.e0, "+forgedTip+"; the card is not finished, and the next sync reads the report again")
	assert.NotContains(t, out, "FRIEND-CARD FINISHED")

	ta.json("card s1-1", &c1)
	assert.Equal(t, sprint.Working, c1.Primary.Col, "tip mismatch: card is not finished")
	assert.Empty(t, c1.Primary.F("result"))

	// Case 5b: Reject missing branch
	ta.a.tip = func(_ context.Context, repo, branch string) (string, error) {
		return "", nil
	}
	out = ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD REFUSED friend=amy card=s1-1.w1: Head "+landHead+", and origin has no branch sprint/s1-1.w1.g1.e0; the card is not finished, and the next sync reads the report again")
	assert.NotContains(t, out, "FRIEND-CARD FINISHED")

	ta.json("card s1-1", &c1)
	assert.Equal(t, sprint.Working, c1.Primary.Col, "missing branch: card is not finished")
	assert.Empty(t, c1.Primary.F("result"))

	// Case 5c: Invalid head format (not a full sha) is rejected as a valid LAND finish
	rInv, err := friendFinish(context.Background(), "amy", sprint.Packet{Card: "s1-1.w1", Gen: 1, Branch: "sprint/s1-1.w1.g1.e0", Brief: b1}, "Verdict: LAND\nHead: not-a-sha\n\nDone.\n", ta.a.tip)
	require.NoError(t, err)
	assert.True(t, rInv.Failed, "invalid head format is marked failed")
	assert.Contains(t, rInv.Report, "friend amy LAND with no Head: <full sha>")

	// Case 5d: Test HOLD / FAIL verdict raises work came back failed judgment (tested with dedicated friend app)
	for _, verdict := range []string{"HOLD", "FAIL"} {
		negApp, negCfg := friendApp(t, "amy")
		_, _, err := negCfg.Update(context.Background(), config.KindFriend, "amy", map[string]string{"width": "1"}, "t")
		require.NoError(t, err)
		negApp.a.tip = tipIs(t, landHead)
		negRoot := t.TempDir()
		negApp.ok("friend sync --root " + negRoot)
		negApp.ok("friend beat amy")

		briefP := filepath.Join(t.TempDir(), "s1-1.md")
		require.NoError(t, os.WriteFile(briefP, []byte(passingBrief("s1-1: probe\nREPO: mas-bandwidth/nova-tools\nWHO: friend amy")), 0o644))
		negApp.ok("add --stream s1 --brief-dir " + filepath.Dir(briefP))
		negApp.ok("start")
		negApp.ok("tick")
		negApp.ok("friend sync --root " + negRoot)

		outboxReport(t, negRoot, "amy", "s1-1.w1", "Verdict: "+verdict+"\n\nThe gate is red: tests failed.\n")
		syncOut := negApp.ok("friend sync --root " + negRoot)
		assert.Contains(t, syncOut, "result=failed")

		negApp.ok("tick")
		g := negApp.group(sprint.NWorkFailed, "s1")
		assert.Equal(t, []string{"s1-1"}, g.Primaries)
		assert.Contains(t, strings.Join(g.Notes, " ")+g.What, "friend amy "+verdict+": The gate is red: tests failed.")

		var negCard cardView
		negApp.json("card s1-1", &negCard)
		assert.Equal(t, "failed", negCard.Primary.F("result"))
		assert.Equal(t, sprint.Review, negCard.Primary.Col)
	}

	// 6. Positive report test:
	//    - Submit valid outbox report with Verdict: LAND and Head: <landHead>.
	//    - friend sync finishes s1-1.w1 with result=ok.
	//    - s1-1 enters review column.
	//    - Reader reads s1-1 (begin, verdict ok), s1-1 is accepted, merges, and lands.
	ta.a.tip = tipIs(t, landHead)
	outboxReport(t, root, "amy", "s1-1.w1", "# s1-1\n\n**Verdict:** LAND\nHead: "+landHead+"\n\nThe changes are clean and tests pass.\n")
	out = ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD FINISHED friend=amy card=s1-1.w1 result=ok head="+landHead)
	assert.Contains(t, out, "delivered=0 finished=1")

	ta.ok("tick")
	ta.json("card s1-1", &c1)
	assert.Equal(t, sprint.Review, c1.Primary.Col, "s1-1 enters review column")
	assert.Equal(t, landHead, c1.Primary.F("head"))
	assert.Equal(t, "ok", c1.Primary.F("result"))

	// Reader reads s1-1 (begin, verdict ok)
	for _, r := range []string{"reader-a", "reader-b"} {
		if code, _, _ := ta.do("read --as " + r + " --begin --epoch 0"); code == 0 {
			ta.ok("read --as " + r + " --ok --epoch 0")
		}
	}

	// s1-1 is accepted, merges, and lands
	out = ta.ok("accept --read-ok")
	assert.Contains(t, out, "ACCEPT OK moved=1")
	ta.ok("merge --stream s1 --batch 1")
	ta.ok("tick")

	ta.json("card s1-1", &c1)
	assert.Equal(t, sprint.Landed, c1.Primary.Col, "s1-1 is landed")

	// 7. Chaining / Eligibility of next card:
	//    - Once s1-1 lands, s1-2's dependencies are met; s1-2 becomes ready.
	//    - On tick, s1-2 is dealt to amy's row (since amy's working count dropped from 1 to 0).
	//    - Sync delivers s1-2 to amy's inbox.
	//    - Finish s1-2 with LAND, accept, merge, and land.
	ta.ok("tick")

	ta.json("card s1-2", &c2)
	require.Len(t, c2.Work, 1, "s1-2 is dealt to amy once dependencies are met and amy has room")
	assert.Equal(t, sprint.FriendRow("amy"), c2.Work[0].Row)
	assert.Equal(t, sprint.Working, c2.Work[0].Col)
	assert.Equal(t, sprint.Working, c2.Primary.Col)

	out = ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD DELIVERED friend=amy card=s1-2.w1 job=s1-2.w1 branch=sprint/s1-2.w1.g1.e0")
	assert.Contains(t, out, "delivered=1 finished=0")

	brief2Text, err := os.ReadFile(filepath.Join(root, "amy-working", "inbox", "s1-2.w1", "BRIEF.md"))
	require.NoError(t, err)
	assert.Contains(t, string(brief2Text), "STATUS: nova-sprint card s1-2.w1, epoch 0, attempt 1")
	assert.Contains(t, string(brief2Text), "s1-2: second card for friend amy")

	// Finish s1-2 with LAND, accept, merge, and land
	outboxReport(t, root, "amy", "s1-2.w1", "# s1-2\n\n**Verdict:** LAND\nHead: "+landHead+"\n\nSecond card clean and tested.\n")
	out = ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD FINISHED friend=amy card=s1-2.w1 result=ok head="+landHead)
	assert.Contains(t, out, "delivered=0 finished=1")

	ta.ok("tick")
	ta.json("card s1-2", &c2)
	assert.Equal(t, sprint.Review, c2.Primary.Col, "s1-2 enters review")

	for _, r := range []string{"reader-a", "reader-b"} {
		if code, _, _ := ta.do("read --as " + r + " --begin --epoch 0"); code == 0 {
			ta.ok("read --as " + r + " --ok --epoch 0")
		}
	}

	out = ta.ok("accept --read-ok")
	assert.Contains(t, out, "ACCEPT OK moved=1")
	ta.ok("merge --stream s1 --batch 1")
	ta.ok("tick")

	ta.json("card s1-2", &c2)
	assert.Equal(t, sprint.Landed, c2.Primary.Col, "s1-2 is landed")

	// Both cards are now landed, completing the whole lifecycle
	ta.json("card s1-1", &c1)
	assert.Equal(t, sprint.Landed, c1.Primary.Col)
	assert.Equal(t, sprint.Landed, c2.Primary.Col)
	assert.Contains(t, ta.ok("where"), "DONE")
	ta.clean()
}
