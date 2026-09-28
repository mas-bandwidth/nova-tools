package record_test

import (
	"context"
	"errors"
	"strings"
	"testing"

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

	if err := record.CheckLedgerDay("2026-09-13", ledgerFixture()); err != nil {
		t.Fatalf("valid fixture failed: %v", err)
	}

	// Empty batch is valid.
	if err := record.CheckLedgerDay("2026-09-13", nil); err != nil {
		t.Fatalf("empty batch failed: %v", err)
	}

	// Bad day formats.
	for _, bad := range []string{"2026-09-1*", "2026-9-1", "not-a-day", "2026-02-30", "2026/09/13"} {
		if err := record.CheckLedgerDay(bad, nil); err == nil {
			t.Errorf("CheckLedgerDay(%q) accepted; want error", bad)
		}
	}

	// Missing key fields.
	badEntries := []record.LedgerEntry{
		{Day: "", Card: "c", Model: "m", Repo: "r"},
		{Day: "2026-09-13", Card: "", Model: "m", Repo: "r"},
		{Day: "2026-09-13", Card: "c", Model: "", Repo: "r"},
		{Day: "2026-09-13", Card: "c", Model: "m", Repo: ""},
	}
	for i, e := range badEntries {
		if err := record.CheckLedgerDay("2026-09-13", []record.LedgerEntry{e}); !errors.Is(err, record.ErrLedgerKey) {
			t.Errorf("entry %d: err = %v; want ErrLedgerKey", i, err)
		}
	}

	// Entry with mismatched day.
	mismatched := ledgerFixture()
	mismatched[0].Day = "2026-09-14"
	if err := record.CheckLedgerDay("2026-09-13", mismatched); err == nil || !strings.Contains(err.Error(), "in the batch for day") {
		t.Errorf("mismatched day accepted or wrong error: %v", err)
	}

	// Duplicate key in the same day.
	dups := ledgerFixture()
	dups[1].Card = dups[0].Card
	if err := record.CheckLedgerDay("2026-09-13", dups); err == nil || !strings.Contains(err.Error(), "twice in one day") {
		t.Errorf("duplicate key accepted or wrong error: %v", err)
	}
}

func TestLedgerKey(t *testing.T) {
	t.Parallel()

	if got := record.LedgerKey("2026-09-13"); got != "tokens:ledger:2026-09-13" {
		t.Fatalf("LedgerKey = %q; want tokens:ledger:2026-09-13", got)
	}
}

func TestMonthDays(t *testing.T) {
	t.Parallel()

	days, err := record.MonthDays("2026-09")
	if err != nil {
		t.Fatalf("MonthDays(2026-09) failed: %v", err)
	}
	if len(days) != 30 {
		t.Fatalf("September 2026 has %d days; want 30", len(days))
	}
	if days[0] != "2026-09-01" || days[29] != "2026-09-30" {
		t.Fatalf("September bounds: %s .. %s; want 2026-09-01 .. 2026-09-30", days[0], days[29])
	}

	feb, err := record.MonthDays("2026-02")
	if err != nil {
		t.Fatalf("MonthDays(2026-02) failed: %v", err)
	}
	if len(feb) != 28 {
		t.Fatalf("February 2026 has %d days; want 28", len(feb))
	}

	for _, bad := range []string{"2026-9", "2026-13", "bad", "2026-09-01"} {
		if _, err := record.MonthDays(bad); err == nil {
			t.Errorf("MonthDays(%q) accepted; want error", bad)
		}
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
	if err != nil {
		t.Fatalf("GroupLedger(model): %v", err)
	}
	if len(byModel) != 2 {
		t.Fatalf("got %d model groups; want 2 (claude, gpt)", len(byModel))
	}
	if byModel[0].Model != "claude" || byModel[1].Model != "gpt" {
		t.Fatalf("order = [%s, %s]; want [claude, gpt]", byModel[0].Model, byModel[1].Model)
	}

	// Group by day
	byDay, err := record.GroupLedger(entries, "day")
	if err != nil {
		t.Fatalf("GroupLedger(day): %v", err)
	}
	if len(byDay) != 2 || byDay[0].Day != "2026-09-12" || byDay[1].Day != "2026-09-13" {
		t.Fatalf("byDay = %+v; want sorted by day", byDay)
	}

	// Group by repo
	byRepo, err := record.GroupLedger(entries, "repo")
	if err != nil {
		t.Fatalf("GroupLedger(repo): %v", err)
	}
	if len(byRepo) != 2 {
		t.Fatalf("byRepo len = %d; want 2", len(byRepo))
	}

	// Group by tuple
	byTuple, err := record.GroupLedger(entries, "tuple")
	if err != nil {
		t.Fatalf("GroupLedger(tuple): %v", err)
	}
	if len(byTuple) != 2 {
		t.Fatalf("byTuple len = %d; want 2", len(byTuple))
	}

	// Invalid grouping
	if _, err := record.GroupLedger(entries, "card"); err == nil {
		t.Fatal("GroupLedger accepted unknown grouping 'card'")
	}
}

func TestReplaceLedgerDaysPrevalidation(t *testing.T) {
	t.Parallel()

	// Pure logic prevalidation: NewRedisLedger(nil) is never touched because
	// empty batches return immediately, and validation errors abort before Redis.
	s := record.NewRedisLedger(nil)
	ctx := context.Background()

	// Empty batch returns nil.
	if err := s.ReplaceLedgerDays(ctx, nil); err != nil {
		t.Fatalf("empty days: %v; want nil", err)
	}
	if err := s.ReplaceLedgerDays(ctx, []record.LedgerDay{}); err != nil {
		t.Fatalf("empty slice: %v; want nil", err)
	}

	// Duplicate day in batch.
	dupBatch := []record.LedgerDay{
		{Day: "2026-09-01", Entries: nil},
		{Day: "2026-09-01", Entries: nil},
	}
	if err := s.ReplaceLedgerDays(ctx, dupBatch); err == nil || !strings.Contains(err.Error(), "twice in the batch") {
		t.Fatalf("duplicate day accepted or wrong error: %v", err)
	}

	// Bad day in batch.
	badDayBatch := []record.LedgerDay{
		{Day: "2026-09-1*", Entries: nil},
	}
	if err := s.ReplaceLedgerDays(ctx, badDayBatch); err == nil {
		t.Fatal("bad day pattern accepted in ReplaceLedgerDays")
	}

	// Bad entry in batch.
	badEntryBatch := []record.LedgerDay{
		{Day: "2026-09-01", Entries: []record.LedgerEntry{{Day: "2026-09-02", Card: "c", Model: "m", Repo: "r"}}},
	}
	if err := s.ReplaceLedgerDays(ctx, badEntryBatch); err == nil {
		t.Fatal("mismatched entry accepted in ReplaceLedgerDays")
	}

	// ReplaceLedgerDay delegating validation.
	if err := s.ReplaceLedgerDay(ctx, "2026-09-1*", nil); err == nil {
		t.Fatal("bad day accepted in ReplaceLedgerDay")
	}
}
