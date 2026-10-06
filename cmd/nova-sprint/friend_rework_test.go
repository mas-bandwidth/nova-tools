package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// friend sync is the documented writer and collector of a friend's cards (docs/FRIENDS.md), so
// it writes a reworked card's brief as the daemon does, with the fix first
// (friend.ReworkedBrief), and holds a LAND whose report does not address that fix
// (friend.LandHeld), whichever of the two gets there first (the night of 2026-10-05: the same
// finding read three, four and five times).
func TestFriendSyncWritesTheFixFirstAndHoldsAnUnaddressedLand(t *testing.T) {
	t.Parallel()
	const fix = "add TestBoundIsAsserted to the gate list"
	ta, root := friendCardApp(t, "friend amy", "amy")
	ta.ok("tick")
	ta.ok("friend sync --root " + root)
	outboxReport(t, root, "amy", "s1-1.w1", "Verdict: HOLD\n\nThe gate is red: TestBoundIsAsserted is not run.\n")
	assert.Contains(t, ta.ok("friend sync --root "+root), "result=failed")
	ta.ok("tick")
	ta.ok("rework s1-1 --fix '" + fix + "'")
	ta.ok("tick")
	out := ta.ok("friend sync --root " + root)
	require.Contains(t, out, "FRIEND-CARD DELIVERED friend=amy card=s1-1.w2", "the rework is dealt to her again")

	// the brief friend sync writes opens with the fix, and its STOP is the fix alone
	job := "s1-1.w2"
	brief := filepath.Join(root, "amy-working", "inbox", job, "BRIEF.md")
	raw, err := os.ReadFile(brief)
	require.NoError(t, err)
	text := string(raw)
	lines := strings.Split(text, "\n")
	require.Greater(t, len(lines), 4)
	assert.True(t, strings.HasPrefix(lines[0], "STATUS: nova-sprint card s1-1.w2, "), lines[0])
	assert.Equal(t, friend.OneThingLeft+fix, lines[1], "the fix is the first line after STATUS")
	assert.True(t, strings.HasPrefix(lines[2], friend.ReaderFoundLabel), lines[2])
	assert.Contains(t, lines[3], "sprint/s1-1.w2", "the brief says the branch the work is carried onto")
	assert.Contains(t, lines[4], "HOLD", "and that a LAND that misses the fix is a HOLD")
	assert.NotContains(t, text, "The coordinator asks:", "the fix is said once, first")
	assert.Contains(t, text, "\nSTOP: "+fix+"\n", "the attempt's STOP is the fix alone")
	assert.Less(t, strings.Index(text, friend.OneThingLeft), strings.Index(text, "s1-1: a friend's card"), "the fix comes before the card")
	assert.Equal(t, fix, friend.BriefFix(text))

	// a LAND whose report does not address the fix is finished a HOLD by friend sync, its head kept
	outboxReport(t, root, "amy", job, "Verdict: LAND\nHead: "+landHead+"\n\nThe change is pushed.\n")
	out = ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD FINISHED friend=amy card=s1-1.w2 result=failed head="+landHead+": friend amy HOLD: held by the daemon: the report says LAND and does not address THE ONE THING LEFT ("+fix+")")
	ta.ok("tick")
	var c cardView
	ta.json("card s1-1", &c)
	assert.Equal(t, "failed", c.Primary.F("result"), "never landed")
	_, err = os.Stat(filepath.Join(root, "amy-working", "outbox", job, "REPORT.md"))
	assert.NoError(t, err, "her report stays as she wrote it")

	// the same packet: a LAND that addresses the fix lands, and one read against the brief she
	// read (her inbox BRIEF.md) is held by that brief's fix, not the server's
	p := sprint.Packet{Card: "c.w2", Gen: 1, Attempt: 2, Branch: "sprint/c.w2.g1.e0", Fix: fix,
		Brief: "c.w2: a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: friend amy"}
	r, err := friendFinish(context.Background(), "amy", p, "Verdict: LAND\nHead: "+landHead+"\n\nTestBoundIsAsserted is in the gate list.\n", "", tipIs(t, landHead))
	require.NoError(t, err)
	assert.False(t, r.Failed, "a LAND that addresses the fix lands")
	assert.Equal(t, landHead, r.Head)
	r, err = friendFinish(context.Background(), "amy", p, "Verdict: LAND\nHead: "+landHead+"\n\nTestBoundIsAsserted is in the gate list.\n", brief, tipIs(t, landHead))
	require.NoError(t, err)
	assert.False(t, r.Failed, "the inbox brief's fix is the same fix")
	first := p
	first.Fix, first.Attempt = "", 1
	r, err = friendFinish(context.Background(), "amy", first, "Verdict: LAND\nHead: "+landHead+"\n\nDone.\n", "", tipIs(t, landHead))
	require.NoError(t, err)
	assert.False(t, r.Failed, "a card with no fix is never held for one")
}
