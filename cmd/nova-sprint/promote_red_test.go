package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// redLog is a failed run's log as gh run view --log-failed prints it: two shards and a
// darwin runner, one test failing on two of them, a subtest's failure, a testify failure
// whose message is on its Error: line, and a test an open card is already on.
const redLog = "unit (ubuntu-latest, 1)\tgo test\t2026-10-05T12:00:01.0000000Z === RUN   TestWallCapsAForkBomb\n" +
	"unit (ubuntu-latest, 1)\tgo test\t2026-10-05T12:00:02.0000000Z --- FAIL: TestWallCapsAForkBomb (0.40s)\n" +
	"unit (ubuntu-latest, 1)\tgo test\t2026-10-05T12:00:02.0000000Z     wall_test.go:88: the fork bomb ran past the cap: 4096 processes\n" +
	"unit (ubuntu-latest, 1)\tgo test\t2026-10-05T12:00:02.0000000Z FAIL\n" +
	"unit (ubuntu-latest, 1)\tgo test\t2026-10-05T12:00:02.0000000Z FAIL\tgithub.com/mas-bandwidth/nova-tools/internal/wall\t0.41s\n" +
	"unit (ubuntu-latest, 1)\tgo test\t2026-10-05T12:00:03.0000000Z --- FAIL: TestSnapshotNotesASymlinkedNovaEntryItSkips (0.01s)\n" +
	"unit (ubuntu-latest, 1)\tgo test\t2026-10-05T12:00:03.0000000Z     --- FAIL: TestSnapshotNotesASymlinkedNovaEntryItSkips/link (0.00s)\n" +
	"unit (ubuntu-latest, 1)\tgo test\t2026-10-05T12:00:03.0000000Z         snapshot_test.go:41: \n" +
	"unit (ubuntu-latest, 1)\tgo test\t2026-10-05T12:00:03.0000000Z         \tError Trace:\t/home/runner/work/nova-tools/cmd/nova-sprint/snapshot_test.go:41\n" +
	"unit (ubuntu-latest, 1)\tgo test\t2026-10-05T12:00:03.0000000Z         \tError:      \tShould contain \"skipped\"\n" +
	"unit (ubuntu-latest, 1)\tgo test\t2026-10-05T12:00:03.0000000Z FAIL\tgithub.com/mas-bandwidth/nova-tools/cmd/nova-sprint\t9.10s\n" +
	"unit (ubuntu-latest, 2)\tgo test\t2026-10-05T12:00:04.0000000Z --- FAIL: TestAlreadyOnACard (0.01s)\n" +
	"unit (ubuntu-latest, 2)\tgo test\t2026-10-05T12:00:04.0000000Z     links_test.go:12: still broken\n" +
	"unit (ubuntu-latest, 2)\tgo test\t2026-10-05T12:00:04.0000000Z FAIL\tgithub.com/mas-bandwidth/nova-tools/internal/links\t0.02s\n" +
	"unit (macos-latest)\tgo test\t2026-10-05T12:00:05.0000000Z --- FAIL: TestWallCapsAForkBomb (0.50s)\n" +
	"unit (macos-latest)\tgo test\t2026-10-05T12:00:05.0000000Z     wall_test.go:90: another line on darwin\n" +
	"unit (macos-latest)\tgo test\t2026-10-05T12:00:05.0000000Z FAIL\tgithub.com/mas-bandwidth/nova-tools/internal/wall\t0.51s\n" +
	"unit (macos-latest)\tgo test\t2026-10-05T12:00:05.0000000Z ok  \tgithub.com/mas-bandwidth/nova-tools/internal/other\t0.10s\n"

// redScript is promoteScript with the tip's go.mod read and the development branch's
// runs answered: devLog, when set, is a red run on dev after the merge.
type redScript struct {
	*promoteScript
	merged bool
	devLog string
}

func (s *redScript) git(ctx context.Context, dir string, args ...string) (string, error) {
	if args[0] == "show" {
		return "module github.com/mas-bandwidth/nova-tools\n\ngo 1.25\n", nil
	}
	return s.promoteScript.git(ctx, dir, args...)
}

