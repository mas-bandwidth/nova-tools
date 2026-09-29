package main

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// H2: two different readers' ok is a judgment, ready to accept, written by
// the read that completes the pair; it stays in the inbox whatever the
// cursor; accept over its group with --group and --expect closes it.
func TestReadyToAcceptIsAJudgmentAcceptedByGroup(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 3")
	ta.ok("start --limit 3")
	ta.ok("take --as m1 --limit 3")
	ta.ok("finish --as m1 s1-1.w1@1 s1-2.w1@1 s1-3.w1@1")
	ta.ok("ask")
	for _, id := range []string{"s1-1", "s1-2"} {
		ta.ok("read --as reader-a --ok " + id + ".r1.reader-a")
		ta.ok("read --as reader-b --ok " + id + ".r1.reader-b")
	}
	ta.ok("read --as reader-a --ok s1-3.r1.reader-a") // one reader: not ready
	ta.ok("inbox --read")
	g := ta.group(sprint.NReadyToAccept, "s1")
	if g.Kind != sprint.Judgment || g.Size != 2 || strings.Join(g.Primaries, ",") != "s1-1,s1-2" {
		t.Fatalf("the ready group after the cursor moved: %+v", g)
	}
	var line string
	for _, c := range g.Commands {
		if c.Decision == "accept" {
			line = c.Lines[0]
		}
	}
	want := "nova-sprint accept --group " + g.ID + " --expect 2 --answers " + strings.Join(g.Notes, ",")
	if line != want {
		t.Fatalf("the accept command: %q, want %q (%+v)", line, want, g.Commands)
	}
	for _, d := range []string{"rework", "drop"} {
		found := false
		for _, c := range g.Commands {
			found = found || c.Decision == d
		}
		if !found {
			t.Fatalf("no %s decision: %+v", d, g.Commands)
		}
	}
	out := ta.ok(strings.TrimPrefix(line, "nova-sprint "))
	if !strings.Contains(out, "GROUP "+g.ID+" acted on 2, the group had 2 when printed") || !strings.Contains(out, "s1-1 review -> merging") || !strings.Contains(out, "s1-2 review -> merging") {
		t.Fatalf("accept by group: %s", out)
	}
	for _, x := range ta.inboxGroups() {
		if x.Type == sprint.NReadyToAccept && x.Kind == sprint.Judgment {
			t.Fatalf("accept left the judgment open: %+v", x)
		}
	}
	ta.clean()
}
