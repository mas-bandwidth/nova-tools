package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// H7: through the command: init names the coordinator, where --json carries it
// and the where frame does not, release is refused for another actor and
// without a reason, and card of a sentinel lists what it needs and who needs it.
func TestReleaseIsTheCoordinators(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1 --coordinator lead")
	var w whereView
	ta.json("where", &w)
	require.Equal(t, "lead", w.Coordinator, "where --json: %+v", w)
	require.NotContains(t, ta.ok("where"), "coordinator:", "where shows the coordinator line")
	ta.ok("add --stream s1 --count 1 --actor lead")
	out := ta.ok("add --stream s1 --sentinel stop --actor lead")
	require.Contains(t, out, "MOVED sentinel stop -> waiting stream=s1", "add --sentinel")
	ta.ok("add --stream s2 b --needs stop --actor lead")
	out = ta.ok("card --fields stop")
	require.Contains(t, out, "NEEDS s1-1 ready\n", "card stop")
	require.Contains(t, out, "NEEDED-BY b\n", "card stop")
	ta.deal(1)
	ta.ok("take --as m1 s1-1.w1@1")
	ta.ok("finish --as m1 s1-1.w1@1")
	ta.ok("ask --actor lead")
	ta.ok("read --as reader-a --ok s1-1.r1.reader-a")
	ta.ok("read --as reader-b --ok s1-1.r1.reader-b")
	ta.ok("accept s1-1 --actor lead")
	ta.ok("merge --stream s1")
	g := ta.group(sprint.NSentinelReached, "s1")
	code, _, errs := ta.do("release stop --reason 'looked' --actor someone")
	require.Equal(t, 2, code, "another actor: %d %s", code, errs)
	require.Contains(t, errs, "release is the coordinator's alone: lead, not someone", "another actor: %d %s", code, errs)
	code, _, errs = ta.do("release stop --actor lead")
	require.Equal(t, 1, code, "no reason: %d %s", code, errs)
	require.Contains(t, errs, "release wants --reason", "no reason: %d %s", code, errs)
	out = ta.ok("release stop --reason 'the layer is read and green' --actor lead --answers " + g.ID)
	require.Contains(t, out, "sentinel stop waiting -> landed (released by lead); 1 cards are now ready", "release")
	require.Contains(t, out, "b waiting -> ready", "release")
	ta.clean()
}