func (s *redScript) gh(ctx context.Context, dir string, args ...string) (string, error) {
	joined := strings.Join(args, " ")
	switch {
	case s.merged && args[0] == "pr" && args[1] == "view":
		s.ghCalls = append(s.ghCalls, args)
		return `{"id":"PR_node_1","state":"MERGED","mergeCommit":{"oid":"cccccccccccccccccccccccccccccccccccccccc"}}`, nil
	case args[0] == "run" && args[1] == "list" && strings.Contains(joined, "--commit"):
		s.ghCalls = append(s.ghCalls, args)
		return `[{"databaseId":9,"conclusion":"failure","status":"completed","name":"ci"},{"databaseId":10,"conclusion":"success","status":"completed","name":"functional"}]`, nil
	case args[0] == "run" && args[1] == "view" && args[2] == "9":
		s.ghCalls = append(s.ghCalls, args)
		return s.devLog, nil
	}
	return s.promoteScript.gh(ctx, dir, args...)
}

// workCards is every placed card of the work table.
func (ta *testApp) workCards() []*sprint.Card {
	ta.t.Helper()
	st := &store.Store{B: ta.m, Names: sprint.Names{}, Now: ta.a.now}
	s, err := st.Load(context.Background(), []string{sprint.Work}, nil)
	require.NoError(ta.t, err)
	var out []*sprint.Card
	for _, c := range s.Work.Cards() {
		if c.Placed() {
			out = append(out, c)
		}
	}
	return out
}

