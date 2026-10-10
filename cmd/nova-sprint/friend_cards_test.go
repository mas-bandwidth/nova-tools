package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
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
		ta.beatUp(f)
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
	assert.Equal(t, sprint.Ready, c.Work[0].Col, "ready until she starts it")
	assert.Equal(t, "friend.amy", c.Who)
	assert.Contains(t, ta.ok("card s1-1"), "who=friend.amy", "card shows who")

	out := ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD DELIVERED friend=amy card=s1-1.w1 job=s1-1.w1 branch=sprint/s1-1.w1.g1.e0")
	assert.Contains(t, out, "delivered=1 finished=0")
	text, err := os.ReadFile(filepath.Join(root, "amy-working", "inbox", "s1-1.w1", "BRIEF.md"))
	require.NoError(t, err)
	lines := strings.Split(string(text), "\n")
	assert.Equal(t, "STATUS: nova-sprint card s1-1.w1, epoch 0, attempt 1; push your work to the branch sprint/s1-1.w1.g1.e0; when done, write outbox/s1-1.w1/REPORT.md with first line exactly Verdict: LAND|HOLD|FAIL, second line exactly Head: <40-hex> (blank for HOLD and FAIL)", lines[0])
	assert.Contains(t, lines[1], "Work in ~/amy-working/jobs/s1-1.w1/")
	assert.Contains(t, string(text), "\n\ns1-1: a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: friend amy\n", "the brief follows")
	_, err = os.Stat(filepath.Join(root, "bob-working", "inbox", "s1-1.w1"))
	assert.True(t, os.IsNotExist(err), "bob's inbox is not written")

	// a sync after a sync delivers nothing again, and the card is no job of hers
	assert.Contains(t, ta.ok("friend sync --root "+root), "nothing to do")
	// once she starts it the friends table counts it under working; the fleet table names no friend
	ta.startFriend("amy", 1)
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
	for _, tc := range []struct {
		name, prefix string
		note         bool
	}{
		{name: "pinned"},
		{name: "later", prefix: "# s1-1\n\n", note: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ta, root := friendCardApp(t, "friend", "amy")
			ta.ok("tick")
			ta.ok("friend sync --root " + root)
			outboxReport(t, root, "amy", "s1-1.w1", tc.prefix+"Verdict: LAND\nHead: "+landHead+"\n\nThe change is pushed and the gate is green.\nTwo files.\n\nMore detail.\n")
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
			if tc.note {
				assert.Contains(t, c.Work[0].F("report"), "NOTE: friend report's Verdict: is on line 3 and Head: is on line 4")
			} else {
				assert.NotContains(t, c.Work[0].F("report"), "NOTE:")
			}
			// collected once: the card is finished, and the sync after finishes nothing
			assert.Contains(t, ta.ok("friend sync --root "+root), "nothing to do")
			assert.Contains(t, tableOf(ta.frame(), sprint.Friends), "amy     |     0 |       0 |     8 |    1 | 100.0% | up")
		})
	}
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

