package decide

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// coverGradeDecision builds a grade Decision answering value with probability p in the
// shape ScoreGrades reads: Answers[GradeQuestion] = Answer{Value, P}.
func coverGradeDecision(id, at, value string, p float64) Decision {
	return Decision{
		ID:       id,
		Decision: GradeName,
		At:       at,
		Answers:  map[string]Answer{GradeQuestion: {Value: value, P: map[string]float64{value: p}}},
	}
}

// TestDecideGradescoreCoverReadLog pins ReadLog: a non-export is refused with the
// nova-sprint log sentence, a line with no primary is skipped, a work-table line places
// the primary, a removed line with outcome=dropped settles LabelDropped, another card's
// cost_record still counts, and a card never placed settles "unplaced".
func TestDecideGradescoreCoverReadLog(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		log         string
		wantErr     bool
		errContains string
		wantEmpty   bool
		primary     string
		wantState   string
		wantMax     int
		wantStart   string
	}{
		{
			name:        "not JSON is refused",
			log:         "not json",
			wantErr:     true,
			errContains: "not a nova-sprint log --json export",
		},
		{
			name:      "a line with no primary is skipped",
			log:       `{"lines":[{"card":"C1","table":"work","to":"landed"}]}`,
			wantEmpty: true,
		},
		{
			name:      "a work-table line places the primary",
			log:       `{"lines":[{"card":"P1","primary":"P1","table":"work","to":"work:landed"}]}`,
			primary:   "P1",
			wantState: "landed",
		},
		{
			name:      "a removed line with outcome dropped is LabelDropped",
			log:       `{"lines":[{"card":"P1","primary":"P1","table":"work","to":"work:landed","removed":true,"set":{"outcome":"dropped"}}]}`,
			primary:   "P1",
			wantState: LabelDropped,
		},
		{
			name:      "another card's cost_record still counts",
			log:       `{"lines":[{"card":"P1","primary":"P1","table":"work","to":"work:landed"},{"card":"C1","primary":"P1","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=-"}}]}`,
			primary:   "P1",
			wantState: "landed",
			wantMax:   1,
			wantStart: GradeFlash,
		},
		{
			name:      "a card never placed settles unplaced",
			log:       `{"lines":[{"card":"P1","primary":"P1","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=-"}}]}`,
			primary:   "P1",
			wantState: "unplaced",
			wantMax:   1,
			wantStart: GradeFlash,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			facts, err := ReadLog(strings.NewReader(tc.log))
			if tc.wantErr {
				require.Error(t, err)
				require.Nil(t, facts)
				assert.Contains(t, err.Error(), tc.errContains)
				return
			}
			require.NoError(t, err)
			if tc.wantEmpty {
				assert.Empty(t, facts)
				return
			}
			f := facts[tc.primary]
			require.NotNil(t, f)
			if tc.wantState != "" {
				assert.Equal(t, tc.wantState, f.State)
			}
			if tc.wantMax != 0 {
				assert.Equal(t, tc.wantMax, f.MaxAttempt)
			}
			if tc.wantStart != "" {
				assert.Equal(t, tc.wantStart, f.Start)
			}
		})
	}
}

// TestDecideGradescoreCoverParseCost pins parseCost: a kind=work record carries attempt,
// on_tier, end and on_route; any other kind has attempt 0; a non-numeric attempt is 0.
func TestDecideGradescoreCoverParseCost(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		record string
		want   costRecord
	}{
		{
			name:   "a work record carries attempt tier end route",
			record: "kind=work attempt=1 on_tier=flash end=ok on_route=-",
			want:   costRecord{attempt: 1, tier: GradeFlash, end: "ok", route: "-"},
		},
		{
			name:   "another kind has attempt 0",
			record: "kind=gate attempt=1 on_tier=flash end=ok",
			want:   costRecord{tier: GradeFlash, end: "ok"},
		},
		{
			name:   "a non-numeric attempt is 0",
			record: "kind=work attempt=bad on_tier=flash end=ok",
			want:   costRecord{tier: GradeFlash, end: "ok"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, parseCost(tc.record))
		})
	}
}

