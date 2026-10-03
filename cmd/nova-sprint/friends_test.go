package main

import (
	"context"
	"errors"
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
// load"): its rows are nova-config's friend rows, its counts the job cards friend
// sync reads from each friend's working directory (inbox/<job>, outbox/<job>,
// outbox/<job>/REPORT.md), its status the friends' rule (asleep after 15 s
// without a beat) over her beats and the coordinator's hold, its order the
// fleet's.

// emptyFriends is the friends table with no friend: its header, one rule and
// the footer, as every empty table is.
const emptyFriends = "friends | ready | working | width | done | ok%  | status\n" +
	"--------+-------+---------+-------+------+------+-------\n" +
	"        |     0 |       0 |     0 |    0 | 0.0% |"

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

// The friends table has the fleet table's columns but load, from the job cards
// friend sync reads of each friend's working directory: a job of inbox/ is ready
// until outbox/<job>/ exists, working until outbox/<job>/REPORT.md exists, then
// done, ok unless the report's verdict says otherwise; width is the friend row's,
// 8 when it names none (TestFriendSyncWritesTheConfiguredWidth); done and ok%
// are the formulas over ok and failed, and the footer sums and pools them. A
// friend with no directory, or no jobs, shows zeros. A sync after the directories
// moved moves the counts, and a sync after a sync writes nothing. The same cells
// are in where --json, under the column names.
func TestTheFriendsTableCountsTheJobCardsFriendSyncReads(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t, "amy", "bob", "cat")
	root := t.TempDir()
	jobs(t, root, "amy", []string{"j1", "j2", "j3", "j4"}, []string{"j2"}, map[string]string{
		"j3": "# j3\n\nVerdict: OK\n",
		"j4": "# j4\n\n**Verdict:** FAIL, the gate is red\n",
	})
	jobs(t, root, "bob", nil, nil, nil) // a directory with no inbox
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bob-working"), 0o755))
	assert.Contains(t, ta.ok("friend sync --root "+root), "FRIEND-SYNC OK added=amy,bob,cat removed=- updated=- friends=3 jobs=4")
	ta.ok("friend beat amy")
	assert.Equal(t, "friends | ready | working | width | done | ok%   | status\n"+
		"--------+-------+---------+-------+------+-------+-------\n"+
		"amy     |     1 |       1 |     8 |    2 | 50.0% | up\n"+
		"bob     |     0 |       0 |     8 |    0 | 0.0%  | asleep\n"+
		"cat     |     0 |       0 |     8 |    0 | 0.0%  | asleep\n"+
		"--------+-------+---------+-------+------+-------+-------\n"+
		"        |     1 |       1 |    24 |    2 | 50.0% |", tableOf(ta.frame(), sprint.Friends))
	var w whereView
	ta.json("where", &w)
	assert.Equal(t, map[string]string{"ready": "1", "working": "1", "width": "8", "done": "2", "okpct": "50.0%", "status": "up", "ok": "1", "failed": "1"}, w.Tables[sprint.Friends]["amy"])
	assert.Equal(t, map[string]string{"ready": "0", "working": "0", "width": "8", "done": "0", "okpct": "0.0%", "status": "asleep", "ok": "0", "failed": "0"}, w.Tables[sprint.Friends]["cat"])

	assert.Contains(t, ta.ok("friend sync --root "+root), "nothing to do")

	// amy starts j1 and finishes j2 with a report that names no verdict: ok
	jobs(t, root, "amy", nil, []string{"j1"}, map[string]string{"j2": "# j2\n\nAll green.\n"})
	assert.Contains(t, ta.ok("friend sync --root "+root), "FRIEND-SYNC OK added=- removed=- updated=amy friends=3 jobs=4")
	assert.Equal(t, "amy     |     0 |       1 |     8 |    3 | 66.7% | up", strings.Split(tableOf(ta.frame(), sprint.Friends), "\n")[2])
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
			out[f] = row[sprint.FieldWidth]
		}
		return out
	}
	assert.Equal(t, map[string]string{"amy": "8", "cat": "3"}, width(), "amy's row has no width: the default; cat's is 3")

	_, _, err = cfg.Update(context.Background(), config.KindFriend, "amy", map[string]string{"width": "2"}, "t")
	require.NoError(t, err)
	assert.Contains(t, ta.ok("friend sync"), "FRIEND-SYNC OK added=- removed=- updated=amy friends=2 jobs=0")
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

