package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// TestSprintCostSpendCoverRecordedSpendIn covers RecordedSpendIn.
func TestSprintCostSpendCoverRecordedSpendIn(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	from := t0
	to := t0.Add(2 * time.Hour)

	cases := []struct {
		name     string
		provider string
		want     float64
	}{
		{"openrouter sums actual", "openrouter", 1.5 + 0.5 + 0.3},
		{"deepseek sums actual", "deepseek", 2.5},
		{"unknown provider returns 0", "unknown", 0},
		{"empty provider returns 0", "", 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &Snapshot{
				Now:   t0,
				Fleet: NewTable(Fleet),
				Work:  NewTable(Work),
				Routes: []Route{
					{Name: "r-or", Provider: "openrouter"},
					{Name: "r-ds", Provider: "deepseek"},
				},
			}
			s.Work.SetRows([]string{"r1"})

			// Card 1: not sentinel, two records in window
			c1 := &Card{ID: "c1", Row: "r1", Col: Landed, Fields: map[string]string{"kind": "primary"}}
			book(c1,
				Consumer{Kind: "work", Card: "c1", Key: "c1#r1", Route: "r-or", End: "ok", At: from.Format(time.RFC3339), Usage: cardcost.ParseUsage("input=10 actual_usd=1.5")},
				Consumer{Kind: "work", Card: "c1", Key: "c1#r2", Route: "r-ds", End: "ok", At: to.Add(-1 * time.Minute).Format(time.RFC3339), Usage: cardcost.ParseUsage("input=10 actual_usd=2.5")},
			)
			s.Work.Put(c1)

			// Card 2: sentinel - should be ignored
			c2 := &Card{ID: "c2", Row: "r1", Col: Landed, Fields: map[string]string{"kind": "sentinel"}}
			book(c2,
				Consumer{Kind: "work", Card: "c2", Key: "c2#r1", Route: "r-or", End: "ok", At: from.Format(time.RFC3339), Usage: cardcost.ParseUsage("input=10 actual_usd=100")},
			)
			s.Work.Put(c2)

			// Card 3: subscription run - should be ignored
			c3 := &Card{ID: "c3", Row: "r1", Col: Landed, Fields: map[string]string{"kind": "primary"}}
			book(c3,
				Consumer{Kind: "work", Card: "c3", Key: "c3#r1", Route: "r-or", End: "ok", At: from.Format(time.RFC3339), Usage: cardcost.ParseUsage("input=10 unpriced=subscription")},
			)
			s.Work.Put(c3)

			// Card 4: record before from - should be ignored
			c4 := &Card{ID: "c4", Row: "r1", Col: Landed, Fields: map[string]string{"kind": "primary"}}
			book(c4,
				Consumer{Kind: "work", Card: "c4", Key: "c4#r1", Route: "r-or", End: "ok", At: from.Add(-1 * time.Minute).Format(time.RFC3339), Usage: cardcost.ParseUsage("input=10 actual_usd=50")},
			)
			s.Work.Put(c4)

			// Card 5: record at to - should be ignored (not in [from, to))
			c5 := &Card{ID: "c5", Row: "r1", Col: Landed, Fields: map[string]string{"kind": "primary"}}
			book(c5,
				Consumer{Kind: "work", Card: "c5", Key: "c5#r1", Route: "r-or", End: "ok", At: to.Format(time.RFC3339), Usage: cardcost.ParseUsage("input=10 actual_usd=50")},
			)
			s.Work.Put(c5)

			// Card 6: record with bad At - should be ignored
			c6 := &Card{ID: "c6", Row: "r1", Col: Landed, Fields: map[string]string{"kind": "primary"}}
			book(c6,
				Consumer{Kind: "work", Card: "c6", Key: "c6#r1", Route: "r-or", End: "ok", At: "bad-date", Usage: cardcost.ParseUsage("input=10 actual_usd=50")},
			)
			s.Work.Put(c6)

			// Card 7: record using predicted_usd (no actual) - should be counted
			c7 := &Card{ID: "c7", Row: "r1", Col: Landed, Fields: map[string]string{"kind": "primary"}}
			book(c7,
				Consumer{Kind: "work", Card: "c7", Key: "c7#r1", Route: "r-or", End: "ok", At: from.Add(30 * time.Minute).Format(time.RFC3339), Usage: cardcost.ParseUsage("input=10 predicted_usd=0.5")},
			)
			s.Work.Put(c7)

			// Card 8: record with provider/model when route is unknown
			c8 := &Card{ID: "c8", Row: "r1", Col: Landed, Fields: map[string]string{"kind": "primary"}}
			book(c8,
				Consumer{Kind: "work", Card: "c8", Key: "c8#r1", Model: "openrouter/m", End: "ok", At: from.Add(45 * time.Minute).Format(time.RFC3339), Usage: cardcost.ParseUsage("input=10 actual_usd=0.3")},
			)
			s.Work.Put(c8)

			got := RecordedSpendIn(s, tc.provider, from, to)
			assert.InDelta(t, tc.want, got, 0.0001, "RecordedSpendIn mismatch for %s", tc.provider)
		})
	}
}

