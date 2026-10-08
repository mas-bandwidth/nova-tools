package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
// up/down/held"; and the same day: "please give friends in the friends table the
// same ready, working, width, done, ok%, status that we have for machines, but no
// load"): its rows are nova-config's friend rows, its counts the friend's sprint
// cards (the cards dealt to her fleet row friend.<name>, their states and their
// finish verdicts), its status the friends' rule (down after 15 s without a
// beat) over her beats and the coordinator's hold, its order the fleet's.

// emptyFriends is the friends table with no friend: its header, one rule and
// the footer, as every empty table is.
const emptyFriends = "friends | ready | working | width | done | ok%  | status | active | tokens\n" +
	"--------+-------+---------+-------+------+------+--------+--------+-------\n" +
	"        |     0 |       0 |     0 |    0 | 0.0% |        |        |      0"

// friendApp is a test app with an initialised sprint whose friend sync reads the
// friend rows of nova-config's in-memory store, each named in friends.
func friendApp(t *testing.T, friends ...string) (*testApp, *config.Mem) {
	t.Helper()
	ta := newTestApp(t)
	// the friends' working directories are under HOME, <HOME>/<friend>-working;
	// none is there until a test writes one (jobs)
	home, prev := t.TempDir(), ta.a.getenv
	ta.a.getenv = func(k string) string {
		if k == "HOME" {
			return home
		}
		return prev(k)
	}
	cfg := config.NewMem()
	ta.a.friends = func(ctx context.Context, _ string) ([]config.Row, error) {
		return cfg.List(ctx, config.KindFriend)
	}
	for _, f := range friends {
		addFriendRow(t, cfg, f)
	}
	ta.ok("init --readers reader-a,reader-b --members m1,m2")
	return ta, cfg
}

// jobs writes a fixture of a friend's working directory under root, the
// inbox/outbox standard: <root>/<friend>-working/inbox/<job>/BRIEF.md for every
// job named; outbox/<job>/ for every job in working; outbox/<job>/REPORT.md with
// the text given for every job done. A friend not named has no directory.
func jobs(t *testing.T, root, friend string, inbox []string, working []string, done map[string]string) {
	t.Helper()
	dir := filepath.Join(root, friend+"-working")
	for _, j := range inbox {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox", j), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", j, "BRIEF.md"), []byte("# "+j+"\n"), 0o644))
	}
	for _, j := range working {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "outbox", j), 0o755))
	}
	for j, report := range done {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "outbox", j), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "outbox", j, "REPORT.md"), []byte(report), 0o644))
	}
}

