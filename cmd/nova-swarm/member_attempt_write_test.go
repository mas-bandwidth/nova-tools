package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/decide"
	"github.com/mas-bandwidth/nova-tools/pkg/member"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// TestMemberAttemptDecisionWrite pins the attempt decision writer after its
// switch to the table form (the owner's rule S4, writers second): attemptDecider
// writes the record as the padded standard base64 of the table wire (S2), what
// it writes reads back equal through the dual reader the reader card added, and
// a value the table cannot hold is refused by name before a byte is written,
// never written in the legacy form as a fallback. A legacy fixture of the same
// record still reads, so old readers keep working through the switch. The
// decider runs on a fake backend with a clock the test passes in, starting at
// 1000.0 seconds.
func TestMemberAttemptDecisionWrite(t *testing.T) {
	t.Parallel()
	at := time.Unix(1000, 0).UTC()
	b := decide.Fixed{Table: map[string]decide.FixedAnswer{
		decide.AttemptQuestion: {Choice: decide.ClassNoResult, P: map[string]float64{decide.ClassNoResult: 0.93, decide.ClassDone: 0.07}},
	}}
	p := member.Packet{Card: "c1.w2", Primary: "c1", Attempt: 2, Brief: "c: the work (s1) tier: flash\n"}
	result := ""
	reason := "budget: no RESULT.md shape; the child ended without a result"

	line, raw, err := attemptDecider(b, func() time.Time { return at })(p, result, reason)
	require.NoError(t, err)

	// the writer carries the table wire as the padded standard base64 (S2),
	// opening with the variable table's form byte.
	wire, err := base64.StdEncoding.DecodeString(string(raw))
	require.NoError(t, err, "the writer carries the table wire as padded standard base64")
	require.NotEmpty(t, wire, "the table wire opens with its form byte")
	assert.Equal(t, byte(1), wire[0], "the table wire opens with the variable-form byte")

	// what the writer writes reads back equal through the dual reader, as a
	// base64 read (S4: old readers keep working through the switch).
	want, err := decide.AttemptDecision(context.Background(), b, p.Primary, p.Attempt, p.Brief, result, reason, at)
	require.NoError(t, err)
	got, res, err := decide.ParseAttemptReader.Read(raw)
	require.NoError(t, err)
	assert.Equal(t, schemaread.FormBase64, res.Form)
	assert.Equal(t, want, got)
	assert.Equal(t, "no-result p=0.930 op="+want.ID, line)

	// the per-form read counter counts the base64 read (S2).
	rd := decide.NewParser()
	got, err = rd.Attempt(raw)
	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.Equal(t, uint64(1), rd.AttemptCounts().Reads(schemaread.FormBase64))

	// a legacy fixture of the same record still reads (the dual reader keeps
	// the legacy form until the retire card), as a legacy read.
	legacy, err := json.Marshal(want)
	require.NoError(t, err)
	legacyGot, err := rd.Attempt(legacy)
	require.NoError(t, err)
	assert.Equal(t, want, legacyGot)
	assert.Equal(t, uint64(1), rd.AttemptCounts().Reads(schemaread.FormLegacy))

	// a value the table cannot hold (a probability past the declared bound) is
	// refused by name before a byte is written, and never falls back to the
	// legacy form.
	over := want
	a := over.Answers[decide.AttemptQuestion]
	a.P = map[string]float64{decide.ClassNoResult: 1.5}
	over.Answers[decide.AttemptQuestion] = a
	rawOver, err := attemptWriter.Write(&over, schemaread.FormBase64)
	var ref *schemaread.Refusal
	require.ErrorAs(t, err, &ref)
	assert.Equal(t, "DECISION", ref.Name)
	assert.Empty(t, rawOver)
}
