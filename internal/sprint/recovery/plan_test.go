package recovery

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func failed() Input {
	return Input{Epoch: 15, Cohort: "flash50", Limits: Limits{Tokens: 20000, DeadlineSeconds: 600, MaxCostMicros: 100000}, Attempts: []Attempt{{ID: "flash50-baseline-check-read", Stream: "flash50", WorkID: "flash50-baseline-check-read.w2", Revision: 10, Generation: 1, Number: 2, State: "review", Failed: true, Tier: "flash", SoundBrief: true, Fix: "Use full40 in the literal step result", ArtifactsInspected: true, Failure: "step 3 not-done: the step line's commit 531bf424044dc is no sha (40 or 12 hex, or -): 531bf424044dc"}}}
}

func TestFailureClassificationAndRetry(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name        string
		edit        func(*Attempt)
		class, tier string
		safe        bool
	}{
		{"repeated mechanical", func(a *Attempt) { a.Number = 3 }, "mechanical", "flash", true},
		{"no targeted capability retry", func(a *Attempt) {
			a.Failure = "semantic failure"
			a.CapabilityFinding = "independent wrong-value probe"
		}, "capability", "flash", false},
		{"diagnosed targeted capability", func(a *Attempt) {
			a.Failure = "semantic failure"
			a.CapabilityFinding = "independent wrong-value probe"
			a.TargetedFlashRetry = true
		}, "capability", "pro", true},
		{"existing pro is not automatic escalation", func(a *Attempt) {
			a.Failure = "semantic failure"
			a.CapabilityFinding = "independent finding"
			a.Tier = "pro"
		}, "capability", "pro", false},
		{"model pin", func(a *Attempt) {
			a.Failure = "semantic failure"
			a.CapabilityFinding = "independent wrong-value probe"
			a.TargetedFlashRetry = true
			a.PinnedModel = "route-model"
		}, "capability", "flash", false},
		{"provider auth", func(a *Attempt) { a.ProviderClass = "auth" }, "infrastructure", "flash", false},
		{"provider outcome unknown", func(a *Attempt) { a.ProviderOutcomeUnknown = true }, "infrastructure", "flash", false},
		{"job artifacts uninspected", func(a *Attempt) { a.ArtifactsInspected = false }, "mechanical", "flash", false},
		{"committed checkpoint", func(a *Attempt) { a.Artifacts = []string{"owned recovery ref"} }, "mechanical", "flash", false},
		{"uncommitted patch", func(a *Attempt) { a.Diff = "docs/ratings/report.md" }, "mechanical", "flash", false},
		{"brief unsound", func(a *Attempt) { a.SoundBrief = false }, "mechanical", "flash", false},
		{"live work", func(a *Attempt) { a.State = "working" }, "unknown", "flash", false},
		{"free prose is no disposition", func(a *Attempt) { a.Failure = "capability problem with the model" }, "unknown", "flash", false},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			in := failed()
			row.edit(&in.Attempts[0])
			p, err := Build(in)
			require.NoError(t, err)
			assert.Equal(t, row.class, p.Actions[0].Class)
			assert.Equal(t, row.tier, p.Actions[0].ProposedTier)
			assert.Equal(t, row.safe, p.Actions[0].Safe)
		})
	}
}

