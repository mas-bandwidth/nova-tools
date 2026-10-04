package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// friend take (docs/SPEC-SPRINT.md section 1, a friend's card taken back): the
// coordinator takes a dealt card a friend has not started off her row, and the tick
// deals its primary again; a card with a push on its branch is hers and is refused by
// name, nothing written. origin is a fixture: a map of branch to tip, read through the
// injected tip (no socket).
func TestFriendTakeReturnsAnUnstartedCardToReadyAndRefusesAPushedOne(t *testing.T) {
	t.Parallel()
	ta, root := friendCardApp(t, "friend", "amy", "bob")
	pushed := map[string]string{}
	ta.a.tip = func(_ context.Context, repo, branch string) (string, error) {
		assert.Equal(t, friendRepo, repo, "the tip is read of the card's repository")
		return pushed[branch], nil
	}
	// a second card, so amy holds one she pushed and one she did not
	brief := filepath.Join(t.TempDir(), "s1-2.md")
	require.NoError(t, os.WriteFile(brief, []byte(passingBrief("s1-2: a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: friend amy")), 0o644))
	ta.ok("add --stream s1 --brief-dir " + filepath.Dir(brief))
	ta.ok("friend down bob --reason 'out of allowance'")
	ta.ok("tick")
	for _, id := range []string{"s1-1", "s1-2"} {
		var c cardView
		ta.json("card "+id, &c)
		require.Len(t, c.Work, 1, id)
		require.Equal(t, sprint.FriendRow("amy"), c.Work[0].Row, "%s is dealt to amy, bob is held", id)
	}
	ta.ok("friend sync --root " + root)
	pushed["sprint/s1-2.w1.g1.e0"] = landHead

	// a pushed card is refused by name, and nothing is written
	{
		before := ta.applies()
		code, out, errs := ta.do("friend take amy s1-1.w1 s1-2.w1")
		assert.Equal(t, 1, code, out+errs)
		assert.Contains(t, out+errs, "s1-2.w1 has a push on its branch at "+landHead+": it is amy's work")
		assert.Equal(t, before, ta.applies(), "one refusal refuses the step: s1-1.w1 is not taken either")
		code, out, errs = ta.do("friend take amy s1-9.w1")
		assert.Equal(t, 1, code, out+errs)
		assert.Contains(t, out+errs, "s1-9.w1 is no card dealt to friend amy")
	}

	// an unstarted card is taken off her row, its primary goes back to ready (the
	// machine is RUNNING: the work table's move is the next pump's), and the tick deals
	// it again as its next attempt, to the friend up with room; the coordinator holds
	// the stalled friend first, or a card for any friend may go back to her
	{
		ta.ok("friend down amy --reason stalled")
		ta.ok("friend up bob")
		ta.ok("friend beat bob")
		out := ta.ok("friend take amy s1-1 --reason 'she stalled'")
		assert.Contains(t, out, "s1-1.w1 taken back from friend.amy (working, unstarted): she stalled; s1-1 working -> ready", "a primary names its live work card")
		assert.Contains(t, tableOf(ta.frame(), sprint.Friends), "amy     |     0 |       1 |", "s1-1.w1 is off her row; s1-2.w1 stays")
		ta.ok("tick")
		ta.ok("tick")
		story := ta.ok("log --card s1-1")
		assert.Equal(t, 1, strings.Count(story, "taken back from friend amy"), "one history line per take:\n%s", story)
		assert.Contains(t, story, "s1-1 is ready again")
		var c cardView
		ta.json("card s1-1", &c)
		assert.Equal(t, sprint.Working, c.Primary.Col, "dealt again")
		assert.Equal(t, "s1-1.w2", c.Primary.F("work"), "as its next attempt: a new job and a new branch")
		rows := map[string]string{}
		for _, w := range c.Work {
			rows[w.ID] = w.Row
		}
		assert.Equal(t, sprint.FriendRow("bob"), rows["s1-1.w2"], "to the friend up with room")
		assert.Contains(t, ta.ok("friend sync --root "+root), "FRIEND-CARD DELIVERED friend=bob card=s1-1.w2 job=s1-1.w2 branch=sprint/s1-1.w2.g1.e0")
		var s2 cardView
		ta.json("card s1-2", &s2)
		assert.Equal(t, sprint.Working, s2.Primary.Col, "the pushed card stays hers")
		assert.Equal(t, sprint.FriendRow("amy"), s2.Work[0].Row)
	}
}
