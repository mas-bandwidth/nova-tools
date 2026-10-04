package store

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// Dev is behind (promotion.go): landings on the sprint branch since the last promotion
// reach 25, or the oldest waits 30 minutes, whichever first; the judgment names the count,
// the branch and the last promotion, and closes when promoted records a merge sha. Injected
// clock, the mem twin.
func TestTheTickRaisesDevBehindAtTwentyFiveLandingsOrThirtyMinutes(t *testing.T) {
	t.Parallel()
	ids := func(from, to int) []string {
		var out []string
		for i := from; i <= to; i++ {
			out = append(out, "s1-"+strconv.Itoa(i))
		}
		return out
	}
	t.Run("thirty minutes", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.setup(2)
		h.startMachine()
		h.landThrough("s1", "s1-1")
		h.machine()
		assert.Empty(t, h.openOf(sprint.NDevBehind), "one landing, just now")
		h.tick(sprint.PromoteAge)
		h.machine()
		open := h.openOf(sprint.NDevBehind)
		require.Len(t, open, 1)
		assert.Equal(t, "dev is behind: 1 cards landed on the sprint branch since no promotion recorded; promote: merge origin/dev into the sprint branch, open the PR to dev, run the functional tier, queue it; then: nova-sprint promoted --sha <merge sha>", open[0].Note.What)
		assert.Equal(t, []string{"promoted", "wait 30m"}, open[0].Note.Decisions)
		cmds := h.commandsOf(sprint.NDevBehind)
		require.Len(t, cmds, 2)
		assert.Equal(t, "nova-sprint promoted --sha '<merge sha>' --answers "+open[0].Note.ID, cmds[0].Lines[0])
		refused := h.run(PromotedStep(sprint.PromotedReq{Sha: "not-a-sha", Who: "tester"})).Refused
		require.Len(t, refused, 1)
		assert.Contains(t, refused[0].Why, "--sha wants the merge commit's sha")
		assert.Contains(t, h.run(PromotedStep(sprint.PromotedReq{Sha: "0123abc", Who: "m1"})).Refused[0].Why, "coordinator")
		res := h.must(PromotedStep(sprint.PromotedReq{Sha: "0123ABCDEF", Who: "tester"}))
		assert.Contains(t, res.Moved[0], "promoted the sprint branch into dev at ")
		assert.Contains(t, res.Moved[0], "(0123abcdef): 1 cards landed since the last promotion")
		assert.Empty(t, h.openOf(sprint.NDevBehind), "the promotion closes it")
		h.machine()
		assert.Empty(t, h.openOf(sprint.NDevBehind), "and the count starts again")
		at, sha, ok := sprint.Promotion(h.snap())
		require.True(t, ok)
		assert.Equal(t, "0123abcdef", sha)
		assert.False(t, at.IsZero())
		h.clean("promoted")
	})
	t.Run("twenty-five landings", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.setup(sprint.PromoteCards)
		h.landThrough("s1", ids(1, sprint.PromoteCards-1)...)
		_, behind := sprint.DevBehind(h.snap())
		assert.False(t, behind, "under the count")
		h.tick(time.Minute)
		h.landThrough("s1", "s1-"+strconv.Itoa(sprint.PromoteCards))
		h.startMachine()
		h.machine()
		open := h.openOf(sprint.NDevBehind)
		require.Len(t, open, 1, "at the count")
		assert.Contains(t, open[0].Note.What, "dev is behind: 25 cards landed on the sprint branch since no promotion recorded")
		h.machine()
		assert.Equal(t, 1, h.written(sprint.NDevBehind), "once while it holds")
	})
}
