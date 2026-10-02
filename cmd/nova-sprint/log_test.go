package main

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
		if !strings.Contains(out, want) {
			t.Errorf("log --card s1-1 has no %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "s1-2.w1") {
		t.Errorf("log --card s1-1 shows s1-2's work:\n%s", out)
	}
	if out := ta.ok("log --member m1"); !strings.Contains(out, "s1-2.w1: attempt 2") && !strings.Contains(out, "attempt 1 dealt to m1") {
		t.Errorf("log --member m1:\n%s", out)
	}
	ta.mu.Lock()
	ta.now = ta.now.Add(time.Hour)
	ta.mu.Unlock()
	if out := ta.ok("log --since 10m"); !strings.Contains(out, "LOG OK lines=0") {
		t.Errorf("log --since 10m an hour later:\n%s", out)
	}
	var j struct {
		Lines []struct {
			Kind, Card string
			Cards      []string
		} `json:"lines"`
	}
	ta.json("log --card s1-2", &j)
	if len(j.Lines) == 0 || j.Lines[0].Kind != "move" || len(j.Lines[0].Cards) != 2 || j.Lines[0].Cards[1] != "s1-2" {
		t.Errorf("log --json: %+v", j)
	}
	ta.ok("clear --confirm sprint")
	if out := ta.ok("log --at-epoch 0 --card s1-1"); !strings.Contains(out, "m1 finished attempt 1: FAILED") {
		t.Errorf("the old epoch's log after a clear:\n%s", out)
	}
	if out := ta.ok("log --card s1-1"); !strings.Contains(out, "LOG OK lines=0") {
		t.Errorf("the new epoch's log of s1-1:\n%s", out)
	}
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
	if now < 0 || brief < 0 || now > brief || !strings.Contains(out, "waits on your judgment: work came back failed") || !strings.Contains(out, "rework with a fix: nova-sprint rework") {
		t.Fatalf("a card in flight, what holds it first:\n%s", out)
	}
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
		if !strings.Contains(out, want) {
			t.Errorf("the story has no %q:\n%s", want, out)
		}
	}
	for _, not := range []string{"work came back ok", "the fix this attempt was given", "score"} {
		if strings.Contains(out, not) {
			t.Errorf("the story of a landed card says %q:\n%s", not, out)
		}
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
		if !strings.Contains(out, want) {
			t.Errorf("take has no %q:\n%s", want, out)
		}
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
	if len(j.Packets) != 1 || j.Packets[0].Fix != "check the nil slice too" || j.Packets[0].Base != "feature/empty" || !strings.HasPrefix(j.Packets[0].Brief, "handle the empty case") {
		t.Fatalf("take --json packets: %+v", j)
	}
	if out := ta.ok("queue --as m1"); !strings.Contains(out, "  fix (this attempt):\n    check the nil slice too") || !strings.Contains(out, "  base: feature/empty") {
		t.Fatalf("queue --as m1:\n%s", out)
	}
	// the packet says why the attempt exists, and card tells it per attempt
	assert.Contains(t, ta.ok("queue --as m1"), "  why this attempt exists:\n    attempt 1 failed: the tests went red")
	assert.Equal(t, 1, strings.Count(ta.ok("card s1-1"), "check the nil slice too"), "card says the fix once")
	assert.Contains(t, ta.ok("card s1-1"), "attempt 2 was given:\n  because:\n    attempt 1 failed: the tests went red\n  the fix:\n    check the nil slice too")
	ta.ok("finish --as m1 s1-1.w2@1 --head h2 --branch feature/empty-2 --base feature/empty --report 'handled; tests green'")
	ta.ok("ask")
	out = ta.ok("queue --as reader-a")
	for _, want := range []string{"PACKET s1-1.r2.reader-a attempt=2", "  work: attempt 2 by m1", "  head: h2", "  branch: feature/empty-2", "  base: feature/empty",
		"  report:\n    handled; tests green", "  report it: nova-sprint read --as reader-a (--ok | --broken) s1-1.r2.reader-a --epoch 0"} {
		if !strings.Contains(out, want) {
			t.Errorf("the reader's queue has no %q:\n%s", want, out)
		}
	}
}

// where does not show the merge table's since column: the machine keeps
// the cell, the view hides it, on a store made before the rule too. The
// table shows once a stream has a card in it (whereFixture has one queued).
func TestWhereHidesTheMergeTablesSince(t *testing.T) {
	t.Parallel()
	ta := whereFixture(t)
	out := ta.ok("where")
	block := tableOf(out, "merge")
	if block == "" {
		t.Fatalf("no merge table:\n%s", out)
	}
	if strings.Contains(block, "since") {
		t.Fatalf("where shows since:\n%s", out)
	}
}
