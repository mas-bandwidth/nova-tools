package sprint

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The review reason (columns.go; docs/SPEC-SPRINT.md section 1, "The review reason"): the
// fix for this card is explicit that the work columns are a locked table shape
// (TABLES.lock), so there is NO new column. `review` keeps its primaries, each carrying a
// review reason, "read" or "defect", and every count review feeds is split. These tests pin
// the field, its two ways in and its exits, the split counts, the defect alarm and the JSON
// the field is read from.

// bounded drives a primary to review at its brief's bound: the attempt cap is 1, so its
// first finish is the bound and the finish raises NBriefWrong, the brief is wrong not the
// worker (brief_bound.go).
func bounded(w *world, id string) {
	w.t.Helper()
	w.s.Work.SetProp(PropAttempts, "1")
	finished(w, id, true)
}

// TestABoundJudgmentMovesTheCardToDefect: a primary in review is "read" while it waits on
// a read, and "defect" once a bound judgment is open on it.
func TestABoundJudgmentMovesTheCardToDefect(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	finished(w, "s1-1", false)
	pr := w.s.Work.Card("s1-1")
	require.Equal(t, Review, pr.Col)
	assert.Equal(t, ReviewReasonRead, ReviewReason(w.s, pr), "ok work in review waits on a read")

	w.s.Work.SetProp(PropAttempts, "1")
	finished(w, "s1-2", true)
	pr = w.s.Work.Card("s1-2")
	require.Equal(t, Review, pr.Col)
	require.Len(t, w.notesOf(NBriefWrong), 1, "the finish raised the bound")
	assert.Equal(t, ReviewReasonDefect, ReviewReason(w.s, pr), "a bound judgment open on it is the defect")
	why, since, ok := ReviewDefect(w.s, pr)
	require.True(t, ok)
	assert.Equal(t, w.s.Now, since, "the judgment's time")
	assert.Contains(t, why, "the brief is wrong, not the worker", "the bound's own words")
	w.clean("bound")
}

// TestAReaderFindingThatNamesTheBriefMarksTheCardDefect: item 1's third way in. A broken
// read whose finding names the brief (the base lacks a PATHS file, PATHS do not hold, a
// duplicate of landed work, a decision delivered) raises the brief defect, stamps the
// primary, and the card reads defect; a finding that names the work does not.
func TestAReaderFindingThatNamesTheBriefMarksTheCardDefect(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	finished(w, "s1-1", false)
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	rcs := readsAt(w.s, w.s.Work.Card("s1-1"), 1)
	require.NotEmpty(t, rcs)
	w.must(Read(w.s, ReadReq{Usage: "input=1000 output=100", As: rcs[0].Row, Verdict: "broken",
		Finding: "the base lacks internal/sprint/columns.go: no such file", Sel: Sel{IDs: []string{rcs[0].ID}}}))

	pr := w.s.Work.Card("s1-1")
	require.Equal(t, Review, pr.Col, "the card stays in review for a mind")
	assert.Equal(t, BriefDefectBase, pr.F(FieldBriefDefect), "the brief defect is the primary's")
	assert.Equal(t, ReviewReasonDefect, ReviewReason(w.s, pr))
	assert.Len(t, w.notesOf(NBriefDefect), 1, "one brief-defect judgment, not a broken read")
	assert.Empty(t, w.notesOf(NReadBroken))
	assert.Equal(t, Decisions[NBriefDefect], w.notesOf(NBriefDefect)[0].Decisions)
}

// TestABriefEditMovesTheDefectOutOfReview: the exit item 1 names. `brief` re-cuts the card
// and opens its next attempt; it leaves review for ready and its reason is cleared.
func TestABriefEditMovesTheDefectOutOfReview(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	bounded(w, "s1-1")
	pr := w.s.Work.Card("s1-1")
	require.Equal(t, ReviewReasonDefect, ReviewReason(w.s, pr))
	require.NotEmpty(t, w.openOn("s1-1"), "the defect is a judgment")

	w.must(Brief(w.s, BriefReq{ID: "s1-1", Brief: proBrief + "\nThe missing file is added to the PATHS.\n", Who: "coordinator"}))
	pr = w.s.Work.Card("s1-1")
	require.Equal(t, Ready, pr.Col, "the edit opens the next attempt")
	assert.Equal(t, "", ReviewReason(w.s, pr), "out of review, no reason")
	assert.Empty(t, w.openOn("s1-1"), "the edit answers the brief's judgment")
	assert.Len(t, w.notesOf(NBriefDefect), 0, "no brief defect is raised for a card that left review")
	w.clean("brief")
}

