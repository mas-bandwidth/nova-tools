package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// TestFriendCardLifecycleEndToEnd verifies the complete friend-card lifecycle against
// the invariants of docs/SPEC-SPRINT.md §1 ("A friend's card"):
// - friend.who: WHO line sets primary who to friend.<name>
// - friend.width: friend row's width bounds active cards; extra ready cards wait
// - friend.deps: dependencies hold dependent cards in waiting until prerequisites land
// - friend.deal: tick deals ready friend cards straight into working on friend.<name> at gen 1
// - friend.sync.deliver: friend sync writes BRIEF.md into friend's inbox once
// - friend.sync.idempotent: repeated sync delivers nothing new and changes nothing
// - friend.report.refusal: missing head, wrong head, stale head are refused; card stays working
// - friend.no-takeback: friend going down/held mid-card keeps the card on her row
// - friend.deadline: tick judges work card exceeding 2-hour deadline (NWorkLate)
// - friend.report.failure: HOLD and FAIL finish card with result=failed and raise NWorkFailed
// - friend.chaining: landing a card frees width, satisfies dependencies, and enables the next deal
func TestFriendCardLifecycleEndToEnd(t *testing.T) {
	t.Parallel()

	// 1. Setup friend app with friend "emma" (width 1) and reader rows (reader-a, reader-b).
	ta, cfg := friendApp(t, "emma")
	_, _, err := cfg.Update(context.Background(), config.KindFriend, "emma", map[string]string{"width": "1"}, "t")
	require.NoError(t, err)
	ta.a.tip = tipIs(t, landHead)
	root := t.TempDir()
	ta.ok("friend sync --root " + root)
	ta.ok("friend beat emma")

	var w whereView
	ta.json("where", &w)
	assert.Equal(t, "1", w.Tables[sprint.Friends]["emma"]["width"], "rule friend.width: emma width is configured to 1")
	assert.Equal(t, "up", w.Tables[sprint.Friends]["emma"]["status"], "rule friend.deal: emma is up")

	// 2. Add two named-friend cards with dependency: s1-1 (WHO: friend emma) and s1-2 (WHO: friend emma, needs: s1-1)
	// and a width of 1 beside a second ready card s1-3 (WHO: friend emma, ready).
	briefDir := t.TempDir()
	b1 := passingBrief("s1-1: first card for friend emma\nREPO: mas-bandwidth/nova-tools\nWHO: friend emma")
	require.NoError(t, os.WriteFile(filepath.Join(briefDir, "s1-1.md"), []byte(b1), 0o644))

	b2 := passingBrief("s1-2: dependent card for friend emma\nREPO: mas-bandwidth/nova-tools\nWHO: friend emma\nNeeds: s1-1")
	require.NoError(t, os.WriteFile(filepath.Join(briefDir, "s1-2.md"), []byte(b2), 0o644))

	b3 := passingBrief("s1-3: second ready card for friend emma\nREPO: mas-bandwidth/nova-tools\nWHO: friend emma")
	require.NoError(t, os.WriteFile(filepath.Join(briefDir, "s1-3.md"), []byte(b3), 0o644))

	ta.ok("add --stream s1 --brief-dir " + briefDir)
	ta.ok("start")

	var c1Init, c2Init, c3Init cardView
	ta.json("card s1-1", &c1Init)
	assert.Equal(t, sprint.Ready, c1Init.Primary.Col, "rule friend.deal: s1-1 has no dependencies: ready")
	assert.Equal(t, "friend.emma", c1Init.Who, "rule friend.who: s1-1 who is friend.emma")

	ta.json("card s1-2", &c2Init)
	assert.Equal(t, sprint.Waiting, c2Init.Primary.Col, "rule friend.deps: s1-2 needs s1-1: waiting")
	assert.Equal(t, "friend.emma", c2Init.Who, "rule friend.who: s1-2 who is friend.emma")
	assert.Contains(t, ta.ok("queue --stream s1 --col waiting"), "s1-2 work:s1:waiting waits for: s1-1", "rule friend.deps: queue waiting shows dependency")

	ta.json("card s1-3", &c3Init)
	assert.Equal(t, sprint.Ready, c3Init.Primary.Col, "rule friend.deal: s1-3 is ready beside s1-1")
	assert.Equal(t, "friend.emma", c3Init.Who, "rule friend.who: s1-3 who is friend.emma")

	// 3. Tick:
	//    - Only s1-1 is dealt to friend.emma once (placed on friend.emma's fleet row straight into working at gen 1, primary ready -> working).
	//    - s1-3 (second ready card) is NOT dealt because emma's width is 1.
	//    - s1-2 is NOT dealt because s1-1 has not landed.
	ta.ok("tick")

	var c1 cardView
	ta.json("card s1-1", &c1)
	require.Len(t, c1.Work, 1, "rule friend.deal: s1-1 has 1 work card on friend.emma")
	assert.Equal(t, sprint.FriendRow("emma"), c1.Work[0].Row, "rule friend.deal: dealt to friend.emma's fleet row")
	assert.Equal(t, sprint.Working, c1.Work[0].Col, "rule friend.deal: straight into working")
	assert.Equal(t, "1", c1.Work[0].F("gen"), "rule friend.deal: dealt at generation 1")
	assert.Equal(t, sprint.Working, c1.Primary.Col, "rule friend.deal: primary moved ready -> working")

	var c3 cardView
	ta.json("card s1-3", &c3)
	assert.Equal(t, sprint.Ready, c3.Primary.Col, "rule friend.width: s1-3 remains ready because emma's width is 1")
	assert.Empty(t, c3.Work, "rule friend.width: s1-3 is not dealt past width")

	var c2 cardView
	ta.json("card s1-2", &c2)
	assert.Equal(t, sprint.Waiting, c2.Primary.Col, "rule friend.deps: s1-2 remains waiting while s1-1 has not landed")
	assert.Empty(t, c2.Work, "rule friend.deps: s1-2 is not dealt while waiting")

	ta.json("where", &w)
	assert.Equal(t, "1", w.Tables[sprint.Friends]["emma"]["working"], "rule friend.width: emma has 1 working card")
	assert.Equal(t, "0", w.Tables[sprint.Friends]["emma"]["ready"], "rule friend.width: emma has 0 ready cards on her row")

	// 4. Deliver once via friend sync --root <root>:
	//    - BRIEF.md is written into emma's inbox (inbox/s1-1.w1/BRIEF.md).
	//    - A second sync reports "nothing to do" and delivers nothing new (delivered=0).
	out := ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD DELIVERED friend=emma card=s1-1.w1 job=s1-1.w1 branch=sprint/s1-1.w1.g1.e0", "rule friend.sync.deliver")
	assert.Contains(t, out, "delivered=1 finished=0", "rule friend.sync.deliver")

	brief1Text, err := os.ReadFile(filepath.Join(root, "emma-working", "inbox", "s1-1.w1", "BRIEF.md"))
	require.NoError(t, err)
	assert.Contains(t, string(brief1Text), "STATUS: nova-sprint card s1-1.w1, epoch 0, attempt 1", "rule friend.sync.deliver")
	assert.Contains(t, string(brief1Text), "s1-1: first card for friend emma", "rule friend.sync.deliver")

	secondSync := ta.ok("friend sync --root " + root)
	assert.Contains(t, secondSync, "nothing to do", "rule friend.sync.idempotent: second sync reports nothing to do")
	assert.NotContains(t, secondSync, "FRIEND-CARD DELIVERED", "rule friend.sync.idempotent: delivers nothing new")

	// 5. Negative reports (refused, card stays working):
	//    - Missing head: Verdict: LAND with missing / empty Head line.
	//    - Wrong head: Head: <sha> that does not match origin tip.
	//    - Stale head: an older attempt's head (does not match origin tip).
	//    - Each is refused with the reason, and the card stays working.

	// Case 5a: Missing head line
	outboxReport(t, root, "emma", "s1-1.w1", "Verdict: LAND\n\nNo head was provided in this report.\n")
	out = ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD REFUSED friend=emma card=s1-1.w1: a LAND report must name the commit Head: <full sha>, none given; the card is not finished, and the next sync reads the report again", "rule friend.report.refusal: missing head refused")
	assert.NotContains(t, out, "FRIEND-CARD FINISHED", "rule friend.report.refusal")

	ta.json("card s1-1", &c1)
	assert.Equal(t, sprint.Working, c1.Primary.Col, "rule friend.report.refusal: card stays working on missing head")
	assert.Empty(t, c1.Primary.F("result"), "rule friend.report.refusal")

	// Case 5b: Wrong head (invented / mismatched SHA)
	const wrongHead = "fedcba9876543210fedcba9876543210fedcba98"
	outboxReport(t, root, "emma", "s1-1.w1", "Verdict: LAND\nHead: "+wrongHead+"\n\nPushed invented commit.\n")
	out = ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD REFUSED friend=emma card=s1-1.w1: Head "+wrongHead+" is not origin's tip of sprint/s1-1.w1.g1.e0, "+landHead+"; the card is not finished, and the next sync reads the report again", "rule friend.report.refusal: wrong head refused")
	assert.NotContains(t, out, "FRIEND-CARD FINISHED", "rule friend.report.refusal")

	ta.json("card s1-1", &c1)
	assert.Equal(t, sprint.Working, c1.Primary.Col, "rule friend.report.refusal: card stays working on wrong head")
	assert.Empty(t, c1.Primary.F("result"), "rule friend.report.refusal")

	// Case 5c: Stale head (older attempt's head)
	const staleHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	outboxReport(t, root, "emma", "s1-1.w1", "Verdict: LAND\nHead: "+staleHead+"\n\nPushed older attempt commit.\n")
	out = ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD REFUSED friend=emma card=s1-1.w1: Head "+staleHead+" is not origin's tip of sprint/s1-1.w1.g1.e0, "+landHead+"; the card is not finished, and the next sync reads the report again", "rule friend.report.refusal: stale head refused")
	assert.NotContains(t, out, "FRIEND-CARD FINISHED", "rule friend.report.refusal")

	ta.json("card s1-1", &c1)
	assert.Equal(t, sprint.Working, c1.Primary.Col, "rule friend.report.refusal: card stays working on stale head")
	assert.Empty(t, c1.Primary.F("result"), "rule friend.report.refusal")

	// 6. Friend going down mid-card keeps the card and deadline judges it:
	//    - When friend goes down mid-card (friend down emma):
	//      - Friend status becomes held.
	//      - Card stays working on friend.emma (friend.no-takeback: presence, rebalance, level never touch a friend's row).
	//    - Advance clock past 2-hour deadline, tick:
	//      - Deadline rule judges it: raises sprint.NWorkLate ("a work card is past its deadline").
	//    - Restore friend (friend up emma, friend beat emma).
	ta.ok("friend down emma")
	ta.json("where", &w)
	assert.Equal(t, "held", w.Tables[sprint.Friends]["emma"]["status"], "rule friend.no-takeback: friend status is held")

	ta.json("card s1-1", &c1)
	require.Len(t, c1.Work, 1, "rule friend.no-takeback: work card retained while held")
	assert.Equal(t, sprint.FriendRow("emma"), c1.Work[0].Row, "rule friend.no-takeback: card remains on friend.emma")
	assert.Equal(t, sprint.Working, c1.Work[0].Col, "rule friend.no-takeback: card remains working")

	// Advance clock past 2-hour deadline (DeadlineUnfinished = 2 * time.Hour)
	ta.mu.Lock()
	ta.now = ta.now.Add(2*time.Hour + time.Minute)
	ta.mu.Unlock()
	ta.ok("tick")

	gLate := ta.group(sprint.NWorkLate, "s1")
	assert.Equal(t, []string{"s1-1"}, gLate.Primaries, "rule friend.deadline: work card past deadline is judged")
	assert.Contains(t, gLate.What, "s1-1.w1", "rule friend.deadline: judgment notes card")

	// Restore friend: friend up and beat
	ta.ok("friend up emma")
	ta.ok("friend beat emma")
	ta.json("where", &w)
	assert.Equal(t, "up", w.Tables[sprint.Friends]["emma"]["status"], "rule friend.no-takeback: restored friend is up")

	// 7. HOLD and FAIL verdicts:
	//    - Test that Verdict: HOLD and Verdict: FAIL finish the card with result=failed,
	//      and on tick raise the judgment sprint.NWorkFailed ("work came back failed")
	//      carrying friend emma <VERDICT>: <para>.
	for _, verdict := range []string{"HOLD", "FAIL"} {
		probeApp, probeCfg := friendApp(t, "emma")
		_, _, err := probeCfg.Update(context.Background(), config.KindFriend, "emma", map[string]string{"width": "1"}, "t")
		require.NoError(t, err)
		probeApp.a.tip = tipIs(t, landHead)
		probeRoot := t.TempDir()
		probeApp.ok("friend sync --root " + probeRoot)
		probeApp.ok("friend beat emma")

		briefP := filepath.Join(t.TempDir(), "s1-1.md")
		require.NoError(t, os.WriteFile(briefP, []byte(passingBrief("s1-1: probe\nREPO: mas-bandwidth/nova-tools\nWHO: friend emma")), 0o644))
		probeApp.ok("add --stream s1 --brief-dir " + filepath.Dir(briefP))
		probeApp.ok("start")
		probeApp.ok("tick")
		probeApp.ok("friend sync --root " + probeRoot)

		outboxReport(t, probeRoot, "emma", "s1-1.w1", "Verdict: "+verdict+"\n\nThe build failed: gate is red.\n")
		syncOut := probeApp.ok("friend sync --root " + probeRoot)
		assert.Contains(t, syncOut, "result=failed", "rule friend.report.failure: finish reports result=failed")

		probeApp.ok("tick")
		gFail := probeApp.group(sprint.NWorkFailed, "s1")
		assert.Equal(t, []string{"s1-1"}, gFail.Primaries, "rule friend.report.failure: judgment groups s1-1")
		assert.Contains(t, strings.Join(gFail.Notes, " ")+gFail.What, "friend emma "+verdict+": The build failed: gate is red.", "rule friend.report.failure: judgment carries friend verdict and para")

		var probeCard cardView
		probeApp.json("card s1-1", &probeCard)
		assert.Equal(t, "failed", probeCard.Primary.F("result"), "rule friend.report.failure: card result is failed")
		assert.Equal(t, sprint.Review, probeCard.Primary.Col, "rule friend.report.failure: card in review awaiting rework/drop")
	}

	// 8. Positive report:
	//    - Submit valid report (Verdict: LAND, Head: <landHead>).
	//    - friend sync finishes s1-1.w1 with result=ok, moves to review.
	//    - Reader reads run (reader-a, reader-b ok), accept --read-ok, merge, and s1-1 lands.
	//    - A repeated sync after landing changes nothing (delivered=0 finished=0).
	outboxReport(t, root, "emma", "s1-1.w1", "# s1-1\n\n**Verdict:** LAND\nHead: "+landHead+"\n\nImplementation complete and tests green.\n")
	out = ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD FINISHED friend=emma card=s1-1.w1 result=ok head="+landHead, "rule friend.report.refusal: valid land finishes ok")
	assert.Contains(t, out, "delivered=0 finished=1")

	ta.ok("tick")
	ta.json("card s1-1", &c1)
	assert.Equal(t, sprint.Review, c1.Primary.Col, "rule friend.deal: s1-1 moves to review on ok finish")
	assert.Equal(t, landHead, c1.Primary.F("head"))
	assert.Equal(t, "ok", c1.Primary.F("result"))

	// Reader reads run
	for _, r := range []string{"reader-a", "reader-b"} {
		if code, _, _ := ta.do("read --as " + r + " --begin --epoch 0"); code == 0 {
			ta.ok("read --as " + r + " --ok --epoch 0")
		}
	}

	// Accept and merge s1-1
	out = ta.ok("accept --read-ok")
	assert.Contains(t, out, "ACCEPT OK moved=1")
	ta.ok("merge --stream s1 --batch 1")

	// Tick drains s1-1 into landed, resolves s1-2 (dependencies met -> ready),
	// and deals s1-3 (next ready card) to emma under width 1.
	ta.ok("tick")

	ta.json("card s1-1", &c1)
	assert.Equal(t, sprint.Landed, c1.Primary.Col, "rule friend.chaining: s1-1 is landed")

	// 9. Next ready card becomes eligible:
	//    - Once s1-1 lands, emma's working count dropped from 1 to 0.
	//    - s1-2's dependency is met (waiting -> ready), and s1-3 has room under width 1.
	//    - On tick, the next ready card (s1-3) is dealt to emma!
	ta.json("card s1-2", &c2)
	assert.Equal(t, sprint.Ready, c2.Primary.Col, "rule friend.deps: s1-2 dependency met once s1-1 landed: now ready")
	assert.Empty(t, c2.Work, "rule friend.width: s1-2 waits in ready because emma took s1-3 under width 1")

	ta.json("card s1-3", &c3)
	require.Len(t, c3.Work, 1, "rule friend.chaining: s1-3 is dealt to emma")
	assert.Equal(t, sprint.FriendRow("emma"), c3.Work[0].Row, "rule friend.chaining: s1-3 on friend.emma")
	assert.Equal(t, sprint.Working, c3.Work[0].Col, "rule friend.chaining: s1-3 working")
	assert.Equal(t, sprint.Working, c3.Primary.Col, "rule friend.chaining: primary working")

	// Deliver s1-3 to emma's inbox
	out = ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD DELIVERED friend=emma card=s1-3.w1 job=s1-3.w1 branch=sprint/s1-3.w1.g1.e0", "rule friend.sync.deliver")

	// A repeated sync after landing and delivery changes nothing (delivered=0 finished=0)
	postLandSync := ta.ok("friend sync --root " + root)
	assert.Contains(t, postLandSync, "nothing to do", "rule friend.sync.idempotent: repeated sync after land changes nothing")
	assert.NotContains(t, postLandSync, "FRIEND-CARD DELIVERED", "rule friend.sync.idempotent")
	assert.NotContains(t, postLandSync, "FRIEND-CARD FINISHED", "rule friend.sync.idempotent")

	// Finish s1-3 with LAND, accept, merge, land
	outboxReport(t, root, "emma", "s1-3.w1", "# s1-3\n\n**Verdict:** LAND\nHead: "+landHead+"\n\nDone s1-3.\n")
	out = ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD FINISHED friend=emma card=s1-3.w1 result=ok head="+landHead)

	ta.ok("tick")
	for _, r := range []string{"reader-a", "reader-b"} {
		if code, _, _ := ta.do("read --as " + r + " --begin --epoch 0"); code == 0 {
			ta.ok("read --as " + r + " --ok --epoch 0")
		}
	}
	ta.ok("accept --read-ok")
	ta.ok("merge --stream s1 --batch 1")

	// Tick drains s1-3 into landed, freeing emma's width, and deals s1-2 to emma
	ta.ok("tick")

	ta.json("card s1-3", &c3)
	assert.Equal(t, sprint.Landed, c3.Primary.Col, "rule friend.chaining: s1-3 landed")

	// Now s1-2 is dealt to emma on the tick that landed s1-3
	ta.json("card s1-2", &c2)
	require.Len(t, c2.Work, 1, "rule friend.chaining: s1-2 dealt to emma once width freed")
	assert.Equal(t, sprint.FriendRow("emma"), c2.Work[0].Row, "rule friend.chaining: s1-2 on friend.emma")
	assert.Equal(t, sprint.Working, c2.Work[0].Col)

	ta.ok("friend sync --root " + root)
	outboxReport(t, root, "emma", "s1-2.w1", "# s1-2\n\n**Verdict:** LAND\nHead: "+landHead+"\n\nDone s1-2.\n")
	ta.ok("friend sync --root " + root)

	ta.ok("tick")
	for _, r := range []string{"reader-a", "reader-b"} {
		if code, _, _ := ta.do("read --as " + r + " --begin --epoch 0"); code == 0 {
			ta.ok("read --as " + r + " --ok --epoch 0")
		}
	}
	ta.ok("accept --read-ok")
	ta.ok("merge --stream s1 --batch 1")
	ta.ok("tick")

	ta.json("card s1-2", &c2)
	assert.Equal(t, sprint.Landed, c2.Primary.Col, "rule friend.chaining: s1-2 landed")

	// All three cards are now landed, completing the whole lifecycle
	ta.json("card s1-1", &c1)
	assert.Equal(t, sprint.Landed, c1.Primary.Col)
	assert.Equal(t, sprint.Landed, c2.Primary.Col)
	assert.Equal(t, sprint.Landed, c3.Primary.Col)
	assert.Contains(t, ta.ok("where"), "DONE", "rule friend.chaining: sprint reaches DONE")

	ta.clean()
}
