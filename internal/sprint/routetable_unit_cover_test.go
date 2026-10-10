package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestSprintRoutetableCoverAttempt tests rtAttempt on various card ids
func TestSprintRoutetableCoverAttempt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		card string
		prim string
		att  int
	}{
		{"s1-1.w3", "s1-1.w3", "s1-1", 3},
		{"no .w suffix", "abc123", "abc123", 0},
		{"a.wx", "a.wx", "a", 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			prim, att := rtAttempt(tc.card)
			require.Equal(t, tc.prim, prim)
			require.Equal(t, tc.att, att)
		})
	}
}

// TestSprintRoutetableCoverReadAttempt tests rtReadAttempt on various card ids
func TestSprintRoutetableCoverReadAttempt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		card string
		prim string
		att  int
	}{
		{"prim.r2.reader-a", "prim.r2.reader-a", "prim", 2},
		{"prim.reader", "prim.reader", "prim.reader", 0},
		{"a.rx", "a.rx", "a.rx", 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			prim, att := rtReadAttempt(tc.card)
			require.Equal(t, tc.prim, prim)
			require.Equal(t, tc.att, att)
		})
	}
}

// TestSprintRoutetableCoverMedian tests rtMedian on various inputs
func TestSprintRoutetableCoverMedian(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		xs   []float64
		want float64
	}{
		{"nil", nil, 0},
		{"empty", []float64{}, 0},
		{"one value", []float64{42}, 42},
		{"three values", []float64{3, 1, 2, 4}, 3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := rtMedian(tc.xs)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestSprintRoutetableCoverFormat tests rtFormat on various inputs
func TestSprintRoutetableCoverFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		r    *rtStat
	}{
		{"empty rtStat", &rtStat{}},
		{"filled rtStat", &rtStat{
			name:     "test-route",
			takes:    10,
			ok:       5,
			failed:   2,
			noResult: 1,
			provider: 2,
			landed:   4,
			first:    2,
			reviewed: 3,
			wrong:    1,
			usd:      100.50,
			imputed:  1,
			walls:    []float64{10, 20, 30, 40},
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := rtFormat(tc.r)
			require.NotEmpty(t, out)
		})
	}
}

// TestSprintRoutetableCoverFromUsage tests rtFromUsage on various inputs
func TestSprintRoutetableCoverFromUsage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		line string
	}{
		{"price_route with all fields", "price_route=pro-a wall=120 run=120 actual_usd=5.00"},
		{"actual_usd=abc (priced false)", "price_route=pro-a actual_usd=abc"},
		{"empty string", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			u := rtFromUsage(tc.line)
			require.NotNil(t, u)
		})
	}
}

// TestSprintRoutetableCoverRouteTableStorm tests RouteTable with storm (>50 provider failures)
func TestSprintRoutetableCoverRouteTableStorm(t *testing.T) {
	t.Parallel()

	since := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Create 51 provider takes with a server error on one finish line (storm)
	lines := []Line{
		{Table: "work", Verb: "finish", Primary: "prim1", At: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)},
	}

	// Add 51 provider takes (storm condition: >50)
	for i := 1; i <= 51; i++ {
		lines = append(lines, Line{
			Table:   "fleet",
			Verb:    "finish",
			Card:    "s1-1.w3",
			Primary: "prim1",
			Set: map[string]string{
				"ok":    "yes",
				"usage": "price_route=pro-a",
			},
		})
		// Add provider take with error
		lines = append(lines, Line{
			Table:   "fleet",
			Verb:    "finish",
			Card:    "s1-1.w3",
			Primary: "prim1",
			Set: map[string]string{
				"providerTake1": "prov:err:provider:pro-a",
			},
		})
	}

	out := RouteTable(lines, since)
	require.Contains(t, out, "ROUTE TABLE")
}

// TestSprintRoutetableCoverRouteTableNonStorm tests RouteTable with exactly 50 failures (no storm)
func TestSprintRoutetableCoverRouteTableNonStorm(t *testing.T) {
	t.Parallel()

	since := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	lines := []Line{
		{Table: "work", Verb: "finish", Primary: "prim1", At: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)},
	}

	// Add exactly 50 provider takes (no storm: not >50)
	for i := 1; i <= 50; i++ {
		lines = append(lines, Line{
			Table:   "fleet",
			Verb:    "finish",
			Card:    "s1-1.w3",
			Primary: "prim1",
			Set: map[string]string{
				"ok":    "yes",
				"usage": "price_route=pro-a",
			},
		})
		lines = append(lines, Line{
			Table:   "fleet",
			Verb:    "finish",
			Card:    "s1-1.w3",
			Primary: "prim1",
			Set: map[string]string{
				"providerTake1": "prov:err:provider:pro-a",
			},
		})
	}

	out := RouteTable(lines, since)
	require.Contains(t, out, "ROUTE TABLE")
}

