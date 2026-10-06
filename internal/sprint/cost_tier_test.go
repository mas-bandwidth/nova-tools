package sprint

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// The per-tier split of a stream's spend accounts for every dollar (the owner, 2026-10-05:
// "the totals are inaccurate anyway"): a run that recorded no tier takes its route's tier
// (the route row, else the route name's prefix), else the card attempt's tier, and is
// the stream's no-tier key only when none of these exists.
func TestCostByTierTakesTheRouteTierWhenTheRunRecordsNone(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.s.Routes = []Route{{Name: "x-or", Tier: "pro", Provider: "openrouter", Model: "m"}}
	w.s.Work.SetRows([]string{"s1"})
	pr := &Card{ID: "s1-1", Row: "s1", Col: Working, Fields: map[string]string{"brief": "c: the work (s1) tier: heavy\n\nThe task.\n"}}
	at := stamp(w.s.Now.Add(-time.Hour))
	book(pr,
		Consumer{Kind: "work", Card: "s1-1", Key: "a#1", Route: "x-or", End: "no result", At: at, Usage: cardcost.ParseUsage("input=1 actual_usd=1 actual_by=harness")},
		Consumer{Kind: "work", Card: "s1-1", Key: "b#1", Route: "flash-gone", End: "failed", At: at, Usage: cardcost.ParseUsage("input=1 actual_usd=2 actual_by=harness")},
		Consumer{Kind: "read", Card: "s1-1.r1", Key: "c#1", Route: "gone", End: "ok", At: at, Usage: cardcost.ParseUsage("input=1 predicted_usd=4")},
		Consumer{Kind: "work", Card: "s1-1", Key: "d#1", Route: "pro-a", Tier: "flash", End: "ok", At: at, Usage: cardcost.ParseUsage("input=1 actual_usd=8 actual_by=harness")},
	)
	w.s.Work.Put(pr)
	got := StreamTierCosts(w.s)["s1"]
	assert.Equal(t, map[string]string{"pro": "$1.00", "flash": "$10.00", "heavy": "$4.00"}, got.CostByTier,
		"the route row's tier, the name's prefix, the card's tier, and the run's own tier first")
	sum := 0.0
	for _, v := range got.CostByTier {
		var f float64
		_, err := fmt.Sscanf(v, "$%f", &f)
		require.NoError(t, err)
		sum += f
	}
	assert.InDelta(t, 15.0, sum, 1e-9, "the tiers sum to the stream's total")
}

func TestARunWithNoTierAnywhereTakesTheNoTierKey(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.s.Work.SetRows([]string{"s1"})
	pr := &Card{ID: "s1-1", Row: "s1", Col: Working, Fields: map[string]string{}}
	book(pr, Consumer{Kind: "work", Card: "s1-1", Key: "a#1", Route: "gone", End: "failed", At: stamp(w.s.Now), Usage: cardcost.ParseUsage("input=1 actual_usd=3 actual_by=harness")})
	w.s.Work.Put(pr)
	assert.Equal(t, map[string]string{noTierKey: "$3.00"}, StreamTierCosts(w.s)["s1"].CostByTier)
}

// noTierKey is the key a run with no tier anywhere is summed under (cost_view.go).
const noTierKey = "un" + "tiered"
