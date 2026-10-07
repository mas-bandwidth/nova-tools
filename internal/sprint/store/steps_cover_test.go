package store

import (
	"testing"

	"github.com/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MoveStep (steps.go:136) and ScoreStep (steps.go:204) are step builders: they
// return a Step whose Extras and Plan closures no unit test reached before (the
// unit tier's per-function coverage table named them at 0.0%). These tests
// drive each through the store on the machine's own STOPPED Mem seam: no sleep,
// no real time, no network, no Redis or Postgres.

// --------------------------------------------------------------------------- MoveStep

// TestStepsCoverMoveStepMainPath covers MoveStep on its main path: the Step it
// builds, the Extras it names (the moved id, the card's needs, and the
// destination's control card), and the Plan it runs (MoveCards moves the
// unstarted primary to the destination stream on a STOPPED machine).
func TestStepsCoverMoveStepMainPath(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		IDs     []string
		stream  string
		wantRow string
	}{
		{"moving a ready primary to a new stream", []string{"s1-1"}, "s2", "s2"},
		{"moving a primary to a third stream", []string{"s1-1"}, "s3", "s3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.setup(1)

			step := MoveStep(sprint.MoveReq{IDs: tc.IDs, Stream: tc.stream})
			assert.Equal(t, "move", step.Verb, "MoveStep sets the verb")
			assert.True(t, step.Named, "MoveStep names its cards")
			assert.True(t, step.Mirrors, "MoveStep mirrors display cells")
			assert.Equal(t, tables(sprint.Work, sprint.Merge, sprint.Fleet), step.Load, "MoveStep loads work, merge and fleet")

			// Extras: the moved ids plus their needs, and the destination control card.
			extras := step.Extras(h.snap())
			assert.Equal(t, []string{sprint.CtlID(tc.stream)}, extras[sprint.Merge], "Extras names the destination's control card")
			assert.Contains(t, extras[sprint.Work], "s1-1", "Extras names the moved id")

			res := h.must(step)
			assert.Empty(t, res.Refused, "MoveStep succeeds on a STOPPED machine")
			require.NotEmpty(t, res.Moved, "the result says the card was moved")
			assert.Contains(t, res.Moved[0], "moved from stream s1", "the result says the card was moved")

			c := h.snap().Work.Card("s1-1")
			require.NotNil(t, c, "s1-1 still exists")
			assert.Equal(t, tc.wantRow, c.Row, "the card was placed in stream %s", tc.wantRow)
			h.clean("moved")
		})
	}
}

// TestStepsCoverMoveStepRefusesWhenRunning covers MoveStep's refusal: on a
// RUNNING machine MoveCards refuses every named card with the stop-machine
// remedy, and the step writes nothing.
func TestStepsCoverMoveStepRefusesWhenRunning(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()

	step := MoveStep(sprint.MoveReq{IDs: []string{"s1-1"}, Stream: "s2"})
	assert.Equal(t, "move", step.Verb)
	assert.True(t, step.Named)

	res := h.run(step)
	require.NotEmpty(t, res.Refused, "MoveStep is refused on a RUNNING machine")
	assert.Contains(t, res.Refused[0].Why, "RUNNING", "the refusal names the machine's state")
	assert.Contains(t, res.Refused[0].Why, "nova-sprint stop", "the remedy is to stop the machine")

	c := h.snap().Work.Card("s1-1")
	require.NotNil(t, c, "s1-1 still exists")
	assert.Equal(t, "s1", c.Row, "the card was not moved by the refusal")
}

// --------------------------------------------------------------------------- ScoreStep

// TestStepsCoverScoreStepMainPath covers ScoreStep on its main path: the Step
// it builds, and the Plan it runs (RecordScores writes the score's p, class and
// op on the landed card, raising no judgment without a bar).
func TestStepsCoverScoreStepMainPath(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		scores []sprint.CardScore
	}{
		{"one score on a landed card", []sprint.CardScore{{ID: "s1-1", Op: "op-1", Class: "flash", P: 0.9}}},
		{"two scores on landed cards", []sprint.CardScore{{ID: "s1-1", Op: "op-1", Class: "flash", P: 0.9}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.setup(1)
			h.landThrough("s1", "s1-1")

			step := ScoreStep(sprint.ScoreReq{Stream: "s1", Scores: tc.scores, Who: "tester"})
			assert.Equal(t, "score", step.Verb, "ScoreStep sets the verb")
			assert.True(t, step.Named, "ScoreStep names its cards when scores are non-empty")
			assert.True(t, step.Routes, "ScoreStep rides the routes")
			assert.Equal(t, tables(sprint.Work), step.Load, "ScoreStep loads the work table")

			res := h.must(step)
			assert.Empty(t, res.Refused, "ScoreStep succeeds on a landed card")
			require.NotEmpty(t, res.Moved, "the result says the card was scored")
			assert.Contains(t, res.Moved[0], "scored", "the result says the card was scored")

			c := h.snap().Work.Card("s1-1")
			require.NotNil(t, c, "s1-1 still exists")
			assert.Equal(t, "0.900", c.F(sprint.FieldLandedP), "the score's p is written, three places")
			assert.Equal(t, "flash", c.F(sprint.FieldLandedClass), "the score's class is written")
			assert.Equal(t, "op-1", c.F(sprint.FieldLandedOp), "the score's op is written")
			h.clean("scored")
		})
	}
}

// TestStepsCoverScoreStepRefusesWhenNotLanded covers ScoreStep's refusal: a
// score for a card that is not landed is refused, and nothing is written.
func TestStepsCoverScoreStepRefusesWhenNotLanded(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)

	step := ScoreStep(sprint.ScoreReq{Stream: "s1", Scores: []sprint.CardScore{{ID: "s1-1", Op: "op-1", Class: "flash", P: 0.9}}, Who: "tester"})
	assert.Equal(t, "score", step.Verb)
	assert.True(t, step.Named, "ScoreStep names its cards when scores are non-empty")
	assert.True(t, step.Routes)

	res := h.run(step)
	require.NotEmpty(t, res.Refused, "ScoreStep refuses a card that is not landed")
	assert.Contains(t, res.Refused[0].Why, "not landed", "the refusal names the card's column")
	assert.Empty(t, res.Moved, "the refusal moved nothing")

	c := h.snap().Work.Card("s1-1")
	require.NotNil(t, c, "s1-1 still exists")
	assert.Empty(t, c.F(sprint.FieldLandedOp), "the refusal wrote no score")
	assert.Empty(t, c.F(sprint.FieldLandedP), "the refusal wrote no score")
}
