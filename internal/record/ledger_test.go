package record_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/record"
)

func ledgerFixture() []record.LedgerEntry {
	a := record.LedgerEntry{Day: "2026-09-13", Card: "card-a", Model: "gpt", Repo: "schema", Provider: "openai", Rough: 1, Sources: "openai:o"}
	a.Tokens, a.Known = [5]int64{10, 20, 0, 0, 3}, [5]bool{true, true, false, false, true}
	b := record.LedgerEntry{Day: "2026-09-13", Card: "card-b", Model: "gpt", Repo: "schema", Provider: "openai", Sources: "openai:o"}
	b.Tokens, b.Known = [5]int64{1, 2, 4, 0, 0}, [5]bool{true, true, true, false, false}
	return []record.LedgerEntry{a, b}
}

func TestCheckLedgerDay(t *testing.T) {
	t.Parallel()

	require.NoError(t, record.CheckLedgerDay("2026-09-13", ledgerFixture()), "valid fixture failed")

	// Empty batch is valid.
	require.NoError(t, record.CheckLedgerDay("2026-09-13", nil), "empty batch failed")

	// Bad day formats.
	for _, bad := range []string{"2026-09-1*", "2026-9-1", "not-a-day", "2026-02-30", "2026/09/13"} {
		assert.Error(t, record.CheckLedgerDay(bad, nil), "CheckLedgerDay(%q) accepted; want error", bad)
	}

	// Missing key fields.
	badEntries := []record.LedgerEntry{
		{Day: "", Card: "c", Model: "m", Repo: "r"},
		{Day: "2026-09-13", Card: "", Model: "m", Repo: "r"},
		{Day: "2026-09-13", Card: "c", Model: "", Repo: "r"},
		{Day: "2026-09-13", Card: "c", Model: "m", Repo: ""},
	}
	for i, e := range badEntries {
		assert.ErrorIs(t, record.CheckLedgerDay("2026-09-13", []record.LedgerEntry{e}), record.ErrLedgerKey, "entry %d: want ErrLedgerKey", i)
	}

	// Entry with mismatched day.
	mismatched := ledgerFixture()
	mismatched[0].Day = "2026-09-14"
	assert.ErrorContains(t, record.CheckLedgerDay("2026-09-13", mismatched), "in the batch for day", "mismatched day accepted or wrong error")

	// Duplicate key in the same day.
	dups := ledgerFixture()
	dups[1].Card = dups[0].Card
	assert.ErrorContains(t, record.CheckLedgerDay("2026-09-13", dups), "twice in one day", "duplicate key accepted or wrong error")
}

func TestLedgerKey(t *testing.T) {
	t.Parallel()

	require.Equal(t, "tokens:ledger:2026-09-13", record.LedgerKey("2026-09-13"), "LedgerKey, want tokens:ledger:2026-09-13")
}

func TestMonthDays(t *testing.T) {
	t.Parallel()

	days, err := record.MonthDays("2026-09")
	require.NoError(t, err, "MonthDays(2026-09) failed")
	require.Len(t, days, 30, "September 2026 has %d days; want 30", len(days))
	require.Equal(t, "2026-09-01", days[0], "September bounds: %s .. %s; want 2026-09-01 .. 2026-09-30", days[0], days[29])
	require.Equal(t, "2026-09-30", days[29], "September bounds: %s .. %s; want 2026-09-01 .. 2026-09-30", days[0], days[29])

	feb, err := record.MonthDays("2026-02")
	require.NoError(t, err, "MonthDays(2026-02) failed")
	require.Len(t, feb, 28, "February 2026 has %d days; want 28", len(feb))

	for _, bad := range []string{"2026-9", "2026-13", "bad", "2026-09-01"} {
		_, err := record.MonthDays(bad)
		assert.Error(t, err, "MonthDays(%q) accepted; want error", bad)
	}
}

