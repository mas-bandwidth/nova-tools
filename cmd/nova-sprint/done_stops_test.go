package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
		require.Contains(t, out, want, "the done tick lacks %q", want)
	}
	require.Equal(t, "DONE", ta.viewLine(), "the view")
	where := ta.ok("where")
	require.Contains(t, where, "SPRINT TABLE\n\nDONE\n", "where's header")
	inbox := ta.ok("inbox")
	// the sprint done is the first thing the coordinator reads
	lines := strings.Split(inbox, "\n")
	require.True(t, strings.HasPrefix(lines[0], "HAPPENED "), "the inbox:\n%s", inbox)
	require.Contains(t, lines[0], "the sprint is done  x1  for=coordinator  9 landed, 0 dropped, took ", "the inbox:\n%s", inbox)
	require.Equal(t, "  "+sprint.DoneHint, lines[1], "the inbox:\n%s", inbox)
	require.NotContains(t, inbox, "JUDGMENT", "the inbox:\n%s", inbox)
	require.Contains(t, inbox, "\nmachine: DONE\n", "the inbox:\n%s", inbox)
	var in struct{ Groups []sprint.Group }
	ta.json("inbox", &in)
	require.NotEmpty(t, in.Groups, "the inbox, for a program: %+v", in.Groups)
	require.Equal(t, sprint.NSprintDone, in.Groups[0].Type, "the inbox, for a program: %+v", in.Groups)
	require.Equal(t, sprint.Happened, in.Groups[0].Kind, "the inbox, for a program: %+v", in.Groups)
	require.Equal(t, "coordinator", in.Groups[0].To, "the inbox, for a program: %+v", in.Groups)
	notes, _, err := ta.m.NotesSince(context.Background(), "", 100000)
	require.NoError(t, err)
	said := 0
	for _, n := range notes {
		if n.Type == sprint.NSprintDone {
			said++
			require.Equal(t, sprint.Happened, n.Kind, "the notes stream: %+v", n)
			require.Equal(t, "coordinator", n.To, "the notes stream: %+v", n)
			require.Equal(t, sprint.DoneHint, n.Hint, "the notes stream: %+v", n)
		}
	}
	open, err := ta.m.OpenNotes(context.Background())
	require.NoError(t, err, "open judgments %+v (%v), the sprint done said %d times", open, err, said)
	require.Empty(t, open, "open judgments %+v (%v), the sprint done said %d times", open, err, said)
	require.Equal(t, 1, said, "open judgments %+v (%v), the sprint done said %d times", open, err, said)
	ta.clean()
	require.Contains(t, ta.ok("tick"), "TICK OK state=STOPPED nothing done", "a tick after the done")

	// Work added: STOPPED, no longer done.
	out = ta.ok("add --stream s2 --count 1")
	require.Contains(t, out, "\nSTOPPED  9/10 90.0%", "an add after the done:\n%s\nview %q", out, ta.viewLine())
	require.Equal(t, "STOPPED", ta.viewLine(), "an add after the done:\n%s\nview %q", out, ta.viewLine())
	require.Contains(t, ta.ok("tick"), "TICK OK state=STOPPED nothing done", "the machine ran after an add")
	// Started: it lands the card and stops again.
	ta.a.sleep(time.Minute)
	ta.ok("start")
	_, out = ta.playToDone(next)
	require.Contains(t, out, "HAPPENED the sprint is done: 10 landed, 0 dropped, took ", "the second done:\n%s", out)
	require.Contains(t, out, "\nSTOPPED  10/10 100.0% done\n", "the second done:\n%s", out)
	require.Equal(t, "DONE", ta.viewLine(), "the second done:\n%s", out)
	ta.clean()
}