// A friend's Head line may spell the sha in upper or mixed case: a sha is
// case-insensitive hex, so the head word friendReportOf returns is lower cased, an
// upper-case full sha takes the LAND branch, the tip is read and compared, and the
// card lands at origin's tip in origin's spelling (docs/SPEC-SPRINT.md section 1,
// friend sync: Head is origin's tip of her branch).
func TestAnUpperCaseHeadStillMatchesTheTip(t *testing.T) {
	t.Parallel()
	upper := strings.ToUpper(landHead)
	r, err := friendFinish(context.Background(), "amy",
		sprint.Packet{Card: "c.w1", Gen: 1, Branch: "sprint/c.w1.g1.e0",
			Brief: "c.w1: a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: friend amy"},
		"Verdict: LAND\nHead: "+upper+"\n\nDone.\n",
		tipIs(t, landHead))
	require.NoError(t, err)
	assert.False(t, r.Failed, "an upper-case Head is a full sha: the LAND branch reads the tip")
	assert.Equal(t, landHead, r.Head, "the card lands at origin's tip, in origin's spelling")
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

// A friend's outbox/<job>/REPORT.md that is a symlink, a non-regular file, or larger
// than the read cap is not read: os.ReadFile follows the link, so the file could
// be outside her working directory, and a large report is held in memory whole.
// The card is left working and one FRIEND-CARD line names the path and the
// reason, and the next sync reads it again. friend clean already treats a
// non-regular report as not done (friendclean.go, the Lstat and Mode().IsRegular()
// check near line 217); sync does the same before its read (docs/FRIENDS.md, the
// inbox/outbox standard; docs/SPEC-SPRINT.md section 1, friend sync).
func TestASymlinkedReportIsNotRead(t *testing.T) {
	t.Parallel()
	ta, root := friendCardApp(t, "friend amy", "amy")
	ta.ok("tick")
	ta.ok("friend sync --root " + root) // delivers the brief, the card is working

	// the LAND report the friend would write lives outside her working directory
	outside := t.TempDir()
	target := filepath.Join(outside, "REPORT.md")
	require.NoError(t, os.WriteFile(target, []byte("Verdict: LAND\nHead: "+landHead+"\n\nThe gate is green.\n"), 0o644))

	// outbox/<job>/REPORT.md is a symlink to that outside report: a bare
	// os.ReadFile would follow it and finish the card from a file outside the
	// working directory
	dir := filepath.Join(root, "amy-working", "outbox", "s1-1.w1")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.Symlink(target, filepath.Join(dir, "REPORT.md")))

	out := ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD", "a line names the path and the reason")
	assert.Contains(t, out, "symlink", "the reason names the symlink")
	assert.NotContains(t, out, "FRIEND-CARD FINISHED", "the symlinked report is not read, the card is not finished")

	var c cardView
	ta.json("card s1-1", &c)
	assert.Equal(t, sprint.Working, c.Primary.Col, "the card stays working")
	assert.Empty(t, c.Primary.F("result"), "the card is not finished")
	ta.clean()
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
	assert.Equal(t, sprint.Ready, c.Work[0].Col, "ready until she starts it")
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
	code, errs = add("only friend nobody")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "nobody is no row")
	code, errs = add("machine")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "WHO: machine is not `friend` or `friend <name>`")
	code, _ = add("friend amy")
	assert.Equal(t, 0, code)
}

// A friend's later attempt starts from the current tip of the card's base branch, as a member's
// rework is staged (nova-tools#5215): she carries the last pushed attempt's work onto it herself,
// and the Head she reports is on that tip; a first attempt's brief says nothing of it.
func TestAFriendsReworkStartsFromTheTipOfItsBase(t *testing.T) {
	t.Parallel()
	p := sprint.Packet{Card: "s1-1.w3", Epoch: 0, Attempt: 3, Branch: "sprint/s1-1.w3.g1.e0", BaseHead: landHead, BaseAttempt: 2,
		Brief: "s1-1: a friend's card\nREPO: mas-bandwidth/nova-tools\nBASE: sprint/s1\nWHO: friend amy\n\nThe task.", Fix: "assert the bound"}
	text := friendBrief("amy", p)
	assert.Contains(t, text, "This attempt starts from the current tip of sprint/s1 on origin, never from an older base: fetch it and start your branch there. "+
		"Carry the work of attempt 2 onto it yourself: its head, "+landHead+", is the last pushed by any attempt before this one (`git diff origin/sprint/s1..."+landHead+"` shows that work); where it does not apply cleanly, redo it. "+
		"The Head you report must be origin's tip of your branch when sync reads it; the attempt is expected to start from the tip named above.\n")
	assert.Equal(t, "THE ONE THING LEFT: assert the bound", strings.Split(text, "\n")[1], "a reworked attempt's fix is its first line after STATUS")
	assert.Contains(t, text, "\nThe carried work: attempt 2's head "+landHead+", carried onto sprint/s1-1.w3.g1.e0 from the tip of its base;")
	assert.NotContains(t, text, "start from it.", "never the old head")

	p.BaseHead, p.BaseAttempt = "", 0
	p.Brief = "s1-1: a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: friend amy\n\nThe task."
	assert.Contains(t, friendBrief("amy", p), "the current tip of the repository's default branch on origin, never from an older base: fetch it and start your branch there. No attempt before this one pushed work to carry. The Head you report must be origin's tip of your branch when sync reads it; the attempt is expected to start from the tip named above.\n")

	p.Attempt = 1
	assert.NotContains(t, friendBrief("amy", p), "This attempt starts")
}

