//go:build functional

package record_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/alicebob/miniredis/v2/server"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/record"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
)

// redisLedger is the store over a miniredis: the real client, the real commands, no host.
func redisLedger(t *testing.T) (*record.RedisLedger, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	// This fixture has unrestricted access. Miniredis lacks acl_check_cmd;
	// provide only the ledger write-command checks. Real ACL denial is tested
	// separately against an owned Redis server, without this shim.
	mr.Server().SetPreHook(func(_ *server.Peer, cmd string, args ...string) bool {
		if cmd == "EVAL" && len(args) > 0 {
			args[0] = "redis.acl_check_cmd = function(command) return command == 'DEL' or command == 'HSET' end\n" + args[0]
		}
		return false
	})
	s := record.NewRedisLedger(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	t.Cleanup(func() { _ = s.Close() })
	return s, mr
}

func TestLedgerRequiresPermissionPreflightBeforeWriting(t *testing.T) {
	t.Parallel()
	// Deliberately use miniredis without the unrestricted-fixture shim: it
	// represents a server that does not expose the required Lua primitive.
	mr := miniredis.RunT(t)
	key := record.LedgerKey("2026-09-01")
	mr.HSet(key, "before", "unchanged")
	store := record.NewRedisLedger(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	t.Cleanup(func() { _ = store.Close() })
	err := store.ReplaceLedgerDay(context.Background(), "2026-09-01", nil)
	require.ErrorContains(t, err, "require redis.acl_check_cmd", "want missing permission-preflight refusal, got %v", err)
	fields, err := mr.HKeys(key)
	require.NoError(t, err, "missing preflight support changed existing state: %v / %v", fields, err)
	require.Len(t, fields, 1, "missing preflight support changed existing state: %v / %v", fields, err)
	require.Equal(t, "unchanged", mr.HGet(key, "before"), "missing preflight support changed existing state: %v / %v", fields, err)
	require.Len(t, mr.Keys(), 1, "missing preflight support changed existing state: %v / %v", fields, err)
}

// TestTheRedisLedgerKeepsTheLedgerContract is the token ledger's promise (#2201, recut of
// #3243 under #2623): a day is replaced whole, the report is a GROUP BY that sums over the
// rows that reported a type, and a type no row reported stays unknown rather than a zero.
func TestTheRedisLedgerKeepsTheLedgerContract(t *testing.T) {
	t.Parallel()

	s, _ := redisLedger(t)
	ctx := context.Background()
	for range 2 {
		require.NoError(t, s.ReplaceLedgerDay(ctx, "2026-09-13", ledgerFixture()), "replace")
	}
	got, indexed, missing, err := s.LedgerReport(ctx, "2026-09", "tuple")
	require.NoError(t, err, "report")
	require.Equal(t, 1, indexed, "indexed=%d missing=%d; want indexed=1 missing=29", indexed, missing)
	require.Equal(t, 29, missing, "indexed=%d missing=%d; want indexed=1 missing=29", indexed, missing)
	require.Len(t, got, 1, "want one (2026-09-13, gpt, schema) group, got %+v", got)
	g := got[0]
	require.Equal(t, "2026-09-13", g.Day, "group key = (%s, %s, %s); want (2026-09-13, gpt, schema)", g.Day, g.Model, g.Repo)
	require.Equal(t, "gpt", g.Model, "group key = (%s, %s, %s); want (2026-09-13, gpt, schema)", g.Day, g.Model, g.Repo)
	require.Equal(t, "schema", g.Repo, "group key = (%s, %s, %s); want (2026-09-13, gpt, schema)", g.Day, g.Model, g.Repo)
	require.Equal(t, 2, g.Rows, "group = %+v; want rows=2 tokens=[11 22 4 0 3] known=[t t t f t]", g)
	require.Equal(t, [5]int64{11, 22, 4, 0, 3}, g.Tokens, "group = %+v; want rows=2 tokens=[11 22 4 0 3] known=[t t t f t]", g)
	require.Equal(t, [5]bool{true, true, true, false, true}, g.Known, "group = %+v; want rows=2 tokens=[11 22 4 0 3] known=[t t t f t]", g)
	byModel, _, _, err := s.LedgerReport(ctx, "2026-09", "model")
	require.NoError(t, err, "--by model = %+v, %v; want one group keyed on the model alone", byModel, err)
	require.Len(t, byModel, 1, "--by model = %+v, %v; want one group keyed on the model alone", byModel, err)
	require.Empty(t, byModel[0].Day, "--by model = %+v, %v; want one group keyed on the model alone", byModel, err)
	require.Empty(t, byModel[0].Repo, "--by model = %+v, %v; want one group keyed on the model alone", byModel, err)
	require.Equal(t, "gpt", byModel[0].Model, "--by model = %+v, %v; want one group keyed on the model alone", byModel, err)
	other, _, _, err := s.LedgerReport(ctx, "2026-10", "tuple")
	require.NoError(t, err, "October's report = %+v, %v; want nothing", other, err)
	require.Empty(t, other, "October's report = %+v, %v; want nothing", other, err)
	_, _, _, err = s.LedgerReport(ctx, "2026-09", "card")
	require.Error(t, err, "--by card was accepted; the report groups on model, repo, day or tuple")
	_, _, _, err = s.LedgerReport(ctx, "2026-9", "tuple")
	require.Error(t, err, "month 2026-9 was accepted; the report reads one YYYY-MM")
	// Replacing with fewer rows drops the rest: the day is the batch, not a merge into it.
	require.NoError(t, s.ReplaceLedgerDay(ctx, "2026-09-13", ledgerFixture()[:1]), "replace")
	got, _, _, _ = s.LedgerReport(ctx, "2026-09", "tuple")
	require.Len(t, got, 1, "after replacing with one row the report is %+v; want rows=1 input=10", got)
	require.Equal(t, 1, got[0].Rows, "after replacing with one row the report is %+v; want rows=1 input=10", got)
	require.Equal(t, int64(10), got[0].Tokens[0], "after replacing with one row the report is %+v; want rows=1 input=10", got)
	// An empty batch clears the day.
	require.NoError(t, s.ReplaceLedgerDay(ctx, "2026-09-13", nil), "replace empty")
	got, _, _, _ = s.LedgerReport(ctx, "2026-09", "tuple")
	require.Empty(t, got, "an empty batch left %+v", got)
}

// TestTheLedgerIsOneHashPerDayUnderTokensLedger pins the key layout the PR states:
// tokens:ledger:<day> is a hash, one field per (card, model, repo), and a type no source
// reported is stored as null, never as 0.
func TestTheLedgerIsOneHashPerDayUnderTokensLedger(t *testing.T) {
	t.Parallel()

	s, mr := redisLedger(t)
	ctx := context.Background()
	require.NoError(t, s.ReplaceLedgerDay(ctx, "2026-09-13", ledgerFixture()), "replace")
	keys := mr.Keys()
	require.Len(t, keys, 1, "keys = %v; want exactly [tokens:ledger:2026-09-13]", keys)
	require.Equal(t, "tokens:ledger:2026-09-13", keys[0], "keys = %v; want exactly [tokens:ledger:2026-09-13]", keys)
	ty := mr.Type("tokens:ledger:2026-09-13")
	require.Equal(t, "hash", ty, "tokens:ledger:2026-09-13 is a %s; want a hash", ty)
	fields, err := mr.HKeys("tokens:ledger:2026-09-13")
	require.NoError(t, err, "fields = %v, %v; want one per (card, model, repo)", fields, err)
	require.Len(t, fields, 2, "fields = %v, %v; want one per (card, model, repo)", fields, err)
	v := mr.HGet("tokens:ledger:2026-09-13", `["card-a","gpt","schema"]`)
	require.Contains(t, v, `"tokens":[10,20,null,null,3]`, "card-a's value = %s; want its tokens as [10,20,null,null,3] (a dash is null, never 0)", v)
}

func TestTheLedgerRefusesARepeatedKeyAForeignDayAndABadDay(t *testing.T) {
	t.Parallel()

	s, mr := redisLedger(t)
	ctx := context.Background()
	rows := ledgerFixture()
	rows[1].Card = rows[0].Card
	err := s.ReplaceLedgerDay(ctx, "2026-09-13", rows)
	require.ErrorContains(t, err, "twice", "a repeated (day, card, model, repo) key was accepted: %v", err)
	require.Error(t, s.ReplaceLedgerDay(ctx, "2026-09-14", ledgerFixture()), "a 2026-09-13 row was accepted in the batch for 2026-09-14")
	require.Error(t, s.ReplaceLedgerDay(ctx, "2026-09-1*", nil), "day 2026-09-1* was accepted; it names a key pattern, not a day")
	keys := mr.Keys()
	require.Empty(t, keys, "a refused batch wrote %v", keys)
}

// TestTheLedgerReportRefusesAValueItCannotRead: a field that does not decode is an error
// naming the key, never a row quietly skipped out of the month.
func TestTheLedgerReportRefusesAValueItCannotRead(t *testing.T) {
	t.Parallel()

	s, mr := redisLedger(t)
	mr.HSet("tokens:ledger:2026-09-02", `["c","m","r"]`, "not json")
	_, _, _, err := s.LedgerReport(context.Background(), "2026-09", "tuple")
	require.ErrorContains(t, err, "tokens:ledger:2026-09-02", "an unreadable value gave %v; want an error naming tokens:ledger:2026-09-02", err)
}

// TestReplaceLedgerDaysTakesOneRoundTrip pins the batching contract from rowan-7fbdefecf56e:
// writing 23 days takes 1 round trip in a single pipeline, where previously each day
// was a separate transaction in a loop (23 round trips for 23 days).
func TestReplaceLedgerDaysTakesOneRoundTrip(t *testing.T) {
	t.Parallel()

	s, _ := redisLedger(t)
	trips := redisconn.CountTrips(s.Client())
	ctx := context.Background()

	// 23 days fixture, matching Rowan's audit benchmark (rowan-7fbdefecf56e: 3+24 trips -> 1 trip for the writes).
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
	require.NoError(t, s.ReplaceLedgerDays(ctx, days), "replace days")
	got := trips.N() - before
	require.Equal(t, int64(1), got, "ReplaceLedgerDays for 23 days took %d round trips; want 1 (rowan-7fbdefecf56e: was 23 transactions in a loop)", got)

	gotTotals, indexed, missing, err := s.LedgerReport(ctx, "2026-09", "day")
	require.NoError(t, err, "report")
	require.Equal(t, 23, indexed, "indexed = %d; want 23", indexed)
	require.Equal(t, 7, missing, "missing = %d; want 7", missing)
	require.Len(t, gotTotals, 23, "got %d totals; want 23", len(gotTotals))
}

// TestReplaceLedgerDaysConflictingWritersAndReaderSnapshots pins the atomic transaction contract
// (Stella note stella-ee1cb7f37a7e, Johnny note johnny-a228317dbe3b): ReplaceLedgerDays uses an EVAL Lua script
// so DEL and HSET execute atomically and revert on error. Competing writers cannot merge partial replacements,
// and readers never see an absent day between DEL and HSET or a torn mixture of rows from conflicting writers.
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
	require.NoError(t, s.ReplaceLedgerDays(ctx, makeBatch("card-a", 100)), "seed failed")

	const iterations = 40
	var writersWg sync.WaitGroup
	writersWg.Add(2)

	// Writer A writes card-a
	go func() {
		defer writersWg.Done()
		batch := makeBatch("card-a", 100)
		for i := 0; i < iterations; i++ {
			if !assert.NoError(t, s.ReplaceLedgerDays(ctx, batch), "writer A error") {
				return
			}
		}
	}()

	// Writer B writes card-b
	go func() {
		defer writersWg.Done()
		batch := makeBatch("card-b", 200)
		for i := 0; i < iterations; i++ {
			if !assert.NoError(t, s.ReplaceLedgerDays(ctx, batch), "writer B error") {
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
			if !assert.NoError(t, err, "reader snapshot error") {
				return
			}
			if !assert.Equal(t, [2]int{3, 27}, [2]int{indexed, missing}, "reader saw incomplete days: indexed=%d missing=%d (want 3 and 27)", indexed, missing) {
				return
			}
			for _, tot := range totals {
				if !assert.Equal(t, 1, tot.Rows, "torn replacement: day %s has %d rows (want 1 row, never merged writers)", tot.Day, tot.Rows) {
					return
				}
				if !assert.Contains(t, []int64{100, 200}, tot.Tokens[0], "corrupt token count in day %s: %d", tot.Day, tot.Tokens[0]) {
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
	require.NoError(t, err, "final report error")
	require.Equal(t, 3, indexed, "final indexed=%d missing=%d; want 3 and 27", indexed, missing)
	require.Equal(t, 27, missing, "final indexed=%d missing=%d; want 3 and 27", indexed, missing)
	require.Len(t, finalTotals, 3, "final totals len = %d; want 3", len(finalTotals))
	for _, tot := range finalTotals {
		require.Equal(t, 1, tot.Rows, "competing writers merged in day %s: rows = %d; want 1", tot.Day, tot.Rows)
	}
}

type injectEvalKeyHook struct {
	fromKey string
	toKey   string
}

func (h *injectEvalKeyHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *injectEvalKeyHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *injectEvalKeyHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "eval" {
			args := cmd.Args()
			for i, a := range args {
				if s, ok := a.(string); ok && s == h.fromKey {
					args[i] = h.toKey
				}
			}
		}
		return next(ctx, cmd)
	}
}

// TestReplaceLedgerDaysInjectedCommandErrorLeavesPriorHashesUnchanged proves that an injected
// command error during execution (such as writing to a key with WRONGTYPE) causes the entire
// batch write to abort atomically, leaving pre-existing day hashes unchanged (Johnny note johnny-a228317dbe3b).
func TestReplaceLedgerDaysInjectedCommandErrorLeavesPriorHashesUnchanged(t *testing.T) {
	t.Parallel()

	s, mr := redisLedger(t)
	ctx := context.Background()

	testDays := []string{"2026-09-01", "2026-09-02", "2026-09-03"}
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

	// 1. Seed all three days with initial rows (token count 100).
	seedBatch := makeBatch("card-seed", 100)
	require.NoError(t, s.ReplaceLedgerDays(ctx, seedBatch), "seed failed")

	// Verify all three days are seeded with token 100.
	for _, day := range testDays {
		key := record.LedgerKey(day)
		fields, err := mr.HKeys(key)
		require.NoError(t, err, "expected 1 field in %s; got fields=%v, err=%v", key, fields, err)
		require.Len(t, fields, 1, "expected 1 field in %s; got fields=%v, err=%v", key, fields, err)
		val := mr.HGet(key, fields[0])
		require.Contains(t, val, `"tokens":[100,null,null,null,null]`, "pre-existing hash %s corrupt: %s", key, val)
	}

	// 2. Set up a non-hash string key in Redis.
	mr.Set("not-a-hash", "plain-string-value")

	// 3. Inject a command error: attach a client hook that retargets the second day key
	// to "not-a-hash" (a string key), causing EVAL execution to encounter WRONGTYPE.
	hook := &injectEvalKeyHook{
		fromKey: record.LedgerKey("2026-09-02"),
		toKey:   "not-a-hash",
	}
	s.Client().AddHook(hook)

	// Attempt ReplaceLedgerDays with a new batch of token count 999.
	newBatch := makeBatch("card-new", 999)
	err := s.ReplaceLedgerDays(ctx, newBatch)
	require.Error(t, err, "expected ReplaceLedgerDays to fail on injected WRONGTYPE error, got nil")
	require.ErrorContains(t, err, "WRONGTYPE", "expected error containing WRONGTYPE, got: %v", err)

	// 4. Assert that every pre-existing day hash remains completely unchanged.
	// Prior days (2026-09-01) and subsequent days (2026-09-02, 2026-09-03) must retain
	// their original seeded rows with token 100, never partially replaced or deleted.
	for _, day := range testDays {
		key := record.LedgerKey(day)
		fields, err := mr.HKeys(key)
		require.NoError(t, err, "pre-existing key %s missing or corrupt after aborted batch: %v", key, err)
		require.Len(t, fields, 1, "pre-existing key %s missing or corrupt after aborted batch: %v", key, err)
		val := mr.HGet(key, fields[0])
		require.Contains(t, val, `"tokens":[100,null,null,null,null]`, "day %s was modified despite aborted batch: %s (want token 100)", key, val)
		require.Contains(t, fields[0], `"card-seed"`, "day %s card was modified: %s (want card-seed)", key, fields[0])
	}

	// 5. Also prove that a recoverable script error occurring mid-batch during mutation
	// (after day 1 was already deleted and rewritten with new rows) catches the error,
	// rolls back day 1, and leaves all pre-existing day hashes unchanged.
	s2, mr2 := redisLedger(t)
	require.NoError(t, s2.ReplaceLedgerDays(ctx, seedBatch), "seed s2 failed")
	mr2.Set("not-a-hash", "plain-string-value")

	s2.Client().AddHook(&injectEvalMidScriptFailHook{})
	err2 := s2.ReplaceLedgerDays(ctx, newBatch)
	require.Error(t, err2, "expected ReplaceLedgerDays to fail on mid-script mutation error, got nil")
	require.ErrorContains(t, err2, "WRONGTYPE", "expected error containing WRONGTYPE, got: %v", err2)

	// Verify day 1 was restored to its pre-existing seeded values (token 100, card-seed).
	for _, day := range testDays {
		key := record.LedgerKey(day)
		fields, err := mr2.HKeys(key)
		require.NoError(t, err, "pre-existing key %s missing or corrupt after mid-script rollback: %v", key, err)
		require.Len(t, fields, 1, "pre-existing key %s missing or corrupt after mid-script rollback: %v", key, err)
		val := mr2.HGet(key, fields[0])
		require.Contains(t, val, `"tokens":[100,null,null,null,null]`, "day %s was not rolled back: %s (want token 100)", key, val)
		require.Contains(t, fields[0], `"card-seed"`, "day %s card was not rolled back: %s (want card-seed)", key, fields[0])
	}
}

type injectEvalMidScriptFailHook struct{}

func (h *injectEvalMidScriptFailHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *injectEvalMidScriptFailHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *injectEvalMidScriptFailHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "eval" {
			args := cmd.Args()
			if script, ok := args[1].(string); ok {
				// Inject command error on day 2 inside pcall: day 1 writes normally,
				// but day 2 calls HSET on "not-a-hash" (a string) to trigger WRONGTYPE mid-batch.
				args[1] = strings.Replace(
					script,
					"set_fields(key, prepared[i])",
					"if i == 2 then redis.call('HSET', 'not-a-hash', 'f', 'v') else set_fields(key, prepared[i]) end",
					1,
				)
			}
		}
		return next(ctx, cmd)
	}
}
