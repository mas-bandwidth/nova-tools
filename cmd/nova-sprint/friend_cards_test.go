package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// A friend's sprint card (docs/SPEC-SPRINT.md section 1; the owner, 2026-10-03: "Could we
// try expressing the work left for nova-tools-1.1.0 into cards, and doing it via the
// sprint, but doing parts on friends where we would normally do friend work."): a card
// whose brief says WHO: friend <name> is dealt by the tick to her row, friend sync writes
// it into her inbox as inbox/<card>/BRIEF.md, and her outbox/<card>/REPORT.md finishes it:
// LAND to review at its Head, HOLD (or FAIL) as work that came back failed. The friends'
// working directories are under an injected root (friend sync --root), and origin's tip of
// every branch is landHead (an injected tip: no socket), unless a test says otherwise.

const landHead = "0123456789abcdef0123456789abcdef01234567"

// friendRepo is the repository the card's REPO: line names, as the tip is asked of it.
var friendRepo = swarm.CardRepoURL("mas-bandwidth/nova-tools")

// tipIs is a tip that is at for every branch of friendRepo.
func tipIs(t *testing.T, at string) tipFn {
	return func(_ context.Context, repo, _ string) (string, error) {
		assert.Equal(t, friendRepo, repo, "the tip is read of the card's repository")
		return at, nil
	}
}

// friendCardApp is a running sprint with the friends given, each beating, a card s1-1
// whose brief says WHO: <who>, and the root their working directories are under.
func friendCardApp(t *testing.T, who string, friends ...string) (*testApp, string) {
	t.Helper()
	ta, _ := friendApp(t, friends...)
	ta.a.tip = tipIs(t, landHead)
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
	ta, root := friendCardApp(t, "friend amy", "bob", "amy")
	ta.ok("tick")
	var c cardView
	ta.json("card s1-1", &c)
	require.Len(t, c.Work, 1)
	assert.Equal(t, sprint.FriendRow("amy"), c.Work[0].Row, "the card is dealt to amy's row")
	assert.Equal(t, sprint.Working, c.Work[0].Col)
	assert.Equal(t, "friend.amy", c.Who)
	assert.Contains(t, ta.ok("card s1-1"), "who=friend.amy", "card shows who")

	out := ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD DELIVERED friend=amy card=s1-1.w1 job=s1-1.w1 branch=sprint/s1-1.w1.g1.e0")
	assert.Contains(t, out, "delivered=1 finished=0")
	text, err := os.ReadFile(filepath.Join(root, "amy-working", "inbox", "s1-1.w1", "BRIEF.md"))
	require.NoError(t, err)
	lines := strings.Split(string(text), "\n")
	assert.Equal(t, "STATUS: nova-sprint card s1-1.w1, epoch 0, attempt 1; push your work to the branch sprint/s1-1.w1.g1.e0; when done, write outbox/s1-1.w1/REPORT.md with Verdict: LAND|HOLD|FAIL and Head: <sha>", lines[0])
	assert.Contains(t, lines[1], "Work in ~/amy-working/jobs/s1-1.w1/")
	assert.Contains(t, string(text), "\n\ns1-1: a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: friend amy\n", "the brief follows")
	_, err = os.Stat(filepath.Join(root, "bob-working", "inbox", "s1-1.w1"))
	assert.True(t, os.IsNotExist(err), "bob's inbox is not written")

	// a sync after a sync delivers nothing again, and the card is no job of hers
	assert.Contains(t, ta.ok("friend sync --root "+root), "nothing to do")
	// the friends table counts it under working; the fleet table names no friend
	frame := ta.frame()
	assert.Contains(t, tableOf(frame, sprint.Friends), "amy     |     0 |       1 |     8 |    0 | 0.0% | up")
	assert.NotContains(t, tableOf(frame, sprint.Fleet), "friend")
	var w whereView
	ta.json("where", &w)
	assert.NotContains(t, w.Tables[sprint.Fleet], sprint.FriendRow("amy"))
	assert.Equal(t, "1", w.Tables[sprint.Friends]["amy"]["working"])
	ta.clean()
}

func TestFriendSyncFinishesALandReportAndTheCardReachesReview(t *testing.T) {
	t.Parallel()
	ta, root := friendCardApp(t, "friend", "amy")
	ta.ok("tick")
	ta.ok("friend sync --root " + root)
	outboxReport(t, root, "amy", "s1-1.w1", "# s1-1\n\n**Verdict:** LAND\nHead: "+landHead+"\n\nThe change is pushed and the gate is green.\nTwo files.\n\nMore detail.\n")
	out := ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD FINISHED friend=amy card=s1-1.w1 result=ok head="+landHead+": friend amy LAND: The change is pushed and the gate is green. Two files.")
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
	assert.Contains(t, tableOf(ta.frame(), sprint.Friends), "amy     |     0 |       0 |     8 |    1 | 100.0% | up")
}