// TestDecideGradescoreCoverSettle pins settle: the state defaults to "unplaced",
// MaxAttempt is the highest attempt, any pro attempt escalates, the fleet only counts a
// route other than "-", and Start and FirstFailed come from attempt 1.
func TestDecideGradescoreCoverSettle(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		attempts      map[int]costRecord
		wantMax       int
		wantEscalated bool
		wantDealt     bool
		wantStart     string
		wantFailed    bool
	}{
		{
			name:      "max attempt is the highest",
			attempts:  map[int]costRecord{1: {attempt: 1, tier: GradeFlash, end: "ok", route: "-"}, 2: {attempt: 2, tier: GradeFlash, end: "ok", route: "fleet"}},
			wantMax:   2,
			wantDealt: true,
			wantStart: GradeFlash,
		},
		{
			name:          "pro on any attempt escalates",
			attempts:      map[int]costRecord{1: {attempt: 1, tier: GradeFlash, end: "failed", route: "fleet"}, 2: {attempt: 2, tier: GradePro, end: "ok", route: "fleet"}},
			wantMax:       2,
			wantEscalated: true,
			wantDealt:     true,
			wantStart:     GradeFlash,
			wantFailed:    true,
		},
		{
			name:      "a dash route is not dealt on the fleet",
			attempts:  map[int]costRecord{1: {attempt: 1, tier: GradeFlash, end: "ok", route: "-"}},
			wantMax:   1,
			wantStart: GradeFlash,
		},
		{
			name:      "a non-dash route is dealt on the fleet",
			attempts:  map[int]costRecord{1: {attempt: 1, tier: GradeFlash, end: "ok", route: "fleet"}},
			wantMax:   1,
			wantDealt: true,
			wantStart: GradeFlash,
		},
		{
			name:          "first failed comes from attempt 1",
			attempts:      map[int]costRecord{1: {attempt: 1, tier: GradePro, end: "failed", route: "fleet"}},
			wantMax:       1,
			wantEscalated: true,
			wantDealt:     true,
			wantStart:     GradePro,
			wantFailed:    true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &CardFacts{attempts: tc.attempts}
			f.settle()
			assert.Equal(t, "unplaced", f.State)
			assert.Equal(t, tc.wantMax, f.MaxAttempt)
			assert.Equal(t, tc.wantEscalated, f.Escalated)
			assert.Equal(t, tc.wantDealt, f.dealtOnFleet)
			assert.Equal(t, tc.wantStart, f.Start)
			assert.Equal(t, tc.wantFailed, f.FirstFailed)
		})
	}
}

