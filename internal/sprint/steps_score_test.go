package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// landedCards takes ids through the sprint and lands them in one batch.
func landedCards(w *world, ids ...string) {
	w.t.Helper()
	accepted(w, ids...)
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Cards: ids}))
}

// A landed batch's scores are written on its cards, and the cards whose top class meets
// the bar are listed, the highest first, in ONE judgment for the batch; a replay of the
// same scores writes nothing and raises no second judgment; ack answers it, on a landed
// card nothing else holds (docs/SPEC-SPRINT.md section 7, the landed score).
func TestRecordScoresRaisesOneJudgmentForTheBatch(t *testing.T) {
	t.Parallel()
	w := setup(t, 4)
	landedCards(w, "s1-1", "s1-2", "s1-3", "s1-4")
	w.s.DecideScoreBar = "0.5"
	req := ScoreReq{Stream: "s1", Who: "lander", Scores: []CardScore{
		{ID: "s1-1", Op: "s1-1@landed@aaa", Class: "cut_citation", P: 0.62},
		{ID: "s1-2", Op: "s1-2@landed@bbb", Class: "record_made_claim", P: 0.5},
		{ID: "s1-3", Op: "s1-3@landed@ccc", Class: "stranded_fragment", P: 0.91},
		{ID: "s1-4", Op: "s1-4@landed@ddd", Class: "cut_citation", P: 0.4999},
	}}
	p := w.must(RecordScores(w.s, req))
	require.Len(t, p.Units, 4)
	assert.Equal(t, "s1-3 scored stranded_fragment 0.910", p.Units[2].Moved)
	c := w.s.Work.Card("s1-1")
	assert.Equal(t, []string{"0.620", "cut_citation", "s1-1@landed@aaa"}, []string{c.F(FieldLandedP), c.F(FieldLandedClass), c.F(FieldLandedOp)})
	low := w.notesOf(NScoredLow)
	require.Len(t, low, 1, "one judgment for the batch")
	assert.Equal(t, []string{"s1-3", "s1-1", "s1-2"}, low[0].Primaries, "the highest first, only the cards at or above the bar: exactly at it is in, just under it is out")
	assert.Equal(t, "s1-3 stranded_fragment 0.91, s1-1 cut_citation 0.62, s1-2 record_made_claim 0.50 (the bar 0.5; nova-decide findings clusters the classes)", low[0].What)
	assert.Equal(t, []string{"add a repair card", "ack"}, low[0].Decisions)

	again := w.must(RecordScores(w.s, req))
	assert.Empty(t, again.Units, "a replay writes nothing")
	assert.Len(t, w.notesOf(NScoredLow), 1, "and raises no second judgment")

	w.must(Ack(w.s, AckReq{Notes: []string{low[0].ID}, Reason: "the citations are paraphrased truly", Who: w.s.Coordinator}))
	assert.Empty(t, w.openOn("s1-3"), "ack answers the judgment on a landed card")
}

// A score for a card not landed is refused, naming where it is, and nothing is written;
// an empty bar (the row's "" or a store nova-config never applied) writes the scores and
// raises no judgment.
func TestRecordScoresRefusesACardNotLandedAndAnEmptyBarJudgesNone(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	landedCards(w, "s1-1")
	w.s.DecideScoreBar = "0.5"
	p := RecordScores(w.s, ScoreReq{Stream: "s1", Scores: []CardScore{{ID: "s1-1", Op: "a", Class: "x", P: 0.9}, {ID: "s1-2", Op: "b", Class: "x", P: 0.9}}})
	require.Len(t, p.Refused, 1)
	assert.Equal(t, "s1-2", p.Refused[0].Key)
	assert.Contains(t, p.Refused[0].Why, "not landed")

	w.s.DecideScoreBar = ""
	w.must(RecordScores(w.s, ScoreReq{Stream: "s1", Scores: []CardScore{{ID: "s1-1", Op: "a", Class: "x", P: 0.99}}}))
	assert.Equal(t, "0.990", w.s.Work.Card("s1-1").F(FieldLandedP))
	assert.Empty(t, w.notesOf(NScoredLow))
}
