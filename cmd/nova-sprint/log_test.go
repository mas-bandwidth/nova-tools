package main

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// log prints every line of the epoch in local time, filtered by card,
// member, stream and time; --json gives the lines; a clear keeps the old
// epoch's log readable with --at-epoch.
func TestLogPrintsTheEpochsLinesFiltered(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.a.loc = time.UTC
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 2 --brief-file " + writeBrief(t, "handle the empty case"))
	ta.deal(2)
	ta.ok("take --as m1 s1-1.w1@1")
	ta.ok("finish --as m1 s1-1.w1@1 --failed --report 'the tests went red'")
	out := ta.ok("log --card s1-1")
	for _, want := range []string{
		"03:04:05  s1-1 added to s1 by coordinator (in a set of 2)", // a set move as the card's own line (log_card_test.go)
		" bytes, shown by nova-sprint card s1-1",                    // a brief is said, not printed (log_brief_test.go)
		"attempt 1 dealt to m1",
		"m1 took attempt 1",
		"m1 finished attempt 1: FAILED",
		"    report: the tests went red",
		"judgment: work came back failed",
	} {
		assert.Contains(t, out, want, "log --card s1-1 has no %q", want)
	}
	assert.NotContains(t, out, "s1-2.w1", "log --card s1-1 shows s1-2's work")
	out = ta.ok("log --member m1")
	assert.True(t, strings.Contains(out, "s1-2.w1: attempt 2") || strings.Contains(out, "attempt 1 dealt to m1"), "log --member m1:\n%s", out)
	ta.mu.Lock()
	ta.now = ta.now.Add(time.Hour)
	ta.mu.Unlock()
	assert.Contains(t, ta.ok("log --since 10m"), "LOG OK lines=0", "log --since 10m an hour later")
	var j struct {
		Lines []struct {
			Kind, Card string
			Cards      []string
		} `json:"lines"`
	}
	ta.json("log --card s1-2", &j)
	if assert.NotEmpty(t, j.Lines, "log --json: %+v", j) {
		assert.Equal(t, "move", j.Lines[0].Kind, "log --json: %+v", j)
		if assert.Len(t, j.Lines[0].Cards, 2, "log --json: %+v", j) {
			assert.Equal(t, "s1-2", j.Lines[0].Cards[1], "log --json: %+v", j)
		}
	}
	ta.ok("clear --confirm sprint")
	assert.Contains(t, ta.ok("log --at-epoch 0 --card s1-1"), "m1 finished attempt 1: FAILED", "the old epoch's log after a clear")
	assert.Contains(t, ta.ok("log --card s1-1"), "LOG OK lines=0", "the new epoch's log of s1-1")
}

// card tells the card's story: a card in flight says first what holds it
// and the commands that answer it; the timeline goes an attempt at a time,
// one line per event with the worker's and the readers' words; a landed
// card ends with one line.
func TestCardTellsTheStory(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.a.loc = time.UTC
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1 --one --brief-file " + writeBrief(t, "handle the empty case, tier: pro")) // pro: two readers
	ta.deal(1)
	ta.ok("take --as m1 s1-1.w1@1")
	ta.ok("finish --as m1 s1-1.w1@1 --failed --report 'the tests went red'")
	out := ta.ok("card s1-1")
	now := strings.Index(out, "now:")
	brief := strings.Index(out, "brief:")
	require.GreaterOrEqual(t, now, 0, "a card in flight, what holds it first:\n%s", out)
	require.GreaterOrEqual(t, brief, 0, "a card in flight, what holds it first:\n%s", out)
	require.LessOrEqual(t, now, brief, "a card in flight, what holds it first:\n%s", out)
	require.Contains(t, out, "waits on your judgment: work came back failed", "a card in flight, what holds it first")
	require.Contains(t, out, "rework with a fix: nova-sprint rework", "a card in flight, what holds it first")
	ta.ok("rework s1-1")
	ta.deal(1)
	ta.ok("take --as m1 s1-1.w2@1")
	ta.ok("finish --as m1 s1-1.w2@1 --head h2 --report 'handled; tests green'")
	ta.ok("ask")
	ta.ok("read --as reader-a --ok s1-1.r2.reader-a --finding 'the empty case is tested'")
	ta.ok("ask") // the second read, the first ok
	ta.ok("read --as reader-b --ok s1-1.r2.reader-b --finding 'fine'")
	ta.ok("accept s1-1")
	ta.ok("merge --stream s1")
	out = ta.ok("card s1-1")
	for _, want := range []string{
		"s1-1   stream s1   landed   attempt 2   head h2",
		"  attempt 1\n",
		`m1 finished attempt 1: FAILED. "the tests went red"`,
		"  attempt 2, because attempt 1 failed\n",
		`s1-1 reworked by coordinator: attempt 2; answers "work came back failed"`,
		`m1 finished attempt 2: ok, head h2. "handled; tests green"`,
		"reader-a asked to read attempt 2 by coordinator",
		"reader-b asked to read attempt 2 by coordinator", // one read at a time: two asks
		`reader-a read attempt 2: ok. "the empty case is tested"`,
		"merged into s1 and landed by coordinator",
		"attempt 1, report by m1 (failed):\n    the tests went red",
		"now:\n  landed; nothing waits",
	} {
		assert.Contains(t, out, want, "the story has no %q", want)
	}
	for _, not := range []string{"work came back ok", "the fix this attempt was given", "score"} {
		assert.NotContains(t, out, not, "the story of a landed card says %q", not)
	}
}

