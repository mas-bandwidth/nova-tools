package tokens

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The package's own tests: the day file's round trip and its strict parse, the shrink
// comparison with a dash on either side, the attribution ladder, and the lock.

func TestTheDayFileRoundTripsByteIdentically(t *testing.T) {
	var c Counts
	c.Set(Input, 100)
	c.Set(CacheRead, 0)
	f := &DayFile{
		Day: "2026-09-11", At: "2026-09-11T23:55:02Z", Build: "abc", Turns: "42",
		Sources: []string{"bus:emma", "claude:glenn"},
		Rows: []DayRow{
			{Date: "2026-09-11", Model: "a", Repo: "schema", Counts: c, Rough: 2, Basis: UTC, Sources: []string{"claude:glenn"}},
		},
	}
	text := f.Render()
	got, findings := ParseDayFile("2026-09-11", text)
	if len(findings) != 0 {
		t.Fatalf("a file this package wrote does not parse: %v", findings)
	}
	if again := (&DayFile{Day: got.Day, At: got.At, Build: got.Build, Turns: got.Turns, Sources: got.Sources, Rows: got.Rows}).Render(); again != text {
		t.Errorf("the round trip is not byte-identical:\n%q\n%q", text, again)
	}
	// A zero is written as a zero and an absence as a dash, and the two are different.
	if !strings.Contains(text, "\t100\t-\t-\t0\t-\t") {
		t.Errorf("the cells are not `100 - - 0 -`:\n%s", text)
	}
}

func TestAnUnversionedFileRefuses(t *testing.T) {
	_, findings := ParseDayFile("2026-09-11", "date\tmodel\trepo\n")
	if len(findings) != 1 || !strings.Contains(findings[0].Reason, Version) {
		t.Errorf("an unversioned file gives %v; it wants one finding naming the version line", findings)
	}
}

func TestTheShrinkComparisonWithADashOnEitherSide(t *testing.T) {
	var was, now Counts
	was.Set(Input, 100)
	was.Set(Reasoning, 40)
	now.Set(Input, 60)
	// reasoning: a number in the file and a dash now -- the source went quiet.
	got := Shrinks(was, now, "2026-09-11")
	if len(got) != 2 {
		t.Fatalf("%d shrinks, want two (input lower, reasoning gone): %v", len(got), got)
	}
	if got[0].Now != "60" || got[1].Now != Dash {
		t.Errorf("the two shrinks are %v", got)
	}
	// The other direction is coverage arriving, not a shrink.
	var thin, fat Counts
	thin.Set(Input, 10)
	fat.Set(Input, 10)
	fat.Set(Reasoning, 40)
	if s := Shrinks(thin, fat, "2026-09-11"); len(s) != 0 {
		t.Errorf("a dash becoming a number is a shrink: %v", s)
	}
}

func TestTheAttributionLadder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repos.tsv")
	os.WriteFile(path, []byte("# a comment\n\nschema\t(^|/)schema($|/)\n"), 0o644)
	rules, err := LoadRules(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, prev, want string
		inputs           []string
	}{
		{name: "the first rule that matches names the repo", inputs: []string{"/w/schema/a.go"}, want: "schema"},
		{name: "a token that matches nothing is seen", inputs: []string{"/w/elsewhere/a.go"}, want: Other},
		{name: "no token at all inherits the stream's previous repo", inputs: []string{"nothing path-like here"}, prev: "schema", want: "schema"},
		{name: "no token and nothing before it is unknown", inputs: []string{""}, want: Unknown},
		{name: "a remote is a path-like token too", inputs: []string{"github.com:mas-bandwidth/schema"}, want: "schema"},
	} {
		if got := rules.AttributeInputs(tc.inputs, tc.prev); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestAMalformedRulesLineIsNamed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repos.tsv")
	os.WriteFile(path, []byte("schema\t(^|/)schema($|/)\nthis line has no tab\n"), 0o644)
	_, err := LoadRules(path)
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("a malformed rules line gives %v; it wants the line number", err)
	}
}

