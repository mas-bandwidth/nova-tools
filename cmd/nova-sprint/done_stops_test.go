package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// playToDone plays rounds of the machine, the world and the coordinator until
// a tick finds the sprint done, with no failure drawn, and returns that tick's
// output. The merge queues the last landings for the next tick's pump, which
// drains them, and the same tick's done part finds the sprint done and stops
// the machine: the driver, which plays only while a machine runs, is not run
// after it.
func (ta *testApp) playToDone(from int) (int, string) {
	ta.t.Helper()
	for round := from; round < from+200; round++ {
		if out := ta.ok("tick"); strings.Contains(out, "the sprint is done") {
			return round + 1, out
		}
		ta.ok(fmt.Sprintf("play --seed %d --ticks 1 --every 1s --fail 0 --broken 0 --stuck 0 --cross 0 --batch 10 --take 20 --reads 20", round))
		ta.coordinate()
	}
	ta.t.Fatalf("not done: %s", ta.ok("where"))
	return 0, ""
}

// A sprint of 3 x 3 on the twin driven to done (errata 3 amendment 6): the tick
// that finds nothing open says "the sprint is done" to the coordinator, a
// HAPPENED line shown first in the inbox and carried on the notes stream, and
// stops the machine: STOPPED with the cause done, DONE in where and the view,
// STOPPED  9/9 100.0% done on the sprint line, no judgment open. A card added
// after leaves it STOPPED; start runs it again, lands the card and stops it
// again.
func TestADoneSprintStopsItsMachineAndTellsTheCoordinator(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1,m2")
	for _, s := range []string{"s1", "s2", "s3"} {
		ta.ok("add --stream " + s + " --count 3")
	}
	ta.ok("start")
	next, out := ta.playToDone(1)
	for _, want := range []string{"HAPPENED the sprint is done: 9 landed, 0 dropped, took ", " from the first start; the machine is STOPPED; " + sprint.DoneHint,
		"TICK OK state=STOPPED", "\nSTOPPED  9/9 100.0% done\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the done tick lacks %q:\n%s", want, out)
		}
	}
	if got := ta.viewLine(); got != "DONE" {
		t.Fatalf("the view: %q", got)
	}
	where := ta.ok("where")
	if !strings.Contains(where, "SPRINT TABLE\n\nDONE\n") {
		t.Fatalf("where's header:\n%s", where)
	}
	inbox := ta.ok("inbox")
	// the notes addressed to the coordinator come first, in time order: the
	// machine's "ready to merge" of each stream and its tick-end note, then the
	// sprint done
	lines := strings.Split(inbox, "\n")
	at := slices.IndexFunc(lines, func(l string) bool { return strings.Contains(l, "the sprint is done") })
	for i := 0; i < at; i++ {
		if !strings.HasPrefix(lines[i], "HAPPENED ") || !strings.Contains(lines[i], "for=coordinator") {
			t.Fatalf("a line before the sprint done is not a note to the coordinator: %q\n%s", lines[i], inbox)
		}
	}
	if at < 0 || !strings.HasPrefix(lines[at], "HAPPENED ") || !strings.Contains(lines[at], "the sprint is done  x1  for=coordinator  9 landed, 0 dropped, took ") ||
		lines[at+1] != "  "+sprint.DoneHint || strings.Contains(inbox, "JUDGMENT") || !strings.Contains(inbox, "\nmachine: DONE\n") {
		t.Fatalf("the inbox:\n%s", inbox)
	}
	var in struct{ Groups []sprint.Group }
	ta.json("inbox", &in)
	doneAt := slices.IndexFunc(in.Groups, func(g sprint.Group) bool { return g.Type == sprint.NSprintDone })
	if doneAt < 0 || in.Groups[doneAt].Kind != sprint.Happened || in.Groups[doneAt].To != "coordinator" {
		t.Fatalf("the inbox, for a program: %+v", in.Groups)
	}
	notes, _, err := ta.m.NotesSince(context.Background(), "", 100000)
	if err != nil {
		t.Fatal(err)
	}
	said := 0
	for _, n := range notes {
		if n.Type == sprint.NSprintDone {
			said++
			if n.Kind != sprint.Happened || n.To != "coordinator" || n.Hint != sprint.DoneHint {
				t.Fatalf("the notes stream: %+v", n)
			}
		}
	}
	if open, err := ta.m.OpenNotes(context.Background()); err != nil || len(open) != 0 || said != 1 {
		t.Fatalf("open judgments %+v (%v), the sprint done said %d times", open, err, said)
	}
	ta.clean()
	if out := ta.ok("tick"); !strings.Contains(out, "TICK OK state=STOPPED nothing done") {
		t.Fatalf("a tick after the done:\n%s", out)
	}

	// Work added: STOPPED, no longer done.
	out = ta.ok("add --stream s2 --count 1")
	if !strings.Contains(out, "\nSTOPPED  9/10 90.0%") || ta.viewLine() != "STOPPED" {
		t.Fatalf("an add after the done:\n%s\nview %q", out, ta.viewLine())
	}
	if out := ta.ok("tick"); !strings.Contains(out, "TICK OK state=STOPPED nothing done") {
		t.Fatalf("the machine ran after an add:\n%s", out)
	}
	// Started: it lands the card and stops again.
	ta.a.sleep(time.Minute)
	ta.ok("start")
	_, out = ta.playToDone(next)
	if !strings.Contains(out, "HAPPENED the sprint is done: 10 landed, 0 dropped, took ") || !strings.Contains(out, "\nSTOPPED  10/10 100.0% done\n") ||
		ta.viewLine() != "DONE" {
		t.Fatalf("the second done:\n%s", out)
	}
	ta.clean()
}
