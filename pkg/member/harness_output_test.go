package member

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A child that exits non-zero whose harness printed output is reported with the last 40
// lines of that output on the finish's --harness, not the harness name: nova-sprint's
// finish --harness means the child's harness output, its last 40 lines, and the member
// was sending the harness name instead (a-member-sends-the-harness-output-not-its-name,
// correcting a-member-reports-a-childs-exit-and-harness). Red before the fix: the finish
// carried the harness name, never the child's printed lines.
func TestAChildsHarnessOutputCarriesItsLastFortyLinesNotItsName(t *testing.T) {
	t.Parallel()
	g := newWidthRig(2, Config{As: "m"})
	p := pk("c1")
	p.Gen = 2
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 2, &p)))
	_, err := g.tick(t)
	require.NoError(t, err)
	var printed []string
	for i := 1; i <= 50; i++ {
		printed = append(printed, "harness line "+strconv.Itoa(i))
	}
	g.r.child("c1").end(Result{Exit: 134, HarnessOutput: strings.Join(printed, "\n") + "\n", Report: "the harness aborted"})
	g.s.reset()
	_, err = g.tick(t)
	require.NoError(t, err)
	lines := g.s.lines("finish")
	require.Len(t, lines, 1)
	require.Contains(t, lines[0], "--exit 134", "the exit code is still named")
	want := strings.Join(printed[10:], "\n") // the last 40 of the 50 lines
	require.Contains(t, lines[0], "--harness "+want, "the finish names the child's output, not the harness")
	require.NotContains(t, lines[0], "harness line 10", "only the last 40 lines are carried")
}

// A child whose harness printed fewer than 40 lines carries them all: the tail is the
// whole output, never padded or dropped.
func TestAChildsHarnessOutputUnderFortyLinesIsCarriedWhole(t *testing.T) {
	t.Parallel()
	g := newWidthRig(2, Config{As: "m"})
	p := pk("c1")
	p.Gen = 2
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 2, &p)))
	_, err := g.tick(t)
	require.NoError(t, err)
	output := "one line\nanother line\nlast line"
	g.r.child("c1").end(Result{Exit: 137, HarnessOutput: output + "\n", Report: "the harness was killed"})
	g.s.reset()
	_, err = g.tick(t)
	require.NoError(t, err)
	lines := g.s.lines("finish")
	require.Len(t, lines, 1)
	require.Contains(t, lines[0], "--harness "+output, "under 40 lines are carried whole")
}
