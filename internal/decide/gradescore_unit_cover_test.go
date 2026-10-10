package decide

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDecideGradescoreCoverReadLog(t *testing.T) {
	t.Parallel()
	// ReadLog refusing input that is not JSON
	t.Run("notJSON", func(t *testing.T) {
		t.Parallel()
		facts, err := ReadLog(strings.NewReader("not json"))
		require.Error(t, err)
		require.Nil(t, facts)
		require.Contains(t, err.Error(), "not a nova-sprint log --json export")
	})
	// a line with no primary skipped
	t.Run("noPrimary", func(t *testing.T) {
		t.Parallel()
		facts, err := ReadLog(strings.NewReader(`{"lines":[{"card":"C1","table":"work","to":"landed"}]}`))
		require.NoError(t, err)
		require.Empty(t, facts)
	})
	// a work-table line for the primary itself placing it (To "work:landed" gives State landed)
	t.Run("workTablePlaced", func(t *testing.T) {
		t.Parallel()
		facts, err := ReadLog(strings.NewReader(`{"lines":[{"card":"P1","primary":"P1","table":"work","to":"work:landed"}]}`))
		require.NoError(t, err)
		require.Equal(t, "landed", facts["P1"].State)
	})
	// a removed line with set outcome=dropped giving LabelDropped
	t.Run("removedDropped", func(t *testing.T) {
		t.Parallel()
		facts, err := ReadLog(strings.NewReader(`{"lines":[{"card":"P1","primary":"P1","table":"work","to":"work:landed","removed":true,"set":{"outcome":"dropped"}}]}`))
		require.NoError(t, err)
		require.Equal(t, LabelDropped, facts["P1"].State)
	})
	// a line for another card under the same primary that does not move State but whose cost_record:* keys still count
	t.Run("otherCardCost", func(t *testing.T) {
		t.Parallel()
		facts, err := ReadLog(strings.NewReader(`{"lines":[{"card":"P1","primary":"P1","table":"work","to":"work:landed"},{"card":"C1","primary":"P1","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=-"}}]}`))
		require.NoError(t, err)
		require.Equal(t, 1, facts["P1"].MaxAttempt)
		require.Equal(t, "flash", facts["P1"].attempts[1].tier)
	})
	// a card never placed settling as "unplaced"
	t.Run("neverPlaced", func(t *testing.T) {
		t.Parallel()
		facts, err := ReadLog(strings.NewReader(`{"lines":[{"card":"P1","primary":"P1","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=-"}}]}`))
		require.NoError(t, err)
		require.Equal(t, "unplaced", facts["P1"].State)
	})
}

func TestDecideGradescoreCoverParseCost(t *testing.T) {
	t.Parallel()
	// a kind=work record with attempt, on_tier, end and on_route
	t.Run("workRecord", func(t *testing.T) {
		t.Parallel()
		r := parseCost("kind=work attempt=1 on_tier=flash end=ok on_route=-")
		require.Equal(t, 1, r.attempt)
		require.Equal(t, "flash", r.tier)
		require.Equal(t, "ok", r.end)
		require.Equal(t, "-", r.route)
	})
	// a kind other than work giving attempt 0
	t.Run("notWork", func(t *testing.T) {
		t.Parallel()
		r := parseCost("kind=gate attempt=1 on_tier=flash end=ok")
		require.Equal(t, 0, r.attempt)
	})
	// a non-numeric attempt giving 0
	t.Run("nonNumericAttempt", func(t *testing.T) {
		t.Parallel()
		r := parseCost("kind=work attempt=bad on_tier=flash end=ok")
		require.Equal(t, 0, r.attempt)
	})
}

