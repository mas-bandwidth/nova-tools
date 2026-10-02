package store

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/require"
)

// Judgments are the coordinator's: ack, wait and every --answers by another
// actor are refused, naming the coordinator; workers and readers keep their
// own verbs.
func TestOnlyTheCoordinatorAnswersAJudgment(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	require.NoError(t, h.m.SetCoordinator(h.ctx, "the-coordinator"))
	h.setup(1)
	h.must(CIStep(sprint.CIReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Red: true, Run: "r1"}))
	open := h.openOf(sprint.NCIRed)
	require.Len(t, open, 1, "ci red: %d", len(open))
	id := open[0].Note.ID
	res := h.run(AckStep(sprint.AckReq{Notes: []string{id}, Reason: "looked", Who: "m1"}))
	require.Len(t, res.Refused, 1, "ack by a worker: %+v", res)
	require.Contains(t, res.Refused[0].Why, "the-coordinator", "ack by a worker: %+v", res)
	require.Len(t, h.openOf(sprint.NCIRed), 1, "ack by a worker: %+v", res)
	res = h.run(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Reason: "x", Answers: []string{id}, Who: "reader-a"}))
	require.Len(t, res.Refused, 1, "drop --answers by a reader: %+v", res)
	require.Empty(t, res.Moved, "drop --answers by a reader: %+v", res)
	require.Contains(t, res.Refused[0].Why, "the-coordinator", "drop --answers by a reader: %+v", res)
	require.Equal(t, sprint.Ready, h.state("s1-1"), "drop --answers by a reader: %+v", res)
	st := *h.st
	st.Actor = "m1"
	_, _, err := st.Wait(h.ctx, id, h.now.Add(10*60*1e9))
	require.ErrorContains(t, err, "the-coordinator", "wait by a worker: %v", err)
	// the coordinator answers it
	h.must(AckStep(sprint.AckReq{Notes: []string{id}, Reason: "a flaky runner", Who: "the-coordinator"}))
	require.Empty(t, h.openOf(sprint.NCIRed), "the coordinator's ack did not close it")
}
