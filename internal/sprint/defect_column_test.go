package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// review is two states in one column (docs/SPEC-SPRINT.md section 6, "review is reads and
// defect"; the card a-brief-defect-is-a-column-not-a-hole-bb): a card whose brief is wrong,
// not the worker, carries review_reason=defect and waits on a person to re-cut the brief; an
// ok report waiting on or under a read carries review_reason=read. These pin the field, the
// split every count reads, and the defect alarm (columns.go, alarms.go).

// defectCard is a review card whose brief is wrong, with the stamp its age is counted from.
func defectCard(id string, at time.Time) *Card {
	return &Card{ID: id, Row: "s1", Col: Review, Score: 1, Rev: 1,
		Fields: map[string]string{FieldReviewReason: ReviewReasonDefect, FieldReviewReasonAt: stamp(at)}}
}

// TestABoundJudgmentMovesTheCardFromReviewToDefect pins the entry: a card the machine judges
// at its brief's bound is in review with review_reason=defect, before and after the
// brief-defect rule marks it.
func TestABoundJudgmentMovesTheCardFromReviewToDefect(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.s.Work.SetProp(PropAttempts, "1") // the first attempt is the cap: the bound's judgment
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	member := w.s.Fleet.Card("s1-1.w1").Row
	w.must(Take(w.s, TakeReq{As: member}))
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: member, Gens: gensOf(w.s, "s1-1.w1"), Failed: true, Report: "tests red"}))
	pr := w.s.Work.Placed("s1-1")
	require.Equal(t, Review, pr.Col)
	require.NotEmpty(t, openOf(w, NBriefWrong, "s1-1"), "the bound judgment is open on it")
	assert.Empty(t, pr.F(FieldBriefDefect), "the rule has not marked it yet")
	assert.Equal(t, ReviewReasonDefect, pr.F(FieldReviewReason), "the bound judgment puts it in defect")
	assert.Equal(t, ReviewReasonDefect, ReviewReasonOf(pr))

	rules(w, TickReq{AnswerRules: true, Who: "tick"})
	assert.NotEmpty(t, pr.F(FieldBriefDefect), "the brief-defect rule marked it")
	assert.Equal(t, ReviewReasonDefect, ReviewReasonOf(pr))
	_, defect := ReviewSplit(w.s.Work)
	assert.Equal(t, 1, defect, "the split counts it as defect")
}

// TestABriefEditMovesADefectCardToReady pins the exit: the brief replaced in place clears the
// reason and the mark and opens the next attempt (ready), so a re-cut card is never still a
// defect.
func TestABriefEditMovesADefectCardToReady(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.s.Work.SetProp(PropAttempts, "1")
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	member := w.s.Fleet.Card("s1-1.w1").Row
	w.must(Take(w.s, TakeReq{As: member}))
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: member, Gens: gensOf(w.s, "s1-1.w1"), Failed: true, Report: "tests red"}))
	rules(w, TickReq{AnswerRules: true, Who: "tick"})
	pr := w.s.Work.Placed("s1-1")
	require.Equal(t, ReviewReasonDefect, ReviewReasonOf(pr))
	require.NotEmpty(t, pr.F(FieldBriefDefect))

	w.must(Brief(w.s, BriefReq{ID: "s1-1", Brief: "c: the re-cut work (s1)\nREPO: mas-bandwidth/nova-tools\n\nA different task.\n", Who: "coordinator"}))
	pr = w.s.Work.Placed("s1-1")
	require.Equal(t, Ready, pr.Col, "the edit opens the next attempt")
	assert.Empty(t, pr.F(FieldReviewReason), "the reason is cleared")
	assert.Empty(t, pr.F(FieldReviewReasonAt), "its stamp is cleared")
	assert.Empty(t, pr.F(FieldBriefDefect), "the defect mark is cleared")
	assert.NotEqual(t, ReviewReasonDefect, ReviewReasonOf(pr), "a ready card is not in defect")
}

