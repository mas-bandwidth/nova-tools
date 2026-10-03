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
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// lifeRepo is the repository the lifecycle cards name on their REPO: line.
// The tip fake is asked for that repository and for the branch the card was dealt.
const lifeRepo = "acme/widgets"

// Heads of the three dealt branches. Each branch has its own tip, so a head that
// satisfied another branch does not satisfy this one.
const (
	headS11 = landHead
	headS13 = "1111111111111111111111111111111111111111"
	headS12 = "2222222222222222222222222222222222222222"
)

// tipCall is one read of origin's tip: the repository, the branch, and the head returned.
type tipCall struct {
	repo, branch, head string
}

// friendSyncView is friend sync --json: how many cards it delivered and finished, and the lines it said.
type friendSyncView struct {
	Delivered int      `json:"delivered"`
	Finished  int      `json:"finished"`
	Cards     []string `json:"cards"`
}

func lifeBrief(lead string) string {
	return passingBrief(lead + "\nREPO: " + lifeRepo + "\nWHO: friend amy")
}

// branchTip is origin's tip of the branches this test dealt. It records every ask,
// checks the repository, and returns that branch's head ("" when the branch is not one of them).
func branchTip(t *testing.T, heads map[string]string, calls *[]tipCall) tipFn {
	t.Helper()
	want := swarm.CardRepoURL(lifeRepo)
	return func(_ context.Context, repo, branch string) (string, error) {
		at := heads[branch]
		*calls = append(*calls, tipCall{repo, branch, at})
		assert.Equal(t, want, repo, "the tip is read of the card's repository")
		return at, nil
	}
}

func synced(ta *testApp, root string) friendSyncView {
	ta.t.Helper()
	var v friendSyncView
	ta.json("friend sync --root "+root, &v)
	return v
}

func noTip(t *testing.T) tipFn {
	t.Helper()
	return func(context.Context, string, string) (string, error) {
		t.Error("this report asks for no tip")
		return "", nil
	}
}