func TestPlanIdentityAndPrepareFence(t *testing.T) {
	t.Parallel()
	in := failed()
	p, err := Build(in)
	require.NoError(t, err)
	out, err := Prepare(p, in, Receipt{}, "coordinator")
	require.NoError(t, err)
	require.Len(t, out.Requests, 1)
	assert.Empty(t, out.Requests[0].Tier)
	assert.Equal(t, in.Limits, out.Limits)
	for _, row := range []struct {
		name string
		edit func(*Input)
	}{
		{"epoch", func(i *Input) { i.Epoch++ }}, {"generation", func(i *Input) { i.Attempts[0].Generation++ }},
		{"revision", func(i *Input) { i.Attempts[0].Revision++ }}, {"head", func(i *Input) { i.Attempts[0].Head = strings.Repeat("b", 40) }},
		{"caps", func(i *Input) { i.Limits.Tokens++ }}, {"new failure", func(i *Input) { i.Attempts[0].Failure += " changed" }},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			current := failed()
			row.edit(&current)
			_, err := Prepare(p, current, Receipt{}, "coordinator")
			require.ErrorContains(t, err, "stale")
		})
	}
	tampered := p
	tampered.Actions = append([]Action(nil), p.Actions...)
	tampered.Actions[0].ProposedTier = "pro"
	_, err = Prepare(tampered, in, Receipt{}, "coordinator")
	require.ErrorContains(t, err, "altered")
	current := failed()
	current.Attempts[0].Generation++
	out, err = Prepare(p, current, Receipt{PlanID: p.ID}, "coordinator")
	require.NoError(t, err)
	assert.True(t, out.AlreadyApplied)
	assert.Empty(t, out.Requests)
}

func TestExactCohortAndWholePlanRefusal(t *testing.T) {
	t.Parallel()
	in := failed()
	second := in.Attempts[0]
	second.ID = "second"
	second.WorkID = "second.w2"
	second.ArtifactsInspected = false
	in.Attempts = append(in.Attempts, second)
	p, err := Build(in)
	require.NoError(t, err)
	in.Attempts[0], in.Attempts[1] = in.Attempts[1], in.Attempts[0]
	other, err := Build(in)
	require.NoError(t, err)
	assert.Equal(t, p.ID, other.ID)
	out, err := Prepare(p, in, Receipt{}, "coordinator")
	require.ErrorContains(t, err, "held")
	assert.Empty(t, out.Requests, "an eligible earlier member cannot partially escape")
	in = failed()
	in.Attempts = append(in.Attempts, in.Attempts[0])
	_, err = Build(in)
	require.ErrorContains(t, err, "duplicate")
	in = failed()
	in.Limits.Tokens = 0
	_, err = Build(in)
	require.ErrorContains(t, err, "caps")
}

func TestSnapshotProjectionDoesNotInventArtifacts(t *testing.T) {
	t.Parallel()
	in := failed()
	a := in.Attempts[0]
	s := &sprint.Snapshot{Epoch: in.Epoch, Work: sprint.NewTable(sprint.Work), Fleet: sprint.NewTable(sprint.Fleet)}
	s.Work.Put(&sprint.Card{ID: a.ID, Row: a.Stream, Col: a.State, Rev: a.Revision, Fields: map[string]string{"work": a.WorkID, "attempt": "2", "result": "failed", "failure": a.Failure, "brief": "RESULT: sample sha=0123456789ab tier: flash\n"}})
	wc := &sprint.Card{ID: a.WorkID, Row: "worker", Col: "done", Fields: map[string]string{sprint.PrimaryField: a.ID, "attempt": "2", "gen": "1", "head": "unverified reported head"}}
	s.Fleet.Put(wc)
	p, err := Observe(s, in.Cohort, []string{a.ID}, in.Limits, nil)
	require.NoError(t, err)
	assert.Equal(t, wc.F("head"), p.Actions[0].Attempt.Head)
	assert.False(t, p.Actions[0].Attempt.ArtifactsInspected)
	assert.False(t, p.Actions[0].Safe)
	evidence := map[string]Evidence{a.ID: {SoundBrief: true, Fix: a.Fix, ArtifactsInspected: true}}
	p, err = Observe(s, in.Cohort, []string{a.ID}, in.Limits, evidence)
	require.NoError(t, err)
	assert.True(t, p.Actions[0].Safe)
	wc.Fields["gen"] = "-1"
	_, err = Observe(s, in.Cohort, []string{a.ID}, in.Limits, evidence)
	require.ErrorContains(t, err, "invalid generation")
}
