package member

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A child that exits 134 is reported with its real exit code and the harness that ran
// it: the store tells an environment death from a no-result, so it never spends a redeal
// on a card whose machine aborted (nova-sprint #129, finish --exit/--harness; the
// member's a-member-reports-a-childs-exit-and-harness). Red before the finish carries
// --exit and --harness: the child's 134 was a bare no-result.
func TestAChildThatExits134IsReportedWithItsExitAndHarness(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "m", Width: 2})
	p := pk("c1")
	p.Gen = 2
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 2, &p)))
	_, err := g.tick(t) // restart: the child is ours now
	require.NoError(t, err)
	g.r.child("c1").end(Result{Exit: 134, Harness: "opencode", Report: "the harness aborted"})
	g.s.reset()
	_, err = g.tick(t)
	require.NoError(t, err)
	want := "finish --as m c1@2 --report no result: no RESULT.md shape; the harness aborted --failed --exit 134 --harness opencode --epoch 7"
	require.Equal(t, []string{want}, g.s.lines("finish"), "the 134 finish names the exit and the harness")
}

// A clean child (exit 0) is reported as it was: no --exit, no --harness, so a finish
// that needs no environment death named is unchanged.
func TestAChildThatExitsZeroCarriesNoExitFlag(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "m", Width: 2})
	g.m.pusher = &fakePusher{def: Push{Sha: fullSha}}
	g.s.set("queue", 0, queueJSON(t, 7, ready("c1")))
	p := pk("c1")
	p.Gen = 3
	g.s.set("take", 0, takeJSON(t, p))
	_, err := g.tick(t)
	require.NoError(t, err)
	g.r.child("c1").end(Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Exit: 0, Harness: "opencode", Report: "# Result\n\nlanded the thing"})
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 3, &p)))
	g.s.reset()
	_, err = g.tick(t)
	require.NoError(t, err)
	lines := g.s.lines("finish")
	require.Len(t, lines, 1)
	require.NotContains(t, lines[0], "--exit", "a clean finish carries no exit code")
	require.NotContains(t, lines[0], "--harness", "a clean finish carries no harness")
}

// A member ahead of its server sends the finish with --exit/--harness, and when the
// server refuses them as unknown flags (a server from before nova-sprint #129) sends the
// same finish again without them: the member refuses no flag its server lacks.
func TestAMemberAheadOfItsServerSendsNoExitFlag(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "m", Width: 2})
	g.s.set("finish", 2, "nova-sprint finish: unknown flag --exit; run: nova-sprint help finish")
	p := pk("c1")
	p.Gen = 2
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 2, &p)))
	_, err := g.tick(t)
	require.NoError(t, err)
	g.r.child("c1").end(Result{Exit: 137, Harness: "opencode", Report: "the harness was killed"})
	g.s.reset()
	_, _ = g.tick(t) // the fake refuses the stripped finish too; the calls are what the test reads
	lines := g.s.lines("finish")
	require.Len(t, lines, 2, "the finish with the flags, then the same without them: %v", lines)
	require.Contains(t, lines[0], "--exit 137")
	require.Contains(t, lines[0], "--harness opencode")
	require.NotContains(t, lines[1], "--exit")
	require.NotContains(t, lines[1], "--harness")
}
