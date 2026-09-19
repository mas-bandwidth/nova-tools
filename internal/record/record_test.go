package record_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/record"
	"github.com/redis/go-redis/v9"
)

// testNow is the clock every unit test pins; nothing here reads the wall clock.
var testNow = time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

func at(t time.Time) *time.Time { return &t }

func TestRowFromMessageCarriesEveryField(t *testing.T) {
	pushed := testNow.Add(-time.Hour)
	done := testNow.Add(-time.Minute)
	fields := map[string]string{
		"label":     "9347",
		"bench":     "space",
		"exit":      "0",
		"result":    "RESULT: CARD-9347 card results in Postgres",
		"job":       "/jobs/card-9347",
		"commit":    "abc1234",
		"branch":    "rowan/postgres-card-results",
		"pr":        "1263",
		"pushed_at": pushed.Format(time.RFC3339),
		"done_at":   done.Format(time.RFC3339),
	}
	r, err := record.RowFromMessage("7-0", fields, testNow)
	if err != nil {
		t.Fatalf("parse a complete result: %s", err)
	}
	if r.StreamID != "7-0" || r.Label != "9347" || r.Bench != "space" || r.Exit != 0 {
		t.Fatalf("identity fields landed wrong: %+v", r)
	}
	if r.ResultLine != "RESULT: CARD-9347 card results in Postgres" || r.JobPath != "/jobs/card-9347" {
		t.Fatalf("result line or job path landed wrong: %+v", r)
	}
	if r.Commit != "abc1234" || r.Branch != "rowan/postgres-card-results" || r.PR != "1263" {
		t.Fatalf("commit, branch or pr landed wrong: %+v", r)
	}
	if r.PushedAt == nil || !r.PushedAt.Equal(pushed) {
		t.Fatalf("pushed_at = %v, want %v", r.PushedAt, pushed)
	}
	if r.DoneAt == nil || !r.DoneAt.Equal(done) {
		t.Fatalf("done_at = %v, want %v", r.DoneAt, done)
	}
	if !r.RecordedAt.Equal(testNow) {
		t.Fatalf("recorded_at = %v, want %v", r.RecordedAt, testNow)
	}
}

func TestRowFromMessageAcceptsTheStreamsAliasNames(t *testing.T) {
	fields := map[string]string{
		"card":        "9347",
		"bench":       "space",
		"exit":        "1",
		"result_line": "boom",
		"job_path":    "/jobs/card-9347",
		"sha":         "deadbeef",
		"branch":      "main",
	}
	r, err := record.RowFromMessage("8-0", fields, testNow)
	if err != nil {
		t.Fatalf("parse alias fields: %s", err)
	}
	if r.Label != "9347" || r.Exit != 1 || r.ResultLine != "boom" || r.JobPath != "/jobs/card-9347" || r.Commit != "deadbeef" {
		t.Fatalf("alias fields landed wrong: %+v", r)
	}
	if r.DoneAt != nil || r.PushedAt != nil {
		t.Fatalf("absent timestamps must stay unknown, got done=%v pushed=%v", r.DoneAt, r.PushedAt)
	}
}

func TestRowFromMessageRefusesAResultWithNoLabel(t *testing.T) {
	if _, err := record.RowFromMessage("9-0", map[string]string{"bench": "space"}, testNow); err == nil {
		t.Fatal("a result with no label parsed; a row needs a card")
	}
}