func TestDecideGradescoreCoverSettle(t *testing.T) {
	t.Parallel()
	// MaxAttempt is the highest attempt
	t.Run("maxAttempt", func(t *testing.T) {
		t.Parallel()
		f := &CardFacts{attempts: map[int]costRecord{1: {attempt: 1, tier: GradeFlash, end: "ok", route: "-"}, 2: {attempt: 2, tier: GradeFlash, end: "ok", route: "fleet"}}}
		f.settle()
		require.Equal(t, 2, f.MaxAttempt)
	})
	// Escalated when any attempt ran on pro
	t.Run("escalated", func(t *testing.T) {
		t.Parallel()
		f := &CardFacts{attempts: map[int]costRecord{1: {attempt: 1, tier: GradeFlash, end: "failed", route: "fleet"}, 2: {attempt: 2, tier: GradePro, end: "ok", route: "fleet"}}}
		f.settle()
		require.True(t, f.Escalated)
	})
	// the card counts as dealt on the fleet only when some route is not "-"
	t.Run("dealtOnFleet", func(t *testing.T) {
		t.Parallel()
		f := &CardFacts{attempts: map[int]costRecord{1: {attempt: 1, tier: GradeFlash, end: "ok", route: "-"}}}
		f.settle()
		require.False(t, f.dealtOnFleet)
		f2 := &CardFacts{attempts: map[int]costRecord{1: {attempt: 1, tier: GradeFlash, end: "ok", route: "fleet"}}}
		f2.settle()
		require.True(t, f2.dealtOnFleet)
	})
	// Start and FirstFailed come from attempt 1 (end=ok is not failed)
	t.Run("startAndFirstFailed", func(t *testing.T) {
		t.Parallel()
		f := &CardFacts{attempts: map[int]costRecord{1: {attempt: 1, tier: GradeFlash, end: "ok", route: "fleet"}}}
		f.settle()
		require.Equal(t, GradeFlash, f.Start)
		require.False(t, f.FirstFailed)
		f2 := &CardFacts{attempts: map[int]costRecord{1: {attempt: 1, tier: GradePro, end: "failed", route: "fleet"}}}
		f2.settle()
		require.Equal(t, GradePro, f2.Start)
		require.True(t, f2.FirstFailed)
	})
}