func TestGroupLedgerAndSortLedgerTotals(t *testing.T) {
	t.Parallel()

	entries := ledgerFixture()
	// Add another day's entries for grouping & sorting tests.
	c := record.LedgerEntry{
		Day: "2026-09-12", Card: "card-c", Model: "claude", Repo: "nova", Provider: "anthropic",
	}
	c.Tokens, c.Known = [5]int64{5, 0, 0, 0, 0}, [5]bool{true, false, false, false, false}
	entries = append(entries, c)

	// Group by model
	byModel, err := record.GroupLedger(entries, "model")
	require.NoError(t, err, "GroupLedger(model)")
	require.Len(t, byModel, 2, "got %d model groups; want 2 (claude, gpt)", len(byModel))
	require.Equal(t, "claude", byModel[0].Model, "order = [%s, %s]; want [claude, gpt]", byModel[0].Model, byModel[1].Model)
	require.Equal(t, "gpt", byModel[1].Model, "order = [%s, %s]; want [claude, gpt]", byModel[0].Model, byModel[1].Model)

	// Group by day
	byDay, err := record.GroupLedger(entries, "day")
	require.NoError(t, err, "GroupLedger(day)")
	require.Len(t, byDay, 2, "byDay = %+v; want sorted by day", byDay)
	require.Equal(t, "2026-09-12", byDay[0].Day, "byDay = %+v; want sorted by day", byDay)
	require.Equal(t, "2026-09-13", byDay[1].Day, "byDay = %+v; want sorted by day", byDay)

	// Group by repo
	byRepo, err := record.GroupLedger(entries, "repo")
	require.NoError(t, err, "GroupLedger(repo)")
	require.Len(t, byRepo, 2, "byRepo len = %d; want 2", len(byRepo))

	// Group by tuple
	byTuple, err := record.GroupLedger(entries, "tuple")
	require.NoError(t, err, "GroupLedger(tuple)")
	require.Len(t, byTuple, 2, "byTuple len = %d; want 2", len(byTuple))

	// Invalid grouping
	_, err = record.GroupLedger(entries, "card")
	require.Error(t, err, "GroupLedger accepted unknown grouping 'card'")
}

func TestReplaceLedgerDaysPrevalidation(t *testing.T) {
	t.Parallel()

	// Pure logic prevalidation: NewRedisLedger(nil) is never touched because
	// empty batches return immediately, and validation errors abort before Redis.
	s := record.NewRedisLedger(nil)
	ctx := context.Background()

	// Empty batch returns nil.
	require.NoError(t, s.ReplaceLedgerDays(ctx, nil), "empty days; want nil")
	require.NoError(t, s.ReplaceLedgerDays(ctx, []record.LedgerDay{}), "empty slice; want nil")

	// Duplicate day in batch.
	dupBatch := []record.LedgerDay{
		{Day: "2026-09-01", Entries: nil},
		{Day: "2026-09-01", Entries: nil},
	}
	require.ErrorContains(t, s.ReplaceLedgerDays(ctx, dupBatch), "twice in the batch", "duplicate day accepted or wrong error")

	// Bad day in batch.
	badDayBatch := []record.LedgerDay{
		{Day: "2026-09-1*", Entries: nil},
	}
	require.Error(t, s.ReplaceLedgerDays(ctx, badDayBatch), "bad day pattern accepted in ReplaceLedgerDays")

	// Bad entry in batch.
	badEntryBatch := []record.LedgerDay{
		{Day: "2026-09-01", Entries: []record.LedgerEntry{{Day: "2026-09-02", Card: "c", Model: "m", Repo: "r"}}},
	}
	require.Error(t, s.ReplaceLedgerDays(ctx, badEntryBatch), "mismatched entry accepted in ReplaceLedgerDays")

	// ReplaceLedgerDay delegating validation.
	require.Error(t, s.ReplaceLedgerDay(ctx, "2026-09-1*", nil), "bad day accepted in ReplaceLedgerDay")
}
