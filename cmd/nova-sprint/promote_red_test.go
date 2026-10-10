package main

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
)

// ghLog is a failed job's log as gh run view --log-failed prints it: each line
// `<job>\t<step>\t<time> <text>`.
func ghLog(job string, lines ...string) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(job + "\tRun go test\t2026-10-05T21:00:00.1234567Z " + l + "\n")
	}
	return b.String()
}

// A red check on the promotion's pull request, and a red run on dev after the
// merge, cut one fix card per distinct failing test into the promote-red stream
// by the machine: the brief names the test and its failing line (START), the
// runner it must go green on (STOP), the test file and the package (PATHS) and
// the test (TEST), heavy tier, ranked ahead of every card on the table, a test
// an open card names cut again by no one, and the one judgment names the cards.
func TestARedPromotionCutsOneFixCardPerFailingTest(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 2")
	fs, c := ta.a.verbSetup("promote")
	_, err := parse(fs, nil)
	require.NoError(t, err)

	const (
		live   = "sprint/live"
		tip    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		base   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		merged = "cccccccccccccccccccccccccccccccccccccccc"
		links  = "TestLinksReportsATargetReachedThroughADirectorySymlinkOutOfTheTree"
		fork   = "TestWallCapsAForkBomb"
		snap   = "TestSnapshotNotesASymlinkedNovaEntryItSkips"
		cover  = "TestWallCoverWallCommitsCountsPastBaseRef"
	)
	logs := map[string]string{
		"7": ghLog("hosted (ubuntu-latest)",
			"=== RUN   "+links,
			"--- FAIL: "+links+" (0.01s)",
			"    links_test.go:88: want the target reported, got none",
			"FAIL",
			"FAIL\tgithub.com/mas-bandwidth/nova-tools/internal/docs\t0.31s",
		) + ghLog("functional (ubuntu-latest)",
			"--- FAIL: "+fork+" (2.00s)",
			"    wall_functional_test.go:41: the fork bomb ran past its cap",
			"FAIL",
			"FAIL\tgithub.com/mas-bandwidth/nova-tools/cmd/nova-sprint\t2.10s",
		),
		"8": ghLog("hosted (macos-latest)",
			"--- FAIL: "+links+" (0.01s)",
			"    links_test.go:88: want the target reported, got none",
			"--- FAIL: "+snap+" (0.02s)",
			"    snapshot_test.go:120: the symlinked entry was not noted",
			"FAIL",
			"FAIL\tgithub.com/mas-bandwidth/nova-tools/internal/docs\t0.40s",
		),
		"9": ghLog("hosted (ubuntu-latest)",
			"--- FAIL: "+cover+" (0.05s)",
			"    wall_test.go:77: counted 0 commits past the base ref, want 3",
			"--- FAIL: "+fork+" (2.00s)",
			"    wall_functional_test.go:41: the fork bomb ran past its cap",
			"FAIL",
			"FAIL\tgithub.com/mas-bandwidth/nova-tools/cmd/nova-sprint\t2.10s",
		),
	}
	s := &promoteScript{live: live, tip: tip, baseSHA: base, logText: "land s1-1 (sprint stream s1)\n"}
	promoted := false
	git := func(ctx context.Context, dir string, args ...string) (string, error) {
		switch {
		case args[0] == "show":
			return "module github.com/mas-bandwidth/nova-tools\n\ngo 1.25\n", nil
		case promoted && args[0] == "rev-parse" && args[len(args)-1] == "refs/promoted/last":
			return merged, nil
		}
		return s.git(ctx, dir, args...)
	}
	gh := func(ctx context.Context, dir string, args ...string) (string, error) {
		joined := strings.Join(args, " ")
		switch {
		case args[0] == "repo":
			return "mas-bandwidth/nova-tools", nil
		case args[0] == "run" && args[1] == "list" && strings.Contains(joined, "--commit "+merged):
			return `[{"databaseId":9,"conclusion":"failure","status":"completed","name":"ci"}]`, nil
		case args[0] == "run" && args[1] == "list":
			return `[{"databaseId":7,"conclusion":"failure","status":"completed","name":"ci"},{"databaseId":6,"conclusion":"success","status":"completed","name":"lint"},{"databaseId":8,"conclusion":"failure","status":"completed","name":"darwin"}]`, nil
		case args[0] == "run" && args[1] == "view":
			return logs[args[2]], nil
		}
		return s.gh(ctx, dir, args...)
	}
	p := &promoter{
		dir: t.TempDir(), live: live, base: "dev",
		now:    time.Date(2026, 10, 5, 21, 0, 0, 0, time.UTC),
		gitRun: git, ghRun: gh,
		gate: func(context.Context, string, string) (string, error) { return "", nil },
		red:  ta.a.redCutter(*c),
	}

	out, code := p.step(context.Background(), io.Discard, io.Discard)
	require.Equal(t, 1, code, "a red pull request is the judgment")
	require.NotNil(t, out.Judgment, "one judgment")
	want := []string{"red-" + links, "red-" + fork, "red-" + snap}
	assert.Equal(t, want, out.Judgment.Cards, "one card per distinct failing test, the judgment naming them")
	assert.Empty(t, out.Judgment.Open)
	assert.Equal(t, promoteDecisions, out.Judgment.Decisions)

	st := &store.Store{B: ta.m, Names: sprint.Names{}, Now: ta.a.now}
	load := func() *sprint.Snapshot {
		snapshot, err := st.Load(context.Background(), []string{sprint.Work}, nil)
		require.NoError(t, err)
		return snapshot
	}
	const stream = "promote-red-2026-10-05"
	cards := func(sn *sprint.Snapshot) map[string]*sprint.Card {
		got := map[string]*sprint.Card{}
		for _, card := range sn.Work.Cards() {
			if card.Row == stream {
				got[card.ID] = card
			}
		}
		return got
	}
	sn := load()
	got := cards(sn)
	require.Len(t, got, 3, "the cards are in the day's promote-red stream")
	for _, card := range sn.Work.Cards() {
		if card.Row == "s1" {
			for _, id := range want {
				assert.Less(t, got[id].Score, card.Score, "%s is ranked ahead of %s", id, card.ID)
			}
		}
	}
	type head struct{ start, stop, paths, test string }
	heads := map[string]head{
		links: {"links_test.go:88: want the target reported, got none", "green on hosted (ubuntu-latest)", "internal/docs/links_test.go,internal/docs/*.go", "./internal/docs " + links},
		fork:  {"wall_functional_test.go:41: the fork bomb ran past its cap", "green on functional (ubuntu-latest)", "cmd/nova-sprint/wall_functional_test.go,cmd/nova-sprint/*.go", "-tags functional ./cmd/nova-sprint " + fork},
		snap:  {"snapshot_test.go:120: the symlinked entry was not noted", "green on hosted (macos-latest)", "internal/docs/snapshot_test.go,internal/docs/*.go", "./internal/docs " + snap},
	}
	for test, h := range heads {
		brief := got["red-"+test].F("brief")
		m, _ := cardhdr.ReadModel(brief)
		assert.Equal(t, "heavy", m.Tier, "%s: line 1 names the heavy tier", test)
		assert.Contains(t, brief, "\nSTART: "+test, test)
		assert.Contains(t, brief, h.start, "%s: START carries the failing line", test)
		assert.Contains(t, brief, "\nSTOP: "+test+" "+h.stop, test)
		assert.Contains(t, brief, "\nPATHS: "+h.paths+"\n", test)
		assert.Contains(t, brief, "\nTEST: "+h.test+"\n", test)
		assert.Contains(t, brief, "\nBASE: "+live+"\n", "%s: a fix starts from the sprint branch", test)
		assert.Contains(t, brief, "\nREPO: mas-bandwidth/nova-tools\n", test)
		assert.Equal(t, test, sprint.RedTestOf(brief))
	}

	// a later red pass of another promotion cuts no card a test already has open:
	// the judged promotion is forgotten, as a fresh clone or a moved tip leaves it
	p.judged = ""
	s.cfg = nil
	again, code := p.step(context.Background(), io.Discard, io.Discard)
	require.Equal(t, 1, code)
	require.NotNil(t, again.Judgment)
	assert.Empty(t, again.Judgment.Cards, "deduplicated by test name against the open cards")
	assert.ElementsMatch(t, []string{links, fork, snap}, again.Judgment.Open)
	assert.Len(t, cards(load()), 3)

	// dev red after the merge: the new failing test is cut, the open one named, once
	promoted = true
	var said strings.Builder
	dev := p.devRed(context.Background(), &said, io.Discard)
	require.NotNil(t, dev, "a red run on dev at the promotion's merge is a judgment")
	assert.Equal(t, []string{"red-" + cover}, dev.Cards)
	assert.Equal(t, []string{fork}, dev.Open)
	assert.Contains(t, said.String(), "JUDGMENT dev red base=dev sha="+merged)
	assert.Contains(t, said.String(), "cards=red-"+cover)
	assert.Contains(t, cards(load())["red-"+cover].F("brief"), "dev at "+merged)
	assert.Nil(t, p.devRed(context.Background(), io.Discard, io.Discard), "a run already judged raises no second judgment")
	assert.Len(t, cards(load()), 4)
	ta.clean()
}
