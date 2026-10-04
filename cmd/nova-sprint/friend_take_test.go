package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A friend's card taken back (docs/SPEC-SPRINT.md section 1; the owner, 2026-10-04: "sounds
// bad, we should fix this"): friend take and friend down, on the twin, with origin's tips
// injected (pushed names the branches origin holds) and no socket.

// takeApp is friendCardApp with more cards for any friend (s1-1 ... s1-n), amy at width 1,
// origin holding only the branches pushed names.
func takeApp(t *testing.T, n int, pushed map[string]string, friends ...string) (*testApp, string) {
	t.Helper()
	ta, root := friendCardApp(t, "friend", friends...)
	ta.a.tip = func(_ context.Context, _, branch string) (string, error) { return pushed[branch], nil }
	for i := 2; i <= n; i++ {
		id := "s1-" + strconv.Itoa(i)
		brief := filepath.Join(t.TempDir(), id+".md")
		require.NoError(t, os.WriteFile(brief, []byte(passingBrief(id+": a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: friend")), 0o644))
		ta.ok("add --stream s1 --brief-dir " + filepath.Dir(brief))
	}
	return ta, root
}

// queueStates is a friend's queue file, card -> state.
func queueStates(t *testing.T, root, friend string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, friend+"-working", "inbox", "QUEUE.json"))
	require.NoError(t, err)
	var q friendQueue
	require.NoError(t, json.Unmarshal(b, &q))
	out := map[string]string{}
	for _, task := range q.Tasks {
		out[task.ID] = task.State
	}
	return out
}

func TestFriendTakeTakesBackAnUnstartedCardAndTheTickDealsItToAnother(t *testing.T) {
	t.Parallel()
	ta, root := takeApp(t, 1, nil, "amy", "bob")
	ta.ok("friend down bob")
	ta.ok("tick")
	var c cardView
	ta.json("card s1-1", &c)
	require.Equal(t, sprint.FriendRow("amy"), c.Work[0].Row)
	ta.ok("friend sync --root " + root)
	ta.ok("friend up bob")
	ta.ok("friend beat bob")

	out := ta.ok("friend take amy s1-1 --reason 'she is on another job'")
	assert.Contains(t, out, "s1-1.w1 withdrawn gen=2")
	assert.Contains(t, out, "FRIEND-TAKE OK moved=1 refused=0")
	ta.ok("friend sync --root " + root)
	assert.Equal(t, "taken", queueStates(t, root, "amy")["s1-1.w1"], "her queue file says it is not hers to start")

	ta.ok("tick") // the pump: its primary ready (only the pump writes the work table while it runs)
	ta.ok("tick")
	ta.json("card s1-1", &c)
	require.Len(t, c.Work, 1, "the same card, no second attempt")
	assert.Equal(t, sprint.FriendRow("bob"), c.Work[0].Row, "dealt to the other friend")
	assert.Equal(t, "3", c.Work[0].F("gen"))
	assert.Equal(t, sprint.Working, c.Primary.Col)
	out = ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD DELIVERED friend=bob card=s1-1.w1 job=s1-1.w1.g3 branch=sprint/s1-1.w1.g3.e0")
	assert.Equal(t, "working", queueStates(t, root, "bob")["s1-1.w1"])
	ta.clean()
}

func TestFriendTakeRefusesAStartedCardAndAnotherFriendsCard(t *testing.T) {
	t.Parallel()
	ta, root := takeApp(t, 2, map[string]string{"sprint/s1-1.w1.g1.e0": landHead}, "amy", "bob")
	ta.ok("friend down bob")
	ta.ok("tick")
	ta.ok("friend sync --root " + root)

	code, _, errs := ta.do("friend take amy s1-1 s1-2")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "REFUSED s1-1: s1-1.w1 has started: a push on its branch sprint/s1-1.w1.g1.e0 at "+landHead+"; it stays with friend amy and finishes")
	assert.Contains(t, errs, "REFUSED s1-2.w1: not written: the verb names several and applies all or none", "all or none: s1-2 stays too")

	ta.ok("friend beat amy --running s1-2.w1")
	code, _, errs = ta.do("friend take amy s1-2")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "REFUSED s1-2: s1-2.w1 has started: her beat names it running")

	code, _, errs = ta.do("friend take bob s1-2")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "REFUSED s1-2: s1-2.w1 is not dealt to friend bob: it is at friend.amy:working")
	code, _, errs = ta.do("friend take cat s1-2")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "no friend cat on the friends table")
	ta.clean()
}

