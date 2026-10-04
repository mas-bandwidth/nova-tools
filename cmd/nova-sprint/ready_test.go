package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// H2: two different readers' ok is a judgment, ready to accept, written by
// the read that completes the pair; it stays in the inbox whatever the
// cursor; accept over its group with --group and --expect closes it.
func TestReadyToAcceptIsAJudgmentAcceptedByGroup(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 3 --brief-file " + proBriefFile(t))
	ta.deal(3)
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
	require.Equal(t, sprint.Judgment, g.Kind, "the ready group after the cursor moved: %+v", g)
	require.Equal(t, 2, g.Size, "the ready group after the cursor moved: %+v", g)
	require.Equal(t, []string{"s1-1", "s1-2"}, g.Primaries, "the ready group after the cursor moved: %+v", g)
	var line string
	for _, c := range g.Commands {
		if c.Decision == "accept" {
			line = c.Lines[0]
		}
	}
	want := "nova-sprint accept --group " + g.ID + " --expect 2 --answers " + strings.Join(g.Notes, ",")
	require.Equal(t, want, line, "the accept command (%+v)", g.Commands)
	for _, d := range []string{"rework", "drop"} {
		found := false
		for _, c := range g.Commands {
			found = found || c.Decision == d
		}
		require.True(t, found, "no %s decision: %+v", d, g.Commands)
	}
	out := ta.ok(strings.TrimPrefix(line, "nova-sprint "))
	require.Contains(t, out, "GROUP "+g.ID+" acted on 2, the group had 2 when printed", "accept by group")
	require.Contains(t, out, "s1-1 review -> merging", "accept by group")
	require.Contains(t, out, "s1-2 review -> merging", "accept by group")
	for _, x := range ta.inboxGroups() {
		require.False(t, x.Type == sprint.NReadyToAccept && x.Kind == sprint.Judgment, "accept left the judgment open: %+v", x)
	}
	ta.clean()
}
