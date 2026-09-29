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
		"03:04:05  s1-1 added to s1 by coordinator, score",
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
