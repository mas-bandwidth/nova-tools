package record_test

import (
	"context"
	"os"
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

// ledgerContract is the token_ledger promise both stores keep: a day is replaced whole, the
// report is a GROUP BY that sums over the rows that reported a type, and a type no row
// reported stays unknown rather than becoming a zero.
func ledgerContract(t *testing.T, s record.LedgerStore) {
	t.Helper()
	ctx := context.Background()
	for range 2 {
		if err := s.ReplaceLedgerDay(ctx, "2026-09-13", ledgerFixture()); err != nil {
			t.Fatalf("replace: %s", err)
		}
	}
	got, err := s.LedgerReport(ctx, "2026-09", "tuple")
	if err != nil {
		t.Fatalf("report: %s", err)
	}
	var mine []record.LedgerTotal
	for _, g := range got {
		if g.Day == "2026-09-13" && g.Model == "gpt" && g.Repo == "schema" {
			mine = append(mine, g)
		}
	}
	if len(mine) != 1 {
		t.Fatalf("want one (2026-09-13, gpt, schema) group, got %+v", got)
	}
	g := mine[0]
	if g.Rows != 2 || g.Tokens != [5]int64{11, 22, 4, 0, 3} || g.Known != [5]bool{true, true, true, false, true} {
		t.Fatalf("group = %+v; want rows=2 tokens=[11 22 4 0 3] known=[t t t f t]", g)
	}
	if other, _ := s.LedgerReport(ctx, "2026-10", "tuple"); len(other) != 0 && other[0].Day == "2026-09-13" {
		t.Fatalf("October's report holds a September day: %+v", other)
	}
	if _, err := s.LedgerReport(ctx, "2026-09", "card"); err == nil {
		t.Fatal("--by card was accepted; the report groups on model, repo, day or tuple")
	}
}

func TestFakeStoreKeepsTheLedgerContract(t *testing.T) {
	ledgerContract(t, record.NewFakeStore())
}

func TestLedgerRefusesARepeatedKeyAndAForeignDay(t *testing.T) {
	s := record.NewFakeStore()
	rows := ledgerFixture()
	rows[1].Card = rows[0].Card
	if err := s.ReplaceLedgerDay(context.Background(), "2026-09-13", rows); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("a repeated (day, card, model, repo) key was accepted: %v", err)
	}
	if err := s.ReplaceLedgerDay(context.Background(), "2026-09-14", ledgerFixture()); err == nil {
		t.Fatal("a 2026-09-13 row was accepted in the batch for 2026-09-14")
	}
}

// TestPostgresLedgerSoak is the real-Postgres half of the token_ledger contract, behind
// RECORD_TEST_PG like the card_results soak.
func TestPostgresLedgerSoak(t *testing.T) {
	dsn := os.Getenv("RECORD_TEST_PG")
	if dsn == "" {
		t.Skip("set RECORD_TEST_PG to a Postgres DSN to run the token_ledger soak")
	}
	store, err := record.OpenPostgres(dsn)
	if err != nil {
		t.Fatalf("open postgres: %s", err)
	}
	defer store.Close()
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %s", err)
	}
	ledgerContract(t, store)
}
