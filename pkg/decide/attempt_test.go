package decide

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixture is a Fixed backend from pkg/decide/testdata.
func fixture(t *testing.T, name string) Fixed {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	f, err := ParseFixed(raw)
	require.NoError(t, err)
	return f
}

// The attempt and grade schemas are schemas like any other, each option's criterion is
// the row SPEC-NOVA-DECIDE sections 10 and 11 state word for word, and each hash is the one
// the calibration of 2026-10-03 asked under: a reworded criterion turns this red, and the
// bars the record supports are read again before the new hash is pinned.
func TestAttemptAndGradeSchemasAreThePinnedOnes(t *testing.T) {
	t.Parallel()
	spec, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-NOVA-DECIDE.md"))
	require.NoError(t, err)
	for _, tc := range []struct {
		s        Schema
		question string
		options  []string
		hash     string
	}{
		{AttemptSchema(), AttemptQuestion, []string{ClassDone, ClassNeedsPro, ClassNoResult, ClassNothingToDo, ClassProviderFailure, ClassWrongScope}, "3156747c139f228f"},
		{GradeSchema(), GradeQuestion, []string{GradeFlash, GradePro, GradeScript}, "ad287c9232ad5008"},
	} {
		assert.Empty(t, tc.s.Problems(), tc.s.Name)
		require.Len(t, tc.s.Questions, 1, tc.s.Name)
		q := tc.s.Questions[tc.question]
		assert.Equal(t, Choice, q.Type, tc.s.Name)
		require.Equal(t, tc.options, slices.Sorted(maps.Keys(q.Criteria)), tc.s.Name)
		for option, rule := range q.Criteria {
			row := "| `" + option + "` | " + rule + " |"
			assert.Contains(t, string(spec), row, "SPEC-NOVA-DECIDE does not state the %s criterion of %s as the schema asks it", option, tc.s.Name)
		}
		assert.Equal(t, tc.hash, tc.s.Hash(), "the %s schema changed from the one calibrated: recalibrate (docs/SPEC-NOVA-DECIDE.md) before pinning the new hash", tc.s.Name)
	}
}

// An attempt decision is asked through the backend it is handed and comes back as the
// record keeps it, unrecorded: its op id names the card, the attempt and the state, its
// card line is the class and its p, and the JSON a finish carries parses back to the same
// decision. A decision that is not the attempt's, answers outside the schema, or an id
// that is not its state's is refused, every problem named.
func TestAnAttemptDecisionRidesWithTheFinishAndParsesBack(t *testing.T) {
	t.Parallel()
	brief, reason := "c: rename the helper (s1) tier: pro\nPATHS: internal/x\n", "verdict not-done; tests red in pkg/decide"
	result := "head: abc\nverdict: not-done\nreport: tests red\n"
	d, err := AttemptDecision(context.Background(), fixture(t, "attempt-needs-pro.json"), "c1", 2, brief, result, reason, at)
	require.NoError(t, err)
	state := AttemptState(brief, result, reason)
	assert.Equal(t, "c1@2."+Sum([]byte(state))[:12], d.ID)
	assert.Equal(t, AttemptOp("c1", 2, state), d.ID)
	assert.Equal(t, state, d.State)
	assert.Equal(t, reason, d.Inputs["reason"])
	dec, err := AttemptDecided(d)
	require.NoError(t, err)
	assert.Equal(t, "needs-pro p=0.840 op="+d.ID, dec.String())
	raw, err := json.Marshal(d)
	require.NoError(t, err)
	back, err := ParseAttempt(raw)
	require.NoError(t, err)
	assert.Equal(t, d, back)

	again, err := AttemptDecision(context.Background(), fixture(t, "attempt-no-result.json"), "c1", 2, brief, "", "no result: no RESULT.md shape", at)
	require.NoError(t, err)
	assert.NotEqual(t, d.ID, again.ID, "another take of the attempt that ended another way is another decision")
	same, err := AttemptDecision(context.Background(), fixture(t, "attempt-no-result.json"), "c1", 2, brief, result, reason, at)
	require.NoError(t, err)
	assert.Equal(t, d.ID, same.ID, "two takes of one attempt with the same state are one decision: the op is per attempt and state")
	card, n, ok := AttemptOf(d.ID)
	assert.Equal(t, []any{"c1", 2, true}, []any{card, n, ok}, "the op names its card and attempt")
	for _, bad := range []string{"c1", "c1@x.0123", "c1@2", "@2.0123"} {
		_, _, ok := AttemptOf(bad)
		assert.False(t, ok, bad)
	}
	assert.Contains(t, again.State, "RESULT (the child's RESULT.md):\n(none: the child wrote no RESULT.md)")

	for name, bend := range map[string]func(*Decision){
		"another decision": func(x *Decision) { x.Decision = ReadName },
		"another schema":   func(x *Decision) { x.Schema = "0000000000000000" },
		"an unasked class": func(x *Decision) {
			x.Answers = map[string]Answer{AttemptQuestion: {Type: Choice, Value: "maybe", P: map[string]float64{"maybe": 1}}}
		},
		"another state": func(x *Decision) { x.State += "more" },
		"no attempt":    func(x *Decision) { x.ID = "c1" },
	} {
		bent := d
		bend(&bent)
		raw, err := json.Marshal(bent)
		require.NoError(t, err)
		_, err = ParseAttempt(raw)
		assert.Error(t, err, name)
	}
	_, err = ParseAttempt([]byte("{"))
	assert.ErrorContains(t, err, "not JSON of a record's decision")
	_, err = AttemptDecided(Decision{Decision: GradeName, Answers: map[string]Answer{AttemptQuestion: {Type: Choice, Value: ClassDone, P: map[string]float64{ClassDone: 1}}}})
	assert.Error(t, err, "a grade is no attempt decision")
}