func TestAHoldReportRaisesTheWorkCameBackFailedJudgment(t *testing.T) {
	t.Parallel()
	ta, root := friendCardApp(t, "friend amy", "amy")
	ta.ok("tick")
	ta.ok("friend sync --root " + root)
	outboxReport(t, root, "amy", "s1-1.w1", "Verdict: HOLD\n\nThe gate is red: TestX fails at the base too.\n\nDetail.\n")
	assert.Contains(t, ta.ok("friend sync --root "+root), "result=failed")
	ta.ok("tick")
	g := ta.group(sprint.NWorkFailed, "s1")
	assert.Equal(t, []string{"s1-1"}, g.Primaries)
	assert.Contains(t, strings.Join(g.Notes, " ")+g.What, "friend amy HOLD: The gate is red: TestX fails at the base too.")
	var c cardView
	ta.json("card s1-1", &c)
	assert.Equal(t, "failed", c.Primary.F("result"))

	// a LAND with no full sha head is failed too, saying what it lacks, and asks no tip
	noTip := func(context.Context, string, string) (string, error) {
		t.Fatal("a LAND with no full sha asked for a tip")
		return "", nil
	}
	r, err := friendFinish(context.Background(), "amy", sprint.Packet{Card: "c.w1", Gen: 1}, "Verdict: LAND\nHead: abc\n\nDone.\n", noTip)
	require.NoError(t, err)
	assert.Equal(t, "friend amy LAND with no Head: <full sha>; Done.", r.Report)
}

// A friend's report with no verdict word — no Verdict: line, or one whose word
// is none of LAND, HOLD, FAIL, FAILED, BROKEN — finishes the card failed, never
// ok, and asks no tip: the absent-verdict-means-ok rule of the removed
// directory scan has no place on the sprint-card path.
func TestAReportWithNoVerdictIsFailedNeverOk(t *testing.T) {
	t.Parallel()
	for _, report := range []string{
		"",
		"# done\n\nAll green.\n",
		"Verdict: OK\n",
	} {
		noTip := func(context.Context, string, string) (string, error) {
			t.Fatalf("a report with no LAND asked for a tip: %q", report)
			return "", nil
		}
		r, err := friendFinish(context.Background(), "amy", sprint.Packet{Card: "c.w1", Gen: 1}, report, noTip)
		require.NoError(t, err)
		assert.True(t, r.Failed, "%q: no LAND/HOLD/FAIL verdict is failed, never ok", report)
		assert.Contains(t, r.Report, "is not LAND, HOLD or FAIL", "%q", report)
	}
}

// A friend's LAND finishes ok only at origin's tip of the card's branch: a Head that is
// not the tip (an invented sha, another branch's commit, an older push) is refused naming
// both shas, as is a branch origin does not hold or a tip that cannot be read; the card is
// not finished, and a sync after the tip is right finishes it.
func TestALandWhoseHeadIsNotOriginsTipIsRefused(t *testing.T) {
	t.Parallel()
	const tip = "fedcba9876543210fedcba9876543210fedcba98"
	for _, c := range []struct {
		name, at string
		fail     error
		say      string
	}{
		{"a forged head", tip, nil, "Head " + landHead + " is not origin's tip of sprint/s1-1.w1.g1.e0, " + tip},
		{"no branch", "", nil, "Head " + landHead + ", and origin has no branch sprint/s1-1.w1.g1.e0"},
		{"an unread tip", "", errors.New("ls-remote timed out"), "origin's tip of sprint/s1-1.w1.g1.e0 in " + friendRepo + " cannot be read: ls-remote timed out"},
	} {
		ta, root := friendCardApp(t, "friend amy", "amy")
		ta.a.tip = func(_ context.Context, repo, branch string) (string, error) {
			assert.Equal(t, friendRepo, repo, c.name)
			assert.Equal(t, "sprint/s1-1.w1.g1.e0", branch, c.name)
			return c.at, c.fail
		}
		ta.ok("tick")
		ta.ok("friend sync --root " + root)
		outboxReport(t, root, "amy", "s1-1.w1", "Verdict: LAND\nHead: "+landHead+"\n\nPushed.\n")
		out := ta.ok("friend sync --root " + root)
		assert.Contains(t, out, "FRIEND-CARD REFUSED friend=amy card=s1-1.w1: "+c.say+"; the card is not finished, and the next sync reads the report again", c.name)
		assert.NotContains(t, out, "FRIEND-CARD FINISHED", c.name)
		ta.ok("tick")
		var card cardView
		ta.json("card s1-1", &card)
		assert.Equal(t, sprint.Working, card.Primary.Col, "%s: nothing is finished", c.name)
		assert.Empty(t, card.Primary.F("result"), c.name)
		// origin's tip is the Head now: the next sync finishes it at that tip
		ta.a.tip = tipIs(t, landHead)
		assert.Contains(t, ta.ok("friend sync --root "+root), "result=ok head="+landHead, c.name)
	}
}