// The friends table has the fleet table's columns but load, from the friend's
// sprint cards: a card dealt to her fleet row friend.<name> is working; a LAND
// report finishes it done ok, a HOLD (or FAIL) report done failed, and a
// REPORT.md with no verdict word is finished failed too, never ok; ready is
// never a friend's card's state (the tick deals a card straight into working),
// and a hand-written inbox job that is no card is shown nowhere. Width is the
// friend row's (8 when it names none; TestFriendSyncWritesTheConfiguredWidth);
// done and ok% are the formulas over ok and failed, and the footer sums and
// pools them. A friend with no card shows zeros. The same cells are in where
// --json, under the column names.
func TestTheFriendsTableCountsTheFriendsSprintCards(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t, "amy", "bob", "cat")
	ta.a.tip = tipIs(t, landHead)
	root := t.TempDir()
	ta.ok("friend sync --root " + root)
	for _, f := range []string{"amy", "bob", "cat"} {
		ta.beatUp(f)
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "s1-1.md"), []byte(passingBrief("s1-1: a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: friend amy")), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "s1-2.md"), []byte(passingBrief("s1-2: a second friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: friend amy")), 0o644))
	ta.ok("add --stream s1 --brief-dir " + dir)
	ta.ok("start")
	ta.ok("tick")

	// a hand-written job of amy's inbox that is no card: it is shown nowhere
	jobs(t, root, "amy", []string{"hand"}, nil, nil)

	ta.ok("friend sync --root " + root)
	amy := func() map[string]any {
		var w whereView
		ta.json("where", &w)
		return withoutRowCards(w.Tables[sprint.Friends]["amy"])
	}
	// dealt, not started: ready, and none working (docs/SPEC-SPRINT.md section 1, a friend's
	// card is working once she starts it)
	assert.Equal(t, map[string]any{"ready": "2", "working": "0", "width": "8", "done": "0", "okpct": "0.0%", "status": "up", "active": "-", "tokens": "0", "ok": "0", "failed": "0"}, amy(), "two cards dealt, neither started")
	ta.startFriend("amy", 2)
	assert.Equal(t, map[string]any{"ready": "0", "working": "2", "width": "8", "done": "0", "okpct": "0.0%", "status": "up", "active": "-", "tokens": "0", "ok": "0", "failed": "0"}, amy(), "two cards started, both working; the hand job is nowhere")

	// amy finishes s1-1 with a LAND, its report carrying her session's usage: done ok,
	// and the tokens column sums it (docs/SPEC-SPRINT.md, a friend's usage)
	outboxReport(t, root, "amy", "s1-1.w1", "# s1-1\n\n**Verdict:** LAND\nHead: "+landHead+"\nusage: in=1000000 out=150000 cache=50000\n\nThe change is pushed.\n")
	ta.ok("friend sync --root " + root)
	assert.Equal(t, map[string]any{"ready": "0", "working": "1", "width": "8", "done": "1", "okpct": "100.0%", "status": "up", "active": "-", "tokens": "1.2M", "ok": "1", "failed": "0"}, amy(), "s1-1 done ok, its usage summed, s1-2 still working")

	// amy reports s1-2 with no verdict word: finished failed, never ok, and no usage adds none
	outboxReport(t, root, "amy", "s1-2.w1", "# s1-2\n\nAll green, nothing more.\n")
	ta.ok("friend sync --root " + root)
	assert.Equal(t, map[string]any{"ready": "0", "working": "0", "width": "8", "done": "2", "okpct": "50.0%", "status": "up", "active": "-", "tokens": "1.2M", "ok": "1", "failed": "1"}, amy(), "s1-2 done failed (no verdict), s1-1 done ok; its tokens stay")

	var w whereView
	ta.json("where", &w)
	assert.Equal(t, map[string]any{"ready": "0", "working": "0", "width": "8", "done": "0", "okpct": "0.0%", "status": "up", "active": "-", "tokens": "0", "ok": "0", "failed": "0"}, withoutRowCards(w.Tables[sprint.Friends]["bob"]), "bob has no card")
	assert.Equal(t, map[string]any{"ready": "0", "working": "0", "width": "8", "done": "0", "okpct": "0.0%", "status": "up", "active": "-", "tokens": "0", "ok": "0", "failed": "0"}, withoutRowCards(w.Tables[sprint.Friends]["cat"]), "cat has no card")
}

// A friend's width is her friend row's (the owner, 2026-10-02: "6/1 seems a bit
// wrong -- need to setup width for friends? Start at 8 for each?"): friend sync
// writes the row's width, 8 when the row has no width field; a width changed
// in nova-config moves the friends table at the next sync (updated=<friend>);
// and a row whose width is below 1 is refused in one line naming the
// nova-config set that fixes it, with nothing written.
func TestFriendSyncWritesTheConfiguredWidth(t *testing.T) {
	t.Parallel()
	ta, cfg := friendApp(t, "amy")
	_, err := cfg.Insert(context.Background(), config.KindFriend, config.Row{Name: "cat", Fields: map[string]string{"slots": "2", "tiers": "flash", "width": "3"}}, "t")
	require.NoError(t, err)
	ta.ok("friend sync")
	width := func() map[string]string {
		var w whereView
		ta.json("where", &w)
		out := map[string]string{}
		for f, row := range w.Tables[sprint.Friends] {
			out[f] = cellText(row[sprint.FieldWidth])
		}
		return out
	}
	assert.Equal(t, map[string]string{"amy": "8", "cat": "3"}, width(), "amy's row has no width: the default; cat's is 3")

	_, _, err = cfg.Update(context.Background(), config.KindFriend, "amy", map[string]string{"width": "2"}, "t")
	require.NoError(t, err)
	assert.Contains(t, ta.ok("friend sync"), "FRIEND-SYNC OK added=- removed=- updated=amy friends=2")
	assert.Equal(t, map[string]string{"amy": "2", "cat": "3"}, width())

	ta.a.friends = func(context.Context, string) ([]config.Row, error) {
		return []config.Row{{Name: "amy", Fields: map[string]string{"width": "0"}}, {Name: "cat", Fields: map[string]string{"width": "5"}}}, nil
	}
	code, out, errs := ta.do("friend sync")
	assert.Equal(t, 1, code)
	assert.Empty(t, out)
	assert.Equal(t, "nova-sprint friend sync: friend amy has width 0, and a friend's width is at least 1; run: nova-config friend set amy --width <n>; nothing was changed\n", errs)
	assert.Equal(t, map[string]string{"amy": "2", "cat": "3"}, width(), "nothing was written")
}