// The brief a friend receives for a later attempt says what friendFinish actually checks,
// in the present tense: a LAND's Head must be origin's tip of her branch when sync reads it
// (one git ls-remote), and the attempt is expected to start from the tip named above. A
// friend has no staged commit, so the brief claims no descent check and never says descends
// (docs/SPEC-CARD-CONTRACT.md, where a rework starts; nova-tools#5215).
func TestTheFriendBriefSaysWhatSyncChecks(t *testing.T) {
	t.Parallel()
	p := sprint.Packet{Card: "s1-1.w3", Epoch: 0, Attempt: 3, Branch: "sprint/s1-1.w3.g1.e0", BaseHead: landHead, BaseAttempt: 2,
		Brief: "s1-1: a friend's card\nREPO: mas-bandwidth/nova-tools\nBASE: sprint/s1\nWHO: friend amy\n\nThe task."}
	text := friendBrief("amy", p)
	assert.Contains(t, text, "The Head you report must be origin's tip of your branch when sync reads it", "the brief names the tip-equality rule friendFinish checks")
	assert.Contains(t, text, "the attempt is expected to start from the tip named above", "the brief says where the attempt starts")
	assert.NotContains(t, text, "descend", "the brief claims no descent check, for friendFinish makes none")
}

// A friend's row is fed like a machine's (the owner, 2026-10-04: "Do it just like the
// fleet, you keep people busy by having 2X width queued up in ready per-friend"): at
// width 1 her second card is dealt ready behind her working one, friend sync delivers
// both and writes her queue file saying which is which, and her finish of the first
// takes the second into working with no tick between.
func TestFriendSyncDeliversHerReadyCardsAndKeepsHerQueueFile(t *testing.T) {
	t.Parallel()
	ta, cfg := friendApp(t)
	_, err := cfg.Insert(context.Background(), config.KindFriend, config.Row{Name: "amy", Fields: map[string]string{"width": "1", "tiers": "flash"}}, "t")
	require.NoError(t, err)
	ta.a.tip = tipIs(t, landHead)
	root := t.TempDir()
	ta.ok("friend sync --root " + root)
	ta.beatUp("amy")
	dir := t.TempDir()
	for _, id := range []string{"s1-1", "s1-2", "s1-3"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, id+".md"), []byte(passingBrief(id+": a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: only friend amy")), 0o644))
	}
	ta.ok("add --stream s1 --brief-dir " + dir)
	ta.ok("start")
	ta.ok("tick")
	ta.startFriend("amy", 1) // dealt ready; she starts what her width holds
	var w whereView
	ta.json("where", &w)
	assert.Equal(t, "1", w.Tables[sprint.Friends]["amy"]["working"], "her width working")
	assert.Equal(t, "1", w.Tables[sprint.Friends]["amy"]["ready"], "and as many again ready behind")
	var third cardView
	ta.json("card s1-3", &third)
	assert.Empty(t, third.Work, "the third waits on the work table, dealt to no one")

	out := ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD DELIVERED friend=amy card=s1-1.w1")
	assert.Contains(t, out, "FRIEND-CARD DELIVERED friend=amy card=s1-2.w1", "the ready card is delivered too")
	queue := filepath.Join(root, "amy-working", "inbox", "QUEUE.json")
	var q friendQueue
	text, err := os.ReadFile(queue)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(text, &q))
	assert.Equal(t, []friendTask{{ID: "s1-1.w1", State: "working", Gen: 1, Job: "s1-1.w1"}, {ID: "s1-2.w1", State: "queued", Gen: 1, Job: "s1-2.w1"}}, q.Tasks)

	// she finishes the first: the second is working at once, the third is dealt ready by
	// the next tick, and the queue file follows
	outboxReport(t, root, "amy", "s1-1.w1", "Verdict: LAND\nHead: "+landHead+"\n")
	out = ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD FINISHED friend=amy card=s1-1.w1 result=ok")
	var c cardView
	ta.json("card s1-2", &c)
	require.Len(t, c.Work, 1)
	assert.Equal(t, sprint.Working, c.Work[0].Col, "her finish took her next ready card, no tick between")
	ta.ok("tick")
	ta.json("card s1-2", &c)
	assert.Equal(t, sprint.Ready, c.Work[0].Col, "no start of hers: the tick puts it back ready until she starts it")
	ta.startFriend("amy", 1)
	ta.json("card s1-3", &c)
	require.Len(t, c.Work, 1)
	assert.Equal(t, sprint.Ready, c.Work[0].Col, "the tick fills her room again")
	ta.ok("friend sync --root " + root)
	text, err = os.ReadFile(queue)
	require.NoError(t, err)
	q = friendQueue{}
	require.NoError(t, json.Unmarshal(text, &q))
	assert.Equal(t, []friendTask{{ID: "s1-1.w1", State: "working", Gen: 1, Job: "s1-1.w1"}, {ID: "s1-2.w1", State: "working", Gen: 1, Job: "s1-2.w1"}, {ID: "s1-3.w1", State: "queued", Gen: 1, Job: "s1-3.w1"}}, q.Tasks, "a finished card's record is left as it was (her session marks it done)")
	ta.clean()
}

