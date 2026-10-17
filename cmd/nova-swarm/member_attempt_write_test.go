package main

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/decide"
	"github.com/mas-bandwidth/nova-tools/pkg/decidewire"
	"github.com/mas-bandwidth/nova-tools/pkg/member"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMemberAttemptDecisionWrite pins the attempt decision writer after its
// switch to the table form (S4, writers second): attemptDecider writes the
// record as the padded standard base64 of the table wire (S2), a value the
// table cannot hold is refused by name before a byte is written and never
// falls back to the legacy form, and the legacy fixture of the same record
// still reads. The decider runs on a fake backend with a clock the test passes
// in, starting at 1000.0 seconds.
func TestMemberAttemptDecisionWrite(t *testing.T) {
	t.Parallel()
	at := time.Unix(1000, 0).UTC()
	b := decide.Fixed{Table: map[string]decide.FixedAnswer{decide.AttemptQuestion: {Choice: decide.ClassNoResult, P: map[string]float64{decide.ClassNoResult: 0.93, decide.ClassDone: 0.07}}}}
	p := member.Packet{Card: "c1.w2", Primary: "c1", Attempt: 2, Brief: "c: the work (s1) tier: flash\n"}
	line, raw, err := attemptDecider(b, func() time.Time { return at })(p, "", "budget: no RESULT.md shape; the child ended without a result")
	require.NoError(t, err)
	d, err := decide.AttemptDecision(t.Context(), b, p.Primary, p.Attempt, p.Brief, "", "budget: no RESULT.md shape; the child ended without a result", at)
	require.NoError(t, err)

	wire, derr := base64.StdEncoding.DecodeString(string(raw))
	require.NoError(t, derr, "the writer carries the table wire as padded standard base64")
	require.Greater(t, len(wire), 0, "the table wire opens with its form byte")
	assert.Equal(t, byte(1), wire[0], "the table wire opens with the variable-form byte")

	var bb decidewire.DecisionBuilder
	require.True(t, bb.Init())
	defer bb.Shutdown()
	var rep decidewire.TableReport
	require.True(t, decidewire.DecisionLoadBuilder(&bb, wire, &rep), "the written wire reads strictly as a table decision")
	root := bb.GetRoot()
	ar := []*decidewire.TableArena{&bb.Arena}
	assert.Equal(t, d.ID, string(decidewire.TableStringAt(&root.Id, ar...).Data))
	assert.Equal(t, d.Decision, string(decidewire.TableStringAt(&root.DecisionName, ar...).Data))
	assert.Equal(t, d.At, string(decidewire.TableStringAt(&root.At, ar...).Data))
	assert.Equal(t, line, "no-result p=0.930 op="+d.ID)
}
