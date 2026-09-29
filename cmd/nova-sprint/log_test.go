package main

import (
	"strings"
	"testing"
	"time"
)

// log prints every line of the epoch in local time, filtered by card,
// member, stream and time; --json gives the lines; a clear keeps the old
// epoch's log readable with --at-epoch.
func TestLogPrintsTheEpochsLinesFiltered(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.a.loc = time.UTC
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 2 --brief 'handle the empty case'")
	ta.deal(2)
	ta.ok("take --as m1 s1-1.w1@1")
	ta.ok("finish --as m1 s1-1.w1@1 --failed --report 'the tests went red'")
	out := ta.ok("log --card s1-1")
	for _, want := range []string{
		"03:04:05  s1-1 added to s1 by coordinator",
		"    brief: handle the empty case",
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
	if strings.Contains(out, "s1-2") {
		t.Errorf("log --card s1-1 shows s1-2:\n%s", out)
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
		} `json:"lines"`
	}
	ta.json("log --card s1-2", &j)
	if len(j.Lines) == 0 || j.Lines[0].Kind != "move" || j.Lines[0].Card != "s1-2" {
		t.Errorf("log --json: %+v", j)
	}
	ta.ok("clear --confirm t-")
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
	ta.ok("add --stream s1 --count 1 --brief 'handle the empty case'")
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
