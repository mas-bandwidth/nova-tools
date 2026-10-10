package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/sprintwire"
)

// A friend takes her own ready cards (docs/SPEC-SPRINT.md section 1; 2026-10-05 11:20 PM: a
// friend held fourteen cards, six of them ready, and take --as friend.<name> was refused as
// "member friend.<name> is -", finish and progress refused the ready ones as not working,
// and only the coordinator's own take unblocked her). On the twin, through the server as her
// session sends it (the HTTP form, no store address of hers).

// friendReadyApp is amy and bob at width 2 in batch mode with four cards for any friend
// dealt to amy (bob down for the deal): two working and two ready on her row; then her
// width is raised to 3 and synced, so a lane of hers is free and s1-3 sits ready in front
// of it. The machines are down.
func friendReadyApp(t *testing.T) (*testApp, *config.Mem, string) {
	t.Helper()
	ta, root := takeApp(t, 4, nil, "amy", "bob")
	cfg := config.NewMem()
	ta.a.friends = func(ctx context.Context, _ string) ([]config.Row, error) { return cfg.List(ctx, config.KindFriend) }
	for _, f := range []string{"amy", "bob"} {
		_, err := cfg.Insert(context.Background(), config.KindFriend, config.Row{Name: f, Fields: map[string]string{"width": "2", "tiers": "flash", "mode": "batch"}}, "t")
		require.NoError(t, err)
	}
	ta.ok("friend sync --root " + root)
	ta.ok("friend down bob")
	ta.ok("friend beat amy")
	ta.ok("tick")
	ta.startFriend("amy", 2) // she starts what her lanes hold
	f := whereFriends(ta)
	require.Equal(t, 2, f["amy"].Working)
	require.Equal(t, 2, f["amy"].Ready)
	_, _, err := cfg.Update(context.Background(), config.KindFriend, "amy", map[string]string{"width": "3"}, "t")
	require.NoError(t, err)
	ta.ok("friend sync --root " + root)
	ta.ok("friend beat amy")
	return ta, cfg, root
}

// freshCard is card <id> --json decoded into a value of its own (a decode into one used
// before keeps the fields the new answer leaves out).
func freshCard(ta *testApp, id string) cardView {
	ta.t.Helper()
	var c cardView
	ta.json("card "+id, &c)
	return c
}

// served runs one worker's verb through the server, as a friend's session sends it.
func (ta *testApp) served(argv ...string) (int, string) {
	ta.t.Helper()
	res := ta.a.serveFrom(sprintwire.Request{Verbs: [][]string{argv}}, false)
	return res.Results[0].Code, res.Results[0].Stdout + res.Results[0].Stderr
}

