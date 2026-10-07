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
		// --answers is held as every answer is: one naming no open judgment refuses the whole step
		bogus := "nope~" + strconv.FormatUint(sprint.IDEpoch(open[0].Note.ID), 10)
		refused = h.run(PromotedStep(sprint.PromotedReq{Sha: "0123abc", Answers: []string{bogus}, Who: "tester"})).Refused
		require.Len(t, refused, 1)
		assert.Contains(t, refused[0].Why, "the whole step is refused and nothing was changed")
		assert.Len(t, h.openOf(sprint.NDevBehind), 1, "nothing was written")
		_, _, ok := sprint.Promotion(h.snap())
		assert.False(t, ok, "no promotion recorded")
		res := h.must(PromotedStep(sprint.PromotedReq{Sha: "0123ABCDEF", Answers: []string{open[0].Note.ID}, Who: "tester"}))
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

// promoteBeforeTheDeal makes the one world event of a tick: a part of the work
// table's update, after the pump's drain and before the deal, records the
// promotion that answers the open "dev is behind". The machine runs, so the
// promotion's properties queue for the next tick's pump while its close of the
// judgment applies at once. The part runs once.
func (h *harness) promoteBeforeTheDeal(answers string) {
	h.t.Helper()
	promoted := false
	work := sprint.TickTables[0]
	var parts []sprint.TickPartDef
	for _, p := range work.Parts {
		if p.Name == "deal" {
			parts = append(parts, sprint.TickPartDef{Name: "world", Fn: func(s *sprint.Snapshot, _ sprint.TickReq) (sprint.Plan, int) {
				if promoted {
					return sprint.Plan{}, 0
				}
				promoted = true
				h.must(PromotedStep(sprint.PromotedReq{Sha: "0123abc", Answers: []string{answers}, Who: "tester"}))
				return sprint.Plan{Notes: []sprint.Note{{Kind: sprint.Happened, Type: "world", At: s.Now}}}, 0
			}})
		}
		parts = append(parts, p)
	}
	work.Parts = parts
	h.st.Updates = []sprint.TableUpdate{work, sprint.TickTables[1], sprint.TickTables[2], sprint.TickTables[3]}
}

// A promotion recorded while the machine runs, after the pump's drain, is the
// last promotion to the tick's deal: the deal judges dev against the queued
// promotion and raises no second "dev is behind" on the landings it covered,
// which the next tick would close again under the coordinator answering it (the
// dirty-tick drive of 2026-10-05: "dev is behind: promoted refused", no open
// judgment). Red without the deal reading the queued promotion
// (withQueuedPromotion).
func TestAPromotionQueuedAfterTheDrainRaisesNoSecondDevBehind(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.startMachine()
	h.landThrough("s1", "s1-1")
	h.machine()
	h.tick(sprint.PromoteAge)
	h.machine()
	open := h.openOf(sprint.NDevBehind)
	require.Len(t, open, 1)
	h.promoteBeforeTheDeal(open[0].Note.ID)
	h.machine()
	assert.Empty(t, h.openOf(sprint.NDevBehind), "the deal raised dev is behind again on the landing the queued promotion covers")
	assert.Equal(t, 1, h.written(sprint.NDevBehind), "one judgment, answered once")
	h.machine()
	at, sha, ok := sprint.Promotion(h.snap())
	require.True(t, ok, "the next pump wrote the queued promotion")
	assert.Equal(t, "0123abc", sha)
	assert.False(t, at.IsZero())
	assert.Empty(t, h.openOf(sprint.NDevBehind))
	assert.Equal(t, 1, h.written(sprint.NDevBehind))
}

// A step on a STOPPED machine drains the queue the machine left before it
// runs; the drain reads and writes through the twin the step holds, so a step
// loading no table (the stop's note, the tick-end note's) reads no table whole
// for it (the dirty-tick drive of 2026-10-05: "/tick end read 1 whole" on the
// tick the sprint was done). Red without the drain given the held twin.
func TestADrainBeforeAStepReadsThroughTheTwinTheStepHolds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.startMachine()
	h.landThrough("s1", "s1-1")
	h.machine()
	h.machine()
	// a promotion while the machine runs: its properties queue for the pump
	h.must(PromotedStep(sprint.PromotedReq{Sha: "0123abc", Who: "tester"}))
	require.Positive(t, h.queueLen())
	before := h.st.stats().reads.Load()
	// the stop's note is a step loading no table: it drains the queue first
	h.stopMachine()
	assert.Zero(t, h.queueLen(), "the stop's step drained the queue first")
	assert.Zero(t, h.st.stats().reads.Load()-before, "the drain read a table whole: the step's twin was not lent to it")
	_, sha, ok := sprint.Promotion(h.snap())
	require.True(t, ok)
	assert.Equal(t, "0123abc", sha)
}
