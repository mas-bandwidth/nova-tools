//go:build functional

package record_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/record"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
)

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
	t.Parallel()

	s, _ := redisLedger(t)
	ctx := context.Background()
	for range 2 {
		if err := s.ReplaceLedgerDay(ctx, "2026-09-13", ledgerFixture()); err != nil {
			t.Fatalf("replace: %s", err)
		}
	}
	got, indexed, missing, err := s.LedgerReport(ctx, "2026-09", "tuple")
	if err != nil {
		t.Fatalf("report: %s", err)
	}
	if indexed != 1 || missing != 29 {
		t.Fatalf("indexed=%d missing=%d; want indexed=1 missing=29", indexed, missing)
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
	byModel, _, _, err := s.LedgerReport(ctx, "2026-09", "model")
	if err != nil || len(byModel) != 1 || byModel[0].Day != "" || byModel[0].Repo != "" || byModel[0].Model != "gpt" {
		t.Fatalf("--by model = %+v, %v; want one group keyed on the model alone", byModel, err)
	}
	if other, _, _, err := s.LedgerReport(ctx, "2026-10", "tuple"); err != nil || len(other) != 0 {
		t.Fatalf("October's report = %+v, %v; want nothing", other, err)
	}
	if _, _, _, err := s.LedgerReport(ctx, "2026-09", "card"); err == nil {
		t.Fatal("--by card was accepted; the report groups on model, repo, day or tuple")
	}
	if _, _, _, err := s.LedgerReport(ctx, "2026-9", "tuple"); err == nil {
		t.Fatal("month 2026-9 was accepted; the report reads one YYYY-MM")
	}
	// Replacing with fewer rows drops the rest: the day is the batch, not a merge into it.
	if err := s.ReplaceLedgerDay(ctx, "2026-09-13", ledgerFixture()[:1]); err != nil {
		t.Fatalf("replace: %s", err)
	}
	if got, _, _, _ := s.LedgerReport(ctx, "2026-09", "tuple"); len(got) != 1 || got[0].Rows != 1 || got[0].Tokens[0] != 10 {
		t.Fatalf("after replacing with one row the report is %+v; want rows=1 input=10", got)
	}
	// An empty batch clears the day.
	if err := s.ReplaceLedgerDay(ctx, "2026-09-13", nil); err != nil {
		t.Fatalf("replace empty: %s", err)
	}
	if got, _, _, _ := s.LedgerReport(ctx, "2026-09", "tuple"); len(got) != 0 {
		t.Fatalf("an empty batch left %+v", got)
	}
}

// TestTheLedgerIsOneHashPerDayUnderTokensLedger pins the key layout the PR states:
// tokens:ledger:<day> is a hash, one field per (card, model, repo), and a type no source
// reported is stored as null, never as 0.
func TestTheLedgerIsOneHashPerDayUnderTokensLedger(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

	s, mr := redisLedger(t)
	mr.HSet("tokens:ledger:2026-09-02", `["c","m","r"]`, "not json")
	_, _, _, err := s.LedgerReport(context.Background(), "2026-09", "tuple")
	if err == nil || !strings.Contains(err.Error(), "tokens:ledger:2026-09-02") {
		t.Fatalf("an unreadable value gave %v; want an error naming tokens:ledger:2026-09-02", err)
	}
}

// TestReplaceLedgerDaysTakesOneRoundTrip pins the batching contract from rowan-7fbdefecf56e:
// writing 23 days takes 1 round trip in a single pipeline, where previously each day
// was a separate transaction in a loop (23 round trips for 23 days).
func TestReplaceLedgerDaysTakesOneRoundTrip(t *testing.T) {
	t.Parallel()

	s, _ := redisLedger(t)
	trips := redisconn.CountTrips(s.Client())
	ctx := context.Background()

	// 23 days fixture, matching Rowan's audit benchmark (3+24 trips -> 1 trip for the writes).
	days := make([]record.LedgerDay, 23)
	for i := range 23 {
		dayStr := fmt.Sprintf("2026-09-%02d", i+1)
		e := record.LedgerEntry{
			Day: dayStr, Card: "card-a", Model: "gpt", Repo: "schema",
			Provider: "openai", Sources: "openai:o",
		}
		e.Tokens, e.Known = [5]int64{10, 20, 0, 0, 0}, [5]bool{true, true, false, false, false}
		days[i] = record.LedgerDay{Day: dayStr, Entries: []record.LedgerEntry{e}}
	}

	before := trips.N()
	if err := s.ReplaceLedgerDays(ctx, days); err != nil {
		t.Fatalf("replace days: %s", err)
	}
	if got := trips.N() - before; got != 1 {
		t.Fatalf("ReplaceLedgerDays for 23 days took %d round trips; want 1 (rowan-7fbdefecf56e: was 23 transactions in a loop)", got)
	}

	gotTotals, indexed, missing, err := s.LedgerReport(ctx, "2026-09", "day")
	if err != nil {
		t.Fatalf("report: %s", err)
	}
	if indexed != 23 {
		t.Fatalf("indexed = %d; want 23", indexed)
	}
	if missing != 7 {
		t.Fatalf("missing = %d; want 7", missing)
	}
	if len(gotTotals) != 23 {
		t.Fatalf("got %d totals; want 23", len(gotTotals))
	}
}

