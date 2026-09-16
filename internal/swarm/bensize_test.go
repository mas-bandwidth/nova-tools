package swarm

import (
	"testing"
)

// rounds turns a map from W to the round it measured into the measure callback
// SizeWidth drives, so a fake bench is a table of rounds.
func rounds(rs map[int]SizeRound) func(int) SizeRound {
	return func(w int) SizeRound { return rs[w] }
}

// The doubling loop keeps going while the three rules hold and stops at the first
// round that breaks one (SPEC-SWARM, "Benches", replay 19).
func TestSizeDoublesUntilARuleBreaks(t *testing.T) {
	cores := 8
	max := 64

	t.Run("load-crosses-at-16", func(t *testing.T) {
		// One-minute load 8 at W=8 holds (8 <= 1.25*8 = 10); 16 at W=16 breaks.
		m := rounds(map[int]SizeRound{
			1:  {Load: 1, CardsPerMin: 10},
			2:  {Load: 2, CardsPerMin: 20},
			4:  {Load: 4, CardsPerMin: 40},
			8:  {Load: 8, CardsPerMin: 80},
			16: {Load: 16, CardsPerMin: 160},
		})
		if w := SizeWidth(max, cores, m); w != 8 {
			t.Fatalf("load crossing 1.25x cores at W=16 records width=8, got %d", w)
		}
	})

	t.Run("throughput-under-scale-at-8", func(t *testing.T) {
		// Cards per minute at 8 (500) is under 1.5x cards per minute at 4 (400).
		m := rounds(map[int]SizeRound{
			1: {CardsPerMin: 100},
			2: {CardsPerMin: 200},
			4: {CardsPerMin: 400},
			8: {CardsPerMin: 500},
		})
		if w := SizeWidth(max, cores, m); w != 4 {
			t.Fatalf("throughput under 1.5x at W=8 records width=4, got %d", w)
		}
	})

	t.Run("abstain-ends-doubling", func(t *testing.T) {
		// A card abstaining at W=2 stops the doubling there: width stays 1.
		m := rounds(map[int]SizeRound{
			1: {CardsPerMin: 100},
			2: {CardsPerMin: 200, Abstains: 1},
		})
		if w := SizeWidth(max, cores, m); w != 1 {
			t.Fatalf("one abstain at W=2 records width=1, got %d", w)
		}
	})
}
