package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// friend reconcile <friend> (docs/SPEC-SPRINT.md section 1, friend reconcile; the owner,
// 2026-10-04: "trust but VERIFY"): her inbox/QUEUE.json and outbox, fixtures under
// t.TempDir(), against the in-memory store: a reported card is collected, a card she
// still works is kept, a card she says done with no report and a card her queue does not
// hold are returned to ready and dealt again, and a queue id that is no card of her row
// is named.

// reconcileApp is a running sprint with amy beating, n cards whose briefs say WHO: only
// friend amy, dealt to her row and delivered into her inbox, and the root her directory
// is under. The hard pin is what keeps a returned card ready while her beat has lapsed:
// a preference would overflow to the fleet, and this test deals it to her again only
// once she beats.
func reconcileApp(t *testing.T, n int) (*testApp, string) {
	t.Helper()
	ta, _ := friendApp(t, "amy")
	ta.a.tip = tipIs(t, landHead)
	root := t.TempDir()
	ta.ok("friend sync --root " + root)
	ta.beatUp("amy")
	dir := t.TempDir()
	for i := 1; i <= n; i++ {
		id := "s1-" + strconv.Itoa(i)
		require.NoError(t, os.WriteFile(filepath.Join(dir, id+".md"), []byte(passingBrief(id+": a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: only friend amy")), 0o644))
	}
	ta.ok("add --stream s1 --brief-dir " + dir)
	ta.ok("start")
	ta.ok("tick")
	ta.startFriend("amy", n) // she starts each: friend reconcile settles the cards working on her row
	ta.ok("friend sync --root " + root)
	return ta, root
}

// writeTestQueueFile writes her inbox/QUEUE.json a minute of the sprint's clock after the cards
// were dealt, its time the clock's: her account is newer than the deal.
func writeTestQueueFile(t *testing.T, ta *testApp, root, friend, body string) {
	t.Helper()
	ta.mu.Lock()
	ta.now = ta.now.Add(time.Minute)
	at := ta.now
	ta.mu.Unlock()
	dir := filepath.Join(root, friend+"-working", "inbox")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, "QUEUE.json")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	require.NoError(t, os.Chtimes(path, at, at))
}

func TestFriendReconcileSettlesEachCardOnTheTwin(t *testing.T) {
	t.Parallel()
	ta, root := reconcileApp(t, 4)
	outboxReport(t, root, "amy", "s1-1.w1", "Verdict: LAND\nHead: "+landHead+"\n\nPushed and green.\n")
	writeTestQueueFile(t, ta, root, "amy", `{"tasks":[{"id":"s1-1.w1","state":"done"},{"id":"s1-2.w1","state":"working"},{"id":"s1-3.w1","state":"done"},{"id":"zz-9.w1","state":"working"}]}`)

	// a dry run says each card's settlement and writes nothing
	before := ta.applies()
	dry := ta.ok("friend reconcile amy --root " + root + " --dry-run")
	assert.Contains(t, dry, "FRIEND-RECONCILE COLLECT friend=amy card=s1-1.w1 job=s1-1.w1: outbox/s1-1.w1/REPORT.md is there (dry run: nothing written)")
	assert.Contains(t, dry, "FRIEND-RECONCILE OK friend=amy collected=1 kept=1 returned=2 refused=0 strays=1 (dry run: nothing written)")
	assert.Equal(t, before, ta.applies(), "a dry run writes nothing")

	out := ta.ok("friend reconcile amy --root " + root)
	assert.Contains(t, out, "FRIEND-CARD FINISHED friend=amy card=s1-1.w1 result=ok head="+landHead)
	assert.Contains(t, out, "FRIEND-RECONCILE KEPT friend=amy card=s1-2.w1 job=s1-2.w1: her QUEUE.json says working")
	assert.Contains(t, out, "FRIEND-RECONCILE RETURNED friend=amy card=s1-3.w1: her QUEUE.json says done and outbox/s1-3.w1/REPORT.md is absent")
	assert.Contains(t, out, "FRIEND-RECONCILE RETURNED friend=amy card=s1-4.w1: her QUEUE.json, written at 2030-01-02T03:05:05Z after it was dealt to her at 2030-01-02T03:04:05Z, does not hold it, and outbox/s1-4.w1/REPORT.md is absent")
	assert.Contains(t, out, "NOTE friend=amy: her QUEUE.json says working for zz-9.w1, which is no card working on her row; nothing was done")
	assert.Contains(t, out, "FRIEND-RECONCILE OK friend=amy collected=1 kept=1 returned=2 refused=0 strays=1")

	// one history line each: the log of each returned card holds its return once
	for _, id := range []string{"s1-3", "s1-4"} {
		assert.Equal(t, 1, strings.Count(ta.ok("log --card "+id), "returned by friend reconcile"), id)
	}
	ta.a.sleep(sprint.FriendFinishWindow + time.Second) // her session's evidence lapses: she is down, so nothing is dealt to her
	ta.ok("tick")
	card := func(id string) cardView {
		var c cardView
		ta.json("card "+id, &c)
		return c
	}
	assert.Equal(t, sprint.Review, card("s1-1").Primary.Col, "the collected card goes to review")
	assert.Equal(t, sprint.Working, card("s1-2").Primary.Col, "the kept card is hers still")
	assert.Equal(t, sprint.Ready, card("s1-3").Primary.Col, "the returned card is ready")
	ta.beatUp("amy") // her session answers again, to be up for the deal
	ta.ok("tick")
	c := card("s1-3")
	assert.Equal(t, sprint.Working, c.Primary.Col, "the returned card is dealt again by the tick")
	assert.Equal(t, "s1-3.w2", c.Primary.F("work"), "at its next attempt")
	ta.startFriend("amy", 2) // she starts the cards dealt again
	ta.clean()

	// the cards dealt again, s1-3.w2 and s1-4.w2, were dealt after her account was written:
	// she has not had the chance to account for them, so they are kept, never returned
	again := ta.ok("friend reconcile amy --root " + root + " --dry-run")
	assert.Contains(t, again, "FRIEND-RECONCILE KEPT friend=amy card=s1-3.w2 job=s1-3.w2: her QUEUE.json does not hold it, and was last written at")
	assert.Contains(t, again, "collected=0 kept=3 returned=0 refused=0 strays=3")
}

