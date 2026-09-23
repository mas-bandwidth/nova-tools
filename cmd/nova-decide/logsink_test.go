package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

// --log takes a path or the word postgres (Glenn 2026-09-18: the decision log
// lives in Postgres beside the card results). The accounting rule of #1327
// stands for both, and the DSN never appears on argv.

// useSink replaces the sink opener for the length of one test, and reports what
// the verb asked for.
type openedSink struct {
	name   string
	dsnEnv string
	sink   decide.LogSink
	err    error
	opens  int
}

func useSink(t *testing.T, o *openedSink) {
	t.Helper()
	was := logSinkOpener
	logSinkOpener = func(name, dsnEnv string) (decide.LogSink, error) {
		o.opens++
		o.name, o.dsnEnv = name, dsnEnv
		if o.err != nil {
			return nil, o.err
		}
		return o.sink, nil
	}
	t.Cleanup(func() { logSinkOpener = was })
}

// A route told --log postgres writes its decision to the table and is accounted
// for: the accounting rule is satisfied by a sink, not by a file.
func TestRouteLogsToTheTable(t *testing.T) {
	useFake(t, &fake{conf: 0.97, usage: decide.Usage{InputTokens: 937, HasInput: true, OutputTokens: 12, HasOutput: true}})
	sink := decide.NewFakeLogSink()
	o := &openedSink{sink: sink}
	useSink(t, o)
	usage := filepath.Join(t.TempDir(), "usage.tsv")

	var stdout, stderr bytes.Buffer
	code := run([]string{"route", "--unit-id", "u", "--kind", "rebase", "--files", "2", "--packages", "1",
		"--usage", usage, "--log", "postgres"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d (stderr=%q)", code, stderr.String())
	}
	if o.name != "postgres" {
		t.Errorf("the verb asked for %q, want postgres", o.name)
	}
	rows, err := sink.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("one decision is one row, got %d", len(rows))
	}
	if rows[0].Unit != "u" || rows[0].Kind != "rebase" {
		t.Errorf("the row is not the decision: %+v", rows[0])
	}
	if rows[0].TokensIn == nil || *rows[0].TokensIn != 937 {
		t.Errorf("the spend is not on the row: %v", rows[0].TokensIn)
	}
}

// --dsn-env names the variable the DSN arrives in, and it is handed to the
// opener rather than read into a flag: a DSN is never on argv.
func TestRoutePassesTheDSNVariableNameNotTheDSN(t *testing.T) {
	useFake(t, &fake{conf: 0.97})
	o := &openedSink{sink: decide.NewFakeLogSink()}
	useSink(t, o)
	usage := filepath.Join(t.TempDir(), "usage.tsv")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"route", "--unit-id", "u", "--kind", "rebase", "--files", "1",
		"--usage", usage, "--log", "postgres", "--dsn-env", "FLEET_PG"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d (stderr=%q)", code, stderr.String())
	}
	if o.dsnEnv != "FLEET_PG" {
		t.Errorf("the opener was given %q, want FLEET_PG", o.dsnEnv)
	}
}