// A failing backend is a BackendError, and nothing is decided.
func TestAnAttemptDecisionThatCannotBeMadeIsABackendError(t *testing.T) {
	t.Parallel()
	_, err := AttemptDecision(context.Background(), Fixed{Table: map[string]FixedAnswer{}}, "c1", 1, "brief", "", "reason", at)
	var failed *BackendError
	require.ErrorAs(t, err, &failed)
	assert.Equal(t, "fixed", failed.Backend)
	assert.ErrorContains(t, err, "no answer to class")
}

// The card's line of a decision round-trips, and holds its bar: at or above it is over;
// an empty bar or one that does not parse is never over; a line that is not one is no
// decision. ParseBar reads the sprint row's bars and names a bad one.
func TestTheDecidedLineRoundTripsAndHoldsItsBar(t *testing.T) {
	t.Parallel()
	d := Decided{Value: GradePro, P: 0.7, Op: "c1@grade.0123456789ab"}
	back, ok := ParseDecided(d.String())
	require.True(t, ok)
	assert.Equal(t, d, back)
	for bar, over := range map[string]bool{"0.7": true, "0.69": true, "0.71": false, "": false, "x": false, "2": false} {
		assert.Equal(t, over, d.Over(bar), "bar %q", bar)
	}
	for _, bad := range []string{"", "pro", "pro p=0.5", "pro p=x op=a", "pro p=1.5 op=a", "pro 0.5 op=a"} {
		_, ok := ParseDecided(bad)
		assert.False(t, ok, bad)
	}
	for raw, want := range map[string]string{"": "", "0.7": "", " 0.5 ": "", "x": `decide_attempt_no_result "x" is not a decimal`, "-0.1": "decide_attempt_no_result -0.1 is not a probability in [0, 1]"} {
		_, _, err := ParseBar("decide_attempt_no_result", raw)
		if want == "" {
			assert.NoError(t, err, raw)
		} else {
			assert.EqualError(t, err, want, raw)
		}
	}
}

// A grade is made over the brief alone and recorded under the brief's op: the same brief
// again is answered from the record with no ask, another brief of the same card is another
// decision. The outcome labels are the card's fate: the attempt's relative to the decided
// attempt and the tier that landed it, the grade's that tier.
func TestGradesAreTheBriefsAndOutcomesTheCardsFate(t *testing.T) {
	t.Parallel()
	record := filepath.Join(t.TempDir(), "grade.jsonl")
	brief := "c: a cross-package refactor (s1) tier: pro\n"
	state := GradeState(brief)
	assert.Equal(t, "CARD (the whole task a worker will be given):\n"+strings.TrimRight(brief, "\n")+"\n", state)
	d, existing, err := Make(context.Background(), fixture(t, "grade-pro.json"), GradeSchema(), state, record, GradeOp("c1", state), nil, at)
	require.NoError(t, err)
	assert.False(t, existing)
	value, p := ChoiceOf(d, GradeQuestion)
	assert.Equal(t, GradePro, value)
	assert.InDelta(t, 0.81, p, 1e-9)
	_, existing, err = Make(context.Background(), Fixed{}, GradeSchema(), state, record, GradeOp("c1", state), nil, at)
	require.NoError(t, err)
	assert.True(t, existing, "the same brief is answered from the record, the empty backend never asked")
	other := GradeState("c: rename one test (s1) tier: pro\n")
	assert.NotEqual(t, GradeOp("c1", state), GradeOp("c1", other))

	for _, tc := range []struct {
		attempt, landed int
		tier, want      string
	}{{2, 2, "flash", LabelLanded}, {1, 3, "pro", "later-pro"}, {1, 2, "flash", "later-flash"}, {2, 0, "", LabelDropped}} {
		assert.Equal(t, tc.want, AttemptLabel(tc.attempt, tc.landed, tc.tier), "%+v", tc)
	}
	assert.Equal(t, GradePro, GradeLabel(GradePro))
	assert.Equal(t, LabelDropped, GradeLabel(""))
}