// friend sync writes only a friend's card's brief into her inbox, and reads
// her outbox/<job>/REPORT.md to finish the card: the inbox/outbox directories
// are the transport of her cards, never a source of the friends table's
// counts. A hand-written inbox job that is no card is left alone and shown
// nowhere.
func TestFriendSyncWritesOnlyACardsBriefAndReadsItsReport(t *testing.T) {
	t.Parallel()
	ta, root := friendCardApp(t, "friend amy", "amy")
	ta.ok("tick")
	ta.startFriend("amy", 1)
	jobs(t, root, "amy", []string{"hand"}, nil, nil) // a hand job, no card
	hand := filepath.Join(root, "amy-working", "inbox", "hand", "BRIEF.md")
	handText, err := os.ReadFile(hand)
	require.NoError(t, err)
	ta.ok("friend sync --root " + root)
	text, err := os.ReadFile(filepath.Join(root, "amy-working", "inbox", "s1-1.w1", "BRIEF.md"))
	require.NoError(t, err)
	assert.Contains(t, string(text), "STATUS: nova-sprint card s1-1.w1")
	assert.Contains(t, string(text), "WHO: friend amy")
	gotHand, err := os.ReadFile(hand)
	require.NoError(t, err)
	assert.Equal(t, handText, gotHand, "the hand job's brief is untouched")
	var w whereView
	ta.json("where", &w)
	assert.Equal(t, "1", w.Tables[sprint.Friends]["amy"]["working"], "the card counts; the hand job is nowhere")
	assert.Equal(t, "0", w.Tables[sprint.Friends]["amy"]["ready"])
	assert.Equal(t, "0", w.Tables[sprint.Friends]["amy"]["done"])
}

// friendRows is a friendsFn over friend rows of the names given, each with
// no width field (so the default width).
func friendRows(names ...string) friendsFn {
	return func(context.Context, string) ([]config.Row, error) {
		rows := make([]config.Row, len(names))
		for i, n := range names {
			rows[i] = config.Row{Name: n, Fields: map[string]string{"slots": "2", "tiers": "flash"}}
		}
		return rows, nil
	}
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
		out[f] = cellText(row[sprint.Status])
	}
	return out
}

