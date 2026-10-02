package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The friends table (docs/SPEC-SPRINT.md section 1; the owner, 2026-10-02: "add a
// friends table, above fleet and below merge. friends | status for now.
// up/down/held"): its rows are nova-config's friend rows, its status the fleet's
// rule over the friend's beats and the coordinator's hold, its order the fleet's.

// friendApp is a test app with an initialised sprint whose friend sync reads the
// friend rows of nova-config's in-memory store, each named in friends.
func friendApp(t *testing.T, friends ...string) (*testApp, *config.Mem) {
	t.Helper()
	ta := newTestApp(t)
	cfg := config.NewMem()
	ta.a.friends = func(ctx context.Context, _ string) ([]string, error) {
		rows, err := cfg.List(ctx, config.KindFriend)
		var names []string
		for _, r := range rows {
			names = append(names, r.Name)
		}
		return names, err
	}
	for _, f := range friends {
		addFriendRow(t, cfg, f)
	}
	ta.ok("init --readers reader-a,reader-b --members m1,m2")
	return ta, cfg
}

func addFriendRow(t *testing.T, cfg *config.Mem, name string) {
	t.Helper()
	_, err := cfg.Insert(context.Background(), config.KindFriend, config.Row{Name: name, Fields: map[string]string{"slots": "2", "tiers": "flash"}}, "t")
	require.NoError(t, err)
}

// frame is one where frame, as where prints it.
func (ta *testApp) frame() string {
	ta.t.Helper()
	return ta.ok("where")
}

// friendStatus is each friend's status as where --json carries it.
func (ta *testApp) friendStatus() map[string]string {
	ta.t.Helper()
	var w whereView
	ta.json("where", &w)
	out := map[string]string{}
	for f, row := range w.Tables[sprint.Friends] {
		out[f] = row[sprint.Status]
	}
	return out
}

// The table of two friends, one up and one held: the header, a rule, the friend
// up first and then the one held, a rule and the summary row with its cell blank.
// The empty store draws the header, its one rule and the summary row, as every
// empty table does. Either way it stands after merge and before fleet.
func TestTheFriendsTableShowsAfterMergeAndBeforeFleet(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		friends []string
		lines   []string
		want    string
	}{
		{name: "the empty store", want: "friends | status\n" +
			"--------+-------\n" +
			"        |"},
		{name: "two friends, one up and one held", friends: []string{"friend-a", "friend-b"},
			lines: []string{"friend sync", "friend beat friend-b", "friend down friend-a"},
			want: "friends  | status\n" +
				"---------+-------\n" +
				"friend-b | up\n" +
				"friend-a | held\n" +
				"---------+-------\n" +
				"         |"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ta, _ := friendApp(t, tc.friends...)
			for _, l := range tc.lines {
				ta.ok(l)
			}
			frame := ta.frame()
			assert.Equal(t, tc.want, tableOf(frame, sprint.Friends))
			var order []string
			for _, block := range strings.Split(frame, "\n\n") {
				if title, _, ok := strings.Cut(block, " |"); ok && !strings.Contains(title, "\n") {
					order = append(order, strings.TrimSpace(title))
				}
			}
			assert.Equal(t, []string{"work", "readers", "merge", "friends", "fleet"}, order)
		})
	}
}

// A friend is up while its beat is alive and down once it has missed the fleet's
// MissedBeatsDown beat windows in a row, or when it has never beaten; held while
// friend down holds it whatever it beats, and friend up releases it. The rows go
// up, then held, then down, each by name (store.FleetOrder).
func TestAFriendsStatusIsTheFleetsRuleOverItsBeatsAndItsHold(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t, "zed", "amy", "bob", "cat")
	ta.ok("friend sync")
	assert.Equal(t, map[string]string{"amy": "down", "bob": "down", "cat": "down", "zed": "down"}, ta.friendStatus(), "none has beaten")
	for _, f := range []string{"zed", "amy", "cat"} {
		ta.ok("friend beat " + f)
	}
	ta.ok("friend down cat")
	assert.Equal(t, map[string]string{"amy": "up", "bob": "down", "cat": "held", "zed": "up"}, ta.friendStatus())
	assert.Equal(t, "friends | status\n"+
		"--------+-------\n"+
		"amy     | up\n"+
		"zed     | up\n"+
		"cat     | held\n"+
		"bob     | down\n"+
		"--------+-------\n"+
		"        |", tableOf(ta.frame(), sprint.Friends), "up, then held, then down, each by name")

	// amy's last beat was now: up through MissedBeatsDown windows, down past them
	ta.a.sleep(time.Duration(sprint.MissedBeatsDown-1) * sprint.BeatDeadline)
	ta.ok("friend beat zed")
	ta.a.sleep(sprint.BeatDeadline)
	got := ta.friendStatus()
	assert.Equal(t, "up", got["amy"], "missed fewer than the windows: still up")
	ta.a.sleep(time.Second)
	got = ta.friendStatus()
	assert.Equal(t, "down", got["amy"], "missed the fleet's windows in a row: down")
	assert.Equal(t, "up", got["zed"], "a beat resets the count")
	assert.Equal(t, "held", got["cat"], "a hold stands whatever the beats")

	ta.ok("friend up cat")
	ta.ok("friend beat cat")
	assert.Equal(t, "up", ta.friendStatus()["cat"], "released, and beating")
}