func TestAFriendTakesAndFinishesItsOwnReadyCard(t *testing.T) {
	t.Parallel()
	ta, _, _ := friendReadyApp(t)
	ta.a.serveAddr = "mem:0"
	c := freshCard(ta, "s1-3")
	require.Equal(t, sprint.FriendRow("amy"), c.Work[0].Row)
	require.Equal(t, sprint.Ready, c.Work[0].Col, "ready on her row in front of a free lane")

	// a stranger is refused: another friend, a machine, a friend not on the table
	code, out := ta.served("take", "--as", "friend.bob", "s1-3.w1@1", "--epoch", "0")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, out, "friend bob is held")
	ta.ok("friend up bob")
	ta.ok("friend beat bob")
	code, out = ta.served("take", "--as", "friend.bob", "s1-3.w1@1", "--epoch", "0")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, out, "not in friend.bob ready (it is friend.amy:ready)")
	code, out = ta.served("take", "--as", "m1", "s1-3.w1@1", "--epoch", "0")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, out, "member m1 is down")
	code, out = ta.served("take", "--as", "friend.zed", "s1-3.w1@1", "--epoch", "0")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, out, "no friend zed on the roster")
	code, out = ta.served("take", "--as", "friend.a b", "s1-3.w1@1", "--epoch", "0")
	assert.Equal(t, 2, code, "a worker's name is checked before the store: %s", out)
	c = freshCard(ta, "s1-3")
	require.Equal(t, sprint.Ready, c.Work[0].Col, "no refusal moved it")

	// before her take, her progress and her finish of the ready card are refused
	code, out = ta.served("progress", "--as", "friend.amy", "s1-3.w1@1", "--epoch", "0")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, out, "not working (it is friend.amy:ready)")

	// her own take, as a member's: ready -> working on her row, taken now
	code, out = ta.served("take", "--as", "friend.amy", "s1-3.w1@1", "--epoch", "0")
	require.Equal(t, 0, code, out)
	assert.Contains(t, out, "s1-3.w1 fleet ready -> working member=friend.amy gen=1")
	c = freshCard(ta, "s1-3")
	assert.Equal(t, sprint.Working, c.Work[0].Col)
	assert.NotEmpty(t, c.Work[0].F("taken"))
	assert.Empty(t, c.Work[0].F("untaken_since"))

	// her lanes are hard, as a member's width: three working of three, s1-4 waits
	code, out = ta.served("take", "--as", "friend.amy", "s1-4.w1@1", "--epoch", "0")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, out, "friend friend.amy is at its width (3 working of 3)")

	// her progress and her finish, as a member's
	code, out = ta.served("progress", "--as", "friend.amy", "s1-3.w1@1", "--epoch", "0")
	require.Equal(t, 0, code, out)
	assert.Contains(t, out, "s1-3.w1 progress at")
	// her daemon's own stamp, as it words it (friend.ProgressArgv), is taken as hers
	for _, argv := range friend.ProgressArgv("amy", []friend.Card{{ID: "s1-3.w1", Outbox: "outbox/s1-3.w1"}}) {
		code, out = ta.served(argv...)
		require.Equal(t, 0, code, "%v: %s", argv, out)
		assert.Contains(t, out, "s1-3.w1 progress at")
	}
	code, out = ta.served("progress", "--as", "friend.bob", "s1-3.w1@1", "--epoch", "0")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, out, "held by friend.amy, not friend.bob")
	code, out = ta.served("finish", "--as", "friend.bob", "s1-3.w1@1", "--head", landHead, "--epoch", "0")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, out, "dealt to friend.amy, not friend.bob")
	code, out = ta.served("finish", "--as", "friend.amy", "s1-3.w1@1", "--head", landHead, "--report", "done", "--epoch", "0")
	require.Equal(t, 0, code, out)
	assert.Contains(t, out, "s1-4.w1 ready -> working (her next, taken now)", "her finish takes her next, as before")
	c = freshCard(ta, "s1-3")
	assert.Equal(t, sprint.DoneOK, c.Work[0].Col)
	code, out = ta.served("take", "--as", "friend.amy", "--epoch", "0")
	assert.Equal(t, 0, code, out)
	assert.Contains(t, out, "friend.amy took 0 of the 1 asked: its ready queue is empty")
	ta.ok("tick") // the pump: the finish's work-table change is written while the machine runs
	c = freshCard(ta, "s1-3")
	assert.Equal(t, sprint.Review, c.Primary.Col)
	ta.clean()
}

// The deal never takes a ready card into a free lane of hers: it is working once she starts
// it (docs/SPEC-SPRINT.md section 1, a friend's card is working once she starts it); a card
// ready on her row while she has a lane free past the start bound and ten minutes more,
// which no one took, is a judgment that names the take-back.
func TestADealTakesAFriendsReadyCardAndOneLeftReadyIsAJudgment(t *testing.T) {
	t.Parallel()
	ta, _, _ := friendReadyApp(t)
	ta.ok("tick")
	c := freshCard(ta, "s1-3")
	assert.Equal(t, sprint.Ready, c.Work[0].Col, "the deal takes nothing into her free lane: she has not started it")
	assert.Empty(t, c.Work[0].F("taken"))
	f := whereFriends(ta)
	assert.Equal(t, 2, f["amy"].Working)
	assert.Equal(t, 2, f["amy"].Ready)
	ta.clean()

	// held, every card of hers is handed back, begun or not: a held friend keeps no card (the
	// owner, 2026-10-09); the ready judgment is for a friend up whose card nobody takes
	ta, cfg, root := friendReadyApp(t)
	ta.ok("hold amy --reason 'away'")
	_, _, err := cfg.Update(context.Background(), config.KindFriend, "amy", map[string]string{"width": "4"}, "t")
	require.NoError(t, err)
	ta.ok("friend sync --root " + root)
	ta.ok("tick")
	c = freshCard(ta, "s1-3")
	require.Equal(t, sprint.Withdrawn, c.Work[0].Col, "the hold hands her ready card back")
	ta.clean()
}
