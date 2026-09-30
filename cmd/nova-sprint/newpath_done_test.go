package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// TestNewPathDoneStopsTheMachine (the 4806 read, M4; errata 3 amendment 6):
// the tick that finds every card landed writes "the sprint is done" as a notice
// addressed to the coordinator, not a judgment, and stops the machine in the
// same step; the inbox then holds the notice and no judgment. A start with
// nothing added says it again and stops at its first tick.
func TestNewPathDoneStopsTheMachine(t *testing.T) {
	t.Parallel()
	na := newNPApp(t)
	for _, l := range []string{"init", "reader add ra rb", "fleet up m1", "fleet beat m1", "add --stream s1 --count 2", "start", "tick", "tick"} {
		na.ok(l)
	}
	var q struct {
		Cards []struct {
			ID  string `json:"id"`
			Gen int    `json:"gen"`
		} `json:"cards"`
	}
	if err := json.Unmarshal([]byte(na.ok("queue --as m1 --json")), &q); err != nil {
		t.Fatal(err)
	}
	var words []string
	for _, c := range q.Cards {
		words = append(words, c.ID+"@"+strconv.Itoa(c.Gen))
	}
	na.ok("take --as m1 " + strings.Join(words, " "))
	na.ok("finish --as m1 " + strings.Join(words, " "))
	na.ok("tick")
	for _, r := range []string{"ra", "rb"} {
		ids := "s1-1.r1." + r + " s1-2.r1." + r
		na.ok("read --as " + r + " --begin " + ids)
		na.ok("read --as " + r + " --ok " + ids)
	}
	na.ok("tick")
	na.ok("merge --stream s1 --batch 100")
	running := func() bool {
		var r struct{ Running bool }
		if err := json.Unmarshal([]byte(na.ok("tick --json")), &r); err != nil {
			t.Fatal(err)
		}
		return r.Running
	}
	running() // the tick that finds it done, and stops the machine
	if running() {
		t.Fatalf("the machine runs after the sprint is done:\n%s", na.ok("where"))
	}
	in := na.ok("inbox")
	if !strings.Contains(in, "HAPPENED") || !strings.Contains(in, "the sprint is done: 2 landed, 0 dropped") || strings.Contains(in, "JUDGMENT") {
		t.Fatalf("the inbox after done:\n%s", in)
	}
	if log := na.ok("log"); strings.Count(log, `"type":"the sprint is done"`) != 1 || !strings.Contains(log, `"kind":"happened"`) {
		t.Fatalf("the log after done:\n%s", log)
	}
	// a start with nothing added: done again, stopped again
	na.ok("start")
	running()
	if running() {
		t.Fatalf("a start of a done sprint left it running")
	}
	if log := na.ok("log"); strings.Count(log, `"type":"the sprint is done"`) != 2 {
		t.Fatalf("a start of a done sprint did not say it again")
	}
}