// friend sync makes the friends table nova-config's friend rows: a row added
// comes in, a row removed goes with its beat, and a friend that stays keeps its
// hold. A second sync writes nothing and says so.
func TestFriendSyncFollowsTheConfigAndAHoldSurvivesIt(t *testing.T) {
	t.Parallel()
	ta, cfg := friendApp(t, "amy", "bob")
	assert.Contains(t, ta.ok("friend sync"), "FRIEND-SYNC OK added=amy,bob removed=- friends=2")
	ta.ok("friend beat bob")
	ta.ok("friend down amy")
	assert.Contains(t, ta.ok("friend sync"), "nothing to do")

	_, err := cfg.Delete(context.Background(), config.KindFriend, "bob", "t")
	require.NoError(t, err)
	addFriendRow(t, cfg, "cat")
	assert.Contains(t, ta.ok("friend sync"), "FRIEND-SYNC OK added=cat removed=bob friends=2")
	assert.Equal(t, map[string]string{"amy": "held", "cat": "down"}, ta.friendStatus(), "amy's hold survived the sync")

	addFriendRow(t, cfg, "bob")
	ta.ok("friend sync")
	assert.Equal(t, "down", ta.friendStatus()["bob"], "bob came back with no beat: its old beat went with its row")
}

// Each refusal names what it wants and changes nothing: a friend that is no row
// of the friends table, a config that cannot be read or holds no friend row
// (friend sync, down and up by another actor: TestEveryCoordinatorVerbIsTheCoordinators).
func TestTheFriendVerbsRefuse(t *testing.T) {
	t.Parallel()
	ta, cfg := friendApp(t)
	code, _, errs := ta.do("friend sync")
	assert.Equal(t, exitCannotRead, code)
	assert.Contains(t, errs, "holds no friend row")

	addFriendRow(t, cfg, "amy")
	ta.ok("friend sync")
	for _, line := range []string{"friend beat bob", "friend down bob", "friend up bob"} {
		code, out, errs := ta.do(line)
		assert.Equal(t, 1, code, line)
		assert.Empty(t, out, line)
		assert.Contains(t, errs, "no friend bob on the friends table (friends: amy)", line)
		assert.Contains(t, errs, "run: nova-sprint friend sync", line)
	}
	code, _, errs = ta.do("friend beat")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "wants one friend")

	ta.a.friends = func(context.Context, string) ([]string, error) { return nil, errors.New("connection refused") }
	code, _, errs = ta.do("friend sync")
	assert.Equal(t, exitCannotRead, code)
	assert.Contains(t, errs, "the config cannot be read")
	assert.Equal(t, map[string]string{"amy": "down"}, ta.friendStatus(), "nothing was changed")
}

// A friend beats through the sprint's server as a member does: `friend beat
// <friend>` and nothing more, its actor the friend.
func TestAFriendBeatsThroughTheServer(t *testing.T) {
	t.Parallel()
	r := newServerRig(t, "nova-sprint init --readers reader-a,reader-b --members m1:2")
	r.a.friends = func(context.Context, string) ([]string, error) { return []string{"amy"}, nil }
	r.boss("nova-sprint friend sync")
	res := r.one("friend", "beat", "amy")
	require.Equal(t, 0, res.Code, res.Stderr)
	assert.Contains(t, res.Stdout, "FRIEND-BEAT OK amy")
	assert.Contains(t, r.boss("nova-sprint where"), "amy     | up")
	for name, argv := range map[string][]string{
		"no friend":       {"friend", "beat"},
		"another actor":   {"friend", "beat", "amy", "--actor", "boss"},
		"a hold":          {"friend", "down", "amy"},
		"a list":          {"friend", "beat", "amy,bob"},
		"a store":         {"friend", "beat", "amy", "--redis", "mem:/tmp/x"},
		"a second friend": {"friend", "beat", "amy", "bob"},
	} {
		res := r.one(argv...)
		assert.Equal(t, 2, res.Code, name)
		assert.Contains(t, res.Stderr, "nothing was changed", name)
	}
}