// TestReviewCountsSplitReadsAndDefect: item 2. Every count review feeds is split, and the
// defect cards are excluded from the reads. DefectCards lists the defect only, oldest first.
func TestReviewCountsSplitReadsAndDefect(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	w.s.Work.SetProp(PropAttempts, "1")
	finished(w, "s1-1", true)  // the bound: defect
	finished(w, "s1-2", false) // ok work: a read waiting
	reads, defect := ReviewSplit(w.s)
	assert.Equal(t, 1, reads, "review's reads exclude the defect")
	assert.Equal(t, 1, defect)
	cards := DefectCards(w.s)
	require.Len(t, cards, 1)
	assert.Equal(t, "s1-1", cards[0].ID)

	// the read card is not a defect and the defect card is not a read
	assert.Equal(t, ReviewReasonRead, ReviewReason(w.s, w.s.Work.Card("s1-2")))
	assert.Equal(t, ReviewReasonDefect, ReviewReason(w.s, w.s.Work.Card("s1-1")))
	w.clean("split")
}

// TestTheDefectAlarmRaisesAtNPlusOneAndAtTwoHoursAndClosesUnder: item 3. The threshold is the
// work table's property, default 10 and on; more than n defect cards raises, one over two
// hours raises, and dropping back under closes.
func TestTheDefectAlarmRaisesAtNPlusOneAndAtTwoHoursAndClosesUnder(t *testing.T) {
	t.Parallel()
	t.Run("more than n raises, under n clears", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 3)
		w.s.Work.SetProp(PropAlarmDefect, "2")
		bounded(w, "s1-1")
		bounded(w, "s1-2")
		_, raised := DefectAlarm(w.s)
		assert.False(t, raised, "2 in defect is not above the alarm of 2")
		bounded(w, "s1-3")
		what, raised := DefectAlarm(w.s)
		require.True(t, raised, "3 in defect is above the alarm of 2")
		assert.Contains(t, what, "s1-1", "the oldest with its reason")
		assert.Contains(t, what, "above the alarm of 2")

		w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"s1-3"}}, Reason: "re-cut as a new card", Who: "coordinator"}))
		_, raised = DefectAlarm(w.s)
		assert.False(t, raised, "2 again: under the alarm, closed")
	})

	t.Run("one in defect over two hours raises", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 1)
		w.s.Work.SetProp(PropAlarmDefect, "10")
		bounded(w, "s1-1")
		_, raised := DefectAlarm(w.s)
		assert.False(t, raised, "one fresh defect is under the age and the count")
		w.tick(3 * time.Hour)
		what, raised := DefectAlarm(w.s)
		require.True(t, raised, "over two hours in defect raises")
		assert.Contains(t, what, "s1-1")
	})
}

// TestTheJSONColumnFieldReadsDefect: item 2's JSON. card --all --json carries the fields
// map; the review reason is added to it, "defect" on a defect card and "read" on a read.
func TestTheJSONColumnFieldReadsDefect(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	bounded(w, "s1-1")
	b, err := json.Marshal(ReviewCardFields(w.s, w.s.Work.Card("s1-1")))
	require.NoError(t, err)
	assert.Contains(t, string(b), `"review_reason":"defect"`)

	reads := setup(t, 1)
	finished(reads, "s1-1", false)
	rb, err := json.Marshal(ReviewCardFields(reads.s, reads.s.Work.Card("s1-1")))
	require.NoError(t, err)
	assert.Contains(t, string(rb), `"review_reason":"read"`)

	// a card out of review carries none
	ready := setup(t, 1)
	assert.NotContains(t, string(mustJSON(t, ReviewCardFields(ready.s, ready.s.Work.Card("s1-1")))), "review_reason")
}

// mustJSON marshals a card's review fields for a test.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}
