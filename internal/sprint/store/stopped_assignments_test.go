package store

import (
	"context"
	"errors"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func stoppedAssignmentRig(t *testing.T) (*harness, *sprint.Card) {
	t.Helper()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.must(DealStep(sprint.DealReq{}))
	h.must(TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 1}}))
	c := h.snap().Fleet.Cell("m1", sprint.Working)[0]
	h.stopMachine()
	// A legacy STOP record has no captured debt; canonical leases still matter.
	m, _, err := h.st.Machine(h.ctx)
	require.NoError(t, err)
	m.StopDebt = nil
	require.NoError(t, h.st.putMachine(h.ctx, m))
	return h, c
}

func TestStoppedTickJournalsLegacyAssignmentsWithoutMovingWork(t *testing.T) {
	t.Parallel()
	h, c := stoppedAssignmentRig(t)
	before, _, err := h.st.Machine(h.ctx)
	require.NoError(t, err)
	_, from, err := h.m.Tails(h.ctx)
	require.NoError(t, err)
	res := h.machine()
	assert.Equal(t, Stopped, res.State)
	assert.Equal(t, 1, res.TickEnd)
	assert.Equal(t, 1, h.written(sprint.NStoppedAssignments+": m1"))
	// A new Store over the same journal does not repeat an unobserved incident.
	restarted := *h.st
	restarted.tw = nil
	h.st = &restarted
	h.machine()
	assert.Equal(t, 1, h.written(sprint.NStoppedAssignments+": m1"))
	got := h.snap().Fleet.Card(c.ID)
	assert.Equal(t, sprint.Working, got.Col)
	assert.Equal(t, c.Int("gen"), got.Int("gen"))
	after, _, err := h.st.Machine(h.ctx)
	require.NoError(t, err)
	assert.Equal(t, before, after, "the observer cannot change STOP, epoch, run sequence or cancellation debt")
	view, err := h.st.Inbox(h.ctx, 0, 0, 100)
	require.NoError(t, err)
	found := false
	for _, g := range view.Groups {
		if g.Type == sprint.NStoppedAssignments+": m1" {
			found = true
			assert.Equal(t, h.st.Actor, g.To)
		}
	}
	assert.True(t, found, "the committed incident is available to inbox --push, before any consumer observation")
	woke, err := h.st.WaitTickEnd(h.ctx, from, 0)
	require.NoError(t, err)
	assert.True(t, woke)
	h.clean("STOP observation")
	// Only a supported owner acknowledgement returns the lease; the observer
	// reports that condition clearing without claiming native execution ended.
	h.must(StopReturnStep(sprint.StopReturnReq{As: c.Row, IDs: []string{c.ID},
		Gens: map[string]int{c.ID: c.Int("gen")}, Reason: "owner acknowledges cancellation"}))
	h.machine()
	h.machine()
	assert.Equal(t, 1, h.written(sprint.NStoppedAssignmentsCleared+": m1"))
	assert.Equal(t, sprint.Ready, h.snap().Fleet.Card(c.ID).Col)
	assert.Equal(t, c.Int("gen")+1, h.snap().Fleet.Card(c.ID).Int("gen"))
	h.clean("observed supported return")
}

type stoppedAlertLostCommit struct {
	*Mem
	before, once bool
}

func (b *stoppedAlertLostCommit) Release(ctx context.Context, op OpRecord, commit bool) error {
	if op.Verb != "tick stopped assignments" || b.once {
		return b.Mem.Release(ctx, op, commit)
	}
	b.once = true
	if !b.before {
		if err := b.Mem.Release(ctx, op, commit); err != nil {
			return err
		}
	}
	return errors.New("notification commit reply lost")
}

func TestStoppedNotificationRetryRepairsItsJournalExactlyOnce(t *testing.T) {
	t.Parallel()
	for _, before := range []bool{true, false} {
		name := "reply lost after commit"
		if before {
			name = "interrupted before commit"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h, c := stoppedAssignmentRig(t)
			h.st.B = &stoppedAlertLostCommit{Mem: h.m, before: before}
			var res TickResult
			firstErr := h.st.stoppedAssignments(h.ctx, &res)
			if before {
				require.Error(t, firstErr)
			}
			h.st.B = h.m
			require.NoError(t, h.st.stoppedAssignments(h.ctx, &res))
			require.NoError(t, h.st.stoppedAssignments(h.ctx, &res))
			assert.Equal(t, 1, h.written(sprint.NStoppedAssignments+": m1"))
			assert.Equal(t, sprint.Working, h.snap().Fleet.Card(c.ID).Col)
			assert.Equal(t, c.Int("gen"), h.snap().Fleet.Card(c.ID).Int("gen"))
			h.clean("repaired notification")
		})
	}
}

func TestAnUnaddressedSTOPNotificationRemainsRetryableOnTheStore(t *testing.T) {
	t.Parallel()
	h, _ := stoppedAssignmentRig(t)
	require.NoError(t, h.m.SetCoordinator(h.ctx, ""))
	var res TickResult
	require.ErrorContains(t, h.st.stoppedAssignments(h.ctx, &res), "canonical coordinator")
	assert.Zero(t, h.written(sprint.NStoppedAssignments+": m1"))
	require.NoError(t, h.m.SetCoordinator(h.ctx, h.st.Actor))
	require.NoError(t, h.st.stoppedAssignments(h.ctx, &res))
	assert.Equal(t, 1, h.written(sprint.NStoppedAssignments+": m1"))
}
