package events

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// openFold opens a fold file inside the test's own temp directory. Every path this package's
// tests write is named here (AGENTS.md rule 10).
func openFold(t *testing.T, name string) *DB {
	t.Helper()
	db, err := OpenDB(context.Background(), filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("opening the fold: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// emitOK writes n `ok` events for n cards, round-robin over two models and two benches so
// the per-model-x-route and per-bench views have something to separate.
func emitOK(t *testing.T, f *FakeStream, n int) {
	t.Helper()
	ctx := context.Background()
	models := []string{"fable", "sonnet"}
	benches := []string{"studio", "hulk"}
	for i := 0; i < n; i++ {
		_, err := f.Emit(ctx, Event{
			Label:   fmt.Sprintf("card-%03d", i),
			Attempt: 1,
			Bench:   benches[i%len(benches)],
			Model:   models[i%len(models)],
			Route:   benches[i%len(benches)],
			Kind:    OK,
			USD:     Float64(0.10),
			At:      at.Add(time.Duration(i) * time.Second),
		})
		if err != nil {
			t.Fatalf("emitting event %d: %v", i, err)
		}
	}
}

// Johnny's bar 3, on the fake: a hundred DONEs are produced, the fold is killed half way
// and restarted, and the count is a hundred. The restart runs under a DIFFERENT consumer
// name, which is what a restarted process really is, so the reclaim has to be XAUTOCLAIM's
// and not "my own pending list".
func TestAHundredDonesSurviveAKillAndARestart(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stream := NewFakeStream()
	stream.Now = func() time.Time { return at }
	emitOK(t, stream, 100)

	db := openFold(t, "ev.sqlite")
	first := &Folder{Reader: stream, DB: db, Group: "fold", Consumer: "fold-a", Count: 10}
	if err := first.Start(ctx); err != nil {
		t.Fatal(err)
	}
	var folded Stats
	for folded.Inserted < 50 {
		s, err := first.Once(ctx)
		if err != nil {
			t.Fatalf("the first fold: %v", err)
		}
		folded.Add(s)
	}
	if folded.Inserted != 50 {
		t.Fatalf("the first fold stopped at %d rows, want 50 (the kill is half way)", folded.Inserted)
	}

	// The kill. Nothing is closed, nothing is drained: the next process just starts.
	second := &Folder{Reader: stream, DB: db, Group: "fold", Consumer: "fold-b", Count: 10}
	if err := second.Start(ctx); err != nil {
		t.Fatal(err)
	}
	for {
		s, err := second.Once(ctx)
		if err != nil {
			t.Fatalf("the restarted fold: %v", err)
		}
		if s.Read == 0 {
			break
		}
		folded.Add(s)
	}

	count, err := db.Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 100 {
		t.Fatalf("the fold holds %d rows after a kill and a restart, want 100 (Johnny's bar 3)", count)
	}
	if got := stream.PendingCount("fold"); got != 0 {
		t.Fatalf("%d entries are still unacked; the fold acks what it has committed", got)
	}
	if folded.Skipped != 0 {
		t.Fatalf("the fold skipped %d entries it could not read, want 0", folded.Skipped)
	}
}

// killAck is the process that dies in the one-instruction window between the commit and the
// XACK. Redis has the entry as delivered-and-unacked, SQLite already has the row, and the
// restart must therefore fold it a second time and end with one row, not two.
type killAck struct {
	Reader
	after int
	seen  int
}

func (k *killAck) Ack(ctx context.Context, group string, ids ...string) error {
	k.seen += len(ids)
	if k.seen > k.after {
		return fmt.Errorf("killed before the ack")
	}
	return k.Reader.Ack(ctx, group, ids...)
}

func TestARedeliveryAfterACommitIsANoOp(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stream := NewFakeStream()
	stream.Now = func() time.Time { return at }
	emitOK(t, stream, 100)

	db := openFold(t, "ev.sqlite")
	dying := &killAck{Reader: stream, after: 50}
	first := &Folder{Reader: dying, DB: db, Group: "fold", Consumer: "fold-a", Count: 10}
	if err := first.Start(ctx); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := first.Once(ctx); err != nil {
			break // the ack that never happened: this is the kill
		}
	}
	if got := stream.PendingCount("fold"); got == 0 {
		t.Fatal("nothing is pending; the test did not reproduce a death between the commit and the ack")
	}

	second := &Folder{Reader: stream, DB: db, Group: "fold", Consumer: "fold-b", Count: 10}
	if err := second.Start(ctx); err != nil {
		t.Fatal(err)
	}
	for {
		s, err := second.Once(ctx)
		if err != nil {
			t.Fatalf("the restarted fold: %v", err)
		}
		if s.Read == 0 {
			break
		}
	}
	count, err := db.Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 100 {
		t.Fatalf("the fold holds %d rows, want 100: the event id is the primary key and a redelivery adds nothing", count)
	}
}

// A rebuild replays the whole stream into a fresh file, and the rows it writes are the rows
// the incremental fold wrote -- byte for byte, because no row carries a fold timestamp.
func TestRebuildProducesTheSameRowsAsTheIncrementalFold(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stream := NewFakeStream()
	stream.Now = func() time.Time { return at }
	emitOK(t, stream, 100)
	// A read, a landing and a decision too, so all four tables are compared and not only
	// attempts.
	for i := 0; i < 3; i++ {
		d := decideEvent()
		d.Label, d.Decision.UnitID = fmt.Sprintf("card-%03d", i), fmt.Sprintf("card-%03d", i)
		if _, err := stream.Emit(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 5; i++ {
		if _, err := stream.Emit(ctx, Event{Label: fmt.Sprintf("card-%03d", i), Kind: Read, Bench: "stella", PR: "2563", At: at}); err != nil {
			t.Fatal(err)
		}
		if _, err := stream.Emit(ctx, Event{Label: fmt.Sprintf("card-%03d", i), Kind: Landed, PR: "2563", Head: "5f544272a1b0", At: at}); err != nil {
			t.Fatal(err)
		}
	}

	incremental := openFold(t, "incremental.sqlite")
	folder := &Folder{Reader: stream, DB: incremental, Group: "fold", Consumer: "fold-a", Count: 7}
	if err := folder.Start(ctx); err != nil {
		t.Fatal(err)
	}
	for {
		s, err := folder.Once(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if s.Read == 0 {
			break
		}
	}

	rebuilt := openFold(t, "rebuilt.sqlite")
	stats, err := Rebuild(ctx, stream, rebuilt, 7)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Inserted != 113 {
		t.Fatalf("the rebuild wrote %d rows, want 113", stats.Inserted)
	}

	var a, b bytes.Buffer
	if err := incremental.Dump(ctx, &a); err != nil {
		t.Fatal(err)
	}
	if err := rebuilt.Dump(ctx, &b); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Fatalf("the rebuild's rows differ from the fold's:\nfold:\n%s\nrebuild:\n%s", firstLines(a.String()), firstLines(b.String()))
	}
	if strings.Count(a.String(), "\n") != 115 { // two headers, 110 card rows and 3 decisions
		t.Fatalf("the dump holds %d lines, want two headers and 113 rows", strings.Count(a.String(), "\n"))
	}

	// A rebuild over a file that already holds the rows is a second no-op, not a doubling.
	again, err := Rebuild(ctx, stream, rebuilt, 7)
	if err != nil {
		t.Fatal(err)
	}
	if again.Inserted != 0 {
		t.Fatalf("a second rebuild wrote %d new rows, want 0", again.Inserted)
	}
}

// The views are the point of the fold: the numbers a coordinator reads and the numbers the
// sprint table will read instead of counting files.
func TestViewsCountPerModelRoutePerBenchAndPerDay(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stream := NewFakeStream()
	stream.Now = func() time.Time { return at }
	emitOK(t, stream, 10) // 5 fable/studio and 5 sonnet/hulk, ten cents each
	if _, err := stream.Emit(ctx, Event{Label: "card-000", Kind: Fail, Bench: "studio", Model: "fable", Route: "studio", USD: Float64(0.05), At: at}); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Emit(ctx, Event{Label: "card-000", Kind: Landed, PR: "2563", At: at}); err != nil {
		t.Fatal(err)
	}

	db := openFold(t, "ev.sqlite")
	folder := &Folder{Reader: stream, DB: db, Group: "fold", Consumer: "fold-a", Count: 100}
	if err := folder.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := folder.Once(ctx); err != nil {
		t.Fatal(err)
	}

	var report bytes.Buffer
	if err := db.Report(ctx, &report, 0); err != nil {
		t.Fatal(err)
	}
	got := report.String()
	for _, want := range []string{
		"# totals",
		"# by_model_route",
		"# by_bench",
		"# by_day",
		"usd_per_ok",
		"usd_per_landed",
		"2026-09-22",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the report does not carry %q:\n%s", want, got)
		}
	}
	// fable/studio: five ok, one fail, $0.55 over one landed card.
	if !strings.Contains(got, "fable\tstudio\t6\t5\t1\t6\t0.55\t0.11\t1\t0.55") {
		t.Errorf("the fable/studio row is not the arithmetic this fold holds:\n%s", got)
	}
	// The row the sprint table reads: eleven done, ten ok, one fail, one landed.
	if !strings.Contains(got, "10\t11\t11\t10\t1\t0\t1\t") {
		t.Errorf("the totals row is not cards=10 rows=11 done=11 ok=10 fail=1 reads=0 landed=1:\n%s", got)
	}
}

// An entry a writer got wrong is a skip with a count, never a stop: one bad writer must not
// stop the fold for every other writer, and the number says how many were lost.
func TestABadEntryIsSkippedAndCounted(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openFold(t, "ev.sqlite")
	inserted, skipped, err := db.Apply(ctx, []Entry{
		{ID: "1-0", Fields: okEvent().Fields()},
		{ID: "2-0", Fields: map[string]string{"label": "card-2", "event": "eaten-by-a-bear"}},
		{ID: "3-0", Fields: map[string]string{"label": "", "event": "ok"}},
		// A cards:done entry written before the event fields were added: no `event`, so it is
		// counted as skipped, never guessed into ok or fail.
		{ID: "4-0", Fields: map[string]string{"label": "card-4", "bench": "studio", "exit": "0", "result": "OK"}},
	})
	if err != nil {
		t.Fatalf("a batch holding a bad entry: %v", err)
	}
	if inserted != 1 || skipped != 3 {
		t.Fatalf("inserted=%d skipped=%d, want 1 and 3", inserted, skipped)
	}
}

// An absent cost folds to NULL, not 0: the dump prints a dash for it, a reported zero stays
// 0, and a totals row over nothing priced is a dash rather than $0.
func TestAMissingCostFoldsToNullNotZero(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openFold(t, "ev.sqlite")
	if _, _, err := db.Apply(ctx, []Entry{
		{ID: "1-0", Fields: map[string]string{"label": "card-1", "event": "queued"}},
		{ID: "2-0", Fields: map[string]string{"label": "card-2", "event": "ok", "usd": "0", "tokens_in": "0", "tokens_out": "0"}},
	}); err != nil {
		t.Fatal(err)
	}
	var nulls, zeros int
	if err := db.db.QueryRowContext(ctx, `SELECT count(*) FROM attempts WHERE usd IS NULL AND tokens_in IS NULL AND tokens_out IS NULL`).Scan(&nulls); err != nil {
		t.Fatal(err)
	}
	if err := db.db.QueryRowContext(ctx, `SELECT count(*) FROM attempts WHERE usd = 0 AND tokens_in = 0 AND tokens_out = 0`).Scan(&zeros); err != nil {
		t.Fatal(err)
	}
	if nulls != 1 || zeros != 1 {
		t.Fatalf("NULL-cost rows=%d zero-cost rows=%d, want 1 and 1: the queued entry reported no cost, the ok entry reported zero", nulls, zeros)
	}
	var dump bytes.Buffer
	if err := db.Dump(ctx, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), "attempts\t1-0\tcard-1\t0\t\t\t\tqueued\t-\t-\t-\t") {
		t.Errorf("the dump does not print the absent cost as dashes:\n%s", dump.String())
	}
	if !strings.Contains(dump.String(), "attempts\t2-0\tcard-2\t0\t\t\t\tok\t0\t0\t0\t") {
		t.Errorf("the dump does not keep the reported zero:\n%s", dump.String())
	}

	unpriced := openFold(t, "unpriced.sqlite")
	if _, _, err := unpriced.Apply(ctx, []Entry{{ID: "1-0", Fields: map[string]string{"label": "card-1", "event": "ok"}}}); err != nil {
		t.Fatal(err)
	}
	var report bytes.Buffer
	if err := unpriced.Report(ctx, &report, 0); err != nil {
		t.Fatal(err)
	}
	// cards rows done ok fail reads landed usd: the usd of a fold that holds no price is a dash.
	if !strings.Contains(report.String(), "1\t1\t1\t1\t0\t0\t0\t-\n") {
		t.Errorf("the totals row prices an unpriced fold; want usd as a dash:\n%s", report.String())
	}
}

func firstLines(s string) string {
	lines := strings.SplitN(s, "\n", 6)
	if len(lines) > 5 {
		lines = lines[:5]
	}
	return strings.Join(lines, "\n")
}