// TestSprintCostSpendCoverRecordedProvidersIn covers RecordedProvidersIn.
func TestSprintCostSpendCoverRecordedProvidersIn(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	from := t0
	to := t0.Add(2 * time.Hour)

	s := &Snapshot{
		Now:   t0,
		Fleet: NewTable(Fleet),
		Work:  NewTable(Work),
		Routes: []Route{
			{Name: "r-or", Provider: "openrouter"},
			{Name: "r-ds", Provider: "deepseek"},
			{Name: "r-gm", Provider: "google"},
		},
	}
	s.Work.SetRows([]string{"r1"})

	// Card 1: priced records in window
	c1 := &Card{ID: "c1", Row: "r1", Col: Landed, Fields: map[string]string{"kind": "primary"}}
	book(c1,
		Consumer{Kind: "work", Card: "c1", Key: "c1#r1", Route: "r-or", End: "ok", At: from.Format(time.RFC3339), Usage: cardcost.ParseUsage("input=10 actual_usd=1.5")},
		Consumer{Kind: "work", Card: "c1", Key: "c1#r2", Model: "deepseek/m", End: "ok", At: to.Add(-1 * time.Minute).Format(time.RFC3339), Usage: cardcost.ParseUsage("input=10 actual_usd=2.5")},
	)
	s.Work.Put(c1)

	// Card 2: subscription run - should NOT add provider
	c2 := &Card{ID: "c2", Row: "r1", Col: Landed, Fields: map[string]string{"kind": "primary"}}
	book(c2,
		Consumer{Kind: "work", Card: "c2", Key: "c2#r1", Route: "r-or", End: "ok", At: from.Format(time.RFC3339), Usage: cardcost.ParseUsage("input=10 unpriced=subscription")},
	)
	s.Work.Put(c2)

	// Card 3: unpriced (not subscription) - should NOT add provider
	c3 := &Card{ID: "c3", Row: "r1", Col: Landed, Fields: map[string]string{"kind": "primary"}}
	book(c3,
		Consumer{Kind: "work", Card: "c3", Key: "c3#r1", Route: "r-or", End: "ok", At: from.Format(time.RFC3339), Usage: cardcost.ParseUsage("input=10 unpriced=no-tokens")},
	)
	s.Work.Put(c3)

	// Card 4: sentinel - should be ignored
	c4 := &Card{ID: "c4", Row: "r1", Col: Landed, Fields: map[string]string{"kind": "sentinel"}}
	book(c4,
		Consumer{Kind: "work", Card: "c4", Key: "c4#r1", Route: "r-or", End: "ok", At: from.Format(time.RFC3339), Usage: cardcost.ParseUsage("input=10 actual_usd=100")},
	)
	s.Work.Put(c4)

	want := []string{"deepseek", "google", "openrouter"}
	got := RecordedProvidersIn(s, from, to)
	assert.Equal(t, want, got)
}

