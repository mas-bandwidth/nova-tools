package decide

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The log has two sinks and one contract. The JSONL file is what it has always
// been; Postgres is the same rows beside the card results (Glenn 2026-09-18).
// Everything below runs on the fake or on a file: a unit test never opens a
// socket.

func sampleEntry() Entry {
	in := 937
	return Entry{
		Time:       "2026-09-18T10:00:00Z",
		Unit:       "u1",
		Kind:       KindRebase,
		Evidence:   Unit{ID: "u1", Kind: KindRebase, Files: 2, Packages: 1, Lanes: 1, LaneOwner: "decide"},
		RungTried:  "flash",
		Height:     0,
		Confidence: 0.94,
		Floor:      DefaultFloor,
		Source:     SourceRules,
		RowanPick:  "flash",
		Wait:       WaitNone,
		Outcome:    OutcomeOK,
		Calls:      1,
		TokensIn:   &in,
	}
}

// OpenLogSink picks the sink by name: "postgres" is the table, anything else is
// a path. An empty name is a refusal, never a guess at one.
func TestOpenLogSinkChoosesByName(t *testing.T) {
	if _, err := OpenLogSink("", ""); err == nil {
		t.Fatal("an empty --log must refuse rather than guess a path")
	}
	path := filepath.Join(t.TempDir(), "decide.jsonl")
	sink, err := OpenLogSink(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	if _, ok := sink.(*FileSink); !ok {
		t.Fatalf("a path is the JSONL sink, got %T", sink)
	}
}

// "postgres" with no DSN in the environment is a refusal that names the
// variable to set, because the DSN never comes in on argv.
func TestPostgresLogRefusesWithoutADSNInTheEnvironment(t *testing.T) {
	name := "NOVA_DECIDE_LOG_DSN_TEST_ABSENT"
	os.Unsetenv(name)
	_, err := OpenLogSink(PostgresLog, name)
	if err == nil {
		t.Fatal("postgres with no DSN must refuse")
	}
	if !strings.Contains(err.Error(), name) {
		t.Errorf("the refusal must name the variable to set: %q", err)
	}
	if strings.Contains(err.Error(), "--dsn ") {
		t.Errorf("the DSN is never asked for on argv: %q", err)
	}
}

// The file sink is the log as it has always been: the same rows AppendEntry
// wrote and ReadEntries read.
func TestFileSinkIsTheJSONLLogItAlwaysWas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decide.jsonl")
	sink, err := OpenLogSink(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	first := sampleEntry()
	second := sampleEntry()
	second.Unit = "u2"
	for _, e := range []Entry{first, second} {
		if err := sink.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	direct, err := ReadEntries(path)
	if err != nil {
		t.Fatal(err)
	}
	through, err := sink.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(direct) != 2 || len(through) != 2 {
		t.Fatalf("two decisions are two rows: direct=%d sink=%d", len(direct), len(through))
	}
	if through[1].Unit != "u2" || through[0].Evidence.Files != 2 {
		t.Errorf("the sink lost the row: %+v", through)
	}
}

// Stella's presence rule: a counter the provider did not report is ABSENT, not
// zero. It survives the row shape the table is written in.
func TestLogRowKeepsAnUnreportedCounterAbsent(t *testing.T) {
	e := sampleEntry()
	if e.TokensOut != nil {
		t.Fatal("the fixture reports no output tokens")
	}
	row, err := logRowFor(e)
	if err != nil {
		t.Fatal(err)
	}
	if !row.TokensIn.Valid || row.TokensIn.Int64 != 937 {
		t.Errorf("a reported counter is a measurement: %+v", row.TokensIn)
	}
	if row.TokensOut.Valid {
		t.Errorf("an unreported counter is SQL NULL, never a zero: %+v", row.TokensOut)
	}
	back, err := row.entry()
	if err != nil {
		t.Fatal(err)
	}
	if back.TokensOut != nil {
		t.Errorf("NULL came back as a number: %v", *back.TokensOut)
	}
	if back.TokensIn == nil || *back.TokensIn != 937 {
		t.Errorf("the measurement did not survive: %v", back.TokensIn)
	}
}

// A reported zero is a measurement and is written as one, so it comes back as a
// zero rather than an absence.
func TestLogRowKeepsAReportedZero(t *testing.T) {
	e := sampleEntry()
	zero := 0
	e.TokensOut = &zero
	row, err := logRowFor(e)
	if err != nil {
		t.Fatal(err)
	}
	if !row.TokensOut.Valid || row.TokensOut.Int64 != 0 {
		t.Fatalf("a reported zero must be written as a zero: %+v", row.TokensOut)
	}
	back, err := row.entry()
	if err != nil {
		t.Fatal(err)
	}
	if back.TokensOut == nil || *back.TokensOut != 0 {
		t.Errorf("a reported zero came back as an absence: %v", back.TokensOut)
	}
}

// The row is the whole decision: the table carries every column the summary
// reads, so a row written to Postgres and read back is the row that was
// written.
func TestLogRowRoundTripsTheWholeDecision(t *testing.T) {
	e := sampleEntry()
	e.SteppedUp = true
	e.Escalated = true
	e.Designated = true
	e.Reason = "the lane's owner is up"
	e.Refusal = "no-rung"
	e.RungSucceeded = "pro"
	e.Wait = WaitAwaitingTermination
	e.AwaitingTermination = true
	e.UsageFailed = true
	e.Evidence.Attempts = []Attempt{{Rung: "flash", Outcome: OutcomeFailed, Reason: "red"}}
	row, err := logRowFor(e)
	if err != nil {
		t.Fatal(err)
	}
	back, err := row.entry()
	if err != nil {
		t.Fatal(err)
	}
	if back.Time != e.Time {
		t.Errorf("time: %q != %q", back.Time, e.Time)
	}
	if back.Unit != e.Unit || back.Kind != e.Kind || back.RungTried != e.RungTried {
		t.Errorf("identity: %+v", back)
	}
	if back.Confidence != e.Confidence || back.Floor != e.Floor || back.Height != e.Height {
		t.Errorf("numbers: %+v", back)
	}
	if back.Wait != e.Wait || !back.AwaitingTermination || back.Refusal != e.Refusal {
		t.Errorf("the wait and the refusal: %+v", back)
	}
	if back.Outcome != e.Outcome || back.RungSucceeded != e.RungSucceeded {
		t.Errorf("the outcome: %+v", back)
	}
	if !back.SteppedUp || !back.Escalated || !back.Designated || !back.UsageFailed {
		t.Errorf("the flags: %+v", back)
	}
	if back.Reason != e.Reason || back.Source != e.Source || back.RowanPick != e.RowanPick {
		t.Errorf("the prose: %+v", back)
	}
	if len(back.Evidence.Attempts) != 1 || back.Evidence.Attempts[0].Rung != "flash" {
		t.Errorf("the evidence's attempts are the escalation count: %+v", back.Evidence)
	}
	if back.Evidence.Files != 2 || back.Evidence.Packages != 1 || back.Evidence.Lanes != 1 {
		t.Errorf("the size buckets: %+v", back.Evidence)
	}
	if back.Evidence.LaneOwner != "decide" {
		t.Errorf("the lane: %+v", back.Evidence)
	}
}

// A row with a time that is not a time is a refusal naming the row, never a
// silent zero stamp in the durable record.
func TestLogRowRefusesATimeThatIsNotOne(t *testing.T) {
	e := sampleEntry()
	e.Time = "yesterday"
	if _, err := logRowFor(e); err == nil {
		t.Fatal("a stamp that is not a time must refuse")
	}
}

// The summary is a projection of the rows, so it reads the same off either
// sink. The fake is the table's stand-in in the unit suite.
func TestSummaryIsTheSameOffEitherSink(t *testing.T) {
	reg, err := LoadRegistry("")
	if err != nil {
		t.Fatal(err)
	}
	entries := []Entry{sampleEntry(), sampleEntry()}
	entries[1].Unit = "u2"
	entries[1].Outcome = OutcomeFailed
	entries[1].RungSucceeded = ""

	path := filepath.Join(t.TempDir(), "decide.jsonl")
	file, err := OpenLogSink(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	table := NewFakeLogSink()
	for _, e := range entries {
		if err := file.Append(e); err != nil {
			t.Fatal(err)
		}
		if err := table.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	fileRows, err := file.Entries()
	if err != nil {
		t.Fatal(err)
	}
	tableRows, err := table.Entries()
	if err != nil {
		t.Fatal(err)
	}
	fileSum, err := Summarize(reg, fileRows)
	if err != nil {
		t.Fatal(err)
	}
	tableSum, err := Summarize(reg, tableRows)
	if err != nil {
		t.Fatal(err)
	}
	if fileSum.Render() != tableSum.Render() {
		t.Errorf("the same rows are the same summary:\nfile:  %s\ntable: %s", fileSum.Render(), tableSum.Render())
	}
	if !strings.Contains(fileSum.Render(), "LOG OK rows=2") {
		t.Errorf("two rows are two rows: %s", fileSum.Render())
	}
}

// The fake is the seam the unit tests run on, and it keeps the same promise the
// table does: the rows come back in the order they were appended.
func TestFakeLogSinkKeepsTheOrder(t *testing.T) {
	sink := NewFakeLogSink()
	for _, id := range []string{"a", "b", "c"} {
		e := sampleEntry()
		e.Unit = id
		if err := sink.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := sink.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].Unit != "a" || rows[2].Unit != "c" {
		t.Fatalf("the rows are the record, in order: %+v", rows)
	}
}

// The migration is a file in the repository, applied by a verb: it creates the
// table if it is not there and may be run twice.
func TestMigrationsAreIdempotentSQLInTheRepository(t *testing.T) {
	stmts, err := logMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(stmts) == 0 {
		t.Fatal("there is no migration to apply")
	}
	joined := strings.Join(stmts, "\n")
	if !strings.Contains(joined, "CREATE TABLE IF NOT EXISTS decide_log") {
		t.Errorf("the migration does not create decide_log: %s", joined)
	}
	for _, want := range []string{"tokens_in", "tokens_out", "rung_succeeded", "refusal", "confidence", "floor"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the table has no %s column: %s", want, joined)
		}
	}
	for _, stmt := range stmts {
		up := strings.ToUpper(stmt)
		switch {
		case strings.HasPrefix(up, "CREATE TABLE"), strings.HasPrefix(up, "CREATE INDEX"):
			if !strings.Contains(up, "IF NOT EXISTS") {
				t.Errorf("a migration that is not idempotent cannot be run twice: %s", stmt)
			}
		}
	}
}