// The table of two friends, one up and one held: the header, a rule, the friend
// up first and then the one held, a rule and the summary row with its cell blank.
// The empty store draws the header, its one rule and the summary row, as every
// empty table does. Either way it stands after work and before fleet in the default
// frame, which hides the readers and merge tables, and after merge and before fleet
// in the frame of where --all.
func TestTheFriendsTableShowsAfterMergeAndBeforeFleet(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		friends []string
		lines   []string
		want    string
	}{
		{name: "the empty store", want: emptyFriends},
		{name: "two friends, one up and one held", friends: []string{"friend-a", "friend-b"},
			lines: []string{"friend sync", "friend beat friend-b", "friend health friend-b --state up --seen 2030-01-02T03:04:05Z --generation 1", "friend down friend-a"},
			want: "friends  | ready | working | width | done | ok%  | status | active | tokens\n" +
				"---------+-------+---------+-------+------+------+--------+--------+-------\n" +
				"friend-b |     0 |       0 |     8 |    0 | 0.0% | up     | -      |      0\n" +
				"friend-a |     0 |       0 |     8 |    0 | 0.0% | held   | -      |      0\n" +
				"---------+-------+---------+-------+------+------+--------+--------+-------\n" +
				"         |     0 |       0 |    16 |    0 | 0.0% |        |        |      0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ta, _ := friendApp(t, tc.friends...)
			for _, l := range tc.lines {
				ta.ok(l)
			}
			frame := ta.frame()
			assert.Equal(t, tc.want, tableOf(frame, sprint.Friends))
			titles := func(frame string) []string {
				var order []string
				for _, block := range strings.Split(frame, "\n\n") {
					if title, _, ok := strings.Cut(block, " |"); ok && !strings.Contains(title, "\n") {
						order = append(order, strings.TrimSpace(title))
					}
				}
				return order
			}
			assert.Equal(t, []string{"work", "friends", "fleet"}, titles(frame))
			assert.Equal(t, []string{"work", "readers", "merge", "friends", "fleet"}, titles(ta.ok("where --all")))
		})
	}
}

// pong is a wake ping the friend's session answered, as the coordinator writes it: friend
// health --state up at the clock now, by the seat's holder at its generation.
func (ta *testApp) pong(friend string) {
	ta.t.Helper()
	s := ta.seatState()
	ta.ok(fmt.Sprintf("friend health %s --state up --seen %s --generation %d --actor %s", friend, ta.now.UTC().Format(time.RFC3339), s.Generation, s.Holder))
}

// beatUp is a friend's beat and a wake ping her session answered: a friend up, as the
// tests that deal to her want her (a beat alone is no evidence).
func (ta *testApp) beatUp(friend string) {
	ta.t.Helper()
	ta.ok("friend beat " + friend)
	ta.pong(friend)
}

// A friend is up on her daemon's beat (at most sprint.FriendBeatLive old) and her session's
// evidence: a wake ping her session answered under sprint.FriendPongWindow old (or a card
// of hers finished); a beat alone is never up; held while friend down holds her whatever
// she does, and friend up releases the hold without counting as evidence. The rows go up, then held, then down, each by name
// (store.FleetOrder).
func TestAFriendsStatusIsTheFriendsRuleOverHerSessionsEvidenceAndHerHold(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t, "zed", "amy", "bob", "cat")
	ta.ok("friend sync")
	assert.Equal(t, map[string]string{"amy": "down", "bob": "down", "cat": "down", "zed": "down"}, ta.friendStatus(), "none has any evidence")
	for _, f := range []string{"zed", "amy", "cat"} {
		ta.ok("friend beat " + f)
	}
	assert.Equal(t, map[string]string{"amy": "down", "bob": "down", "cat": "down", "zed": "down"}, ta.friendStatus(), "a beat is no evidence")
	for _, f := range []string{"zed", "amy", "cat"} {
		ta.pong(f)
	}
	ta.ok("friend down cat")
	assert.Equal(t, map[string]string{"amy": "up", "bob": "down", "cat": "held", "zed": "up"}, ta.friendStatus())
	assert.Equal(t, []string{"amy", "zed", "cat", "bob"}, rowsOf(tableOf(ta.frame(), sprint.Friends)), "up, then held, then down, each by name")

	// every pong was at t: up at the window less a second, down at the window and a second
	ta.a.sleep(sprint.FriendPongWindow - time.Second)
	for _, f := range []string{"zed", "amy", "cat"} {
		ta.ok("friend beat " + f) // their daemons beat every second throughout
	}
	ta.pong("zed")
	assert.Equal(t, map[string]string{"amy": "up", "bob": "down", "cat": "held", "zed": "up"}, ta.friendStatus(), "within the window: still up")
	ta.a.sleep(2 * time.Second)
	got := ta.friendStatus()
	assert.Equal(t, "down", got["amy"], "the window out with no pong: down")
	assert.Equal(t, "up", got["zed"], "ponged within the window: up")
	assert.Equal(t, "held", got["cat"], "a hold stands whatever the evidence")
	ta.ok("friend beat amy")
	assert.Equal(t, "down", ta.friendStatus()["amy"], "a beat never wakes her")
	ta.pong("amy")
	assert.Equal(t, "up", ta.friendStatus()["amy"], "her session's pong does")

	// friend up is no evidence: cat's pong is past the window, so released she is down
	ta.ok("friend up cat")
	assert.Equal(t, "down", ta.friendStatus()["cat"], "released with no recent evidence: down, never up")
	ta.a.sleep(sprint.FriendBeatLive + time.Second)
	ta.pong("cat")
	assert.Equal(t, "down", ta.friendStatus()["cat"], "her session answered while her daemon was not beating: down")
	assert.Equal(t, "daemon not beating (last beat 13s ago), session pong 0s ago", whereFriends(ta)["cat"].Evidence, "the row names the half missing: her last beat was before the 2s and the 11s sleeps")
	ta.ok("friend beat cat")
	ta.pong("cat")
	assert.Equal(t, "up", ta.friendStatus()["cat"], "released, and her session answered")
}