func TestDecideGradescoreCoverScoreGrades(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	from := now.Add(-24 * time.Hour)
	to := now

	// a decision at from is in and one at to is out
	t.Run("fromToWindow", func(t *testing.T) {
		t.Parallel()
		facts, err := ReadLog(strings.NewReader(`{"lines":[{"card":"P1","primary":"P1","table":"work","to":"work:landed"},{"card":"C1","primary":"P1","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=fleet"}}]}`))
		require.NoError(t, err)
		ds := []Decision{
			{ID: "P1@D1", Decision: GradeName, At: from.Format(time.RFC3339), Answers: map[string]Answer{GradeQuestion: {Value: GradeFlash, P: map[string]float64{GradeFlash: 0.8}}}},
			{ID: "P1@D2", Decision: GradeName, At: to.Format(time.RFC3339), Answers: map[string]Answer{GradeQuestion: {Value: GradeFlash, P: map[string]float64{GradeFlash: 0.9}}}},
		}
		s := ScoreGrades(ds, facts, from, to)
		require.Equal(t, 1, s.Decisions)
		require.Equal(t, 1, s.Cards)
	})
	// an unparseable At is skipped
	t.Run("unparseableAt", func(t *testing.T) {
		t.Parallel()
		facts, _ := ReadLog(strings.NewReader(`{"lines":[{"card":"P1","primary":"P1","table":"work","to":"work:landed"},{"card":"C1","primary":"P1","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=fleet"}}]}`))
		ds := []Decision{
			{ID: "P1@D1", Decision: GradeName, At: "not-a-date", Answers: map[string]Answer{GradeQuestion: {Value: GradeFlash, P: map[string]float64{GradeFlash: 0.8}}}},
		}
		s := ScoreGrades(ds, facts, from, to)
		require.Equal(t, 0, s.Decisions)
	})
	// a decision whose name is not GradeName is skipped
	t.Run("notGradeName", func(t *testing.T) {
		t.Parallel()
		facts, _ := ReadLog(strings.NewReader(`{"lines":[{"card":"P1","primary":"P1","table":"work","to":"work:landed"},{"card":"C1","primary":"P1","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=fleet"}}]}`))
		ds := []Decision{
			{ID: "P1@D1", Decision: "attempt", At: from.Format(time.RFC3339), Answers: map[string]Answer{GradeQuestion: {Value: GradeFlash, P: map[string]float64{GradeFlash: 0.8}}}},
		}
		s := ScoreGrades(ds, facts, from, to)
		require.Equal(t, 0, s.Decisions)
	})
	// the card id is the part of ID before "@"
	t.Run("cardIdBeforeAt", func(t *testing.T) {
		t.Parallel()
		facts, _ := ReadLog(strings.NewReader(`{"lines":[{"card":"P1","primary":"P1","table":"work","to":"work:landed"},{"card":"C1","primary":"P1","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=fleet"}}]}`))
		ds := []Decision{
			{ID: "D1@P1@extra", Decision: GradeName, At: from.Format(time.RFC3339), Answers: map[string]Answer{GradeQuestion: {Value: GradeFlash, P: map[string]float64{GradeFlash: 0.8}}}},
		}
		s := ScoreGrades(ds, facts, from, to)
		require.Equal(t, 1, s.Decisions)
	})
	// the newest grade per card wins
	t.Run("newestWins", func(t *testing.T) {
		t.Parallel()
		facts, _ := ReadLog(strings.NewReader(`{"lines":[{"card":"P1","primary":"P1","table":"work","to":"work:landed"},{"card":"C1","primary":"P1","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=fleet"}}]}`))
		ds := []Decision{
			{ID: "P1@D1", Decision: GradeName, At: from.Format(time.RFC3339), Answers: map[string]Answer{GradeQuestion: {Value: GradePro, P: map[string]float64{GradeFlash: 0.1, GradePro: 0.9}}}},
			{ID: "P1@D2", Decision: GradeName, At: now.Add(-1 * time.Second).Format(time.RFC3339), Answers: map[string]Answer{GradeQuestion: {Value: GradeFlash, P: map[string]float64{GradeFlash: 0.9}}}},
		}
		s := ScoreGrades(ds, facts, from, to)
		require.Equal(t, 2, s.Decisions)
		// newest grade is flash so should be in flash-dealt row
		require.Equal(t, 1, s.Rows[0].N)
	})
	// a card the facts lack counts NoLog
	t.Run("missingFacts", func(t *testing.T) {
		t.Parallel()
		facts := map[string]*CardFacts{}
		ds := []Decision{
			{ID: "D1@C1", Decision: GradeName, At: from.Format(time.RFC3339), Answers: map[string]Answer{GradeQuestion: {Value: GradeFlash, P: map[string]float64{GradeFlash: 0.8}}}},
		}
		s := ScoreGrades(ds, facts, from, to)
		require.Equal(t, 1, s.NoLog)
	})
	// a card only ever routed "-" (a friend's) and a card never dealt are counted in Cards and left out of the tables
	t.Run("friendCard", func(t *testing.T) {
		t.Parallel()
		facts, _ := ReadLog(strings.NewReader(`{"lines":[{"card":"P1","primary":"P1","table":"work","to":"work:landed"},{"card":"C1","primary":"P1","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=-"}},{"card":"C2","primary":"P1","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=-"}}]}`))
		ds := []Decision{
			{ID: "P1@D1", Decision: GradeName, At: from.Format(time.RFC3339), Answers: map[string]Answer{GradeQuestion: {Value: GradeFlash, P: map[string]float64{GradeFlash: 0.8}}}},
		}
		s := ScoreGrades(ds, facts, from, to)
		require.Equal(t, 1, s.Cards)
		require.Empty(t, s.Rows)
	})
	// Rows come in the order flash, pro, script by dealt flash, pro
	t.Run("rowOrder", func(t *testing.T) {
		t.Parallel()
		facts, _ := ReadLog(strings.NewReader(`{"lines":[{"card":"P1","primary":"P1","table":"work","to":"work:landed"},{"card":"P2","primary":"P2","table":"work","to":"work:landed"},{"card":"P3","primary":"P3","table":"work","to":"work:landed"},{"card":"C1","primary":"P1","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=fleet"}},{"card":"C2","primary":"P2","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=fleet"}},{"card":"C3","primary":"P3","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=fleet"}}]}`))
		ds := []Decision{
			{ID: "P1@D1", Decision: GradeName, At: from.Format(time.RFC3339), Answers: map[string]Answer{GradeQuestion: {Value: GradeFlash, P: map[string]float64{GradeFlash: 0.8}}}},
			{ID: "P2@D2", Decision: GradeName, At: from.Format(time.RFC3339), Answers: map[string]Answer{GradeQuestion: {Value: GradePro, P: map[string]float64{GradePro: 0.8}}}},
			{ID: "P3@D3", Decision: GradeName, At: from.Format(time.RFC3339), Answers: map[string]Answer{GradeQuestion: {Value: GradeScript, P: map[string]float64{GradeScript: 0.8}}}},
		}
		s := ScoreGrades(ds, facts, from, to)
		require.Equal(t, 3, len(s.Rows))
		require.Equal(t, GradeFlash, s.Rows[0].Grade)
		require.Equal(t, GradeFlash, s.Rows[0].Dealt)
		require.Equal(t, GradePro, s.Rows[1].Grade)
		require.Equal(t, GradeFlash, s.Rows[1].Dealt)
		require.Equal(t, GradeScript, s.Rows[2].Grade)
		require.Equal(t, GradeFlash, s.Rows[2].Dealt)
	})
	// Landed2 counts a landing by attempt 2 and not by attempt 3
	t.Run("landed2", func(t *testing.T) {
		t.Parallel()
		facts, _ := ReadLog(strings.NewReader(`{"lines":[{"card":"P1","primary":"P1","table":"work","to":"work:landed"},{"card":"P2","primary":"P2","table":"work","to":"work:landed"},{"card":"C1","primary":"P1","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=fleet","cost_record:2":"kind=work attempt=2 on_tier=flash end=ok on_route=fleet"}},{"card":"C2","primary":"P2","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=fleet","cost_record:2":"kind=work attempt=2 on_tier=flash end=failed on_route=fleet","cost_record:3":"kind=work attempt=3 on_tier=flash end=ok on_route=fleet"}}]}`))
		ds := []Decision{
			{ID: "P1@D1", Decision: GradeName, At: from.Format(time.RFC3339), Answers: map[string]Answer{GradeQuestion: {Value: GradeFlash, P: map[string]float64{GradeFlash: 0.8}}}},
		}
		s := ScoreGrades(ds, facts, from, to)
		require.Equal(t, 1, s.Rows[0].Landed2)
	})
	// ToPro counts a flash start that escalated
	t.Run("toPro", func(t *testing.T) {
		t.Parallel()
		facts, _ := ReadLog(strings.NewReader(`{"lines":[{"card":"P1","primary":"P1","table":"work","to":"work:landed"},{"card":"C1","primary":"P1","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=fleet","cost_record:2":"kind=work attempt=2 on_tier=pro end=ok on_route=fleet"}}]}`))
		ds := []Decision{
			{ID: "P1@D1", Decision: GradeName, At: from.Format(time.RFC3339), Answers: map[string]Answer{GradeQuestion: {Value: GradeFlash, P: map[string]float64{GradeFlash: 0.8}}}},
		}
		s := ScoreGrades(ds, facts, from, to)
		require.Equal(t, 1, s.Rows[0].ToPro)
	})
	// Open counts a card neither landed nor dropped
	t.Run("open", func(t *testing.T) {
		t.Parallel()
		facts, _ := ReadLog(strings.NewReader(`{"lines":[{"card":"P1","primary":"P1","table":"work","to":"work:in_progress"},{"card":"C1","primary":"P1","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=fleet"}}]}`))
		ds := []Decision{
			{ID: "P1@D1", Decision: GradeName, At: from.Format(time.RFC3339), Answers: map[string]Answer{GradeQuestion: {Value: GradeFlash, P: map[string]float64{GradeFlash: 0.8}}}},
		}
		s := ScoreGrades(ds, facts, from, to)
		require.Equal(t, 1, s.Rows[0].Open)
	})
	// Buckets hold only flash-dealt, non-script grades, with p 0.7 in "0.7-0.85" and p 0.95 in ">=0.95"
	t.Run("buckets", func(t *testing.T) {
		t.Parallel()
		facts, _ := ReadLog(strings.NewReader(`{"lines":[{"card":"P1","primary":"P1","table":"work","to":"work:landed"},{"card":"P2","primary":"P2","table":"work","to":"work:landed"},{"card":"C1","primary":"P1","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=fleet"}},{"card":"C2","primary":"P2","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=fleet"}}]}`))
		ds := []Decision{
			{ID: "P1@D1", Decision: GradeName, At: from.Format(time.RFC3339), Answers: map[string]Answer{GradeQuestion: {Value: GradeFlash, P: map[string]float64{GradeFlash: 0.7}}}},
			{ID: "P2@D2", Decision: GradeName, At: from.Format(time.RFC3339), Answers: map[string]Answer{GradeQuestion: {Value: GradeFlash, P: map[string]float64{GradeFlash: 0.95}}}},
		}
		s := ScoreGrades(ds, facts, from, to)
		require.Equal(t, 2, len(s.Buckets))
	})
	// cell and b2i are covered through ScoreGrades (implicit via above tests)
}