// TestSprintCostSpendCoverRecordedTokensIn covers RecordedTokensIn.
func TestSprintCostSpendCoverRecordedTokensIn(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	from := t0
	to := t0.Add(2 * time.Hour)

	s := &Snapshot{
		Now:   t0,
		Fleet: NewTable(Fleet),
		Work:  NewTable(Work),
	}
	s.Work.SetRows([]string{"r1"})

	// Card 1: subscription records with different who
	c1 := &Card{ID: "c1", Row: "r1", Col: Landed, Fields: map[string]string{"kind": "primary"}}
	book(c1,
		Consumer{Kind: "work", Card: "c1", Key: "c1#r1", Who: "member-a", End: "ok", At: from.Format(time.RFC3339), Usage: cardcost.ParseUsage("input=100 output=50 unpriced=subscription")},
		Consumer{Kind: "work", Card: "c1", Key: "c1#r2", Who: "member-a", End: "ok", At: to.Add(-1 * time.Minute).Format(time.RFC3339), Usage: cardcost.ParseUsage("input=200 output=100 unpriced=subscription")},
	)
	s.Work.Put(c1)

	// Card 2: subscription record with no who - should use "-"
	c2 := &Card{ID: "c2", Row: "r1", Col: Landed, Fields: map[string]string{"kind": "primary"}}
	book(c2,
		Consumer{Kind: "work", Card: "c2", Key: "c2#r1", End: "ok", At: from.Format(time.RFC3339), Usage: cardcost.ParseUsage("input=10 output=5 unpriced=subscription")},
	)
	s.Work.Put(c2)

	// Card 3: priced record - should NOT count tokens
	c3 := &Card{ID: "c3", Row: "r1", Col: Landed, Fields: map[string]string{"kind": "primary"}}
	book(c3,
		Consumer{Kind: "work", Card: "c3", Key: "c3#r1", Who: "member-b", End: "ok", At: from.Format(time.RFC3339), Usage: cardcost.ParseUsage("input=10 actual_usd=1.5")},
	)
	s.Work.Put(c3)

	// Card 4: record before from - should be ignored
	c4 := &Card{ID: "c4", Row: "r1", Col: Landed, Fields: map[string]string{"kind": "primary"}}
	book(c4,
		Consumer{Kind: "work", Card: "c4", Key: "c4#r1", Who: "member-c", End: "ok", At: from.Add(-1 * time.Minute).Format(time.RFC3339), Usage: cardcost.ParseUsage("input=100 output=50 unpriced=subscription")},
	)
	s.Work.Put(c4)

	// Card 5: record at to - should be ignored
	c5 := &Card{ID: "c5", Row: "r1", Col: Landed, Fields: map[string]string{"kind": "primary"}}
	book(c5,
		Consumer{Kind: "work", Card: "c5", Key: "c5#r1", Who: "member-c", End: "ok", At: to.Format(time.RFC3339), Usage: cardcost.ParseUsage("input=100 output=50 unpriced=subscription")},
	)
	s.Work.Put(c5)

	want := map[string]int64{
		"member-a": 450, // 150 + 300
		"-":        15,  // 10 + 5
	}
	got := RecordedTokensIn(s, from, to)
	assert.Equal(t, want, got)
}

// TestSprintCostSpendCoverNilWork covers the nil Work case for all functions.
func TestSprintCostSpendCoverNilWork(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	from := t0
	to := t0.Add(2 * time.Hour)

	s := &Snapshot{
		Now:   t0,
		Fleet: NewTable(Fleet),
		Work:  nil,
	}

	assert.Equal(t, 0.0, RecordedSpendIn(s, "openrouter", from, to), "RecordedSpendIn with nil Work should be 0")

	got := RecordedProvidersIn(s, from, to)
	assert.Empty(t, got, "RecordedProvidersIn with nil Work should be empty")

	assert.Equal(t, map[string]int64{}, RecordedTokensIn(s, from, to), "RecordedTokensIn with nil Work should be empty")
}