// The one rule of a report's verdict: the first line of REPORT.md whose key is
// Verdict or Status (after any markdown marks), its first word; HOLD, FAIL,
// FAILED or BROKEN, in any case, is a job done failed, and any other word, or no
// such line, is a job done ok.
func TestAReportsVerdictIsItsFirstVerdictOrStatusLine(t *testing.T) {
	t.Parallel()
	for report, ok := range map[string]bool{
		"":                                     true,
		"# done\n\nAll green.\n":               true,
		"Verdict: OK\n":                        true,
		"Verdict: PASS\nStatus: FAIL\n":        true,
		"verdict: fail\n":                      false,
		"Status: HOLD, a question for Glenn\n": false,
		"- **Verdict:** BROKEN\n":              false,
		"## Status\n\nVerdict: FAILED (2 tests)\n": false,
		"Not a verdict: FAIL\nVerdict: OK\n":       true,
	} {
		assert.Equal(t, ok, reportOK(report), "%q", report)
	}
}

// friend sync reads each friend's directory and never writes there: the fixture
// is unchanged after the sync. A directory that cannot be read is refused,
// naming it, and nothing is written to the store.
func TestFriendSyncReadsTheDirectoriesAndNeverWritesThem(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t, "amy")
	root := t.TempDir()
	jobs(t, root, "amy", []string{"j1"}, nil, nil)
	before := treeOf(t, root)
	ta.ok("friend sync --root " + root)
	assert.Equal(t, before, treeOf(t, root), "the sync wrote into the friend's directory")

	// inbox is a file, not a directory: it cannot be read as one
	ta2, _ := friendApp(t, "amy")
	root2 := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root2, "amy-working"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root2, "amy-working", "inbox"), []byte("x"), 0o644))
	code, out, errs := ta2.do("friend sync --root " + root2)
	assert.Equal(t, 1, code)
	assert.Empty(t, out)
	assert.Contains(t, errs, filepath.Join(root2, "amy-working", "inbox"))
	assert.Contains(t, errs, "nothing was changed")
	assert.Equal(t, emptyFriends, tableOf(ta2.frame(), sprint.Friends), "nothing was written")
}

// treeOf is every path under root with its size, for a before/after check.
func treeOf(t *testing.T, root string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	require.NoError(t, filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out[path] = info.Size()
		return nil
	}))
	return out
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
		out[f] = row[sprint.Status]
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
			lines: []string{"friend sync", "friend beat friend-b", "friend down friend-a"},
			want: "friends  | ready | working | width | done | ok%  | status\n" +
				"---------+-------+---------+-------+------+------+-------\n" +
				"friend-b |     0 |       0 |     8 |    0 | 0.0% | up\n" +
				"friend-a |     0 |       0 |     8 |    0 | 0.0% | held\n" +
				"---------+-------+---------+-------+------+------+-------\n" +
				"         |     0 |       0 |    16 |    0 | 0.0% |"},
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

// A friend is up while her last beat is under sprint.FriendAsleepAfter (15 s)
// old and asleep once she has gone that long without one, or when she has
// never beaten; a beat wakes her at once; held while friend down holds her
// whatever she beats, and friend up releases the hold without counting as a
// beat. The rows go up, then held, then asleep, each by name
// (store.FleetOrder).
func TestAFriendsStatusIsTheFriendsRuleOverItsBeatsAndItsHold(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t, "zed", "amy", "bob", "cat")
	ta.ok("friend sync")
	assert.Equal(t, map[string]string{"amy": "asleep", "bob": "asleep", "cat": "asleep", "zed": "asleep"}, ta.friendStatus(), "none has beaten")
	for _, f := range []string{"zed", "amy", "cat"} {
		ta.ok("friend beat " + f)
	}
	ta.ok("friend down cat")
	assert.Equal(t, map[string]string{"amy": "up", "bob": "asleep", "cat": "held", "zed": "up"}, ta.friendStatus())
	assert.Equal(t, []string{"amy", "zed", "cat", "bob"}, rowsOf(tableOf(ta.frame(), sprint.Friends)), "up, then held, then asleep, each by name")

	// every last beat was at t: up at t+14 s, asleep at t+16 s, up again at a beat
	require.Equal(t, 15*time.Second, sprint.FriendAsleepAfter)
	ta.a.sleep(14 * time.Second)
	ta.ok("friend beat zed")
	assert.Equal(t, map[string]string{"amy": "up", "bob": "asleep", "cat": "held", "zed": "up"}, ta.friendStatus(), "t+14 s: still up")
	ta.a.sleep(2 * time.Second)
	got := ta.friendStatus()
	assert.Equal(t, "asleep", got["amy"], "t+16 s with no beat: asleep")
	assert.Equal(t, "up", got["zed"], "beat at t+14 s: up")
	assert.Equal(t, "held", got["cat"], "a hold stands whatever the beats")
	ta.ok("friend beat amy")
	assert.Equal(t, "up", ta.friendStatus()["amy"], "a beat wakes her at once")

	// friend up is not a beat: cat's last beat is 16 s old, so released she is asleep
	ta.ok("friend up cat")
	assert.Equal(t, "asleep", ta.friendStatus()["cat"], "released with no recent beat: asleep, never up")
	ta.ok("friend beat cat")
	assert.Equal(t, "up", ta.friendStatus()["cat"], "released, and beating")
}