func TestMissingDaysAreNamedAndNeverFilled(t *testing.T) {
	got := MissingDays([]string{"2026-09-07", "2026-09-08", "2026-09-10"})
	if len(got) != 1 || got[0] != "2026-09-09" {
		t.Errorf("missing days are %v, want [2026-09-09]", got)
	}
	if n := MissingDays([]string{"2026-02-27", "2026-03-02"}); len(n) != 2 || n[0] != "2026-02-28" || n[1] != "2026-03-01" {
		t.Errorf("across a month end the missing days are %v", n)
	}
	if n := MissingDays([]string{"2026-09-11"}); n != nil {
		t.Errorf("one day has no gaps, got %v", n)
	}
}

func TestTheFoldLockIsExclusiveAndNamesItsHolder(t *testing.T) {
	dir := t.TempDir()
	release, err := TakeFoldLock(dir, LockWait)
	if err != nil {
		t.Fatal(err)
	}
	if pid := HolderPID(filepath.Join(dir, LockName)); pid == Dash {
		t.Error("the lock file holds no pid, so a waiter could not name the holder")
	}
	_, err = TakeFoldLock(dir, 50*time.Millisecond)
	if err == nil {
		t.Fatal("a second fold took the lock")
	}
	if !strings.Contains(err.Error(), LockName) {
		t.Errorf("the refusal does not name the lock: %v", err)
	}
	release()
	release() // safe more than once
	again, err := TakeFoldLock(dir, LockWait)
	if err != nil {
		t.Fatalf("the lock was not released: %v", err)
	}
	again()
}

func TestTheBusGrammarIsOneGrammar(t *testing.T) {
	// The parser is the serializer's inverse, which is the only way the two stay one
	// grammar: report writes this and fold --bus reads it.
	line := BodyLine("2026-09-11", "emma", "gemini", "schema", Input, 1234, UTC)
	if f := strings.Split(line, "\t"); len(f) != 6 {
		t.Errorf("a UTC line has %d fields, want six: %q", len(f), line)
	}
	zoned := BodyLine("2026-09-11", "emma", "gemini", "unattributed", Output, 7, "America/Los_Angeles")
	if !strings.HasSuffix(zoned, "\tday_basis=America/Los_Angeles") {
		t.Errorf("a zoned line does not carry its basis: %q", zoned)
	}
	subject := Subject("2026-09-11", "2026-09-11T23:55:02Z", "b", []string{"emma-000000000001", "emma-000000000002"})
	p, ok := ParseSubject(subject)
	if !ok || p.day != "2026-09-11" || len(p.supersedes) != 2 || p.badSet != "" {
		t.Errorf("the subject %q does not parse back: %+v ok=%v", subject, p, ok)
	}
	// `at=` is an RFC 3339 UTC stamp: `at=garbage build=b` was taken for a tokens note,
	// and the fold validates every note's Date: against that stamp.
	for _, bad := range []string{"Tokens 2026-09-11", "tokens 2026-09-11 (rough)", "tokens 2026-09-11 at=x",
		"tokens 11-09-2026", "tokens 2026-09-11 at=garbage build=b", "tokens 2026-09-11 at=2026-09-11T23:55:02-07:00 build=b"} {
		if _, ok := ParseSubject(bad); ok {
			t.Errorf("%q was taken for a tokens note's subject", bad)
		}
	}
	if p, _ := ParseSubject("tokens 2026-09-11 at=2026-09-11T23:55:02Z build=b supersedes=emma-000000000002,emma-000000000001"); p.badSet == "" {
		t.Error("an unsorted predecessor set was accepted")
	}
}

func TestASourceLineFieldIsADashWhereItIsNotAMeasurement(t *testing.T) {
	s := &Source{Kind: KindClaude}
	if s.StatField("nousage") != Dash {
		t.Error("a transcript has no job directories, so nousage is a dash and not a zero")
	}
	if s.StatField("dup") != "0" {
		t.Error("a transcript CAN have duplicate ids, so zero of them is a measurement")
	}
	// A transcript line whose timestamp is not a stamp this tool can read IS an unparsed
	// line (rule 17, and rule 3: counted and printed, never skipped silently), so zero of
	// them is a measurement. The grammar's aside "a transcript has no unparsed lines" is
	// the sentence this contradicts, and the PR body carries it as a spec question.
	if s.StatField("unparsed") != "0" {
		t.Error("a transcript CAN have a line whose stamp does not parse, so zero of them is a measurement")
	}
	b := &Source{Kind: KindBus}
	if b.StatField("dup") != Dash || b.StatField("comments") != "0" {
		t.Error("a bus lane has no duplicate ids and does have comments")
	}
}