// TestReviewSplitsIntoReadsAndDefect pins the count: the review column is reads plus defect,
// and a card admitted before the field (only FieldBriefDefect) still reads defect.
func TestReviewSplitsIntoReadsAndDefect(t *testing.T) {
	t.Parallel()
	work := NewTable(Work)
	work.SetRows([]string{"s1"})
	work.Put(&Card{ID: "s1-1", Row: "s1", Col: Review, Score: 1, Rev: 1,
		Fields: map[string]string{FieldReviewReason: ReviewReasonDefect}})
	work.Put(&Card{ID: "s1-2", Row: "s1", Col: Review, Score: 2, Rev: 1,
		Fields: map[string]string{FieldReviewReason: ReviewReasonRead}})
	work.Put(&Card{ID: "s1-3", Row: "s1", Col: Review, Score: 3, Rev: 1,
		Fields: map[string]string{FieldBriefDefect: BriefDefectBase}}) // admitted before the field
	work.Put(&Card{ID: "s1-4", Row: "s1", Col: Working, Score: 4, Rev: 1}) // not in review
	reads, defect := ReviewSplit(work)
	assert.Equal(t, 1, reads, "the read and no other")
	assert.Equal(t, 2, defect, "the reason and the mark")
	assert.Empty(t, ReviewReasonOf(work.Card("s1-4")), "a card not in review is no reason at all")
	assert.Len(t, ReviewDefectCards(work), 2)
}

// TestTheDefectAlarmRaisesAtNPlusOneAndAtTwoHours pins the alarm's condition: more than n
// cards in defect, or any one card in defect longer than DefectAlarmAge.
func TestTheDefectAlarmRaisesAtNPlusOneAndAtTwoHours(t *testing.T) {
	t.Parallel()
	now := t0
	work := func(ids ...string) *Table {
		tb := NewTable(Work)
		tb.SetRows([]string{"s1"})
		for i, id := range ids {
			tb.Put(defectCard(id, now.Add(-time.Duration(i)*time.Minute)))
		}
		return tb
	}

	assert.Empty(t, DefectAlarm(work("s1-1", "s1-2"), 2, now), "at n, not above it")
	assert.NotEmpty(t, DefectAlarm(work("s1-1", "s1-2", "s1-3"), 2, now), "n+1 raises")
	assert.NotEmpty(t, DefectList(ReviewDefectCards(work("s1-1", "s1-2", "s1-3")), now, MaxDefectList), "the oldest are listed")

	// one card, under the count, but older than two hours
	old := NewTable(Work)
	old.SetRows([]string{"s1"})
	old.Put(defectCard("s1-1", now.Add(-3*time.Hour)))
	assert.Greater(t, DefectAge(old.Card("s1-1"), now), DefectAlarmAge, "three hours old")
	assert.NotEmpty(t, DefectAlarm(old, AlarmDefectDefault, now), "2h raises however few")

	// one card, under the count and the age: quiet
	fresh := NewTable(Work)
	fresh.SetRows([]string{"s1"})
	fresh.Put(defectCard("s1-1", now.Add(-time.Minute)))
	assert.LessOrEqual(t, DefectAge(fresh.Card("s1-1"), now), DefectAlarmAge)
	assert.Empty(t, DefectAlarm(fresh, AlarmDefectDefault, now), "under n and under 2h closes")

	// the sprint's alarmFacts: the default is 10, and off takes it off
	s := &Snapshot{Now: now, Work: fresh}
	assert.Empty(t, alarmFacts(s)[NAlarmDefect], "under the default of 10")
	over := NewTable(Work)
	over.SetRows([]string{"s1"})
	for i := 0; i < AlarmDefectDefault+1; i++ {
		over.Put(defectCard("s1-"+itoa(i), now.Add(-time.Minute)))
	}
	s.Work = over
	assert.NotEmpty(t, alarmFacts(s)[NAlarmDefect], "above the default of 10")
	s.Work.SetProp(PropAlarmDefect, AlarmOff)
	assert.Empty(t, alarmFacts(s)[NAlarmDefect], "off takes it off")
}

// TestTheReviewReasonFieldReadsDefect pins the field the JSON carries: the key the card and
// where --json --rows render in their fields map is review_reason, and its defect value.
func TestTheReviewReasonFieldReadsDefect(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "review_reason", FieldReviewReason)
	assert.Equal(t, "review_reason_at", FieldReviewReasonAt)
	assert.Equal(t, "defect", ReviewReasonDefect)
	assert.Equal(t, "read", ReviewReasonRead)
	c := defectCard("s1-1", t0)
	assert.Equal(t, ReviewReasonDefect, c.F(FieldReviewReason))
	assert.Equal(t, ReviewReasonDefect, ReviewReasonOf(c))
}