func TestInsertIsIdempotentOnTheStreamID(t *testing.T) {
	s := record.NewFakeStore()
	ctx := context.Background()
	row := record.Row{StreamID: "1-0", Label: "9347", Bench: "space", RecordedAt: testNow}
	first, err := s.Insert(ctx, row)
	if err != nil {
		t.Fatalf("first insert: %s", err)
	}
	second, err := s.Insert(ctx, row)
	if err != nil {
		t.Fatalf("second insert: %s", err)
	}
	if !first || second {
		t.Fatalf("inserted flags = %v, %v; want true, false", first, second)
	}
	if got := len(s.Rows()); got != 1 {
		t.Fatalf("rows after redelivery = %d, want 1", got)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	s := record.NewFakeStore()
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("first migrate: %s", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("second migrate: %s", err)
	}
	if got := s.Versions(); got != 1 {
		t.Fatalf("schema versions after two migrations = %d, want 1", got)
	}
}

func TestListFiltersSinceBenchAndFailed(t *testing.T) {
	s := record.NewFakeStore()
	ctx := context.Background()
	rows := []record.Row{
		{StreamID: "1-0", Label: "old", Bench: "space", Exit: 0, DoneAt: at(testNow.Add(-2 * time.Hour)), RecordedAt: testNow},
		{StreamID: "2-0", Label: "failed", Bench: "space", Exit: 1, DoneAt: at(testNow.Add(-time.Minute)), RecordedAt: testNow},
		{StreamID: "3-0", Label: "other", Bench: "mac", Exit: 1, DoneAt: at(testNow), RecordedAt: testNow},
	}
	for _, r := range rows {
		if _, err := s.Insert(ctx, r); err != nil {
			t.Fatalf("seed %s: %s", r.StreamID, err)
		}
	}
	got, err := s.List(ctx, record.Filter{Since: testNow.Add(-time.Hour), Bench: "space", Failed: true})
	if err != nil {
		t.Fatalf("list: %s", err)
	}
	if len(got) != 1 || got[0].StreamID != "2-0" {
		t.Fatalf("filtered rows = %+v, want only 2-0", got)
	}
}

func openConsumer(t *testing.T, mr *miniredis.Miniredis) record.Consumer {
	t.Helper()
	c, err := record.NewRedisConsumer(context.Background(), mr.Addr(), record.Stream, record.Group, "test-consumer")
	if err != nil {
		t.Fatalf("open redis consumer over miniredis: %s", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func addResult(t *testing.T, mr *miniredis.Miniredis, id string, kv ...string) {
	t.Helper()
	if _, err := mr.XAdd(record.Stream, id, kv); err != nil {
		t.Fatalf("xadd %s: %s", id, err)
	}
}

func pending(t *testing.T, mr *miniredis.Miniredis) int64 {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	p, err := rdb.XPending(context.Background(), record.Stream, record.Group).Result()
	if err != nil {
		t.Fatalf("xpending: %s", err)
	}
	return p.Count
}

func TestConsumeWritesARowForEachResultAndAcksIt(t *testing.T) {
	mr := miniredis.RunT(t)
	store := record.NewFakeStore()
	addResult(t, mr, "1-0", "label", "a", "bench", "space", "exit", "0", "result", "one", "job", "/j/a", "commit", "aaa", "branch", "b")
	addResult(t, mr, "2-0", "label", "b", "bench", "space", "exit", "1", "result", "two", "job", "/j/b", "commit", "bbb", "branch", "b")
	var out strings.Builder
	counts, err := record.Consume(context.Background(), store, openConsumer(t, mr),
		record.RecordOptions{Once: true, Now: func() time.Time { return testNow }}, &out)
	if err != nil {
		t.Fatalf("consume: %s", err)
	}
	if counts.Seen != 2 || counts.Inserted != 2 || counts.Duplicate != 0 {
		t.Fatalf("counts = %+v, want 2 seen, 2 inserted", counts)
	}
	if got := len(store.Rows()); got != 2 {
		t.Fatalf("rows = %d, want 2", got)
	}
	if p := pending(t, mr); p != 0 {
		t.Fatalf("pending after consume = %d, want 0 (every committed row is acked)", p)
	}
	if !strings.Contains(out.String(), "inserted=true") {
		t.Fatalf("output does not name the inserted rows:\n%s", out.String())
	}
}

func TestConsumeIsIdempotentOnTheStreamID(t *testing.T) {
	mr := miniredis.RunT(t)
	store := record.NewFakeStore()
	if _, err := store.Insert(context.Background(), record.Row{StreamID: "1-0", Label: "a", RecordedAt: testNow}); err != nil {
		t.Fatalf("seed: %s", err)
	}
	addResult(t, mr, "1-0", "label", "a", "bench", "space", "exit", "0")
	var out strings.Builder
	counts, err := record.Consume(context.Background(), store, openConsumer(t, mr),
		record.RecordOptions{Once: true, Now: func() time.Time { return testNow }}, &out)
	if err != nil {
		t.Fatalf("consume: %s", err)
	}
	if counts.Seen != 1 || counts.Inserted != 0 || counts.Duplicate != 1 {
		t.Fatalf("counts = %+v, want 1 seen, 1 duplicate", counts)
	}
	if got := len(store.Rows()); got != 1 {
		t.Fatalf("rows after a redelivered result = %d, want 1", got)
	}
	if p := pending(t, mr); p != 0 {
		t.Fatalf("pending = %d, want 0; a duplicate is still safe to ack", p)
	}
}

// failingStore refuses a committed insert a fixed number of times, standing in for a
// Postgres that is briefly unreachable. It must leave the message pending until a later
// consume commits it.
type failingStore struct {
	*record.FakeStore
	failures int
}

func (f *failingStore) Insert(ctx context.Context, r record.Row) (bool, error) {
	if f.failures > 0 {
		f.failures--
		return false, errors.New("commit refused")
	}
	return f.FakeStore.Insert(ctx, r)
}

func TestConsumeDoesNotAckWhenTheCommitFailsThenRecovers(t *testing.T) {
	mr := miniredis.RunT(t)
	store := &failingStore{FakeStore: record.NewFakeStore(), failures: 1}
	addResult(t, mr, "1-0", "label", "a", "bench", "space", "exit", "0")
	c := openConsumer(t, mr)
	var out strings.Builder
	if _, err := record.Consume(context.Background(), store, c,
		record.RecordOptions{Once: true, Now: func() time.Time { return testNow }}, &out); err == nil {
		t.Fatal("consume swallowed a failed commit")
	}
	if p := pending(t, mr); p != 1 {
		t.Fatalf("a failed commit acked the message: pending = %d, want 1", p)
	}
	if got := len(store.Rows()); got != 0 {
		t.Fatalf("a failed commit wrote %d rows, want 0", got)
	}
	counts, err := record.Consume(context.Background(), store, c,
		record.RecordOptions{Once: true, Now: func() time.Time { return testNow }}, &out)
	if err != nil {
		t.Fatalf("retry consume: %s", err)
	}
	if counts.Inserted != 1 {
		t.Fatalf("retry counts = %+v, want 1 inserted", counts)
	}
	if p := pending(t, mr); p != 0 {
		t.Fatalf("pending after the retry = %d, want 0", p)
	}
}

func TestConsumeReportsAMalformedResultAndStillAcksIt(t *testing.T) {
	mr := miniredis.RunT(t)
	store := record.NewFakeStore()
	addResult(t, mr, "1-0", "bench", "space", "exit", "0")
	var out strings.Builder
	counts, err := record.Consume(context.Background(), store, openConsumer(t, mr),
		record.RecordOptions{Once: true, Now: func() time.Time { return testNow }}, &out)
	if err != nil {
		t.Fatalf("consume: %s", err)
	}
	if counts.Malformed != 1 || counts.Inserted != 0 {
		t.Fatalf("counts = %+v, want 1 malformed", counts)
	}
	if p := pending(t, mr); p != 0 {
		t.Fatalf("a malformed result was left to poison the group: pending = %d", p)
	}
	if !strings.Contains(out.String(), "RECORD BAD") {
		t.Fatalf("the malformed result was not named:\n%s", out.String())
	}
}
