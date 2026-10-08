package store

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStopReturnKeepsOwnedWorkReadyAndFencesOldFinish(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	s := h.snap()
	pr := s.Work.Card("s1-1")
	wc := s.Fleet.Card(pr.F("work"))
	require.NotNil(t, wc)
	h.must(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}}))
	old := wc.Int("gen")
	row, branch := wc.Row, wc.F("branch")
	_, _, _, err := h.st.StopUntil(h.ctx, "owner stopped children", h.now.Add(time.Hour))
	require.NoError(t, err)
	assert.NotEmpty(t, h.run(TakeStep(sprint.TakeReq{As: row})).Refused, "STOP fences new takes")
	assert.NotEmpty(t, h.run(FinishStep(sprint.FinishReq{As: row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: old}})).Refused, "STOP fences a report before cancellation is acknowledged")
	_, _, _, err = h.st.SetMachine(h.ctx, true)
	require.ErrorContains(t, err, row+":"+wc.ID+"@1", "start identifies the owned job that has not stopped")
	r := sprint.StopReturnReq{As: row, IDs: []string{wc.ID}, Gens: map[string]int{wc.ID: old}, Reason: "owned child stopped"}
	h.must(StopReturnStep(r))
	back := h.snap().Fleet.Card(wc.ID)
	require.NotNil(t, back)
	assert.Equal(t, row, back.Row)
	assert.Equal(t, sprint.Ready, back.Col)
	assert.Equal(t, old+1, back.Int("gen"))
	assert.Equal(t, branch, back.F("branch"))
	assert.Equal(t, wc.ID, h.snap().Work.Card(pr.ID).F("work"), "same attempt and branch resume")
	assert.Empty(t, h.must(StopReturnStep(r)).Moved, "ack replay is idempotent")
	h.startMachine()
	late := h.run(FinishStep(sprint.FinishReq{As: row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: old}, Head: "0123456789012345678901234567890123456789"}))
	assert.NotEmpty(t, late.Refused, "old child cannot finish the returned card")
	h.clean("returned work")
}

func TestStopReturnReasksSameReadAndFencesLateVerdict(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.asked1(1)
	rc := h.snap().Readers.Of("s1-1")[0]
	h.must(ReadStep(sprint.ReadReq{As: rc.Row, Begin: true, Sel: sprint.Sel{IDs: []string{rc.ID}}}))
	old := max(rc.Int("gen"), 1)
	_, _, _, err := h.st.StopUntil(h.ctx, "owner stopped children", h.now.Add(time.Hour))
	require.NoError(t, err)
	assert.NotEmpty(t, h.run(ReadStep(sprint.ReadReq{As: rc.Row, Begin: true, Sel: sprint.Sel{IDs: []string{rc.ID}}})).Refused)
	assert.NotEmpty(t, h.run(ReadStep(sprint.ReadReq{As: rc.Row, Verdict: "ok", Sel: sprint.Sel{IDs: []string{rc.ID}}})).Refused, "STOP fences a late read before cancellation is acknowledged")
	_, _, _, err = h.st.SetMachine(h.ctx, true)
	require.ErrorContains(t, err, rc.Row+":"+rc.ID+"@1", "start identifies the reader that has not stopped")
	h.must(StopReturnStep(sprint.StopReturnReq{As: rc.Row, IDs: []string{rc.ID}, Gens: map[string]int{rc.ID: old}, Reason: "reader child stopped"}))
	back := h.snap().Readers.Card(rc.ID)
	require.NotNil(t, back)
	assert.Equal(t, sprint.Asked, back.Col)
	assert.Equal(t, rc.Row, back.Row)
	assert.Equal(t, old+1, back.Int("gen"))
	late := h.run(ReadStep(sprint.ReadReq{As: rc.Row, Verdict: "ok", Sel: sprint.Sel{IDs: []string{rc.ID}}, Gens: map[string]int{rc.ID: old}}))
	assert.NotEmpty(t, late.Refused, "old reader cannot report after STOP")
	assert.Equal(t, sprint.Asked, h.snap().Readers.Card(rc.ID).Col)
	h.startMachine()
	h.must(ReadStep(sprint.ReadReq{As: rc.Row, Begin: true, Sel: sprint.Sel{IDs: []string{rc.ID}}}))
	assert.Equal(t, sprint.Reading, h.snap().Readers.Card(rc.ID).Col, "the returned read can begin from its same-owner queue")
	assert.NotEmpty(t, h.run(ReadStep(sprint.ReadReq{As: rc.Row, Verdict: "ok", Sel: sprint.Sel{IDs: []string{rc.ID}}})).Refused, "a post-STOP verdict must name its generation")
	h.clean("returned read")
}