// A friend down works nothing: her working count is 0 in the friends table
// and in where --json while her cards stay on her row, and they count again
// when she beats; ready and done are as they were. The owner, 2026-10-02
// 9:48 PM ET: "[a friend] being down, she automatically is 0/8 working OK?"
func TestADownFriendShowsNoneWorking(t *testing.T) {
	t.Parallel()
	ta, root := friendCardApp(t, "friend amy", "amy")
	ta.ok("tick")
	ta.startFriend("amy", 1)
	ta.ok("friend sync --root " + root)
	cells := func() map[string]string {
		var w whereView
		ta.json("where", &w)
		c := w.Tables[sprint.Friends]["amy"]
		return map[string]string{"status": cellText(c["status"]), "ready": cellText(c["ready"]), "working": cellText(c["working"]), "done": cellText(c["done"])}
	}
	footer := func() string {
		lines := strings.Split(tableOf(ta.frame(), sprint.Friends), "\n")
		return strings.TrimSpace(strings.Split(lines[len(lines)-1], "|")[2]) // the footer's working sum
	}
	assert.Equal(t, map[string]string{"status": "up", "ready": "0", "working": "1", "done": "0"}, cells(), "t: her session answered, one working")
	assert.Equal(t, "1", footer())
	ta.a.sleep(sprint.FriendPongWindow + time.Second)
	assert.Equal(t, map[string]string{"status": "down", "ready": "0", "working": "0", "done": "0"}, cells(), "past the window with no pong: down, none working")
	assert.Equal(t, "0", footer(), "the footer sums the rows as shown")
	ta.beatUp("amy")
	assert.Equal(t, map[string]string{"status": "up", "ready": "0", "working": "1", "done": "0"}, cells(), "her session answers, working again")
}

// friend sync makes the friends table nova-config's friend rows: a row added
// comes in, a row removed goes with its beat, and a friend that stays keeps its
// hold. A second sync writes nothing and says so.
func TestFriendSyncFollowsTheConfigAndAHoldSurvivesIt(t *testing.T) {
	t.Parallel()
	ta, cfg := friendApp(t, "amy", "bob")
	assert.Contains(t, ta.ok("friend sync"), "FRIEND-SYNC OK added=amy,bob removed=- updated=- friends=2")
	ta.beatUp("bob")
	ta.ok("friend down amy")
	assert.Contains(t, ta.ok("friend sync"), "nothing to do")

	_, err := cfg.Delete(context.Background(), config.KindFriend, "bob", "t")
	require.NoError(t, err)
	addFriendRow(t, cfg, "cat")
	assert.Contains(t, ta.ok("friend sync"), "FRIEND-SYNC OK added=cat removed=bob updated=- friends=2")
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

	ta.a.friends = func(context.Context, string) ([]config.Row, error) { return nil, errors.New("connection refused") }
	code, _, errs = ta.do("friend sync")
	assert.Equal(t, exitCannotRead, code)
	assert.Contains(t, errs, "the config cannot be read")
	assert.Equal(t, map[string]string{"amy": "down"}, ta.friendStatus(), "nothing was changed")
}