// There is no --dsn flag on route: a password never reaches a process listing.
func TestRouteHasNoDSNFlag(t *testing.T) {
	useFake(t, &fake{conf: 0.97})
	var stdout, stderr bytes.Buffer
	code := run([]string{"route", "--unit-id", "u", "--kind", "rebase",
		"--usage", "/dev/null", "--log", "postgres", "--dsn", "postgres://u:p@h/db"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("a DSN on argv must be refused, exit = %d", code)
	}
	if !strings.Contains(stderr.String(), "bad-flags") {
		t.Errorf("want bad-flags, got %q", stderr.String())
	}
}

// A log that will not open is refused BEFORE the provider is called: a jev call
// with nowhere to record it is refused rather than made (#1327).
func TestRouteRefusesBeforeTheCallWhenTheLogWillNotOpen(t *testing.T) {
	f := &fake{conf: 0.97}
	useFake(t, f)
	useSink(t, &openedSink{err: errors.New("$NOVA_DECIDE_LOG_DSN is not set")})
	usage := filepath.Join(t.TempDir(), "usage.tsv")

	var stdout, stderr bytes.Buffer
	code := run([]string{"route", "--unit-id", "u", "--kind", "rebase", "--files", "1",
		"--usage", usage, "--log", "postgres"}, &stdout, &stderr)
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
	if !strings.Contains(line, "NOVA_DECIDE_LOG_DSN") {
		t.Errorf("the refusal must name the variable to set: %q", line)
	}
}

// The summary is the same read off either sink.
func TestLogSummaryReadsTheTable(t *testing.T) {
	entries := []decide.Entry{
		{Time: "2026-09-18T10:00:00Z", Unit: "u1", Kind: "rebase", Evidence: decide.Unit{ID: "u1", Kind: "rebase", Files: 2},
			RungTried: "flash", Confidence: 0.94, Floor: decide.DefaultFloor, Source: decide.SourceRules,
			RowanPick: "flash", Wait: decide.WaitNone, Outcome: decide.OutcomeOK, RungSucceeded: "flash"},
		{Time: "2026-09-18T10:01:00Z", Unit: "u2", Kind: "rebase", Evidence: decide.Unit{ID: "u2", Kind: "rebase", Files: 9},
			RungTried: "flash", Confidence: 0.5, Floor: decide.DefaultFloor, Source: decide.SourceRules,
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
	var tableOut, tableErr bytes.Buffer
	if code := run([]string{"log", "--log", "postgres", "--summary"}, &tableOut, &tableErr); code != 0 {
		t.Fatalf("table summary exit = %d (stderr=%q)", code, tableErr.String())
	}
	if o.name != "postgres" {
		t.Errorf("the verb asked for %q, want postgres", o.name)
	}
	if fileOut.String() != tableOut.String() {
		t.Errorf("the same rows are the same summary:\nfile:  %q\ntable: %q", fileOut.String(), tableOut.String())
	}
	if !strings.Contains(fileOut.String(), "LOG OK rows=2") {
		t.Errorf("two rows are two rows: %q", fileOut.String())
	}
}

// migratingSink is a sink that records that it was migrated.
type migratingSink struct {
	*decide.FakeLogSink
	migrated int
	err      error
}

func (m *migratingSink) Migrate(ctx context.Context) error {
	m.migrated++
	return m.err
}

// `log migrate` applies the migration to the table and says so on one line.
func TestLogMigrateAppliesTheMigration(t *testing.T) {
	m := &migratingSink{FakeLogSink: decide.NewFakeLogSink()}
	o := &openedSink{sink: m}
	useSink(t, o)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"log", "migrate", "--dsn-env", "FLEET_PG"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d (stderr=%q)", code, stderr.String())
	}
	if m.migrated != 1 {
		t.Errorf("the migration ran %d times, want 1", m.migrated)
	}
	if o.name != "postgres" || o.dsnEnv != "FLEET_PG" {
		t.Errorf("migrate opened %q with %q; it is the table's verb", o.name, o.dsnEnv)
	}
	line := strings.TrimSuffix(stdout.String(), "\n")
	if !strings.HasPrefix(line, "MIGRATE OK") {
		t.Errorf("one line, and it says what happened: %q", stdout.String())
	}
	if strings.Contains(line, "\n") {
		t.Errorf("one line: %q", stdout.String())
	}
}

// `log migrate` never takes a DSN on argv.
func TestLogMigrateHasNoDSNFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"log", "migrate", "--dsn", "postgres://u:p@h/db"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("a DSN on argv must be refused, exit = %d", code)
	}
	if !strings.Contains(stderr.String(), "bad-flags") {
		t.Errorf("want bad-flags, got %q", stderr.String())
	}
}

// A migration that fails is a refusal on one line, not a silent success.
func TestLogMigrateRefusesWhenTheMigrationFails(t *testing.T) {
	m := &migratingSink{FakeLogSink: decide.NewFakeLogSink(), err: errors.New("permission denied for schema public")}
	useSink(t, &openedSink{sink: m})
	var stdout, stderr bytes.Buffer
	if code := run([]string{"log", "migrate"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "bad-migration") {
		t.Errorf("want bad-migration, got %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("a refusal belongs on stderr: %q", stdout.String())
	}
}

// A file sink has nothing to migrate, and says so rather than pretending.
func TestLogMigrateRefusesAFileSink(t *testing.T) {
	useSink(t, &openedSink{sink: decide.NewFakeLogSink()}) // no Migrate method
	var stdout, stderr bytes.Buffer
	if code := run([]string{"log", "migrate"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "bad-migration") {
		t.Errorf("want bad-migration, got %q", stderr.String())
	}
}
