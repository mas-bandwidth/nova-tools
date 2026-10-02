package main

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		"03:04:05  2 cards: s1-1 added to s1 by coordinator (with s1-2)",
		" bytes, shown by nova-sprint card s1-1", // a brief is said, not printed (log_brief_test.go)
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
	assert.False(t, !strings.Contains(out, "s1-2.w1: attempt 2") && !strings.Contains(out, "attempt 1 dealt to m1"), "log --member m1:\n%s", out)
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
	ta.ok("add --stream s1 --count 1 --brief-file " + writeBrief(t, "handle the empty case"))
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
		"reader-a and reader-b asked to read attempt 2 by coordinator",
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
	ta.ok("add --stream s1 --count 1 --brief-file " + writeBrief(t, "handle the empty case"))
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
