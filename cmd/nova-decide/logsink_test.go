package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/events"
)

// --log is the JSON lines log; --store also writes each decision as one decide
// event on cards:done, which the fold keeps in its decisions table (#2623; the
// decide_log table this replaced is retired). The accounting rule of #1327 stands
// for both, and no credential ever appears on argv.

// openedSink replaces the log opener for the length of one test, and reports
// what the verb asked for.
type openedSink struct {
	path  string
	sink  decide.LogSink
	err   error
	opens int
}

func useSink(t *testing.T, o *openedSink) {
	t.Helper()
	was := logSinkOpener
	logSinkOpener = func(path string) (decide.LogSink, error) {
		o.opens++
		o.path = path
		if o.err != nil {
			return nil, o.err
		}
		return o.sink, nil
	}
	t.Cleanup(func() { logSinkOpener = was })
}

// A route told --log writes its decision to the log and is accounted for. The
// kind is a judgement kind: a mechanical kind with no confirmed failure never
// calls the provider (#1513), so it would have no spend to record.
func TestRouteLogsToTheLog(t *testing.T) {
	useFake(t, &fake{conf: 0.97, usage: decide.Usage{InputTokens: 937, HasInput: true, OutputTokens: 12, HasOutput: true}})
	sink := decide.NewFakeLogSink()
	o := &openedSink{sink: sink}
	useSink(t, o)
	usage := filepath.Join(t.TempDir(), "usage.tsv")
	log := filepath.Join(t.TempDir(), "decide.jsonl")

	var stdout, stderr bytes.Buffer
	code := run([]string{"route", "--unit-id", "u", "--kind", "new-verb", "--files", "2", "--packages", "1",
		"--usage", usage, "--log", log}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d (stderr=%q)", code, stderr.String())
	}
	if o.path != log {
		t.Errorf("the verb opened %q, want %q", o.path, log)
	}
	rows, err := sink.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("one decision is one row, got %d", len(rows))
	}
	if rows[0].Unit != "u" || rows[0].Kind != "new-verb" {
		t.Errorf("the row is not the decision: %+v", rows[0])
	}
	if rows[0].TokensIn == nil || *rows[0].TokensIn != 937 {
		t.Errorf("the spend is not on the row: %v", rows[0].TokensIn)
	}
}

