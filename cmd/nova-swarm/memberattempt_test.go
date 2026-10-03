package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/member"
)

// A work member asks the attempt decision through the backend it is handed: its card line
// and the record line the finish carries, which the sprint's server accepts (ParseAttempt).
// A reader asks none, and a member with no key asks none; a backend that fails is an error,
// and the finish then goes by its reason line.
func TestAWorkMembersAttemptDecisionIsTheRecordLineTheServerTakes(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	b := decide.Fixed{Table: map[string]decide.FixedAnswer{decide.AttemptQuestion: {Choice: decide.ClassNoResult, P: map[string]float64{decide.ClassNoResult: 0.93, decide.ClassDone: 0.07}}}}
	p := member.Packet{Card: "c1.w2", Primary: "c1", Attempt: 2, Brief: "c: the work (s1) tier: flash\n", DecideAttempt: "0.7"}
	line, raw, err := attemptDecider(b, func() time.Time { return at })(p, "", "budget: no RESULT.md shape; the child ended without a result")
	require.NoError(t, err)
	d, err := decide.ParseAttempt(raw)
	require.NoError(t, err)
	assert.Equal(t, "no-result p=0.930 op="+d.ID, line)
	assert.Equal(t, decide.AttemptOp("c1", 2, d.State), d.ID)
	assert.Equal(t, at.Format(time.RFC3339), d.At)

	_, _, err = attemptDecider(decide.Fixed{}, time.Now)(p, "", "r")
	assert.ErrorContains(t, err, "no answer to class")

	key := func(string) string { return "k" }
	assert.Nil(t, workAttempt(true, key), "a reader asks no attempt decision")
	assert.Nil(t, workAttempt(false, func(string) string { return "" }), "no key, no attempt decision")
	assert.NotNil(t, workAttempt(false, key))
}