// The friends paragraph of nova-sprint help friend says what friend sync does
// (docs/SPEC-SPRINT.md section 1, a friend's card): the job directory is the
// card's stored id, <card> at epoch 0 and <card>~<epoch> after a clear; a
// full-sha LAND is refused and left working when the tip differs (both shas
// named), when origin has no branch of that name, when the tip cannot be read,
// or when the card names no REPO: line; HOLD, FAIL, FAILED, BROKEN, a LAND
// without a full sha, an empty report and any other verdict finish the card
// failed with the report's first paragraph; and the friends table is drawn
// after merge only under where --all, the default frame drawing no merge table.
func TestFriendHelpMatchesWhatFriendSyncDoes(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	code := newApp(func(string) string { return "" }).run([]string{"help", "friend"}, &out, &errb)
	require.Equal(t, 0, code, errb.String())
	help := strings.Join(strings.Fields(out.String()), " ")
	for _, want := range []string{
		"inbox/<job>/BRIEF.md",
		"the job directory <card> at epoch 0 and <card>~<epoch> after a clear",
		"outbox/<job>/REPORT.md",
		"when the tip is another sha (both named), when origin has no branch of that name, when the tip cannot be read, or when the card names no REPO: line",
		"Verdict: HOLD, FAIL, FAILED or BROKEN",
		"a LAND without a full sha, an empty report and any other verdict finish the card failed too",
		"the friends table is drawn after merge only under where --all",
	} {
		assert.Contains(t, help, want, "nova-sprint help friend says %q", want)
	}
	for _, gone := range []string{
		"inbox/<card>/BRIEF.md",
		"outbox/<card>/REPORT.md",
		"is refused naming both shas",
		"Verdict: HOLD or FAIL",
		"friends after merge and before fleet",
	} {
		assert.NotContains(t, help, gone, "nova-sprint help friend no longer says %q", gone)
	}
}

// A friend beats through the sprint's server as a member does: `friend beat
// <friend>` and nothing more, its actor the friend.
func TestAFriendBeatsThroughTheServer(t *testing.T) {
	t.Parallel()
	r := newServerRig(t, "nova-sprint init --readers reader-a,reader-b --members m1:2")
	r.a.friends = friendRows("amy")
	r.boss("nova-sprint friend sync --root " + t.TempDir())
	res := r.one("friend", "beat", "amy")
	require.Equal(t, 0, res.Code, res.Stderr)
	assert.Contains(t, res.Stdout, "FRIEND-BEAT OK amy")
	assert.Contains(t, r.boss("nova-sprint where"), "amy     |     0 |       0 |     8 |    0 | 0.0% | down", "a beat is recorded, and never makes her up")
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

// A friend's beat answers her row as friend sync last wrote it, her delivery
// mode and her width, so her daemon reads her nova-config row from its beat
// without reaching the config store; a row with no mode is batch, and a sync
// after the row changes is what the next beat says.
func TestAFriendsBeatAnswersHerRowsModeAndWidth(t *testing.T) {
	t.Parallel()
	r := newServerRig(t, "nova-sprint init --readers reader-a,reader-b --members m1:2")
	mode, width := "", ""
	r.a.friends = func(context.Context, string) ([]config.Row, error) {
		f := map[string]string{"slots": "2", "tiers": "flash"}
		if mode != "" {
			f["mode"], f["width"] = mode, width
		}
		return []config.Row{{Name: "amy", Fields: f}}, nil
	}
	root := t.TempDir()
	r.boss("nova-sprint friend sync --root " + root)
	res := r.one("friend", "beat", "amy")
	require.Equal(t, 0, res.Code, res.Stderr)
	assert.Contains(t, res.Stdout, " row_mode=batch row_width=8")
	assert.Contains(t, res.Stdout, " row_token_cap=6000000")
	mode, width = "one-shot", "1"
	r.boss("nova-sprint friend sync --root " + root)
	res = r.one("friend", "beat", "amy")
	require.Equal(t, 0, res.Code, res.Stderr)
	assert.Contains(t, res.Stdout, " row_mode=one-shot row_width=1")
	assert.Contains(t, res.Stdout, " row_token_cap=6000000")
}
