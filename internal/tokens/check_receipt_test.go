package tokens

import "testing"

// The receipt join: receipts are children of a day row, and the join names the ways that
// parentage can be wrong rather than asserting a wrong number.

func counts(v ...int64) Counts {
	var c Counts
	for t := Type(0); t < NTypes; t++ {
		if int(t) < len(v) {
			c.Set(t, v[t])
		}
	}
	return c
}

func TestCheckReceiptJoins(t *testing.T) {
	dayRows := []DayRow{
		{Date: "2026-09-15", Model: "claude-x", Repo: "nova-tools",
			Counts: counts(1200, 800, 300, 150, 0)},
		{Date: "2026-09-15", Model: "opencode", Repo: "nova-tools",
			Counts: counts(500, 400, 100, 50, 0)},
	}

	t.Run("clean join when receipts match day rows as valid subsets", func(t *testing.T) {
		receipts := []ReceiptRow{
			{Day: "2026-09-15", Model: "claude-x", Repo: "nova-tools",
				Receipt: "r1", Turns: 3,
				InputTokens: 700, OutputTokens: 500, CacheReadTokens: 100, CacheWriteTokens: 200},
			{Day: "2026-09-15", Model: "claude-x", Repo: "nova-tools",
				Receipt: "r2", Turns: 2,
				InputTokens: 400, OutputTokens: 250, CacheReadTokens: 50, CacheWriteTokens: 80},
		}
		got := CheckReceiptJoins(dayRows, receipts)
		if len(got) != 0 {
			t.Fatalf("clean join produced %d findings, want 0: %+v", len(got), got)
		}
	})

	t.Run("DUPLICATE join finding emitted when duplicate receipt ID appears", func(t *testing.T) {
		receipts := []ReceiptRow{
			{Day: "2026-09-15", Model: "claude-x", Repo: "nova-tools",
				Receipt: "r1", InputTokens: 100, OutputTokens: 100},
			{Day: "2026-09-15", Model: "claude-x", Repo: "nova-tools",
				Receipt: "r1", InputTokens: 100, OutputTokens: 100},
		}
		got := CheckReceiptJoins(dayRows, receipts)
		if len(got) != 1 || got[0].Type != JoinDuplicate || got[0].Receipt != "r1" {
			t.Fatalf("want one DUPLICATE finding for r1, got %+v", got)
		}
	})

	t.Run("EXCEEDS join finding emitted when sum exceeds day file tokens", func(t *testing.T) {
		receipts := []ReceiptRow{
			{Day: "2026-09-15", Model: "claude-x", Repo: "nova-tools",
				Receipt: "r1", InputTokens: 800, OutputTokens: 400},
			{Day: "2026-09-15", Model: "claude-x", Repo: "nova-tools",
				Receipt: "r2", InputTokens: 800, OutputTokens: 400},
		}
		got := CheckReceiptJoins(dayRows, receipts)
		if len(got) != 1 || got[0].Type != JoinExceeds {
			t.Fatalf("want one EXCEEDS finding, got %+v", got)
		}
		if got[0].Model != "claude-x" || got[0].Day != "2026-09-15" {
			t.Fatalf("EXCEEDS finding names the wrong row: %+v", got[0])
		}
	})

	t.Run("ORPHAN join finding emitted when receipt references a missing day row", func(t *testing.T) {
		receipts := []ReceiptRow{
			{Day: "2026-09-15", Model: "claude-x", Repo: "nova-tools",
				Receipt: "r1", InputTokens: 100, OutputTokens: 100},
			{Day: "2026-09-15", Model: "gpt-x", Repo: "other-repo",
				Receipt: "r2", InputTokens: 100, OutputTokens: 100},
		}
		got := CheckReceiptJoins(dayRows, receipts)
		if len(got) != 1 || got[0].Type != JoinOrphan {
			t.Fatalf("want one ORPHAN finding, got %+v", got)
		}
		if got[0].Model != "gpt-x" || got[0].Repo != "other-repo" {
			t.Fatalf("ORPHAN finding names the wrong row: %+v", got[0])
		}
	})
}
