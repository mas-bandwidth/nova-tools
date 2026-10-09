package store

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func landingAdmissionRig(t *testing.T) (*harness, LandAdmissionReq) {
	t.Helper()
	h := newHarness(t)
	h.setup(2)
	h.startMachine()
	h.through("s1-1", "s1-2")
	h.machine()
	h.setPrimary("s1-1", map[string]string{"head": "abcdef1"})
	c := h.snap().Work.Placed("s1-1")
	require.NotNil(t, c)
	r := LandAdmissionReq{Stream: "s1", Repo: "test/repo", Base: "main", Check: "test-gate",
		Pins: []LandAdmissionPin{{ID: c.ID, Attempt: c.F("attempt"), Head: c.F("head"), BriefSHA256: LandBriefSHA256(c.F("brief"))}}}
	return h, r
}

func TestLandAdmissionIsDurableAndCompletionContinuesWhilePaused(t *testing.T) {
	t.Parallel()
	h, r := landingAdmissionRig(t)
	step := LandAdmissionStep(r)
	step.CallerOp = "exact-batch"
	accepted := h.must(step)
	require.NotEmpty(t, accepted.Op)
	assert.Equal(t, 1, accepted.Notes, "a note forces the admission through Acquire and commit")
	raw, ok, err := h.m.Done(h.ctx, step.CallerOp)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Contains(t, raw, accepted.Op, "admission survives loss of the response")
	_, _, _, err = h.st.SetPaused(h.ctx, true)
	require.NoError(t, err)
	replay := h.must(step)
	assert.True(t, replay.Replay)
	assert.Equal(t, accepted.Op, replay.Op, "the exact accepted logical task remains in flight")
	// A fresh preparation has no old caller receipt and cannot start while paused.
	assert.NotEmpty(t, h.run(LandAdmissionStep(r)).Refused)
	h.must(MergeStep(sprint.MergeReq{Stream: "s1", Cards: []string{"s1-1"}}))
	h.machine()
	assert.Equal(t, sprint.Landed, h.snap().Work.Placed("s1-1").Col)
}

func TestLandAdmissionLosesThePauseRaceBeforeCommit(t *testing.T) {
	t.Parallel()
	h, r := landingAdmissionRig(t)
	b := &pausedTakeAcquire{Mem: h.m, verb: "land admission", entered: make(chan struct{}), release: make(chan struct{})}
	h.st.B = b
	type outcome struct {
		res Result
		err error
	}
	done := make(chan outcome, 1)
	go func() { res, err := h.st.Run(h.ctx, LandAdmissionStep(r)); done <- outcome{res, err} }()
	<-b.entered
	_, _, _, err := h.st.SetPaused(h.ctx, true)
	require.NoError(t, err)
	close(b.release)
	got := <-done
	require.NoError(t, got.err)
	require.NotEmpty(t, got.res.Refused)
	assert.Contains(t, got.res.Refused[0].Why, "PAUSED")
	_, committed, err := h.m.Done(h.ctx, got.res.Op)
	require.NoError(t, err)
	assert.False(t, committed, "a candidate operation id in refusal statistics is not admission")
	notes, _, err := h.m.NotesSince(h.ctx, "", 10000)
	require.NoError(t, err)
	for _, n := range notes {
		assert.NotEqual(t, "landing batch admitted", n.Type)
	}
}

func TestLandAdmissionBindsTheCutAndRefusesChangedInputs(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"head", "attempt", "brief"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			h, r := landingAdmissionRig(t)
			h.setCard(sprint.Work, "s1-1", map[string]string{field: "changed"})
			got := h.run(LandAdmissionStep(r))
			require.NotEmpty(t, got.Refused)
			assert.Empty(t, got.Op)
		})
	}
	h, r := landingAdmissionRig(t)
	step := LandAdmissionStep(r)
	step.CallerOp = "exact-batch"
	h.must(step)
	for _, changed := range []LandAdmissionReq{
		{Stream: r.Stream, Repo: "other/repo", Base: r.Base, Check: r.Check, Pins: r.Pins},
		{Stream: r.Stream, Repo: r.Repo, Base: "other-base", Check: r.Check, Pins: r.Pins},
		{Stream: r.Stream, Repo: r.Repo, Base: r.Base, Check: "other-gate", Pins: r.Pins},
	} {
		s := LandAdmissionStep(changed)
		s.CallerOp = step.CallerOp
		_, err := h.st.Run(h.ctx, s)
		assert.ErrorContains(t, err, "arguments", "an old receipt cannot admit another cut or gate")
	}
	h.stopMachine()
	assert.NotEmpty(t, h.run(LandAdmissionStep(r)).Refused)
}