// A red check after a promotion cuts one fix card per failing test into the day's
// promote-red stream, by the machine (the owner, 2026-10-05: "every step the coordinator did
// by hand today is a missing instruction"): START the test and its failing line from the
// log, STOP the test green on that runner, PATHS the test file and the package, TEST the
// test; heavy, ranked in front of every card on the table, deduplicated by test name across
// runners and against open cards (a hand-written one too), and one judgment naming the cards.
// A later red run of the same tests cuts nothing again, and a red development branch after
// the merge cuts its own.
func TestARedPromotionCutsOneFixCardPerFailingTest(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream a --count 1 --one --brief-file " + writeBrief(t, "work already in line"))
	ta.ok("add --stream fixes by-hand --one --brief-file " + writeBrief(t, "TEST: ./internal/links TestAlreadyOnACard"))
	low := 0.0
	for i, c := range ta.workCards() {
		if i == 0 || c.Score < low {
			low = c.Score
		}
	}

	const (
		live = "sprint/live"
		tip  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		base = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	s := &redScript{promoteScript: &promoteScript{
		live: live, tip: tip, baseSHA: base,
		logText:  "land s1-1 (sprint stream s1)\n",
		groupLog: redLog,
	}}
	p := &promoter{
		dir: t.TempDir(), live: live, base: "dev",
		now:    time.Date(2026, 10, 5, 16, 0, 0, 0, time.UTC),
		gitRun: s.git, ghRun: s.gh,
		gate: func(context.Context, string, string) (string, error) { return "", nil },
		cut:  ta.a.promoteCutter(common{verb: "promote", redis: "mem:0"}),
	}
	var out bytes.Buffer
	o, code := p.step(context.Background(), &out, io.Discard)
	require.Equal(t, 1, code, "a red check is the judgment: %s", out.String())
	require.NotNil(t, o.Judgment, "one judgment")
	want := []string{"red-TestWallCapsAForkBomb", "red-TestSnapshotNotesASymlinkedNovaEntryItSkips"}
	require.Equal(t, want, o.Judgment.Cards, "one card per failing test, deduplicated by name across runners; the test an open card is on cuts none: %s", out.String())
	require.Contains(t, out.String(), "cards=red-TestWallCapsAForkBomb,red-TestSnapshotNotesASymlinkedNovaEntryItSkips", "the judgment names the cards")
	require.Contains(t, out.String(), "TestAlreadyOnACard failed again, and by-hand is open on it")

	wall := ta.primary("red-TestWallCapsAForkBomb")
	require.Equal(t, "promote-red-2026-10-05", wall.Row, "the day's promote-red stream")
	brief := wall.F("brief")
	require.True(t, strings.HasPrefix(brief, "tier: heavy\n"), "heavy tier: %s", brief)
	for _, line := range []string{
		"REPO: mas-bandwidth/nova-tools\n",
		"BASE: sprint/live\n",
		"START: TestWallCapsAForkBomb fails on unit (ubuntu-latest, 1), unit (macos-latest) (the pull request #42 of promo/2026-10-05-1): wall_test.go:88: the fork bomb ran past the cap: 4096 processes\n",
		"STOP: TestWallCapsAForkBomb passes on unit (ubuntu-latest, 1), unit (macos-latest)",
		"PATHS: internal/wall/wall_test.go,internal/wall/*.go\n",
		"TEST: ./internal/wall TestWallCapsAForkBomb\n",
		"Work only in the job directory this card names.",
	} {
		require.Contains(t, brief, line)
	}
	snap := ta.primary("red-TestSnapshotNotesASymlinkedNovaEntryItSkips").F("brief")
	require.Contains(t, snap, "START: TestSnapshotNotesASymlinkedNovaEntryItSkips fails on unit (ubuntu-latest, 1) (the pull request #42 of promo/2026-10-05-1): snapshot_test.go:41: Should contain \"skipped\"\n", "the testify message is the failing line")
	require.Contains(t, snap, "PATHS: cmd/nova-sprint/snapshot_test.go,cmd/nova-sprint/*.go\n")
	require.Contains(t, snap, "TEST: ./cmd/nova-sprint TestSnapshotNotesASymlinkedNovaEntryItSkips\n")
	for _, id := range want {
		require.Less(t, ta.primary(id).Score, low, "%s is ranked in front of every card on the table", id)
	}
	require.Less(t, wall.Score, ta.primary(want[1]).Score, "in the order the log names them")

	// another pass of the same red branch raises no second judgment and cuts nothing
	n := len(ta.workCards())
	again, code := p.step(context.Background(), io.Discard, io.Discard)
	require.Equal(t, 1, code)
	require.Nil(t, again.Judgment)
	require.Len(t, ta.workCards(), n)

	// a later promotion red on the same tests cuts nothing again: the cards are open
	p2 := *p
	p2.judged = ""
	o, _ = p2.step(context.Background(), io.Discard, io.Discard)
	require.NotNil(t, o.Judgment)
	require.Empty(t, o.Judgment.Cards, "deduplicated against the open cards by test name: %v", o.Judgment.Said)
	require.Len(t, ta.workCards(), n)

	// the promotion merges; dev's own run on the merge goes red on a test no card is on
	s.merged = true
	s.devLog = "functional (ubuntu-latest)\ttest\t2026-10-05T13:00:00.0000000Z --- FAIL: TestWallCoverWallCommitsCountsPastBaseRef (1.00s)\n" +
		"functional (ubuntu-latest)\ttest\t2026-10-05T13:00:00.0000000Z     cover_test.go:7: 3 commits, want 2\n" +
		"functional (ubuntu-latest)\ttest\t2026-10-05T13:00:00.0000000Z FAIL\tgithub.com/mas-bandwidth/nova-tools/internal/wall\t1.10s\n"
	p3 := *p
	p3.judged = ""
	o, code = p3.step(context.Background(), io.Discard, io.Discard)
	require.Equal(t, 0, code)
	require.Equal(t, "cccccccccccccccccccccccccccccccccccccccc", o.Promoted)
	out.Reset()
	o, code = p3.step(context.Background(), &out, io.Discard)
	require.Equal(t, 1, code, "%s", out.String())
	require.NotNil(t, o.Judgment)
	require.Equal(t, []string{"red-TestWallCoverWallCommitsCountsPastBaseRef"}, o.Judgment.Cards, "%s", out.String())
	require.Equal(t, []string{"fix", "revert"}, o.Judgment.Decisions)
	require.Contains(t, ta.primary("red-TestWallCoverWallCommitsCountsPastBaseRef").F("brief"), "(dev at cccccccccccccccccccccccccccccccccccccccc): cover_test.go:7: 3 commits, want 2\n")
	// dev's run is read once: the next pass raises nothing more
	o, _ = p3.step(context.Background(), io.Discard, io.Discard)
	require.Nil(t, o.Judgment)
}
