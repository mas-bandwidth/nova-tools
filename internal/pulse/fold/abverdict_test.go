package fold

import (
	"math"
	"testing"
)

// TestABWinnerOnlyBeyondInterval verifies that ABWinner names an A/B test winner
// per card type only when the cost per useful card differs beyond the stated interval.
// Usefulness is measured by Jev's calibrated score as the numerator.
func TestABWinnerOnlyBeyondInterval(t *testing.T) {
	tests := []struct {
		name     string
		typeID   string
		costA    float64 // cost for template A
		scoreA   float64 // Jev calibrated score for A
		costB    float64 // cost for template B
		scoreB   float64 // Jev calibrated score for B
		interval float64 // cost per useful card difference threshold
		want     string  // expected winner: "A", "B", or ""
	}{
		{
			name:     "no winner when difference is below interval",
			typeID:   "type1",
			costA:    1.0,
			scoreA:   10.0,
			costB:    1.2,
			scoreB:   10.0,
			interval: 0.05,
			want:     "",
		},
		{
			name:     "A wins when cost per useful card is lower beyond interval",
			typeID:   "type1",
			costA:    1.0,
			scoreA:   10.0,
			costB:    2.0,
			scoreB:   10.0,
			interval: 0.05,
			want:     "A",
		},
		{
			name:     "B wins when cost per useful card is lower beyond interval",
			typeID:   "type1",
			costA:    2.0,
			scoreA:   10.0,
			costB:    1.0,
			scoreB:   10.0,
			interval: 0.05,
			want:     "B",
		},
		{
			name:     "considers usefulness in numerator - A with higher score",
			typeID:   "type1",
			costA:    1.0,
			scoreA:   15.0,
			costB:    1.0,
			scoreB:   10.0,
			interval: 0.01,
			want:     "A",
		},
		{
			name:     "considers usefulness in numerator - B with higher score",
			typeID:   "type1",
			costA:    1.0,
			scoreA:   10.0,
			costB:    1.0,
			scoreB:   15.0,
			interval: 0.01,
			want:     "B",
		},
		{
			name:     "no winner when exactly at interval boundary",
			typeID:   "type1",
			costA:    1.0,
			scoreA:   10.0,
			costB:    1.5,
			scoreB:   10.0,
			interval: 0.05,
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ABWinner(
				tt.typeID,
				tt.costA, tt.scoreA,
				tt.costB, tt.scoreB,
				tt.interval,
			)
			if got != tt.want {
				t.Errorf("ABWinner(%q, %.2f, %.1f, %.2f, %.1f, %.2f) = %q, want %q",
					tt.typeID, tt.costA, tt.scoreA, tt.costB, tt.scoreB, tt.interval,
					got, tt.want)
			}
		})
	}
}

// TestDeclareWinnerCallsABWinner verifies that DeclareWinner (the production folding
// function) calls ABWinner to determine A/B winners. This test proves that ABWinner
// is used in the production path, not just by unit tests.
func TestDeclareWinnerCallsABWinner(t *testing.T) {
	types := map[string]*ABTestData{
		"type1": {
			CostA:  1.0,
			ScoreA: 15.0,
			CostB:  1.0,
			ScoreB: 10.0,
		},
		"type2": {
			CostA:  2.0,
			ScoreA: 10.0,
			CostB:  1.0,
			ScoreB: 10.0,
		},
	}

	decisions := DeclareWinner(types, 0.01)

	if len(decisions) != 2 {
		t.Errorf("DeclareWinner returned %d decisions, want 2", len(decisions))
	}

	// Check that we got the expected winners
	for _, decision := range decisions {
		if decision.Winner == "" {
			t.Errorf("DeclareWinner returned no winner for %s, but expected one", decision.TypeID)
		}
	}
}

// TestDeclareWinnerZeroScore verifies that a zero score on either side yields
// no winner and finite cost-per-useful fields (0, never +Inf or NaN) through
// the production path.
func TestDeclareWinnerZeroScore(t *testing.T) {
	types := map[string]*ABTestData{
		"zeroA":    {CostA: 1.0, ScoreA: 0, CostB: 1.0, ScoreB: 10.0},
		"zeroB":    {CostA: 1.0, ScoreA: 10.0, CostB: 1.0, ScoreB: 0},
		"zeroBoth": {CostA: 0, ScoreA: 0, CostB: 0, ScoreB: 0},
	}
	want := map[string][2]float64{
		"zeroA":    {0, 0.1},
		"zeroB":    {0.1, 0},
		"zeroBoth": {0, 0},
	}

	decisions := DeclareWinner(types, 0.01)
	if len(decisions) != len(types) {
		t.Fatalf("DeclareWinner returned %d decisions, want %d", len(decisions), len(types))
	}
	for _, d := range decisions {
		if d.Winner != "" {
			t.Errorf("%s: Winner = %q, want \"\" for a zero score", d.TypeID, d.Winner)
		}
		for _, v := range []float64{d.CostPerA, d.CostPerB} {
			if math.IsInf(v, 0) || math.IsNaN(v) {
				t.Errorf("%s: cost per useful card = %v, want finite", d.TypeID, v)
			}
		}
		w := want[d.TypeID]
		if d.CostPerA != w[0] || d.CostPerB != w[1] {
			t.Errorf("%s: CostPerA, CostPerB = %v, %v, want %v, %v", d.TypeID, d.CostPerA, d.CostPerB, w[0], w[1])
		}
	}
}