// The worker's packet: take hands the brief, this attempt's fix, the notes,
// the epoch and generation and the branch to work on (a rework starts from
// the attempt before's branch); queue --as shows the same; finish takes
// --branch and --base; a reader's packet carries the work it reads, its
// branch and the worker's report. No actor needs card to learn its task.
func TestTakeAndQueueHandTheirPackets(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1 --one --brief-file " + writeBrief(t, "handle the empty case"))
	ta.deal(1)
	out := ta.ok("take --as m1 s1-1.w1@1")
	for _, want := range []string{"PACKET s1-1.w1 attempt=1 gen=1 epoch=0", "  branch: sprint/s1-1.w1.g1.e0", "  brief:\n    handle the empty case", "  notes: none",
		"  report it: nova-sprint finish --as m1 s1-1.w1@1 --epoch 0 --branch sprint/s1-1.w1.g1.e0"} {
		assert.Contains(t, out, want, "take has no %q", want)
	}
	ta.ok("finish --as m1 s1-1.w1@1 --failed --branch feature/empty --report 'the tests went red'")
	ta.ok("rework s1-1 --fix 'check the nil slice too'")
	ta.deal(1)
	var j struct {
		Packets []struct {
			Card, Branch, Base, Brief, Fix string
			Gen                            int
		} `json:"packets"`
	}
	ta.json("take --as m1 s1-1.w2@1", &j)
	require.Len(t, j.Packets, 1, "take --json packets: %+v", j)
	require.Equal(t, "check the nil slice too", j.Packets[0].Fix, "take --json packets: %+v", j)
	require.Equal(t, "feature/empty", j.Packets[0].Base, "take --json packets: %+v", j)
	require.True(t, strings.HasPrefix(j.Packets[0].Brief, "handle the empty case"), "take --json packets: %+v", j)
	out = ta.ok("queue --as m1")
	require.Contains(t, out, "  fix (this attempt):\n    check the nil slice too", "queue --as m1")
	require.Contains(t, out, "  base: feature/empty", "queue --as m1")
	// the packet says why the attempt exists, and card tells it per attempt
	assert.Contains(t, ta.ok("queue --as m1"), "  why this attempt exists:\n    attempt 1 failed: the tests went red")
	assert.Equal(t, 1, strings.Count(ta.ok("card s1-1"), "check the nil slice too"), "card says the fix once")
	assert.Contains(t, ta.ok("card s1-1"), "attempt 2 was given:\n  because:\n    attempt 1 failed: the tests went red\n  the fix:\n    check the nil slice too")
	ta.ok("finish --as m1 s1-1.w2@1 --head h2 --branch feature/empty-2 --base feature/empty --report 'handled; tests green'")
	ta.ok("ask")
	out = ta.ok("queue --as reader-a")
	for _, want := range []string{"PACKET s1-1.r2.reader-a attempt=2", "  work: attempt 2 by m1", "  head: h2", "  branch: feature/empty-2", "  base: feature/empty",
		"  report:\n    handled; tests green", "  report it: nova-sprint read --as reader-a (--ok | --broken) s1-1.r2.reader-a --epoch 0"} {
		assert.Contains(t, out, want, "the reader's queue has no %q", want)
	}
}

// where does not show the merge table's since column: the machine keeps
// the cell, the view hides it, on a store made before the rule too. The
// table shows once a stream has a card in it (whereFixture has one queued).
func TestWhereHidesTheMergeTablesSince(t *testing.T) {
	t.Parallel()
	ta := whereFixture(t)
	out := ta.ok("where --all")
	block := tableOf(out, "merge")
	require.NotEmpty(t, block, "no merge table:\n%s", out)
	require.NotContains(t, block, "since", "where shows since:\n%s", out)
}