// TestFriendCardLifecycleEndToEnd is a friend's card from deal to land
// (docs/SPEC-SPRINT.md section 1): width holds an independent ready card, a
// dependency stays waiting until its need lands, a head that is not the tip is
// refused, and a LAND with no full sha is a failed finish.
func TestFriendCardLifecycleEndToEnd(t *testing.T) {
	t.Parallel()

	ta, cfg := friendApp(t, "amy")
	_, _, err := cfg.Update(context.Background(), config.KindFriend, "amy", map[string]string{"width": "1"}, "t")
	require.NoError(t, err)
	heads := map[string]string{
		"sprint/s1-1.w1.g1.e0": headS11,
		"sprint/s1-3.w1.g1.e0": headS13,
		"sprint/s1-2.w1.g1.e0": headS12,
	}
	var tips []tipCall
	ta.a.tip = branchTip(t, heads, &tips)
	root := t.TempDir()
	ta.ok("friend sync --root " + root)
	ta.ok("friend beat amy")

	var w whereView
	ta.json("where", &w)
	assert.Equal(t, "1", w.Tables[sprint.Friends]["amy"]["width"], "rule friend.width: width is 1")
	assert.Equal(t, "up", w.Tables[sprint.Friends]["amy"]["status"], "rule friend.deal: the friend is up")

	// s1-3 has no Needs line. It is ready beside s1-1, so it is the width witness:
	// a card that only waited on s1-1 would stay waiting with the width check removed.
	briefDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(briefDir, "s1-1.md"), []byte(lifeBrief("s1-1: first card")), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(briefDir, "s1-2.md"), []byte(passingBrief("s1-2: dependent card\nREPO: "+lifeRepo+"\nWHO: friend amy\nNeeds: s1-1")), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(briefDir, "s1-3.md"), []byte(lifeBrief("s1-3: second ready card")), 0o644))

	ta.ok("add --stream s1 --brief-dir " + briefDir)
	ta.ok("start")

	var c1Init, c2Init, c3Init cardView
	ta.json("card s1-1", &c1Init)
	assert.Equal(t, sprint.Ready, c1Init.Primary.Col, "rule friend.deal: s1-1 has no dependency and is ready")
	assert.Equal(t, "friend.amy", c1Init.Who, "rule friend.who: s1-1 who is friend.amy")

	ta.json("card s1-2", &c2Init)
	assert.Equal(t, sprint.Waiting, c2Init.Primary.Col, "rule friend.deps: s1-2 needs s1-1 and is waiting")
	assert.Equal(t, "friend.amy", c2Init.Who, "rule friend.who: s1-2 who is friend.amy")
	assert.Contains(t, ta.ok("queue --stream s1 --col waiting"), "s1-2 work:s1:waiting waits for: s1-1", "rule friend.deps: the waiting view names s1-1")

	ta.json("card s1-3", &c3Init)
	assert.Equal(t, sprint.Ready, c3Init.Primary.Col, "rule friend.deal: s1-3 is ready beside s1-1")
	assert.Equal(t, "friend.amy", c3Init.Who, "rule friend.who: s1-3 who is friend.amy")
	assert.Empty(t, c3Init.Needs, "s1-3 waits on no card")

	// Width 1 deals s1-1 and leaves s1-3 ready. s1-2 stays waiting on s1-1.
	ta.ok("tick")

	var c1 cardView
	ta.json("card s1-1", &c1)
	require.Len(t, c1.Work, 1, "rule friend.deal: s1-1 has one work card")
	assert.Equal(t, sprint.FriendRow("amy"), c1.Work[0].Row, "rule friend.deal: dealt to the friend's row")
	assert.Equal(t, sprint.Working, c1.Work[0].Col, "rule friend.deal: straight into working")
	assert.Equal(t, "1", c1.Work[0].F("gen"), "rule friend.deal: dealt at generation 1")
	assert.Equal(t, sprint.Working, c1.Primary.Col, "rule friend.deal: primary moved ready to working")

	var c3 cardView
	ta.json("card s1-3", &c3)
	assert.Equal(t, sprint.Ready, c3.Primary.Col, "rule friend.width: s1-3 stays ready at width 1")
	assert.Empty(t, c3.Work, "rule friend.width: s1-3 is not dealt past width")

	var c2 cardView
	ta.json("card s1-2", &c2)
	assert.Equal(t, sprint.Waiting, c2.Primary.Col, "rule friend.deps: s1-2 stays waiting while s1-1 has not landed")
	assert.Empty(t, c2.Work, "rule friend.deps: s1-2 is not dealt while waiting")
	assert.Contains(t, ta.ok("queue --stream s1 --col waiting"), "s1-2 work:s1:waiting waits for: s1-1", "rule friend.deps: the waiting view still names s1-1 after the deal")

	ta.json("where", &w)
	assert.Equal(t, "1", w.Tables[sprint.Work]["s1"][sprint.Ready], "rule friend.width: the ready queue is s1-3")
	assert.Equal(t, "1", w.Tables[sprint.Work]["s1"][sprint.Waiting], "rule friend.deps: s1-2 is the waiting queue")
	assert.Equal(t, "1", w.Tables[sprint.Friends]["amy"]["working"], "rule friend.width: one card is working on her row")
	assert.Equal(t, "0", w.Tables[sprint.Friends]["amy"]["ready"], "rule friend.width: her row's ready count is the cards on it, not the stream")

	out := ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD DELIVERED friend=amy card=s1-1.w1 job=s1-1.w1 branch=sprint/s1-1.w1.g1.e0", "rule friend.sync.deliver")
	assert.Contains(t, out, "delivered=1 finished=0", "rule friend.sync.deliver")

	brief1Text, err := os.ReadFile(filepath.Join(root, "amy-working", "inbox", "s1-1.w1", "BRIEF.md"))
	require.NoError(t, err)
	assert.Contains(t, string(brief1Text), "STATUS: nova-sprint card s1-1.w1, epoch 0, attempt 1", "rule friend.sync.deliver")
	assert.Contains(t, string(brief1Text), "s1-1: first card", "rule friend.sync.deliver")

	secondSync := ta.ok("friend sync --root " + root)
	assert.Contains(t, secondSync, "nothing to do", "rule friend.sync.idempotent: a second sync delivers nothing")
	assert.NotContains(t, secondSync, "FRIEND-CARD DELIVERED", "rule friend.sync.idempotent")

	// A head that is not the tip is refused. The card stays working and the next sync reads the report again.
	const wrongHead = "9999999999999999999999999999999999999999"
	outboxReport(t, root, "amy", "s1-1.w1", "Verdict: LAND\nHead: "+wrongHead+"\n\nPushed an invented commit.\n")
	out = ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD REFUSED friend=amy card=s1-1.w1: Head "+wrongHead+" is not origin's tip of sprint/s1-1.w1.g1.e0, "+headS11+"; the card is not finished, and the next sync reads the report again", "rule friend.report.refusal: a head that is not the tip is refused")
	assert.NotContains(t, out, "FRIEND-CARD FINISHED", "rule friend.report.refusal")
	require.Len(t, tips, 1, "the refusal read the tip once")
	assert.Equal(t, tipCall{swarm.CardRepoURL(lifeRepo), "sprint/s1-1.w1.g1.e0", headS11}, tips[0], "the tip check sees the repository, the dealt branch, and that branch's head")

	ta.json("card s1-1", &c1)
	assert.Equal(t, sprint.Working, c1.Primary.Col, "rule friend.report.refusal: the card stays working")
	assert.Empty(t, c1.Primary.F("result"), "rule friend.report.refusal")

	// A friend held mid-card keeps the card. The deadline judges it. Coming back up does not move it.
	ta.ok("friend down amy")
	ta.json("where", &w)
	assert.Equal(t, "held", w.Tables[sprint.Friends]["amy"]["status"], "rule friend.no-takeback: a friend down is held")

	ta.json("card s1-1", &c1)
	require.Len(t, c1.Work, 1, "rule friend.no-takeback: the work card stays")
	assert.Equal(t, sprint.FriendRow("amy"), c1.Work[0].Row, "rule friend.no-takeback: the card stays on her row")
	assert.Equal(t, sprint.Working, c1.Work[0].Col, "rule friend.no-takeback: the card stays working")

	ta.mu.Lock()
	ta.now = ta.now.Add(2*time.Hour + time.Minute)
	ta.mu.Unlock()
	ta.ok("tick")

	gLate := ta.group(sprint.NWorkLate, "s1")
	assert.Equal(t, []string{"s1-1"}, gLate.Primaries, "rule friend.deadline: a work card past its deadline is judged")
	assert.Contains(t, gLate.What, "s1-1.w1", "rule friend.deadline: the judgment names the card")

	ta.ok("friend up amy")
	ta.ok("friend beat amy")
	ta.json("where", &w)
	assert.Equal(t, "up", w.Tables[sprint.Friends]["amy"]["status"], "rule friend.no-takeback: the friend is up again")

	// HOLD, FAIL, and a LAND with no full sha are failed finishes. The card is in review.
	// A missing Head is the same finish as Head: abc (docs/SPEC-SPRINT.md section 1).
	for _, report := range []string{
		"Verdict: HOLD\n\nThe build failed: gate is red.\n",
		"Verdict: FAIL\n\nThe build failed: gate is red.\n",
		"Verdict: LAND\n\nNo head was provided in this report.\n",
	} {
		probeApp, probeCfg := friendApp(t, "amy")
		_, _, err := probeCfg.Update(context.Background(), config.KindFriend, "amy", map[string]string{"width": "1"}, "t")
		require.NoError(t, err)
		probeApp.a.tip = noTip(t)
		probeRoot := t.TempDir()
		probeApp.ok("friend sync --root " + probeRoot)
		probeApp.ok("friend beat amy")
		briefP := filepath.Join(t.TempDir(), "s1-1.md")
		require.NoError(t, os.WriteFile(briefP, []byte(lifeBrief("s1-1: probe")), 0o644))
		probeApp.ok("add --stream s1 --brief-dir " + filepath.Dir(briefP))
		probeApp.ok("start")
		probeApp.ok("tick")
		probeApp.ok("friend sync --root " + probeRoot)

		outboxReport(t, probeRoot, "amy", "s1-1.w1", report)
		syncOut := probeApp.ok("friend sync --root " + probeRoot)
		assert.Contains(t, syncOut, "result=failed", "rule friend.report.failure: the finish is failed")
		assert.NotContains(t, syncOut, "FRIEND-CARD REFUSED", "rule friend.report.failure: a failed finish is not a refusal")
		if strings.Contains(report, "Verdict: LAND") {
			assert.Contains(t, syncOut, "friend amy LAND with no Head: <full sha>", "rule friend.report.failure: an empty head says what the report lacks")
			assert.Contains(t, syncOut, "head=-", "rule friend.report.failure: an empty head records no head")
		}

		probeApp.ok("tick")
		gFail := probeApp.group(sprint.NWorkFailed, "s1")
		assert.Equal(t, []string{"s1-1"}, gFail.Primaries, "rule friend.report.failure: the judgment groups the card")
		assert.Contains(t, strings.Join(gFail.Notes, " ")+gFail.What, "friend amy ", "rule friend.report.failure: the judgment carries the friend's report")

		var probeCard cardView
		probeApp.json("card s1-1", &probeCard)
		assert.Equal(t, "failed", probeCard.Primary.F("result"), "rule friend.report.failure: the result is failed")
		assert.Equal(t, sprint.Review, probeCard.Primary.Col, "rule friend.report.failure: the card is in review")

		again := probeApp.ok("friend sync --root " + probeRoot)
		assert.Contains(t, again, "nothing to do", "rule friend.report.failure: the card is finished, so the next sync does not read the report as a refusal")
		assert.NotContains(t, again, "FRIEND-CARD REFUSED")
		assert.NotContains(t, again, "FRIEND-CARD FINISHED")
	}

	// The ok finish frees her row before any land. The next tick deals s1-3 while s1-2 is still waiting on s1-1.
	outboxReport(t, root, "amy", "s1-1.w1", "# s1-1\n\n**Verdict:** LAND\nHead: "+headS11+"\n\nImplementation complete and tests green.\n")
	out = ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD FINISHED friend=amy card=s1-1.w1 result=ok head="+headS11, "rule friend.report.land: a head that is the tip finishes ok")
	assert.Contains(t, out, "delivered=0 finished=1")

	ta.ok("tick")
	ta.json("card s1-1", &c1)
	assert.Equal(t, sprint.Review, c1.Primary.Col, "rule friend.deal: an ok finish leaves the card in review")
	assert.Equal(t, headS11, c1.Primary.F("head"))
	assert.Equal(t, "ok", c1.Primary.F("result"))

	ta.json("card s1-3", &c3)
	require.Len(t, c3.Work, 1, "rule friend.width: s1-3 is dealt once the finish frees the row")
	assert.Equal(t, sprint.FriendRow("amy"), c3.Work[0].Row, "rule friend.width: s1-3 is on her row")
	assert.Equal(t, sprint.Working, c3.Work[0].Col, "rule friend.width: s1-3 is working")
	assert.Equal(t, sprint.Working, c3.Primary.Col, "rule friend.width: the primary is working")

	ta.json("card s1-2", &c2)
	assert.Equal(t, sprint.Waiting, c2.Primary.Col, "rule friend.deps: s1-2 stays waiting while s1-1 has not landed")
	assert.Empty(t, c2.Work, "rule friend.deps: s1-2 is not dealt")
	assert.Contains(t, ta.ok("queue --stream s1 --col waiting"), "s1-2 work:s1:waiting waits for: s1-1", "rule friend.deps: the waiting view still names s1-1")

	for _, r := range []string{"reader-a", "reader-b"} {
		if code, _, _ := ta.do("read --as " + r + " --begin --epoch 0"); code == 0 {
			ta.ok("read --as " + r + " --ok --epoch 0")
		}
	}

	out = ta.ok("accept --read-ok")
	assert.Contains(t, out, "ACCEPT OK moved=1")
	ta.ok("merge --stream s1 --batch 1")
	ta.ok("tick")

	ta.json("card s1-1", &c1)
	assert.Equal(t, sprint.Landed, c1.Primary.Col, "rule friend.chaining: s1-1 is landed")

	ta.json("card s1-2", &c2)
	assert.Equal(t, sprint.Ready, c2.Primary.Col, "rule friend.deps: s1-2 is ready once s1-1 has landed")
	assert.Empty(t, c2.Work, "rule friend.width: s1-2 is not dealt while s1-3 holds the one slot")

	ta.json("card s1-3", &c3)
	require.Len(t, c3.Work, 1, "rule friend.chaining: s1-3 stays the card on her row")
	assert.Equal(t, sprint.Working, c3.Work[0].Col, "rule friend.chaining: s1-3 is still working")
	assert.Equal(t, sprint.Working, c3.Primary.Col)

	out = ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD DELIVERED friend=amy card=s1-3.w1 job=s1-3.w1 branch=sprint/s1-3.w1.g1.e0", "rule friend.sync.deliver")

	postLandSync := ta.ok("friend sync --root " + root)
	assert.Contains(t, postLandSync, "nothing to do", "rule friend.sync.idempotent: a repeated sync changes nothing")
	assert.NotContains(t, postLandSync, "FRIEND-CARD DELIVERED")
	assert.NotContains(t, postLandSync, "FRIEND-CARD FINISHED")

	outboxReport(t, root, "amy", "s1-3.w1", "# s1-3\n\n**Verdict:** LAND\nHead: "+headS13+"\n\nDone s1-3.\n")
	out = ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD FINISHED friend=amy card=s1-3.w1 result=ok head="+headS13)

	ta.ok("tick")
	for _, r := range []string{"reader-a", "reader-b"} {
		if code, _, _ := ta.do("read --as " + r + " --begin --epoch 0"); code == 0 {
			ta.ok("read --as " + r + " --ok --epoch 0")
		}
	}
	ta.ok("accept --read-ok")
	ta.ok("merge --stream s1 --batch 1")
	ta.ok("tick")

	ta.json("card s1-3", &c3)
	assert.Equal(t, sprint.Landed, c3.Primary.Col, "rule friend.chaining: s1-3 is landed")

	ta.json("card s1-2", &c2)
	require.Len(t, c2.Work, 1, "rule friend.chaining: s1-2 is dealt once s1-3's finish frees the row")
	assert.Equal(t, sprint.FriendRow("amy"), c2.Work[0].Row, "rule friend.chaining: s1-2 is on her row")
	assert.Equal(t, sprint.Working, c2.Work[0].Col)

	ta.ok("friend sync --root " + root)
	outboxReport(t, root, "amy", "s1-2.w1", "# s1-2\n\n**Verdict:** LAND\nHead: "+headS12+"\n\nDone s1-2.\n")
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
	assert.Equal(t, sprint.Landed, c2.Primary.Col, "rule friend.chaining: s1-2 is landed")
	ta.json("card s1-1", &c1)
	assert.Equal(t, sprint.Landed, c1.Primary.Col)
	assert.Equal(t, sprint.Landed, c2.Primary.Col)
	assert.Equal(t, sprint.Landed, c3.Primary.Col)
	assert.Contains(t, ta.ok("where"), "DONE", "rule friend.chaining: the sprint reaches DONE")

	assert.Equal(t, []tipCall{
		{swarm.CardRepoURL(lifeRepo), "sprint/s1-1.w1.g1.e0", headS11},
		{swarm.CardRepoURL(lifeRepo), "sprint/s1-1.w1.g1.e0", headS11},
		{swarm.CardRepoURL(lifeRepo), "sprint/s1-3.w1.g1.e0", headS13},
		{swarm.CardRepoURL(lifeRepo), "sprint/s1-2.w1.g1.e0", headS12},
	}, tips, "every land asks the tip of the repository and of the branch that card was dealt")

	ta.clean()
}

