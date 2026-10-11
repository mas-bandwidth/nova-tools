package decide

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Jev answers each raised judgment in shadow (SPEC-NOVA-DECIDE section 13, jev-shadow-judgments.w1):
// the judgment decision is asked for every judgment as it is raised, the answer is recorded with
// each probability marked shadow, and nothing is applied. When the coordinator's real answer
// arrives (decision-record) the pair is joined by note and card, and the agreement is scored per
// judgment kind: how many pairs, how often the verbs agree, and how often at p 0.95 and above.
func TestShadowAnswersAreRecordedBesideTheRealOneAndScoredPerKind(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 4, 18, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	shadowRecord, realRecord := filepath.Join(dir, ShadowName+".jsonl"), filepath.Join(dir, JudgmentAnswerName+".jsonl")
	jev := Fixed{Table: map[string]FixedAnswer{
		"verb":   {Choice: VerbRework, P: map[string]float64{VerbRework: 0.97, VerbWait: 0.03}},
		"fix":    {Choice: "own", P: map[string]float64{"own": 1}},
		"reason": {Choice: "already-done", P: map[string]float64{"already-done": 1}},
	}}
	raised := []JudgmentInput{
		{Kind: "a reader found it broken", Text: "s1-1 broken", Card: "s1-1", Cards: 1, Allowed: Verbs},
		{Kind: "a reader found it broken", Text: "s1-2 broken", Card: "s1-2", Cards: 1, Allowed: Verbs},
		{Kind: "stalled", Text: "s1-3 stalled", Card: "s1-3", Cards: 1, Allowed: Verbs},
	}
	notes := []string{"n1", "n2", "n3"}
	for i, in := range raised {
		d, existing, err := ShadowAsk(context.Background(), jev, in, notes[i], shadowRecord, at)
		require.NoError(t, err)
		assert.False(t, existing)
		assert.Equal(t, ShadowName, d.Decision)
		assert.Equal(t, "true", d.Inputs["shadow"])
		assert.Equal(t, VerbRework, d.Answers["verb"].Value)
		for q, a := range d.Answers {
			assert.Equal(t, ShadowMethod, a.Method, q)
		}
		_, existing, err = ShadowAsk(context.Background(), jev, in, notes[i], shadowRecord, at)
		require.NoError(t, err)
		assert.True(t, existing, "a judgment is shadowed once")
	}
	shadows, err := Load(shadowRecord)
	require.NoError(t, err)
	require.Len(t, shadows, 3, "three judgments, three shadow records")
	for _, d := range shadows {
		assert.Empty(t, d.Acts, "a shadow answer is never applied")
	}

	for _, r := range []struct{ note, card, kind, verb string }{
		{"n1", "s1-1", "a reader found it broken", VerbRework},
		{"n3", "s1-3", "stalled", VerbWait},
	} {
		_, err := Append(realRecord, AnswerDecision(AnswerInput{Note: r.note, Kind: r.kind, Card: r.card, Verb: r.verb, Actor: "rowan"}, at))
		require.NoError(t, err)
	}
	real, err := Load(realRecord)
	require.NoError(t, err)

	got := ShadowAgreement(shadows, real)
	assert.Equal(t, []KindAgreement{
		{Kind: "a reader found it broken", Count: 1, Agree: 1, Pct: 100, HighCount: 1, HighAgree: 1, HighPct: 100},
		{Kind: "stalled", Count: 1, Agree: 0, Pct: 0, HighCount: 1, HighAgree: 0, HighPct: 0},
	}, got)
	assert.Equal(t, 1, ShadowPending(shadows, real), "s1-2 has no real answer yet")
}