// log --json --since with a window wider than about 22 h returns every entry
// since that time, in a test over the twin store with an injected clock, no
// real time.
func TestLogJsonSinceWithAWindowWiderThan22HoursReturnsEveryEntry(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.a.loc = time.UTC
	ta.ok("init --readers reader-a --members m1")

	// Add an early card at T0
	ta.ok("add --stream s1 --count 1 --one --brief-file " + writeBrief(t, "early card"))
	ta.deal(1)
	ta.ok("take --as m1 s1-1.w1@1")
	ta.ok("finish --as m1 s1-1.w1@1 --head h1 --report 'done'")

	// Advance injected clock forward 25 hours (window wider than 22 hours)
	ta.mu.Lock()
	t0 := ta.now.Add(-25 * time.Hour) // time when s1-1 was created
	ta.now = ta.now.Add(25 * time.Hour)
	ta.mu.Unlock()

	// Add a later card at T0 + 25h
	ta.ok("add --stream s1 --count 1 --one --brief-file " + writeBrief(t, "later card"))
	ta.deal(1)
	ta.ok("take --as m1 s1-2.w1@1")
	ta.ok("finish --as m1 s1-2.w1@1 --head h2 --report 'done'")

	// Test 1: Query with day duration (e.g. 1d = 24h ago).
	// Should return s1-2 (added at T0+25h), but not s1-1 (added at T0, which is 25h ago).
	var j1 struct {
		Lines []sprint.Line `json:"lines"`
	}
	ta.json("log --json --since 1d", &j1)
	require.NotEmpty(t, j1.Lines, "log --json --since 1d should return entries")
	cards1 := make(map[string]bool)
	for _, l := range j1.Lines {
		if l.Card != "" {
			cards1[l.Card] = true
		}
	}
	assert.True(t, cards1["s1-2"], "s1-2 should be in log --json --since 1d")
	assert.False(t, cards1["s1-1"], "s1-1 should not be in log --json --since 1d")

	// Test 2: Query with duration wider than 25h (e.g. 26h).
	// Should return both s1-1 and s1-2.
	var j2 struct {
		Lines []sprint.Line `json:"lines"`
	}
	ta.json("log --json --since 26h", &j2)
	require.NotEmpty(t, j2.Lines, "log --json --since 26h should return entries")
	cards2 := make(map[string]bool)
	for _, l := range j2.Lines {
		if l.Card != "" {
			cards2[l.Card] = true
		}
	}
	assert.True(t, cards2["s1-1"], "s1-1 should be in log --json --since 26h")
	assert.True(t, cards2["s1-2"], "s1-2 should be in log --json --since 26h")

	// Test 3: Query with date-only format (25h ago date: e.g. 2026-10-02).
	// Window is wider than 22h, should return every entry from that date onwards.
	var j3 struct {
		Lines []sprint.Line `json:"lines"`
	}
	sinceDate := t0.Format(time.DateOnly)
	ta.json("log --json --since "+sinceDate, &j3)
	require.NotEmpty(t, j3.Lines, "log --json --since <DateOnly> should return entries")
	cards3 := make(map[string]bool)
	for _, l := range j3.Lines {
		if l.Card != "" {
			cards3[l.Card] = true
		}
	}
	assert.True(t, cards3["s1-1"], "s1-1 should be in log --json --since "+sinceDate)
	assert.True(t, cards3["s1-2"], "s1-2 should be in log --json --since "+sinceDate)

	// Test 4: Query with RFC3339 time wider than 22h (e.g. at T0).
	var j4 struct {
		Lines []sprint.Line `json:"lines"`
	}
	ta.json("log --json --since "+t0.Format(time.RFC3339), &j4)
	require.NotEmpty(t, j4.Lines, "log --json --since <RFC3339> should return entries")
	cards4 := make(map[string]bool)
	for _, l := range j4.Lines {
		if l.Card != "" {
			cards4[l.Card] = true
		}
	}
	assert.True(t, cards4["s1-1"], "s1-1 should be in log --json --since <RFC3339>")
	assert.True(t, cards4["s1-2"], "s1-2 should be in log --json --since <RFC3339>")

	// Test 5: Query with time without seconds and space separator (wider than 22h)
	var j5 struct {
		Lines []sprint.Line `json:"lines"`
	}
	ta.json("log --json --since '"+t0.Format("2006-01-02 15:04Z07:00")+"'", &j5)
	require.NotEmpty(t, j5.Lines, "log --json --since <without seconds> should return entries")
	cards5 := make(map[string]bool)
	for _, l := range j5.Lines {
		if l.Card != "" {
			cards5[l.Card] = true
		}
	}
	assert.True(t, cards5["s1-1"], "s1-1 should be in log --json --since without seconds")
	assert.True(t, cards5["s1-2"], "s1-2 should be in log --json --since without seconds")
}
