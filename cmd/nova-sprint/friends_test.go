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
// outbox/<job>/REPORT.md), its status the fleet's rule over the friend's beats and
// the coordinator's hold, its order the fleet's.

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
// done, ok unless the report's verdict says otherwise; width is 1; done and ok%
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
		"amy     |     1 |       1 |     1 |    2 | 50.0% | up\n"+
		"bob     |     0 |       0 |     1 |    0 | 0.0%  | down\n"+
		"cat     |     0 |       0 |     1 |    0 | 0.0%  | down\n"+
		"--------+-------+---------+-------+------+-------+-------\n"+
		"        |     1 |       1 |     3 |    2 | 50.0% |", tableOf(ta.frame(), sprint.Friends))
	var w whereView
	ta.json("where", &w)
	assert.Equal(t, map[string]string{"ready": "1", "working": "1", "width": "1", "done": "2", "okpct": "50.0%", "status": "up", "ok": "1", "failed": "1"}, w.Tables[sprint.Friends]["amy"])
	assert.Equal(t, map[string]string{"ready": "0", "working": "0", "width": "1", "done": "0", "okpct": "0.0%", "status": "down", "ok": "0", "failed": "0"}, w.Tables[sprint.Friends]["cat"])

	assert.Contains(t, ta.ok("friend sync --root "+root), "nothing to do")

	// amy starts j1 and finishes j2 with a report that names no verdict: ok
	jobs(t, root, "amy", nil, []string{"j1"}, map[string]string{"j2": "# j2\n\nAll green.\n"})
	assert.Contains(t, ta.ok("friend sync --root "+root), "FRIEND-SYNC OK added=- removed=- updated=amy friends=3 jobs=4")
	assert.Equal(t, "amy     |     0 |       1 |     1 |    3 | 66.7% | up", strings.Split(tableOf(ta.frame(), sprint.Friends), "\n")[2])
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
				"friend-b |     0 |       0 |     1 |    0 | 0.0% | up\n" +
				"friend-a |     0 |       0 |     1 |    0 | 0.0% | held\n" +
				"---------+-------+---------+-------+------+------+-------\n" +
				"         |     0 |       0 |     2 |    0 | 0.0% |"},
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
	assert.Equal(t, []string{"amy", "zed", "cat", "bob"}, rowsOf(tableOf(ta.frame(), sprint.Friends)), "up, then held, then down, each by name")

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
	assert.Contains(t, ta.ok("friend sync"), "FRIEND-SYNC OK added=amy,bob removed=- updated=- friends=2 jobs=0")
	ta.ok("friend beat bob")
	ta.ok("friend down amy")
	assert.Contains(t, ta.ok("friend sync"), "nothing to do")

	_, err := cfg.Delete(context.Background(), config.KindFriend, "bob", "t")
	require.NoError(t, err)
	addFriendRow(t, cfg, "cat")
	assert.Contains(t, ta.ok("friend sync"), "FRIEND-SYNC OK added=cat removed=bob updated=- friends=2 jobs=0")
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
	r.boss("nova-sprint friend sync --root " + t.TempDir())
	res := r.one("friend", "beat", "amy")
	require.Equal(t, 0, res.Code, res.Stderr)
	assert.Contains(t, res.Stdout, "FRIEND-BEAT OK amy")
	assert.Contains(t, r.boss("nova-sprint where"), "amy     |     0 |       0 |     1 |    0 | 0.0% | up")
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