// docs/FRIENDS.md: the queue records the delivered job and resets completion only for a new assignment.
func TestFriendQueueCarriesTheAssignmentGenerationAndJob(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox"), 0o755))
	path := filepath.Join(dir, "inbox", "QUEUE.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"tasks":[{"id":"c.w1","state":"done","deliverable":"old result"}]}`), 0o600))
	left := func([]string) (map[string]bool, error) { return nil, nil }
	packets := []sprint.Packet{{Card: "c.w1", Gen: 2, Epoch: 15}}
	require.NoError(t, writeQueueFile(dir, map[string]string{"c.w1": "queued"}, left, packets))
	var q friendQueue
	text, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(text, &q))
	assert.Equal(t, []friendTask{{ID: "c.w1", State: "queued", Gen: 2, Job: "c.w1~15.g2"}}, q.Tasks)

	q.Tasks[0].State = "done"
	text, err = json.Marshal(q)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, text, 0o600))
	require.NoError(t, writeQueueFile(dir, map[string]string{"c.w1": "working"}, left, packets))
	text, err = os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(text, &q))
	assert.Equal(t, "done", q.Tasks[0].State, "the same generation preserves completion")

	packets[0].Epoch = 16
	require.NoError(t, writeQueueFile(dir, map[string]string{"c.w1": "queued"}, left, packets))
	text, err = os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(text, &q))
	assert.Equal(t, "queued", q.Tasks[0].State, "a new epoch is a new job too")
	assert.Equal(t, "c.w1~16.g2", q.Tasks[0].Job)
}

// A named friend's configured work restriction is held at admission (docs/SPEC-SPRINT.md
// section 1, a friend's card): a card whose stream or KIND is outside her streams and
// kinds is refused at add and at brief, with the restriction named, so it never sits
// undealable.
func TestAddAndBriefRefuseNamedFriendOutsideRestrictions(t *testing.T) {
	t.Parallel()
	ta, cfg := friendApp(t, "amy")
	_, _, err := cfg.Update(context.Background(), config.KindFriend, "amy", map[string]string{"streams": " security* ", "kinds": " fix-red, review "}, "test")
	require.NoError(t, err)
	ta.ok("friend sync")
	brief := passingBrief("s1-1: restricted friend card\nREPO: mas-bandwidth/nova-tools\nWHO: friend amy\nKIND: fix-red")
	briefPath := filepath.Join(t.TempDir(), "brief.md")
	require.NoError(t, os.WriteFile(briefPath, []byte(brief), 0o644))
	code, _, errs := ta.do("add --stream s1 --one --brief-file " + briefPath)
	assert.NotZero(t, code)
	assert.Contains(t, errs, "streams restriction")

	ta, cfg = friendApp(t, "amy")
	_, _, err = cfg.Update(context.Background(), config.KindFriend, "amy", map[string]string{"streams": "security*"}, "test")
	require.NoError(t, err)
	ta.ok("friend sync")
	ta.ok("add --stream s1 --count 1 --one")
	code, _, errs = ta.do("brief s1-1 --brief-file " + briefPath)
	assert.NotZero(t, code)
	assert.Contains(t, errs, "streams restriction")
}