// TestAStaleFriendReportDoesNotFinishALaterAttemptOrEpoch keeps a LAND report
// across a rework and a clear. Sync finishes only the report whose job is the
// card working now, and only when its head is the tip of that card's branch
// (docs/SPEC-SPRINT.md section 1; sprint.StoredID).
func TestAStaleFriendReportDoesNotFinishALaterAttemptOrEpoch(t *testing.T) {
	t.Parallel()
	const (
		branch1  = "sprint/s1-1.w1.g1.e0"
		branch2  = "sprint/s1-1.w2.g1.e0"
		branchE  = "sprint/s1-1.w1.g1.e1"
		headA    = landHead
		headB    = "fedcba9876543210fedcba9876543210fedcba98"
		headC    = "abcdefabcdefabcdefabcdefabcdefabcdefabcd"
		job1     = "s1-1.w1"
		job2     = "s1-1.w2"
		jobE     = "s1-1.w1~1"
		wrongJob = "s9-9.w1"
	)
	ta, cfg := friendApp(t, "amy")
	_, _, err := cfg.Update(context.Background(), config.KindFriend, "amy", map[string]string{"width": "1"}, "t")
	require.NoError(t, err)
	var calls []tipCall
	ta.a.tip = branchTip(t, map[string]string{branch1: headA, branch2: headB, branchE: headC}, &calls)
	root := t.TempDir()
	ta.ok("friend sync --root " + root)
	ta.ok("friend beat amy")

	briefDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(briefDir, "s1-1.md"), []byte(lifeBrief("s1-1: one card")), 0o644))
	ta.ok("add --stream s1 --brief-dir " + briefDir)
	ta.ok("start")
	ta.ok("tick")

	calls = nil
	first := synced(ta, root)
	assert.Equal(t, 1, first.Delivered)
	assert.Equal(t, 0, first.Finished)
	assert.Contains(t, strings.Join(first.Cards, "\n"), "job="+job1+" branch="+branch1)
	assert.Empty(t, calls, "a delivery asks no tip")

	outboxReport(t, root, "amy", job1, "Verdict: LAND\nHead: "+headA+"\n\nAttempt one is pushed.\n")
	calls = nil
	landed := synced(ta, root)
	assert.Equal(t, 0, landed.Delivered)
	assert.Equal(t, 1, landed.Finished)
	assert.Contains(t, strings.Join(landed.Cards, "\n"), "result=ok head="+headA)
	require.Equal(t, []tipCall{{swarm.CardRepoURL(lifeRepo), branch1, headA}}, calls, "attempt 1 is accepted at its own branch's tip")

	kept, err := os.ReadFile(filepath.Join(root, "amy-working", "outbox", job1, "REPORT.md"))
	require.NoError(t, err)

	// The next attempt is another job and another branch. The old report stays where it was.
	ta.ok("rework s1-1 --fix again")
	ta.ok("tick")
	var c cardView
	ta.json("card s1-1", &c)
	assert.Equal(t, sprint.Working, c.Primary.Col, "the next attempt is working")
	assert.Equal(t, "2", c.Primary.F("attempt"))
	var working *sprint.Card
	for _, wcard := range c.Work {
		if wcard.Col == sprint.Working {
			working = wcard
		}
	}
	require.NotNil(t, working, "a work card is working")
	assert.Equal(t, job2, working.ID)

	calls = nil
	dealt := synced(ta, root)
	assert.Equal(t, 1, dealt.Delivered, "the new attempt is delivered")
	assert.Equal(t, 0, dealt.Finished, "the previous attempt's report does not finish it")
	assert.Contains(t, strings.Join(dealt.Cards, "\n"), "job="+job2+" branch="+branch2)
	assert.NotContains(t, strings.Join(dealt.Cards, "\n"), "FRIEND-CARD FINISHED")
	assert.Empty(t, calls, "the old report is not read, so no tip is asked")
	brief2, err := os.ReadFile(filepath.Join(root, "amy-working", "inbox", job2, "BRIEF.md"))
	require.NoError(t, err)
	assert.Contains(t, string(brief2), "STATUS: nova-sprint card s1-1.w2, epoch 0, attempt 2; push your work to the branch "+branch2)

	// A report filed under another card, and the report left on the old job, change nothing.
	outboxReport(t, root, "amy", wrongJob, "Verdict: LAND\nHead: "+headB+"\n\nThis report names another card.\n")
	calls = nil
	stale := synced(ta, root)
	assert.Equal(t, 0, stale.Delivered)
	assert.Equal(t, 0, stale.Finished)
	assert.Empty(t, stale.Cards)
	assert.Empty(t, calls)
	ta.json("card s1-1", &c)
	assert.Equal(t, sprint.Working, c.Primary.Col)
	assert.Empty(t, c.Primary.F("result"))
	still, err := os.ReadFile(filepath.Join(root, "amy-working", "outbox", job1, "REPORT.md"))
	require.NoError(t, err)
	assert.Equal(t, string(kept), string(still), "the old report is kept")

	// The previous attempt's tip does not satisfy the new branch.
	outboxReport(t, root, "amy", job2, "Verdict: LAND\nHead: "+headA+"\n\nThe previous attempt's tip.\n")
	calls = nil
	refused := synced(ta, root)
	assert.Equal(t, 0, refused.Delivered)
	assert.Equal(t, 0, refused.Finished)
	assert.Contains(t, strings.Join(refused.Cards, "\n"), "Head "+headA+" is not origin's tip of "+branch2+", "+headB)
	assert.NotContains(t, strings.Join(refused.Cards, "\n"), "FRIEND-CARD FINISHED")
	require.Equal(t, []tipCall{{swarm.CardRepoURL(lifeRepo), branch2, headB}}, calls, "the tip check sees the new attempt's repository, branch, and head")
	ta.json("card s1-1", &c)
	assert.Equal(t, sprint.Working, c.Primary.Col, "a stale head leaves the new attempt working")
	assert.Empty(t, c.Primary.F("result"))

	outboxReport(t, root, "amy", job2, "Verdict: LAND\nHead: "+headB+"\n\nAttempt two is pushed.\n")
	calls = nil
	ok2 := synced(ta, root)
	assert.Equal(t, 0, ok2.Delivered)
	assert.Equal(t, 1, ok2.Finished)
	assert.Contains(t, strings.Join(ok2.Cards, "\n"), "result=ok head="+headB)
	require.Equal(t, []tipCall{{swarm.CardRepoURL(lifeRepo), branch2, headB}}, calls)
	ta.json("card s1-1", &c)
	assert.Equal(t, sprint.Review, c.Primary.Col)
	assert.Equal(t, headB, c.Primary.F("head"))
	assert.Equal(t, "ok", c.Primary.F("result"))
	still, err = os.ReadFile(filepath.Join(root, "amy-working", "outbox", job1, "REPORT.md"))
	require.NoError(t, err)
	assert.Equal(t, string(kept), string(still), "accepting the new attempt leaves the old report in place")

	// A clear brings the card id back as another job. Reports of the old epoch stay on disk.
	ta.ok("clear --confirm sprint")
	var w whereView
	ta.json("where", &w)
	assert.Equal(t, uint64(1), w.Epoch)
	ta.ok("friend sync --root " + root)
	ta.ok("friend beat amy")
	ta.ok("add --stream s1 --brief-dir " + briefDir)
	ta.ok("start")
	ta.ok("tick")

	calls = nil
	epochDeal := synced(ta, root)
	assert.Equal(t, 1, epochDeal.Delivered)
	assert.Equal(t, 0, epochDeal.Finished, "no report of the old epoch finishes the new one")
	assert.Contains(t, strings.Join(epochDeal.Cards, "\n"), "job="+jobE+" branch="+branchE)
	assert.NotContains(t, strings.Join(epochDeal.Cards, "\n"), "FRIEND-CARD FINISHED")
	assert.Empty(t, calls)
	briefE, err := os.ReadFile(filepath.Join(root, "amy-working", "inbox", jobE, "BRIEF.md"))
	require.NoError(t, err)
	assert.Contains(t, string(briefE), "STATUS: nova-sprint card s1-1.w1, epoch 1, attempt 1; push your work to the branch "+branchE)

	calls = nil
	epochStale := synced(ta, root)
	assert.Equal(t, 0, epochStale.Delivered)
	assert.Equal(t, 0, epochStale.Finished)
	assert.Empty(t, epochStale.Cards)
	assert.Empty(t, calls, "the old jobs and the other card are not read")
	ta.json("card s1-1", &c)
	assert.Equal(t, sprint.Working, c.Primary.Col)
	assert.Equal(t, "1", c.Primary.F("attempt"))
	assert.Empty(t, c.Primary.F("result"))
	still, err = os.ReadFile(filepath.Join(root, "amy-working", "outbox", job1, "REPORT.md"))
	require.NoError(t, err)
	assert.Equal(t, string(kept), string(still))

	outboxReport(t, root, "amy", jobE, "Verdict: LAND\nHead: "+headB+"\n\nThe previous epoch's tip.\n")
	calls = nil
	epochRefused := synced(ta, root)
	assert.Equal(t, 0, epochRefused.Delivered)
	assert.Equal(t, 0, epochRefused.Finished)
	assert.Contains(t, strings.Join(epochRefused.Cards, "\n"), "Head "+headB+" is not origin's tip of "+branchE+", "+headC)
	assert.NotContains(t, strings.Join(epochRefused.Cards, "\n"), "FRIEND-CARD FINISHED")
	require.Equal(t, []tipCall{{swarm.CardRepoURL(lifeRepo), branchE, headC}}, calls, "the tip check sees the new epoch's repository, branch, and head")
	ta.json("card s1-1", &c)
	assert.Equal(t, sprint.Working, c.Primary.Col)
	assert.Empty(t, c.Primary.F("result"))

	outboxReport(t, root, "amy", jobE, "Verdict: LAND\nHead: "+headC+"\n\nThis epoch is pushed.\n")
	calls = nil
	epochOK := synced(ta, root)
	assert.Equal(t, 0, epochOK.Delivered)
	assert.Equal(t, 1, epochOK.Finished)
	assert.Contains(t, strings.Join(epochOK.Cards, "\n"), "FRIEND-CARD FINISHED friend=amy card=s1-1.w1 result=ok head="+headC)
	require.Equal(t, []tipCall{{swarm.CardRepoURL(lifeRepo), branchE, headC}}, calls)
	ta.json("card s1-1", &c)
	assert.Equal(t, sprint.Review, c.Primary.Col)
	assert.Equal(t, headC, c.Primary.F("head"))
	assert.Equal(t, "ok", c.Primary.F("result"))
	still, err = os.ReadFile(filepath.Join(root, "amy-working", "outbox", job1, "REPORT.md"))
	require.NoError(t, err)
	assert.Equal(t, string(kept), string(still), "the epoch before's report is still the file it was")

	ta.clean()
}
