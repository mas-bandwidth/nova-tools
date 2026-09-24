package record_test

import (
	"context"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/record"
)

func ledgerFixture() []record.LedgerEntry {
	a := record.LedgerEntry{Day: "2026-09-13", Card: "card-a", Model: "gpt", Repo: "schema", Provider: "openai", Rough: 1, Sources: "openai:o"}
	a.Tokens, a.Known = [5]int64{10, 20, 0, 0, 3}, [5]bool{true, true, false, false, true}
	b := record.LedgerEntry{Day: "2026-09-13", Card: "card-b", Model: "gpt", Repo: "schema", Provider: "openai", Sources: "openai:o"}
	b.Tokens, b.Known = [5]int64{1, 2, 4, 0, 0}, [5]bool{true, true, true, false, false}
	return []record.LedgerEntry{a, b}
}

// redisLedger is the store over a miniredis: the real client, the real commands, no host.
func redisLedger(t *testing.T) (*record.RedisLedger, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	s := record.NewRedisLedger(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	t.Cleanup(func() { _ = s.Close() })
	return s, mr
}

// TestTheRedisLedgerKeepsTheLedgerContract is the token ledger's promise (#2201, recut of
// #3243 under #2623): a day is replaced whole, the report is a GROUP BY that sums over the
// rows that reported a type, and a type no row reported stays unknown rather than a zero.
func TestTheRedisLedgerKeepsTheLedgerContract(t *testing.T) {
	s, _ := redisLedger(t)
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
	if len(got) != 1 {
		t.Fatalf("want one (2026-09-13, gpt, schema) group, got %+v", got)
	}
	g := got[0]
	if g.Day != "2026-09-13" || g.Model != "gpt" || g.Repo != "schema" {
		t.Fatalf("group key = (%s, %s, %s); want (2026-09-13, gpt, schema)", g.Day, g.Model, g.Repo)
	}
	if g.Rows != 2 || g.Tokens != [5]int64{11, 22, 4, 0, 3} || g.Known != [5]bool{true, true, true, false, true} {
		t.Fatalf("group = %+v; want rows=2 tokens=[11 22 4 0 3] known=[t t t f t]", g)
	}
	byModel, err := s.LedgerReport(ctx, "2026-09", "model")
	if err != nil || len(byModel) != 1 || byModel[0].Day != "" || byModel[0].Repo != "" || byModel[0].Model != "gpt" {
		t.Fatalf("--by model = %+v, %v; want one group keyed on the model alone", byModel, err)
	}
	if other, err := s.LedgerReport(ctx, "2026-10", "tuple"); err != nil || len(other) != 0 {
		t.Fatalf("October's report = %+v, %v; want nothing", other, err)
	}
	if _, err := s.LedgerReport(ctx, "2026-09", "card"); err == nil {
		t.Fatal("--by card was accepted; the report groups on model, repo, day or tuple")
	}
	if _, err := s.LedgerReport(ctx, "2026-9", "tuple"); err == nil {
		t.Fatal("month 2026-9 was accepted; the report reads one YYYY-MM")
	}
	// Replacing with fewer rows drops the rest: the day is the batch, not a merge into it.
	if err := s.ReplaceLedgerDay(ctx, "2026-09-13", ledgerFixture()[:1]); err != nil {
		t.Fatalf("replace: %s", err)
	}
	if got, _ := s.LedgerReport(ctx, "2026-09", "tuple"); len(got) != 1 || got[0].Rows != 1 || got[0].Tokens[0] != 10 {
		t.Fatalf("after replacing with one row the report is %+v; want rows=1 input=10", got)
	}
	// An empty batch clears the day.
	if err := s.ReplaceLedgerDay(ctx, "2026-09-13", nil); err != nil {
		t.Fatalf("replace empty: %s", err)
	}
	if got, _ := s.LedgerReport(ctx, "2026-09", "tuple"); len(got) != 0 {
		t.Fatalf("an empty batch left %+v", got)
	}
}

// TestTheLedgerIsOneHashPerDayUnderTokensLedger pins the key layout the PR states:
// tokens:ledger:<day> is a hash, one field per (card, model, repo), and a type no source
// reported is stored as null, never as 0.
func TestTheLedgerIsOneHashPerDayUnderTokensLedger(t *testing.T) {
	s, mr := redisLedger(t)
	ctx := context.Background()
	if err := s.ReplaceLedgerDay(ctx, "2026-09-13", ledgerFixture()); err != nil {
		t.Fatalf("replace: %s", err)
	}
	if keys := mr.Keys(); len(keys) != 1 || keys[0] != "tokens:ledger:2026-09-13" {
		t.Fatalf("keys = %v; want exactly [tokens:ledger:2026-09-13]", keys)
	}
	if ty := mr.Type("tokens:ledger:2026-09-13"); ty != "hash" {
		t.Fatalf("tokens:ledger:2026-09-13 is a %s; want a hash", ty)
	}
	fields, err := mr.HKeys("tokens:ledger:2026-09-13")
	if err != nil || len(fields) != 2 {
		t.Fatalf("fields = %v, %v; want one per (card, model, repo)", fields, err)
	}
	v := mr.HGet("tokens:ledger:2026-09-13", `["card-a","gpt","schema"]`)
	if !strings.Contains(v, `"tokens":[10,20,null,null,3]`) {
		t.Fatalf("card-a's value = %s; want its tokens as [10,20,null,null,3] (a dash is null, never 0)", v)
	}
}

func TestTheLedgerRefusesARepeatedKeyAForeignDayAndABadDay(t *testing.T) {
	s, mr := redisLedger(t)
	ctx := context.Background()
	rows := ledgerFixture()
	rows[1].Card = rows[0].Card
	if err := s.ReplaceLedgerDay(ctx, "2026-09-13", rows); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("a repeated (day, card, model, repo) key was accepted: %v", err)
	}
	if err := s.ReplaceLedgerDay(ctx, "2026-09-14", ledgerFixture()); err == nil {
		t.Fatal("a 2026-09-13 row was accepted in the batch for 2026-09-14")
	}
	if err := s.ReplaceLedgerDay(ctx, "2026-09-1*", nil); err == nil {
		t.Fatal("day 2026-09-1* was accepted; it names a key pattern, not a day")
	}
	if keys := mr.Keys(); len(keys) != 0 {
		t.Fatalf("a refused batch wrote %v", keys)
	}
}

// TestTheLedgerReportRefusesAValueItCannotRead: a field that does not decode is an error
// naming the key, never a row quietly skipped out of the month.
func TestTheLedgerReportRefusesAValueItCannotRead(t *testing.T) {
	s, mr := redisLedger(t)
	mr.HSet("tokens:ledger:2026-09-02", `["c","m","r"]`, "not json")
	_, err := s.LedgerReport(context.Background(), "2026-09", "tuple")
	if err == nil || !strings.Contains(err.Error(), "tokens:ledger:2026-09-02") {
		t.Fatalf("an unreadable value gave %v; want an error naming tokens:ledger:2026-09-02", err)
	}
}