// A friend asleep works nothing: her working count is 0 in the friends table
// and in where --json while her jobs stay in her outbox, and they count again
// when she beats; ready and done are as they were. The owner, 2026-10-02
// 9:48 PM ET: "[a friend] being down, she automatically is 0/8 working OK?"
func TestAnAsleepFriendShowsNoneWorking(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t, "amy")
	root := t.TempDir()
	working := []string{"w1", "w2", "w3", "w4", "w5", "w6"}
	jobs(t, root, "amy", append([]string{"r1", "d1"}, working...), working, map[string]string{"d1": "Verdict: OK\n"})
	ta.ok("friend sync --root " + root)
	ta.ok("friend beat amy")
	cells := func() map[string]string {
		var w whereView
		ta.json("where", &w)
		c := w.Tables[sprint.Friends]["amy"]
		return map[string]string{"status": c["status"], "ready": c["ready"], "working": c["working"], "done": c["done"]}
	}
	footer := func() string {
		lines := strings.Split(tableOf(ta.frame(), sprint.Friends), "\n")
		return strings.TrimSpace(strings.Split(lines[len(lines)-1], "|")[2]) // the footer's working sum
	}
	assert.Equal(t, map[string]string{"status": "up", "ready": "1", "working": "6", "done": "1"}, cells(), "t: beating, six working")
	assert.Equal(t, "6", footer())
	ta.a.sleep(16 * time.Second)
	assert.Equal(t, map[string]string{"status": "asleep", "ready": "1", "working": "0", "done": "1"}, cells(), "t+16 s: asleep, none working")
	assert.Equal(t, "0", footer(), "the footer sums the rows as shown")
	ta.a.sleep(4 * time.Second)
	ta.ok("friend beat amy")
	assert.Equal(t, map[string]string{"status": "up", "ready": "1", "working": "6", "done": "1"}, cells(), "t+20 s: a beat, six working again")
}

// friend sync makes the friends table nova-config's friend rows: a row added
// comes in, a row removed goes with its beat, and a friend that stays keeps its
// hold. A second sync writes nothing and says so.
func TestFriendSyncFollowsTheConfigAndAHoldSurvivesIt(t *testing.T) {
	t.Parallel()
	ta, cfg := friendApp(t, "amy", "bob")
	assert.Contains(t, ta.ok("friend sync"), "FRIEND-SYNC OK added=amy,bob removed=- updated=- friends=2 jobs=0")
	ta.ok("friend beat bob")
	ta.ok("friend down amy")
	assert.Contains(t, ta.ok("friend sync"), "nothing to do")

	_, err := cfg.Delete(context.Background(), config.KindFriend, "bob", "t")
	require.NoError(t, err)
	addFriendRow(t, cfg, "cat")
	assert.Contains(t, ta.ok("friend sync"), "FRIEND-SYNC OK added=cat removed=bob updated=- friends=2 jobs=0")
	assert.Equal(t, map[string]string{"amy": "held", "cat": "asleep"}, ta.friendStatus(), "amy's hold survived the sync")

	addFriendRow(t, cfg, "bob")
	ta.ok("friend sync")
	assert.Equal(t, "asleep", ta.friendStatus()["bob"], "bob came back with no beat: its old beat went with its row")
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
	assert.Equal(t, map[string]string{"amy": "asleep"}, ta.friendStatus(), "nothing was changed")
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
	assert.Contains(t, r.boss("nova-sprint where"), "amy     |     0 |       0 |     8 |    0 | 0.0% | up")
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