// TestDecideGradescoreCoverScoreGrades pins ScoreGrades end to end: the [from, to)
// window, the skip rules, the card id before "@", the newest grade per card winning,
// NoLog, the friend's/never-dealt exclusions, row and bucket order, Landed2, ToPro,
// Dropped and Open. cell and b2i are covered through these tables.
func TestDecideGradescoreCoverScoreGrades(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	from := now.Add(-24 * time.Hour)
	to := now
	at := func(d time.Duration) string { return from.Add(d).Format(time.RFC3339) }

	// one card P1 landed on flash, on the fleet, by attempt 1
	placed := `{"lines":[{"card":"P1","primary":"P1","table":"work","to":"work:landed"},{"card":"C1","primary":"P1","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=fleet"}}]}`

	for _, tc := range []struct {
		name      string
		log       string
		decisions []Decision
		want      GradeScore
	}{
		{
			name: "from is in and to is out",
			log:  placed,
			decisions: []Decision{
				coverGradeDecision("P1@D1", at(0), GradeFlash, 0.8),
				coverGradeDecision("P1@D2", at(24*time.Hour), GradeFlash, 0.9),
			},
			want: GradeScore{
				Decisions: 1, Cards: 1,
				Rows:    []GradeRow{{Grade: GradeFlash, Dealt: GradeFlash, N: 1, Landed2: 1, Landed: 1}},
				Buckets: []BucketRow{{Grade: GradeFlash, Bucket: "0.7-0.85", N: 1, Landed2: 1}},
			},
		},
		{
			name:      "an unparseable At is skipped",
			log:       placed,
			decisions: []Decision{coverGradeDecision("P1@D1", "not-a-date", GradeFlash, 0.8)},
			want:      GradeScore{},
		},
		{
			name:      "a decision that is not the grade name is skipped",
			log:       placed,
			decisions: []Decision{{ID: "P1@D1", Decision: "attempt", At: at(0), Answers: map[string]Answer{GradeQuestion: {Value: GradeFlash, P: map[string]float64{GradeFlash: 0.8}}}}},
			want:      GradeScore{},
		},
		{
			name:      "the card id is the part before @",
			log:       placed,
			decisions: []Decision{coverGradeDecision("P1@D1@extra", at(0), GradeFlash, 0.8)},
			want: GradeScore{
				Decisions: 1, Cards: 1,
				Rows:    []GradeRow{{Grade: GradeFlash, Dealt: GradeFlash, N: 1, Landed2: 1, Landed: 1}},
				Buckets: []BucketRow{{Grade: GradeFlash, Bucket: "0.7-0.85", N: 1, Landed2: 1}},
			},
		},
		{
			name: "the newest grade per card wins",
			log:  placed,
			decisions: []Decision{
				coverGradeDecision("P1@D1", at(0), GradePro, 0.9),
				coverGradeDecision("P1@D2", at(time.Hour), GradeFlash, 0.9),
			},
			want: GradeScore{
				Decisions: 2, Cards: 1,
				Rows:    []GradeRow{{Grade: GradeFlash, Dealt: GradeFlash, N: 1, Landed2: 1, Landed: 1}},
				Buckets: []BucketRow{{Grade: GradeFlash, Bucket: "0.85-0.95", N: 1, Landed2: 1}},
			},
		},
		{
			name:      "a card the facts lack counts NoLog",
			log:       `{"lines":[]}`,
			decisions: []Decision{coverGradeDecision("D1@C1", at(0), GradeFlash, 0.8)},
			want:      GradeScore{Decisions: 1, Cards: 1, NoLog: 1},
		},
		{
			name: "a friend's and a never-dealt card count but are not tabled",
			log:  `{"lines":[{"card":"P1","primary":"P1","table":"work","to":"work:landed"},{"card":"P2","primary":"P2","table":"work","to":"work:landed"},{"card":"P3","primary":"P3","table":"work","to":"work:landed"},{"card":"C1","primary":"P1","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=fleet"}},{"card":"C2","primary":"P2","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=-"}}]}`,
			decisions: []Decision{
				coverGradeDecision("P1@D1", at(0), GradeFlash, 0.8),
				coverGradeDecision("P2@D2", at(0), GradeFlash, 0.8),
				coverGradeDecision("P3@D3", at(0), GradeFlash, 0.8),
			},
			want: GradeScore{
				Decisions: 3, Cards: 3,
				Rows:    []GradeRow{{Grade: GradeFlash, Dealt: GradeFlash, N: 1, Landed2: 1, Landed: 1}},
				Buckets: []BucketRow{{Grade: GradeFlash, Bucket: "0.7-0.85", N: 1, Landed2: 1}},
			},
		},
		{
			name: "rows are flash, pro, script by dealt flash, pro",
			log:  `{"lines":[{"card":"P1","primary":"P1","table":"work","to":"work:landed"},{"card":"P2","primary":"P2","table":"work","to":"work:landed"},{"card":"P3","primary":"P3","table":"work","to":"work:landed"},{"card":"P4","primary":"P4","table":"work","to":"work:landed"},{"card":"P5","primary":"P5","table":"work","to":"work:landed"},{"card":"C1","primary":"P1","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=fleet"}},{"card":"C2","primary":"P2","set":{"cost_record:1":"kind=work attempt=1 on_tier=pro end=ok on_route=fleet"}},{"card":"C3","primary":"P3","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=fleet"}},{"card":"C4","primary":"P4","set":{"cost_record:1":"kind=work attempt=1 on_tier=pro end=ok on_route=fleet"}},{"card":"C5","primary":"P5","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=fleet"}}]}`,
			decisions: []Decision{
				coverGradeDecision("P1@D1", at(0), GradeFlash, 0.8),
				coverGradeDecision("P2@D2", at(0), GradeFlash, 0.8),
				coverGradeDecision("P3@D3", at(0), GradePro, 0.8),
				coverGradeDecision("P4@D4", at(0), GradePro, 0.8),
				coverGradeDecision("P5@D5", at(0), GradeScript, 0.8),
			},
			want: GradeScore{
				Decisions: 5, Cards: 5,
				Rows: []GradeRow{
					{Grade: GradeFlash, Dealt: GradeFlash, N: 1, Landed2: 1, Landed: 1},
					{Grade: GradeFlash, Dealt: GradePro, N: 1, Landed2: 1, Landed: 1},
					{Grade: GradePro, Dealt: GradeFlash, N: 1, Landed2: 1, Landed: 1},
					{Grade: GradePro, Dealt: GradePro, N: 1, Landed2: 1, Landed: 1},
					{Grade: GradeScript, Dealt: GradeFlash, N: 1, Landed2: 1, Landed: 1},
				},
				Buckets: []BucketRow{
					{Grade: GradeFlash, Bucket: "0.7-0.85", N: 1, Landed2: 1},
					{Grade: GradePro, Bucket: "0.7-0.85", N: 1, Landed2: 1},
				},
			},
		},
		{
			name: "Landed2 counts a landing by attempt 2 and not by attempt 3",
			log:  `{"lines":[{"card":"P1","primary":"P1","table":"work","to":"work:landed"},{"card":"P2","primary":"P2","table":"work","to":"work:landed"},{"card":"C1","primary":"P1","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=fleet","cost_record:2":"kind=work attempt=2 on_tier=flash end=ok on_route=fleet"}},{"card":"C2","primary":"P2","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=fleet","cost_record:2":"kind=work attempt=2 on_tier=flash end=failed on_route=fleet","cost_record:3":"kind=work attempt=3 on_tier=flash end=ok on_route=fleet"}}]}`,
			decisions: []Decision{
				coverGradeDecision("P1@D1", at(0), GradeFlash, 0.8),
				coverGradeDecision("P2@D2", at(0), GradeFlash, 0.8),
			},
			want: GradeScore{
				Decisions: 2, Cards: 2,
				Rows:    []GradeRow{{Grade: GradeFlash, Dealt: GradeFlash, N: 2, Landed2: 1, Landed: 2}},
				Buckets: []BucketRow{{Grade: GradeFlash, Bucket: "0.7-0.85", N: 2, Landed2: 1}},
			},
		},
		{
			name: "ToPro counts a flash start that escalated",
			log:  `{"lines":[{"card":"P1","primary":"P1","table":"work","to":"work:landed"},{"card":"C1","primary":"P1","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=fleet","cost_record:2":"kind=work attempt=2 on_tier=pro end=ok on_route=fleet"}}]}`,
			decisions: []Decision{
				coverGradeDecision("P1@D1", at(0), GradeFlash, 0.8),
			},
			want: GradeScore{
				Decisions: 1, Cards: 1,
				Rows:    []GradeRow{{Grade: GradeFlash, Dealt: GradeFlash, N: 1, Landed2: 1, Landed: 1, ToPro: 1}},
				Buckets: []BucketRow{{Grade: GradeFlash, Bucket: "0.7-0.85", N: 1, Landed2: 1}},
			},
		},
		{
			name: "Open counts a card neither landed nor dropped",
			log:  `{"lines":[{"card":"P1","primary":"P1","table":"work","to":"work:in_progress"},{"card":"C1","primary":"P1","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=fleet"}}]}`,
			decisions: []Decision{
				coverGradeDecision("P1@D1", at(0), GradeFlash, 0.8),
			},
			want: GradeScore{
				Decisions: 1, Cards: 1,
				Rows:    []GradeRow{{Grade: GradeFlash, Dealt: GradeFlash, N: 1, Open: 1}},
				Buckets: []BucketRow{{Grade: GradeFlash, Bucket: "0.7-0.85", N: 1}},
			},
		},
		{
			name: "a dropped card counts Dropped and not Open",
			log:  `{"lines":[{"card":"P1","primary":"P1","table":"work","to":"work:landed","removed":true,"set":{"outcome":"dropped"}},{"card":"C1","primary":"P1","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=fleet"}}]}`,
			decisions: []Decision{
				coverGradeDecision("P1@D1", at(0), GradeFlash, 0.8),
			},
			want: GradeScore{
				Decisions: 1, Cards: 1,
				Rows:    []GradeRow{{Grade: GradeFlash, Dealt: GradeFlash, N: 1, Dropped: 1}},
				Buckets: []BucketRow{{Grade: GradeFlash, Bucket: "0.7-0.85", N: 1}},
			},
		},
		{
			name: "buckets hold flash-dealt non-script grades at their p",
			log:  `{"lines":[{"card":"P1","primary":"P1","table":"work","to":"work:landed"},{"card":"P2","primary":"P2","table":"work","to":"work:landed"},{"card":"P3","primary":"P3","table":"work","to":"work:landed"},{"card":"P4","primary":"P4","table":"work","to":"work:landed"},{"card":"C1","primary":"P1","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=fleet"}},{"card":"C2","primary":"P2","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=fleet"}},{"card":"C3","primary":"P3","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=ok on_route=fleet"}},{"card":"C4","primary":"P4","set":{"cost_record:1":"kind=work attempt=1 on_tier=pro end=ok on_route=fleet"}}]}`,
			decisions: []Decision{
				coverGradeDecision("P1@D1", at(0), GradeFlash, 0.7),
				coverGradeDecision("P2@D2", at(0), GradeFlash, 0.95),
				coverGradeDecision("P3@D3", at(0), GradeScript, 0.7),
				coverGradeDecision("P4@D4", at(0), GradeFlash, 0.7),
			},
			want: GradeScore{
				Decisions: 4, Cards: 4,
				Rows: []GradeRow{
					{Grade: GradeFlash, Dealt: GradeFlash, N: 2, Landed2: 2, Landed: 2},
					{Grade: GradeFlash, Dealt: GradePro, N: 1, Landed2: 1, Landed: 1},
					{Grade: GradeScript, Dealt: GradeFlash, N: 1, Landed2: 1, Landed: 1},
				},
				Buckets: []BucketRow{
					{Grade: GradeFlash, Bucket: "0.7-0.85", N: 1, Landed2: 1},
					{Grade: GradeFlash, Bucket: ">=0.95", N: 1, Landed2: 1},
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			facts, err := ReadLog(strings.NewReader(tc.log))
			require.NoError(t, err)
			assert.Equal(t, tc.want, ScoreGrades(tc.decisions, facts, from, to))
		})
	}
}
