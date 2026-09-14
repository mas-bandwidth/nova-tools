package tokens

import (
	"fmt"
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

// R5 (issue #268): the merge itself -- retained, replaced, blended, collision. A retained
// row comes back byte for byte, because the fold that wrote it is the only run that could
// compute it and this one must not touch it.
func TestMergeDayRetainsReplacesAndRefuses(t *testing.T) {
	row := func(model, repo string, in int64, sources ...string) DayRow {
		var c Counts
		c.Set(Input, in)
		return DayRow{Date: "2026-09-14", Model: model, Repo: repo, Counts: c, Basis: UTC, Sources: sources}
	}

	// Retained and replaced, in one merge.
	old := []DayRow{row("claude-x", "serialize.rs", 410, "swarm:glenn"), row("mercury-2.5", "serialize.rs", 1, "swarm:freddy")}
	fresh := []DayRow{row("mercury-2.5", "serialize.rs", 2000, "swarm:freddy")}
	merged, retained, partials := MergeDay(old, fresh, []string{"swarm:freddy"})
	if len(partials) != 0 {
		t.Fatalf("partials on a clean merge: %v", partials)
	}
	if retained != 1 {
		t.Errorf("retained=%d, want 1", retained)
	}
	if len(merged) != 2 || merged[0].Model != "claude-x" || merged[1].Model != "mercury-2.5" {
		t.Fatalf("merged rows are not the two, sorted by (model, repo): %v", merged)
	}
	if got, _ := merged[0].Counts.Get(Input); got != 410 {
		t.Errorf("the retained row's input is %d, want 410 -- byte for byte is the promise", got)
	}
	if got, _ := merged[1].Counts.Get(Input); got != 2000 {
		t.Errorf("the replaced row's input is %d, want 2000 (replaced, never summed with the file's 1)", got)
	}

	// A blended row: one row's sources name a declared label and an undeclared one.
	old = []DayRow{row("claude-x", "serialize.rs", 410, "swarm:freddy", "swarm:glenn")}
	fresh = []DayRow{row("mercury-2.5", "serialize.rs", 2000, "swarm:freddy")}
	_, _, partials = MergeDay(old, fresh, []string{"swarm:freddy"})
	if len(partials) != 1 || partials[0].Model != "claude-x" {
		t.Fatalf("a blended row was not refused: %v", partials)
	}
	if partials[0].Why != PartialBlended {
		t.Errorf("why=%q, want %q", partials[0].Why, PartialBlended)
	}

	// A collision: a retained row and a recomputed row with the same (model, repo).
	old = []DayRow{row("claude-x", "serialize.rs", 410, "swarm:glenn")}
	fresh = []DayRow{row("claude-x", "serialize.rs", 2000, "swarm:freddy")}
	_, _, partials = MergeDay(old, fresh, []string{"swarm:freddy"})
	if len(partials) != 1 || partials[0].Why != PartialCollision {
		t.Fatalf("a collision was not refused: %v", partials)
	}

	// Full replacement: nothing retained, and the merge is exactly this run's rows.
	old = []DayRow{row("claude-x", "serialize.rs", 410, "swarm:glenn")}
	fresh = []DayRow{row("claude-x", "serialize.rs", 900, "swarm:glenn")}
	merged, retained, partials = MergeDay(old, fresh, []string{"swarm:glenn"})
	if len(partials) != 0 || retained != 0 || len(merged) != 1 {
		t.Fatalf("full replacement is not today's behaviour: %d rows, retained=%d, %v", len(merged), retained, partials)
	}
	if got, _ := merged[0].Counts.Get(Input); got != 900 {
		t.Errorf("input %d, want 900 (replaced, not summed)", got)
	}

	// Byte for byte: the retained row renders exactly the line it was parsed from.
	before := (&DayFile{Day: "2026-09-14", At: "s", Build: "b", Turns: Dash,
		Sources: []string{"swarm:glenn"}, Rows: []DayRow{row("claude-x", "serialize.rs", 410, "swarm:glenn")}}).Render()
	parsed, findings := ParseDayFile("2026-09-14", before)
	if len(findings) != 0 {
		t.Fatalf("the fixture file does not parse: %v", findings)
	}
	merged, _, _ = MergeDay(parsed.Rows, []DayRow{row("mercury-2.5", "serialize.rs", 2000, "swarm:freddy")}, []string{"swarm:freddy"})
	after := (&DayFile{Day: "2026-09-14", At: "s", Build: "b", Turns: Dash,
		Sources: []string{"swarm:glenn"}, Rows: []DayRow{merged[0]}}).Render()
	if after != before {
		t.Errorf("a retained row did not come back byte-identical:\nwas:  %q\nnow:  %q", before, after)
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
	// Rule 6 names ONE trailer in ONE order, `at=<stamp> build=<id>[ supersedes=<set>]`,
	// and says any other text after the date is not a tokens note. The keys used to be
	// accepted in any order and any position, so three arrangements nobody wrote were
	// tokens notes. A day is a date on the calendar too: 2026-02-30 is not one.
	for _, bad := range []string{"Tokens 2026-09-11", "tokens 2026-09-11 (rough)", "tokens 2026-09-11 at=x",
		"tokens 11-09-2026", "tokens 2026-09-11 at=garbage build=b", "tokens 2026-09-11 at=2026-09-11T23:55:02-07:00 build=b",
		"tokens 2026-09-11 build=b at=2026-09-11T23:55:02Z",
		"tokens 2026-09-11 supersedes=emma-000000000001 at=2026-09-11T23:55:02Z build=b",
		"tokens 2026-09-11 at=2026-09-11T23:55:02Z supersedes=emma-000000000001 build=b",
		"tokens 2026-09-11 at=2026-09-11T23:55:02Z build=b supersedes=emma-000000000001 at=2026-09-11T23:55:02Z",
		"tokens 2026-02-30", "tokens 2026-13-40"} {
		if _, ok := ParseSubject(bad); ok {
			t.Errorf("%q was taken for a tokens note's subject", bad)
		}
	}
	if p, _ := ParseSubject("tokens 2026-09-11 at=2026-09-11T23:55:02Z build=b supersedes=emma-000000000002,emma-000000000001"); p.badSet == "" {
		t.Error("an unsorted predecessor set was accepted")
	}
}

// TestValidDayIsACalendarCheck: a day is a date, not a ten-character shape. `--day
// 2026-13-40` was accepted, wrote a day file, passed check, and left MissingDays walking
// from a day that does not exist.
func TestValidDayIsACalendarCheck(t *testing.T) {
	for _, good := range []string{"2026-09-11", "2024-02-29", "2026-01-01", "2026-12-31"} {
		if !ValidDay(good) {
			t.Errorf("%s is a day", good)
		}
	}
	for _, bad := range []string{"2026-13-40", "2026-02-30", "2026-00-10", "2026-09-31", "2026-09-00", "2026-9-11", "not-a-day!"} {
		if ValidDay(bad) {
			t.Errorf("%s is not a day", bad)
		}
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
	// SPEC-TOKENS' TOKENS SOURCE paragraph says "a transcript has no unparsed lines",
	// so the column is a dash there and the code follows the spec rather than arguing
	// with it in the output. The lines themselves are not lost: a transcript line whose
	// stamp does not parse is one TOKENS UNPARSED line naming the label and is in the
	// unparsed= total on TOKENS FAIL (rule 3). Striking the spec's clause is a spec
	// decision, and the PR body carries it as one; this assertion moves with the spec.
	if s.StatField("unparsed") != Dash {
		t.Error("the spec says a transcript has no unparsed lines, so the column is a dash")
	}
	b := &Source{Kind: KindBus}
	if b.StatField("dup") != Dash || b.StatField("comments") != "0" {
		t.Error("a bus lane has no duplicate ids and does have comments")
	}
}

// L10a: readSource is the one whole-file read, and it had no ceiling, so one oversized
// ledger or bus file took the process's memory. A file over the cap must be refused by
// name rather than read.
func TestReadSourceRefusesAnOversizedFile(t *testing.T) {
	const capInTest = 64 << 20
	path := filepath.Join(t.TempDir(), "huge.csv")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(capInTest + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = readSource(path)
	if err == nil {
		t.Fatalf("readSource read %d bytes with no cap", capInTest+1)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("the refusal must name the path: %v", err)
	}
	if !strings.Contains(err.Error(), fmt.Sprint(capInTest)) {
		t.Errorf("the refusal must name the cap %d: %v", capInTest, err)
	}
}

// L10b: onCycle marked a node seen, walked its predecessors, then deleted it on the way
// out, so a diamond was re-walked once per path. A node already proven acyclic must stay
// memoized, which the walk's own seen map must show; the answer is unchanged.
func TestOnCycleDoesNotRewalkAProvenAcyclicDiamond(t *testing.T) {
	bottom := &note{id: "d"}
	left := &note{id: "b", subject: parsedSubject{supersedes: []string{"d"}}}
	right := &note{id: "c", subject: parsedSubject{supersedes: []string{"d"}}}
	top := &note{id: "a", subject: parsedSubject{supersedes: []string{"b", "c"}}}
	all := map[string]*note{"a": top, "b": left, "c": right, "d": bottom}
	seen := map[string]bool{}
	if onCycle(top, all, seen) {
		t.Fatal("onCycle reported a cycle in an acyclic diamond")
	}
	if len(seen) != len(all) {
		t.Errorf("onCycle left %d of %d nodes proven: it re-walked an acyclic node", len(seen), len(all))
	}
}

// L10b: the memo must not hide a cycle. A diamond with a back edge still reports true.
func TestOnCycleStillSeesARealCycle(t *testing.T) {
	bottom := &note{id: "d"}
	left := &note{id: "b", subject: parsedSubject{supersedes: []string{"d"}}}
	right := &note{id: "c", subject: parsedSubject{supersedes: []string{"d", "a"}}}
	top := &note{id: "a", subject: parsedSubject{supersedes: []string{"b", "c"}}}
	all := map[string]*note{"a": top, "b": left, "c": right, "d": bottom}
	if !onCycle(top, all, map[string]bool{}) {
		t.Fatal("onCycle missed a cycle through a supersedes back edge")
	}
}