// friend sync writes only inside the friend's working directory: a card id that is not
// one, or an inbox/<job> that is a symlink, is refused and nothing is written.
func TestTheInboxRefusesABadCardIDAndASymlinkedJob(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, why, err := friendInbox(dir, sprint.Packet{Card: "../../x.w1"})
	require.NoError(t, err)
	assert.Equal(t, "the card id is not one a job directory may be named by", why)

	elsewhere := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox"), 0o755))
	require.NoError(t, os.Symlink(elsewhere, filepath.Join(dir, "inbox", "s1-1.w1")))
	_, why, err = friendInbox(dir, sprint.Packet{Card: "s1-1.w1"})
	require.NoError(t, err)
	assert.Contains(t, why, "inbox/s1-1.w1 is a symlink or a file, not a directory")

	in, why, err := friendInbox(dir, sprint.Packet{Card: "s1-2.w1", Epoch: 3})
	require.NoError(t, err)
	assert.Empty(t, why)
	assert.Equal(t, filepath.Join(dir, "inbox", "s1-2.w1~3"), in)
}

// A twin's verbs beat its machines, never a friend's row: a twin holding a friend's card
// opens as before.
func TestATwinBeatsNoFriendRow(t *testing.T) {
	t.Parallel()
	ta, _ := friendCardApp(t, "friend amy", "amy")
	ta.ok("tick")
	st := &store.Store{B: ta.m, Names: sprint.Names{}, Now: ta.a.now}
	require.NoError(t, ta.a.beatTwin(context.Background(), st))
}

// The stored view sprint draws no friend's row, as where does not: her row is hidden in
// the fleet table, and every machine's row is drawn.
func TestTheStoredViewHidesAFriendsRow(t *testing.T) {
	t.Parallel()
	ta, _ := friendCardApp(t, "friend amy", "amy")
	ta.ok("tick")
	shapes, err := ta.m.Shapes(context.Background(), []string{sprint.Names{}.Table(sprint.Fleet)})
	require.NoError(t, err)
	hidden := map[string]bool{}
	for _, r := range shapes[0].Rows {
		hidden[r.Key] = r.Hidden
	}
	assert.Equal(t, map[string]bool{"m1": false, "m2": false, sprint.FriendRow("amy"): true}, hidden)
}

// Fleet sync takes no friend's row for a machine: with her card working, it neither holds,
// removes nor drops her row, and her card stays hers.
func TestFleetSyncLeavesAFriendsRowAndCard(t *testing.T) {
	t.Parallel()
	ta, root := friendCardApp(t, "friend amy", "amy")
	inv := newInventory(t)
	ta.a.inventory = inv.fn()
	inv.set("m1", 64)
	inv.set("m2", 64)
	ta.ok("tick")
	// the inventory is the fleet's machines: no drift, her row is none of it
	var rep syncReport
	ta.json("fleet sync --check", &rep)
	assert.Empty(t, rep.Drift, "fleet sync takes a friend's row for no machine")
	// a sync with a machine gone removes it and leaves her row
	inv.remove("m2")
	out := ta.ok("fleet sync")
	assert.NotContains(t, out, "friend", "fleet sync names no friend's row")
	ta.ok("tick")
	var c cardView
	ta.json("card s1-1", &c)
	require.Len(t, c.Work, 1)
	assert.Equal(t, sprint.FriendRow("amy"), c.Work[0].Row, "her card stays on her row")
	assert.Equal(t, sprint.Working, c.Work[0].Col)
	assert.Contains(t, ta.ok("friend sync --root "+root), "FRIEND-CARD DELIVERED friend=amy card=s1-1.w1")
}

func TestAddHoldsTheWhoLineToTheFriendsTable(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t, "amy")
	ta.ok("friend sync --root " + t.TempDir())
	add := func(who string) (int, string) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "c1.md"), []byte(passingBrief("c1: a card\nWHO: "+who)), 0o644))
		code, _, errs := ta.do("add --stream s1 --brief-dir " + dir)
		return code, errs
	}
	code, errs := add("friend nobody")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "WHO: friend nobody, and nobody is no row of the friends table (friends: amy)")
	code, errs = add("machine")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "WHO: machine is not `friend` or `friend <name>`")
	code, _ = add("friend amy")
	assert.Equal(t, 0, code)
}