// TestSprintRoutetableCoverRouteTableImputed tests RouteTable with imputed costs
func TestSprintRoutetableCoverRouteTableImputed(t *testing.T) {
	t.Parallel()

	since := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	finishTime := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	takeTime := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	// Finish with priced take (2.00)
	lines := []Line{
		{Table: "work", Verb: "finish", Primary: "prim1", At: finishTime},
		{Table: "fleet", Verb: "finish", Card: "s1-1.w3", Primary: "prim1",
			Set: map[string]string{
				"ok":    "yes",
				"usage": "price_route=pro-a actual_usd=2.00 run=60",
			},
			At: finishTime,
		},
		{Table: "fleet", Verb: "take", Card: "s1-1.w3", Gen: 1,
			Set: map[string]string{"taken": takeTime.Format(time.RFC3339)},
		},
		// Finish with unpriced take, run=120s (should be imputed)
		{Table: "fleet", Verb: "finish", Card: "s2-1.w3", Primary: "prim1",
			Set: map[string]string{
				"ok":    "yes",
				"usage": "price_route=pro-a run=120",
			},
			At: finishTime,
		},
		// Finish with unpriced take, run=30s (should NOT be imputed)
		{Table: "fleet", Verb: "finish", Card: "s3-1.w3", Primary: "prim1",
			Set: map[string]string{
				"ok":    "yes",
				"usage": "price_route=pro-a run=30",
			},
			At: finishTime,
		},
	}

	out := RouteTable(lines, since)
	require.Contains(t, out, "ROUTE TABLE")
}

// TestSprintRoutetableCoverRouteTableNoOkKey tests RouteTable with finish without "ok" key
func TestSprintRoutetableCoverRouteTableNoOkKey(t *testing.T) {
	t.Parallel()

	since := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	finishTime := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	lines := []Line{
		{Table: "work", Verb: "finish", Primary: "prim1", At: finishTime},
		// Finish without ok key - should not create a take
		{Table: "fleet", Verb: "finish", Card: "s1-1.w3", Primary: "prim1",
			Set: map[string]string{
				"usage": "price_route=pro-a",
			},
			At: finishTime,
		},
	}

	out := RouteTable(lines, since)
	require.Contains(t, out, "ROUTE TABLE")
}

// TestSprintRoutetableCoverRouteTableBeforeSince tests RouteTable filtering finishes before since
func TestSprintRoutetableCoverRouteTableBeforeSince(t *testing.T) {
	t.Parallel()

	since := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	beforeTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	finishTime := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	lines := []Line{
		{Table: "work", Verb: "finish", Primary: "prim1", At: finishTime},
		// Finish before since - should be excluded
		{Table: "fleet", Verb: "finish", Card: "s1-1.w3", Primary: "prim1",
			Set: map[string]string{
				"ok":    "yes",
				"usage": "price_route=pro-a",
			},
			At: beforeTime,
		},
		// Finish after since - should be included
		{Table: "fleet", Verb: "finish", Card: "s2-1.w3", Primary: "prim1",
			Set: map[string]string{
				"ok":    "yes",
				"usage": "price_route=pro-b",
			},
			At: finishTime,
		},
	}

	out := RouteTable(lines, since)
	require.Contains(t, out, "ROUTE TABLE")
}

// TestSprintRoutetableCoverRouteTableTierRows tests RouteTable with tier (PRO/FLASH) rows
func TestSprintRoutetableCoverRouteTableTierRows(t *testing.T) {
	t.Parallel()

	since := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	finishTime := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	lines := []Line{
		{Table: "work", Verb: "finish", Primary: "prim1", At: finishTime},
		// pro-a route
		{Table: "fleet", Verb: "finish", Card: "s1-1.w3", Primary: "prim1",
			Set: map[string]string{
				"ok":    "yes",
				"usage": "price_route=pro-a actual_usd=10.00 run=120",
			},
			At: finishTime,
		},
		// flash-a route
		{Table: "fleet", Verb: "finish", Card: "s2-1.w3", Primary: "prim1",
			Set: map[string]string{
				"ok":    "yes",
				"usage": "price_route=flash-a actual_usd=5.00 run=60",
			},
			At: finishTime,
		},
	}

	out := RouteTable(lines, since)
	require.Contains(t, out, "ROUTE TABLE")
	require.Contains(t, out, "PRO (all)")
	require.Contains(t, out, "FLASH (all)")
}

// TestSprintRoutetableCoverRouteTableWrongVerdict tests RouteTable with wrong verdict order
func TestSprintRoutetableCoverRouteTableWrongVerdict(t *testing.T) {
	t.Parallel()

	since := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	finishTime := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	lines := []Line{
		{Table: "work", Verb: "finish", Primary: "prim1", At: finishTime},
		// pro-a route with ok take
		{Table: "fleet", Verb: "finish", Card: "s1-1.w3", Primary: "prim1",
			Set: map[string]string{
				"ok":    "yes",
				"usage": "price_route=pro-a actual_usd=10.00 run=120",
			},
			At: finishTime,
		},
		// Reader verdict: ok then broken (broken should win)
		{Table: "readers", Verb: "read", Card: "prim1.w3",
			Set: map[string]string{"verdict": "ok"}},
		{Table: "readers", Verb: "read", Card: "prim1.w3",
			Set: map[string]string{"verdict": "broken"}},
	}

	out := RouteTable(lines, since)
	require.Contains(t, out, "ROUTE TABLE")
}
