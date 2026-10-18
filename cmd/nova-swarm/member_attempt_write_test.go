package main

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/decide"
	"github.com/mas-bandwidth/nova-tools/pkg/member"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// attemptedBackend answers the attempt question fixed answers.
type attemptedBackend struct {
	answers map[string]decide.Answer
	usage   decide.Usage
}

func (b *attemptedBackend) Name() string { return "fake" }

func (b *attemptedBackend) Ask(_ context.Context, _ decide.Schema, _ string) (map[string]decide.Answer, decide.Usage, error) {
	return b.answers, b.usage, nil
}

// TestMemberAttemptDecisionWrite pins the member's attempt decision writer
// (attemptDecider): it writes the decision in the table's variable form, the
// padded standard base64 of the generated image (the owner's rule S2), what it
// writes reads back equal through the dual reader, the legacy form still reads,
// and a value the table cannot hold is refused by name before a byte is
// written. The decider runs on a fake backend and a clock the test passes in,
// starting at 1000.0 seconds.
func TestMemberAttemptDecisionWrite(t *testing.T) {
	t.Parallel()

	now := func() time.Time { return time.Unix(1000, 0).UTC() }
	card, attempt := "card@1", 3
	brief, result, reason := "the whole task", "the child's result", "landed"

	good := map[string]decide.Answer{
		decide.AttemptQuestion: {Type: decide.Choice, Value: decide.ClassDone,
			P: map[string]float64{decide.ClassDone: 0.7, decide.ClassNeedsPro: 0.3}},
	}
	back := &attemptedBackend{answers: good, usage: decide.Usage{InputTokens: 12, OutputTokens: 4}}

	want, err := decide.AttemptDecision(context.Background(), back, card, attempt, brief, result, reason, now())
	require.NoError(t, err)

	decider := attemptDecider(back, now)
	line, raw, err := decider(member.Packet{Primary: card, Attempt: attempt, Brief: brief}, result, reason)
	require.NoError(t, err)
	require.NotEmpty(t, line)
	require.NotEmpty(t, raw)

	got, res, err := decide.ParseAttemptReader.Read(raw)
	require.NoError(t, err)
	assert.Equal(t, schemaread.FormBase64, res.Form)
	assert.Equal(t, want, got)

	legacy, err := json.Marshal(want)
	require.NoError(t, err)
	legacyGot, _, err := decide.ParseAttemptReader.Read(legacy)
	require.NoError(t, err)
	assert.Equal(t, want, legacyGot, "the legacy form of the same record still reads")

	// A NaN probability passes the schema's [0, 1] check (NaN compares less
	// than no number) but is not a value the table holds: the reader's check
	// refuses it by name before any byte is written.
	bad := map[string]decide.Answer{
		decide.AttemptQuestion: {Type: decide.Choice, Value: decide.ClassDone,
			P: map[string]float64{decide.ClassDone: math.NaN()}},
	}
	badBack := &attemptedBackend{answers: bad}
	_, badRaw, err := attemptDecider(badBack, now)(member.Packet{Primary: card, Attempt: attempt, Brief: brief}, result, reason)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "REFUSED")
	assert.Nil(t, badRaw, "a value the table cannot hold is refused before a byte is written")
}