// TestReplaceLedgerDaysConflictingWritersAndReaderSnapshots pins the atomic transaction contract
// (Stella note stella-ee1cb7f37a7e): ReplaceLedgerDays uses TxPipeline so DEL and HSET execute
// atomically. Competing writers cannot merge partial replacements, and readers never see an absent
// day between DEL and HSET or a torn mixture of rows from conflicting writers.
func TestReplaceLedgerDaysConflictingWritersAndReaderSnapshots(t *testing.T) {
	t.Parallel()

	s, _ := redisLedger(t)
	ctx := context.Background()

	testDays := []string{"2026-09-10", "2026-09-11", "2026-09-12"}
	makeBatch := func(card string, tokenVal int64) []record.LedgerDay {
		batch := make([]record.LedgerDay, len(testDays))
		for i, day := range testDays {
			e := record.LedgerEntry{
				Day:      day,
				Card:     card,
				Model:    "gpt-4",
				Repo:     "nova-tools",
				Provider: "openai",
				Sources:  "openai:o",
			}
			e.Tokens, e.Known = [5]int64{tokenVal, 0, 0, 0, 0}, [5]bool{true, false, false, false, false}
			batch[i] = record.LedgerDay{Day: day, Entries: []record.LedgerEntry{e}}
		}
		return batch
	}

	// Seed with initial batch so days exist in Redis.
	if err := s.ReplaceLedgerDays(ctx, makeBatch("card-a", 100)); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	const iterations = 40
	var writersWg sync.WaitGroup
	writersWg.Add(2)

	// Writer A writes card-a
	go func() {
		defer writersWg.Done()
		batch := makeBatch("card-a", 100)
		for i := 0; i < iterations; i++ {
			if err := s.ReplaceLedgerDays(ctx, batch); err != nil {
				t.Errorf("writer A error: %v", err)
				return
			}
		}
	}()

	// Writer B writes card-b
	go func() {
		defer writersWg.Done()
		batch := makeBatch("card-b", 200)
		for i := 0; i < iterations; i++ {
			if err := s.ReplaceLedgerDays(ctx, batch); err != nil {
				t.Errorf("writer B error: %v", err)
				return
			}
		}
	}()

	// Reader continuously verifies snapshots during concurrent replacements.
	// Reader must NEVER see:
	// 1. Incomplete / absent days (indexed must be 3, missing must be 27).
	// 2. Torn / merged rows from both writers in the same day (rows must be exactly 1).
	stopReaders := make(chan struct{})
	var readerWg sync.WaitGroup
	readerWg.Add(1)
	go func() {
		defer readerWg.Done()
		for {
			select {
			case <-stopReaders:
				return
			default:
			}
			totals, indexed, missing, err := s.LedgerReport(ctx, "2026-09", "tuple")
			if err != nil {
				t.Errorf("reader snapshot error: %v", err)
				return
			}
			if indexed != 3 || missing != 27 {
				t.Errorf("reader saw incomplete days: indexed=%d missing=%d (want 3 and 27)", indexed, missing)
				return
			}
			for _, tot := range totals {
				if tot.Rows != 1 {
					t.Errorf("torn replacement: day %s has %d rows (want 1 row, never merged writers)", tot.Day, tot.Rows)
					return
				}
				if tot.Tokens[0] != 100 && tot.Tokens[0] != 200 {
					t.Errorf("corrupt token count in day %s: %d", tot.Day, tot.Tokens[0])
					return
				}
			}
		}
	}()

	writersWg.Wait()
	close(stopReaders)
	readerWg.Wait()

	// Verify final state: all days are intact and contain rows from exactly one writer, not a merge.
	finalTotals, indexed, missing, err := s.LedgerReport(ctx, "2026-09", "tuple")
	if err != nil {
		t.Fatalf("final report error: %v", err)
	}
	if indexed != 3 || missing != 27 {
		t.Fatalf("final indexed=%d missing=%d; want 3 and 27", indexed, missing)
	}
	if len(finalTotals) != 3 {
		t.Fatalf("final totals len = %d; want 3", len(finalTotals))
	}
	for _, tot := range finalTotals {
		if tot.Rows != 1 {
			t.Fatalf("competing writers merged in day %s: rows = %d; want 1", tot.Day, tot.Rows)
		}
	}
}