// The decide_log table's flags are gone, not ignored: a DSN or its variable on
// route, and the migrate sub-verb on log, are refusals (#2623).
func TestTheRetiredTableFlagsAreRefused(t *testing.T) {
	useFake(t, &fake{conf: 0.97})
	for _, args := range [][]string{
		{"route", "--unit-id", "u", "--kind", "rebase", "--no-jev", "--dsn-env", "FLEET_PG"},
		{"route", "--unit-id", "u", "--kind", "rebase", "--no-jev", "--dsn", "db://u:p@h/db"},
		{"log", "--log", "./decide.jsonl", "--summary", "--dsn-env", "FLEET_PG"},
		{"log", "migrate"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 2 {
			t.Errorf("%v: exit = %d, want 2", args, code)
		}
		if !strings.Contains(stderr.String(), "bad-flags") {
			t.Errorf("%v: want bad-flags, got %q", args, stderr.String())
		}
	}
}

// A log that will not open is refused BEFORE the provider is called: a jev call
// with nowhere to record it is refused rather than made (#1327).
func TestRouteRefusesBeforeTheCallWhenTheLogWillNotOpen(t *testing.T) {
	f := &fake{conf: 0.97}
	useFake(t, f)
	useSink(t, &openedSink{err: errors.New("open /nowhere/decide.jsonl: no such file or directory")})
	usage := filepath.Join(t.TempDir(), "usage.tsv")

	var stdout, stderr bytes.Buffer
	code := run([]string{"route", "--unit-id", "u", "--kind", "rebase", "--files", "1",
		"--usage", usage, "--log", "/nowhere/decide.jsonl"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr=%q)", code, stderr.String())
	}
	if f.calls != 0 {
		t.Errorf("the provider was called %d times with nowhere to record the decision", f.calls)
	}
	line := strings.TrimSuffix(stderr.String(), "\n")
	if strings.Contains(line, "\n") {
		t.Errorf("a refusal is one line: %q", stderr.String())
	}
	if !strings.Contains(line, "no-accounting") {
		t.Errorf("want no-accounting, got %q", line)
	}
}

// A store that does not answer is refused BEFORE the provider is called too,
// and the password is read from the variable --password-env names, never argv.
func TestRouteRefusesBeforeTheCallWhenTheStoreWillNotAnswer(t *testing.T) {
	f := &fake{conf: 0.97}
	useFake(t, f)
	useSink(t, &openedSink{sink: decide.NewFakeLogSink()})
	t.Setenv("FLEET_REDIS_PW", "s3cret")
	var gotAddr, gotUser, gotPassword string
	was := eventSinkOpener
	eventSinkOpener = func(addr, user, password string) (decide.LogSink, error) {
		gotAddr, gotUser, gotPassword = addr, user, password
		return nil, errors.New("redis at store.invalid:6380: dial tcp: no such host")
	}
	t.Cleanup(func() { eventSinkOpener = was })

	var stdout, stderr bytes.Buffer
	code := run([]string{"route", "--unit-id", "u", "--kind", "rebase", "--files", "1",
		"--usage", filepath.Join(t.TempDir(), "usage.tsv"), "--log", filepath.Join(t.TempDir(), "decide.jsonl"),
		"--store", "store.invalid:6380", "--user", "bench", "--password-env", "FLEET_REDIS_PW"}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "no-accounting") {
		t.Fatalf("exit = %d, want 2 with no-accounting (stderr=%q)", code, stderr.String())
	}
	if f.calls != 0 {
		t.Errorf("the provider was called %d times with a store that does not answer", f.calls)
	}
	if gotAddr != "store.invalid:6380" || gotUser != "bench" || gotPassword != "s3cret" {
		t.Errorf("the opener got addr=%q user=%q password=%q; want the flags and the variable's value", gotAddr, gotUser, gotPassword)
	}
	if strings.Contains(stderr.String(), "s3cret") {
		t.Errorf("the refusal printed the password: %q", stderr.String())
	}
}

// The round trip #2623 asks for, end to end: `nova-decide route --store` writes
// the decision through the events writer onto cards:done, and the fold reads it
// into its decisions table. A --no-jev decision makes no call, so it reports no
// tokens: the counters are ABSENT on the entry and NULL in the fold, never 0.
// The JSON lines log gets the same decision beside it.
func TestRouteStoreWritesADecideEventTheFoldReads(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	log := filepath.Join(t.TempDir(), "decide.jsonl")

	var stdout, stderr bytes.Buffer
	code := run([]string{"route", "--unit-id", "card-41", "--kind", "rebase", "--files", "2", "--packages", "1",
		"--no-jev", "--log", log, "--store", mr.Addr()}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d (stderr=%q)", code, stderr.String())
	}
	rows, err := decide.ReadEntries(log)
	if err != nil || len(rows) != 1 {
		t.Fatalf("the JSON lines log holds %d rows (err %v), want 1", len(rows), err)
	}

	ctx := context.Background()
	store, err := events.Open(ctx, events.Dial{Addr: mr.Addr()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	raw, err := store.Range(ctx, "-", 10)
	if err != nil || len(raw) != 1 {
		t.Fatalf("cards:done holds %d entries (err %v), want the one decision", len(raw), err)
	}
	f := raw[0].Fields
	if f["event"] != "decide" || f["unit_id"] != "card-41" || f["kind"] != "rebase" || f["rung_tried"] != rows[0].RungTried {
		t.Fatalf("the entry is not the decision: %v", f)
	}
	for _, absent := range []string{"tokens_in", "tokens_out", "usd", "refusal", "outcome"} {
		if v, ok := f[absent]; ok {
			t.Errorf("a decision that made no call carries %s=%q; it must be absent", absent, v)
		}
	}

	db, err := events.OpenDB(ctx, filepath.Join(t.TempDir(), "ev.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	folder := &events.Folder{Reader: store, DB: db}
	if err := folder.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := folder.Once(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := db.CountKind(ctx, events.Decide); err != nil || n != 1 {
		t.Fatalf("the fold holds %d decisions (err %v), want 1", n, err)
	}
	var dump bytes.Buffer
	if err := db.Dump(ctx, &dump); err != nil {
		t.Fatal(err)
	}
	// calls tokens_in tokens_out usd usage_failed: no call, so no counters.
	if !strings.Contains(dump.String(), "\tcard-41\trebase\t2\t1\t") || !strings.Contains(dump.String(), "\t0\t-\t-\t-\t0\n") {
		t.Fatalf("the fold's row is not the decision, or an absent counter is not a dash:\n%s", dump.String())
	}
}

// The summary reads the JSON lines log, and reads the same rows off the fake.
func TestLogSummaryReadsTheLog(t *testing.T) {
	entries := []decide.Entry{
		{Time: "2026-09-18T10:00:00Z", Unit: "u1", Kind: "rebase", Evidence: decide.Unit{ID: "u1", Kind: "rebase", Files: 2},
			RungTried: "flash", Confidence: num(0.94), Floor: num(decide.DefaultFloor), Source: decide.SourceRules,
			RowanPick: "flash", Wait: decide.WaitNone, Outcome: decide.OutcomeOK, RungSucceeded: "flash"},
		{Time: "2026-09-18T10:01:00Z", Unit: "u2", Kind: "rebase", Evidence: decide.Unit{ID: "u2", Kind: "rebase", Files: 9},
			RungTried: "flash", Confidence: num(0.5), Floor: num(decide.DefaultFloor), Source: decide.SourceRules,
			RowanPick: "flash", Wait: decide.WaitNone, Outcome: decide.OutcomeFailed},
	}
	path := filepath.Join(t.TempDir(), "decide.jsonl")
	for _, e := range entries {
		if err := decide.AppendEntry(path, e); err != nil {
			t.Fatal(err)
		}
	}
	var fileOut, fileErr bytes.Buffer
	if code := run([]string{"log", "--log", path, "--summary"}, &fileOut, &fileErr); code != 0 {
		t.Fatalf("file summary exit = %d (stderr=%q)", code, fileErr.String())
	}

	sink := decide.NewFakeLogSink()
	for _, e := range entries {
		if err := sink.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	o := &openedSink{sink: sink}
	useSink(t, o)
	var fakeOut, fakeErr bytes.Buffer
	if code := run([]string{"log", "--log", "elsewhere.jsonl", "--summary"}, &fakeOut, &fakeErr); code != 0 {
		t.Fatalf("fake summary exit = %d (stderr=%q)", code, fakeErr.String())
	}
	if o.path != "elsewhere.jsonl" {
		t.Errorf("the verb opened %q, want elsewhere.jsonl", o.path)
	}
	if fileOut.String() != fakeOut.String() {
		t.Errorf("the same rows are the same summary:\nfile: %q\nfake: %q", fileOut.String(), fakeOut.String())
	}
	if !strings.Contains(fileOut.String(), "LOG OK rows=2") {
		t.Errorf("two rows are two rows: %q", fileOut.String())
	}
}

// num is a confidence or floor a row carries: present, so a pointer to it.
func num(v float64) *float64 { return &v }