func TestFriendReconcileRefusesWhatItCannotCompare(t *testing.T) {
	t.Parallel()
	ta, root := reconcileApp(t, 1)
	require.NoError(t, os.Remove(filepath.Join(root, "amy-working", filepath.FromSlash(queueFile))))
	before := ta.applies()
	code, _, errs := ta.do("friend reconcile amy --root " + root)
	assert.Equal(t, 1, code, errs)
	assert.Contains(t, errs, "QUEUE.json is not there")
	writeTestQueueFile(t, ta, root, "amy", `{"tasks":[{"state":"done"}]}`)
	code, _, errs = ta.do("friend reconcile amy --root " + root)
	assert.Equal(t, 1, code, errs)
	assert.Contains(t, errs, "task 1 has no id")
	code, _, errs = ta.do("friend reconcile bob --root " + root)
	assert.Equal(t, 1, code, errs)
	assert.Contains(t, errs, "no friend bob on the friends table (friends: amy)")
	assert.Equal(t, before, ta.applies(), "a refusal writes nothing")
}

// --op is carried, never accepted and ignored: each collect and the return run under an
// operation id of their own beneath it (friendCollect, cmdFriendReconcile), and the store
// looks each one up for its recorded result before it runs (store.callerOp), so a retry
// with the same --op replays. Without --op no step asks for a recorded result.
func TestFriendReconcileRunsEachStepUnderTheOp(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		op    string
		asked int
	}{{"", 0}, {" --op rc-1", 2}} {
		ta, root := reconcileApp(t, 2)
		outboxReport(t, root, "amy", "s1-1.w1", "Verdict: LAND\nHead: "+landHead+"\n\nPushed and green.\n")
		writeTestQueueFile(t, ta, root, "amy", `{"tasks":[{"id":"s1-1.w1","state":"done"},{"id":"s1-2.w1","state":"done"}]}`)
		before := ta.m.Calls["done"]
		out := ta.ok("friend reconcile amy --root " + root + tc.op)
		assert.Contains(t, out, "FRIEND-RECONCILE OK friend=amy collected=1 kept=0 returned=1 refused=0 strays=0", tc.op)
		asked := ta.m.Calls["done"] - before
		if tc.asked == 0 {
			assert.Zero(t, asked, "with no --op no step asks for a recorded result")
		} else {
			assert.GreaterOrEqual(t, asked, tc.asked, "the collect and the return each run under an operation id")
		}
	}
}

// A card of hers finished from the report her session wrote is her session's evidence: past
// her pong's window, the collect makes her up on the finish for sprint.FriendFinishWindow,
// her row naming it and its age, and down again after it (docs/SPEC-FRIEND.md, "Presence
// is her session's evidence").
func TestAFinishFromHerReportIsHerSessionsEvidence(t *testing.T) {
	t.Parallel()
	ta, root := reconcileApp(t, 1)
	ta.a.sleep(sprint.FriendPongWindow)
	ta.ok("friend beat amy")
	f := whereFriends(ta)["amy"]
	assert.Equal(t, sprint.Down, f.Status, "her pong out of its window, and a beat is none")
	assert.Contains(t, f.Evidence, "no card finished within 30m0s")
	outboxReport(t, root, "amy", "s1-1.w1", "Verdict: LAND\nHead: "+landHead+"\n\nPushed and green.\n")
	assert.Contains(t, ta.ok("friend sync --root "+root), "FRIEND-CARD FINISHED friend=amy card=s1-1.w1 result=ok")
	f = whereFriends(ta)["amy"]
	assert.Equal(t, sprint.Up, f.Status)
	assert.Equal(t, "daemon up, finish 0s ago", f.Evidence)
	assert.Equal(t, ta.now.UTC(), f.Finished.UTC())
	ta.a.sleep(sprint.FriendFinishWindow)
	f = whereFriends(ta)["amy"]
	assert.Equal(t, sprint.Down, f.Status, "the finish out of its window")
	assert.Contains(t, f.Evidence, "session deaf 30m0s:")
	assert.Contains(t, f.Evidence, "no card finished within 30m0s")
}