func TestFriendDownGivesBackWhatSheHasNotStartedAndKeepsTheRest(t *testing.T) {
	t.Parallel()
	ta, root := takeApp(t, 3, map[string]string{"sprint/s1-1.w1.g1.e0": landHead}, "amy")
	ta.ok("tick") // amy at width 8: all three on her row
	ta.ok("friend sync --root " + root)

	out := ta.ok("friend down amy")
	assert.Contains(t, out, "FRIEND-DOWN OK moved=2 refused=0")
	assert.Contains(t, out, "NOTE friend amy held")
	assert.Contains(t, out, "NOTE friend amy keeps s1-1.w1: a push on its branch sprint/s1-1.w1.g1.e0")
	ta.ok("tick")
	var c cardView
	ta.json("card s1-1", &c)
	assert.Equal(t, sprint.Working, c.Work[0].Col, "the started one stays with her")
	ta.json("card s1-2", &c)
	assert.Equal(t, sprint.Withdrawn, c.Work[0].Col, "held: nothing is dealt to her")
	assert.Equal(t, sprint.Ready, c.Primary.Col)
	assert.Contains(t, c.Work[0].F(sprint.FieldTakenBack), "the hold of friend amy")

	// released and beating, her cards come back to her
	ta.ok("friend up amy")
	ta.ok("friend beat amy")
	ta.ok("tick")
	ta.json("card s1-2", &c)
	assert.Equal(t, sprint.FriendRow("amy"), c.Work[0].Row)
	assert.Equal(t, sprint.Working, c.Primary.Col)

	// take --all-unstarted takes every one not started, and names the one she keeps
	out = ta.ok("friend take amy --all-unstarted --reason 'rebalance'")
	assert.Contains(t, out, "FRIEND-TAKE OK moved=2")
	assert.Contains(t, out, "NOTE friend amy keeps s1-1.w1")
	ta.clean()
}

// The server runs a friend's beat with what she reports, each flag once with its value,
// and nothing more.
func TestTheServerRunsAFriendsBeatWithTheCardsSheIsRunning(t *testing.T) {
	t.Parallel()
	as, words, why := workerVerb([]string{"friend", "beat", "amy", "--running", "s1-1.w1,s1-2.w1.g3"})
	assert.Equal(t, "", why)
	assert.Equal(t, "amy", as)
	assert.Equal(t, 2, words)
	for _, argv := range [][]string{
		{"friend", "beat", "amy", "--running"},
		{"friend", "beat", "amy", "--running", "a b"},
		{"friend", "beat", "amy", "--running", "x", "--running", "y"},
		{"friend", "beat", "amy", "--redis", "mem:0"},
	} {
		_, _, why := workerVerb(argv)
		assert.NotEmpty(t, why, "%q", argv)
	}
}

// sentTo is the messages the app sent on the friends' bus to friend, in order.
func (ta *testApp) sentTo(friend string) []bus.Message {
	ta.mu.Lock()
	defer ta.mu.Unlock()
	var out []bus.Message
	for _, m := range ta.sent {
		if slices.Contains(m.To, friend) {
			out = append(out, m)
		}
	}
	return out
}

// A card that leaves a friend without her starting it tells her (docs/SPEC-SPRINT.md section
// 1, a friend's card taken back): the friend sync that marks it taken in her queue file
// sends her one bus message from the coordinator, by the delivery's wake path, naming the card
// and why; the queue file is the record, so a later sync says nothing again. A working card
// taken and dealt to another before the sync is told too, and a send that fails is said on
// sync's line and on the card's story, as a delivery's.
func TestATakeBackTellsTheFriendWithOneBusMessage(t *testing.T) {
	t.Parallel()
	ta, root := takeApp(t, 1, nil, "amy", "bob")
	ta.ok("friend down bob")
	ta.ok("tick")
	ta.ok("friend sync --root " + root)
	require.Len(t, ta.sentTo("amy"), 1, "dealt: one message")
	ta.ok("friend up bob")
	ta.ok("friend beat bob")

	ta.ok("friend take amy s1-1 --reason 'she is on another job'")
	ta.ok("friend sync --root " + root)
	sent := ta.sentTo("amy")
	require.Len(t, sent, 2, "taken back: one message more")
	m := sent[1]
	assert.Equal(t, "coordinator", m.From)
	assert.Equal(t, "card s1-1.w1 taken back: taken back by the coordinator: she is on another job", m.Subject)
	assert.Contains(t, m.Body, "Your sprint card s1-1.w1 (attempt 1 of s1-1) is no longer yours: do not start it")
	ta.ok("friend sync --root " + root)
	assert.Len(t, ta.sentTo("amy"), 2, "a sync that marks nothing taken says nothing")

	// her working card taken and dealt to another before the next sync: she is told it left her
	ta2, root2 := takeApp(t, 1, nil, "amy", "bob")
	ta2.ok("friend down bob")
	ta2.ok("tick")
	ta2.ok("friend sync --root " + root2)
	ta2.ok("friend up bob")
	ta2.ok("friend beat bob")
	ta2.ok("friend take amy s1-1")
	ta2.ok("tick")
	ta2.ok("tick")
	ta2.a.bus = func(_ context.Context, m bus.Message) error {
		if slices.Contains(m.To, "amy") {
			return errors.New("dial tcp: connection refused")
		}
		return nil
	}
	out := ta2.ok("friend sync --root " + root2)
	assert.Equal(t, "taken", queueStates(t, root2, "amy")["s1-1.w1"], "it left her: not hers to start")
	assert.Contains(t, out, "FRIEND-CARD DELIVERED friend=bob card=s1-1.w1")
	assert.Contains(t, out, "FRIEND-CARD NOTE friend=amy card=s1-1.w1: the bus message to her was not sent (dial tcp: connection refused); her queue file marks it taken, tell her by hand")
	story := ta2.ok("card s1-1")
	assert.Contains(t, story, "a friend was not told of her card", story)
	assert.Contains(t, story, "nova-bus send --as coordinator --to amy --subject 'card s1-1.w1 taken back'", story)
	ta.clean()
	ta2.clean()
}
