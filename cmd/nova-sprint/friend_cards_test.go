package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A friend's sprint card (docs/SPEC-SPRINT.md section 5; the owner, 2026-10-03: "Could we
// try expressing the work left for nova-tools-1.1.0 into cards, and doing it via the
// sprint, but doing parts on friends where we would normally do friend work."): a card
// whose brief says WHO: friend <name> is dealt by the tick to her row, friend sync writes
// it into her inbox as inbox/<card>/BRIEF.md, and her outbox/<card>/REPORT.md finishes it:
// LAND to review at its Head, HOLD (or FAIL) as work that came back failed. The friends'
// working directories are under an injected root (friend sync --root).

const landHead = "0123456789abcdef0123456789abcdef01234567"

// friendCardApp is a running sprint with the friends given, each beating, a card s1-1
// whose brief says WHO: <who>, and the root their working directories are under.
func friendCardApp(t *testing.T, who string, friends ...string) (*testApp, string) {
	t.Helper()
	ta, _ := friendApp(t, friends...)
	root := t.TempDir()
	ta.ok("friend sync --root " + root)
	for _, f := range friends {
		ta.ok("friend beat " + f)
	}
	brief := filepath.Join(t.TempDir(), "s1-1.md")
	require.NoError(t, os.WriteFile(brief, []byte(passingBrief("s1-1: a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: "+who)), 0o644))
	ta.ok("add --stream s1 --brief-dir " + filepath.Dir(brief))
	ta.ok("start")
	return ta, root
}

// outboxReport writes the friend's REPORT.md of the job.
func outboxReport(t *testing.T, root, friend, job, report string) {
	t.Helper()
	dir := filepath.Join(root, friend+"-working", "outbox", job)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "REPORT.md"), []byte(report), 0o644))
}

func TestADealToANamedFriendWritesTheBriefIntoHerInbox(t *testing.T) {
	t.Parallel()
	ta, root := friendCardApp(t, "friend stella", "emma", "stella")
	ta.ok("tick")
	var c cardView
	ta.json("card s1-1", &c)
	require.Len(t, c.Work, 1)
	assert.Equal(t, sprint.FriendRow("stella"), c.Work[0].Row, "the card is dealt to stella's row")
	assert.Equal(t, sprint.Working, c.Work[0].Col)
	assert.Equal(t, "friend.stella", c.Who)
	assert.Contains(t, ta.ok("card s1-1"), "who=friend.stella", "card shows who")

	out := ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD DELIVERED friend=stella card=s1-1.w1 job=s1-1.w1 branch=sprint/s1-1.w1.g1.e0")
	assert.Contains(t, out, "delivered=1 finished=0")
	text, err := os.ReadFile(filepath.Join(root, "stella-working", "inbox", "s1-1.w1", "BRIEF.md"))
	require.NoError(t, err)
	lines := strings.Split(string(text), "\n")
	assert.Equal(t, "STATUS: nova-sprint card s1-1.w1, epoch 0, attempt 1; push your work to the branch sprint/s1-1.w1.g1.e0; when done, write outbox/s1-1.w1/REPORT.md with Verdict: LAND|HOLD|FAIL and Head: <sha>", lines[0])
	assert.Contains(t, lines[1], "Work in ~/stella-working/jobs/s1-1.w1/")
	assert.Contains(t, string(text), "\n\ns1-1: a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: friend stella\n", "the brief follows")
	_, err = os.Stat(filepath.Join(root, "emma-working", "inbox", "s1-1.w1"))
	assert.True(t, os.IsNotExist(err), "emma's inbox is not written")

	// a sync after a sync delivers nothing again, and the card is no job of hers
	assert.Contains(t, ta.ok("friend sync --root "+root), "nothing to do")
	// the friends table counts it under working; the fleet table names no friend
	frame := ta.frame()
	assert.Contains(t, tableOf(frame, sprint.Friends), "stella  |     0 |       1 |     8 |    0 | 0.0% | up")
	assert.NotContains(t, tableOf(frame, sprint.Fleet), "friend")
	var w whereView
	ta.json("where", &w)
	assert.NotContains(t, w.Tables[sprint.Fleet], sprint.FriendRow("stella"))
	assert.Equal(t, "1", w.Tables[sprint.Friends]["stella"]["working"])
	ta.clean()
}

func TestFriendSyncFinishesALandReportAndTheCardReachesReview(t *testing.T) {
	t.Parallel()
	ta, root := friendCardApp(t, "friend", "stella")
	ta.ok("tick")
	ta.ok("friend sync --root " + root)
	outboxReport(t, root, "stella", "s1-1.w1", "# s1-1\n\n**Verdict:** LAND\nHead: "+landHead+"\n\nThe change is pushed and the gate is green.\nTwo files.\n\nMore detail.\n")
	out := ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD FINISHED friend=stella card=s1-1.w1 result=ok head="+landHead+": friend stella LAND: The change is pushed and the gate is green. Two files.")
	assert.Contains(t, out, "delivered=0 finished=1")
	ta.ok("tick")
	var c cardView
	ta.json("card s1-1", &c)
	assert.Equal(t, sprint.Review, c.Primary.Col, "LAND is a worker's ok: the card is in review, for its reads")
	assert.Equal(t, landHead, c.Primary.F("head"))
	assert.Equal(t, "ok", c.Primary.F("result"))
	assert.Equal(t, "sprint/s1-1.w1.g1.e0", c.Work[0].F("branch"))
	// collected once: the card is finished, and the sync after finishes nothing
	assert.Contains(t, ta.ok("friend sync --root "+root), "nothing to do")
	assert.Contains(t, tableOf(ta.frame(), sprint.Friends), "stella  |     0 |       0 |     8 |    1 | 100.0% | up")
}

func TestAHoldReportRaisesTheWorkCameBackFailedJudgment(t *testing.T) {
	t.Parallel()
	ta, root := friendCardApp(t, "friend stella", "stella")
	ta.ok("tick")
	ta.ok("friend sync --root " + root)
	outboxReport(t, root, "stella", "s1-1.w1", "Verdict: HOLD\n\nThe gate is red: TestX fails at the base too.\n\nDetail.\n")
	assert.Contains(t, ta.ok("friend sync --root "+root), "result=failed")
	ta.ok("tick")
	g := ta.group(sprint.NWorkFailed, "s1")
	assert.Equal(t, []string{"s1-1"}, g.Primaries)
	assert.Contains(t, strings.Join(g.Notes, " ")+g.What, "friend stella HOLD: The gate is red: TestX fails at the base too.")
	var c cardView
	ta.json("card s1-1", &c)
	assert.Equal(t, "failed", c.Primary.F("result"))

	// a LAND with no full sha head is failed too, saying what it lacks
	assert.Equal(t, "friend stella LAND with no Head: <full sha>; Done.", friendFinish("stella", sprint.Packet{Card: "c.w1", Gen: 1}, "Verdict: LAND\nHead: abc\n\nDone.\n").Report)
}

func TestAddHoldsTheWhoLineToTheFriendsTable(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t, "stella")
	ta.ok("friend sync --root " + t.TempDir())
	add := func(who string) (int, string) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "c1.md"), []byte(passingBrief("c1: a card\nWHO: "+who)), 0o644))
		code, _, errs := ta.do("add --stream s1 --brief-dir " + dir)
		return code, errs
	}
	code, errs := add("friend nobody")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "WHO: friend nobody, and nobody is no row of the friends table (friends: stella)")
	code, errs = add("machine")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "WHO: machine is not `friend` or `friend <name>`")
	code, _ = add("friend stella")
	assert.Equal(t, 0, code)
}
