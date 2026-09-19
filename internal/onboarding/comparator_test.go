package onboarding

import (
	"strings"
	"testing"
)

// The comparator is the thing every transcript test is judged by, so it is seen
// red three ways before it is trusted: a line dropped, a value altered, a line
// moved. Each seed is ONE edit to a copy of the document -- the same three edits
// the `transcript-test` kind's control applies to a copy of a tool's real
// section -- and the run it is compared against does not change. A comparator
// that stayed green under any of them would let an abridged, a reordered or a
// silently-changed transcript pass, which is the whole failure this exists to
// close.

// documented is the transcript a reader is shown: two commands, and under each
// one every line the document says it prints.
var documented = []string{
	`$ nova-bus post --topic pit --text "the wall is up"`,
	"BUS POST id=3f2a1b at=2026-09-19T11:02:03Z topic=pit",
	"",
	"$ nova-bus read --topic pit",
	"BUS READ topic=pit n=1",
	"  3f2a1b the wall is up",
	"BUS OK n=1",
}

// theRun is what the tool printed when a reader typed those two lines: exactly
// what the document says, so that every red below is the seed and nothing else.
func theRun() []Result {
	return []Result{
		{Code: 0, Stdout: "BUS POST id=3f2a1b at=2026-09-19T11:02:03Z topic=pit\n"},
		{Code: 0, Stdout: "BUS READ topic=pit n=1\n  3f2a1b the wall is up\nBUS OK n=1\n"},
	}
}

// parse cuts a copy of the document into steps, failing the test rather than the
// comparator when the copy is not a transcript at all.
func parse(t *testing.T, lines []string) []Step {
	t.Helper()
	steps, err := Steps("nova-bus", lines)
	if err != nil {
		t.Fatalf("the seeded document is not a transcript: %v", err)
	}
	return steps
}

// copyWith returns a copy of the document with one edit applied, so the seeds
// cannot leak into each other.
func copyWith(edit func(lines []string) []string) []string {
	lines := make([]string, len(documented))
	copy(lines, documented)
	return edit(lines)
}

// The unseeded document and the run agree, line for line, in order. Without this
// every red below would prove nothing: a comparator that failed everything would
// pass all three seeds.
func TestCompareAcceptsTheDocumentTheToolPrints(t *testing.T) {
	problems := CompareTranscript(parse(t, documented), theRun(), nil)
	if len(problems) != 0 {
		t.Fatalf("the document the tool printed drew %d problem(s):\n%s", len(problems), joinProblems(problems))
	}
}

// SEED 1, one edit: a line the tool prints is dropped from the document. A
// comparator that asks only whether each documented line was printed stays green
// here, which is exactly how an abridged transcript survived.
func TestCompareRejectsADroppedLine(t *testing.T) {
	seeded := copyWith(func(lines []string) []string { return append(lines[:6:6], lines[6+1:]...) })
	problems := CompareTranscript(parse(t, seeded), theRun(), nil)
	if len(problems) == 0 {
		t.Fatal("a line dropped from the document drew no problem; an abridged transcript passes")
	}
	if !strings.Contains(problems[0].Message, "prints 3 line(s) and the document shows 2") {
		t.Errorf("the dropped line's problem does not count the lines:\n%s", problems[0].Message)
	}
}

// SEED 2, one edit: a value on a documented line is altered. Every value is
// compared as written unless it is named from the Volatile table, so this is red
// with no normalisation declared.
func TestCompareRejectsAnAlteredValue(t *testing.T) {
	seeded := copyWith(func(lines []string) []string {
		lines[4] = "BUS READ topic=pit n=2"
		return lines
	})
	problems := CompareTranscript(parse(t, seeded), theRun(), nil)
	if len(problems) != 1 {
		t.Fatalf("an altered value drew %d problem(s), want 1:\n%s", len(problems), joinProblems(problems))
	}
	if !strings.Contains(problems[0].Message, "n=2") || !strings.Contains(problems[0].Message, "n=1") {
		t.Errorf("the altered value's problem shows neither side of the difference:\n%s", problems[0].Message)
	}
}

// SEED 3, one edit: two lines of one command's output change places. The set of
// lines is unchanged and the count is unchanged, so this is the seed that a
// `printed map[string]bool` cannot see.
func TestCompareRejectsAMovedLine(t *testing.T) {
	seeded := copyWith(func(lines []string) []string {
		lines[5], lines[6] = lines[6], lines[5]
		return lines
	})
	problems := CompareTranscript(parse(t, seeded), theRun(), nil)
	if len(problems) != 2 {
		t.Fatalf("a moved line drew %d problem(s), want 2 (the two lines that changed places):\n%s", len(problems), joinProblems(problems))
	}
	for _, p := range problems {
		if !strings.Contains(p.Message, "the document's line") {
			t.Errorf("a moved line's problem does not name the line:\n%s", p.Message)
		}
	}
}

// A run-owned value is matched by shape only when the transcript names it from
// the shared table, and a name the table does not hold is REFUSED rather than
// quietly applied or quietly ignored. A test that could invent a normalisation
// could make any red green by widening one pattern.
func TestVolatileFieldOutsideTheTableIsRefused(t *testing.T) {
	problems := CompareTranscript(parse(t, documented), theRun(), []Field{{Name: "elapsed"}})
	if len(problems) != 1 {
		t.Fatalf("an invented volatile field drew %d problem(s), want 1:\n%s", len(problems), joinProblems(problems))
	}
	msg := problems[0].Message
	if !strings.Contains(msg, "elapsed") {
		t.Errorf("the refusal does not name the invented field:\n%s", msg)
	}
	if !strings.Contains(msg, "onboarding.Volatile") {
		t.Errorf("the refusal does not name the table:\n%s", msg)
	}
	for _, name := range VolatileNames() {
		if !strings.Contains(msg, name) {
			t.Errorf("the refusal does not show the table's %q entry, so a reader cannot see what they may name:\n%s", name, msg)
		}
	}
}

// The table holds the run-owned values SPEC-TOOLWORK §7 rule 2 names, and is the
// only place they are named. A field that leaves the table without a reading is
// a widening nobody read.
func TestTheVolatileTableHoldsTheNamedRunOwnedValues(t *testing.T) {
	want := []string{"at", "took", "created", "tmpdir", "sha"}
	got := VolatileNames()
	if len(got) != len(want) {
		t.Fatalf("onboarding.Volatile holds %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("onboarding.Volatile entry %d is %q, want %q", i, got[i], want[i])
		}
	}
	for _, f := range Volatile {
		if strings.TrimSpace(f.What) == "" {
			t.Errorf("the %q entry says nothing about what it is; What is what a reader of a failing test is told is not compared", f.Name)
		}
	}
}

// A field named from the table IS matched by shape, on both sides, so a
// transcript whose instant belongs to the run is green -- and the failure
// message still says what was not compared.
func TestAVolatileFieldFromTheTableIsMatchedByShape(t *testing.T) {
	run := theRun()
	run[0].Stdout = "BUS POST id=3f2a1b at=2026-09-19T14:55:01Z topic=pit\n"

	if problems := CompareTranscript(parse(t, documented), run, nil); len(problems) != 1 {
		t.Fatalf("an instant that belongs to the run drew %d problem(s) with nothing declared, want 1:\n%s", len(problems), joinProblems(problems))
	}
	if problems := CompareTranscript(parse(t, documented), run, []Field{{Name: "at"}}); len(problems) != 0 {
		t.Fatalf("`at` named from the table still drew %d problem(s):\n%s", len(problems), joinProblems(problems))
	}
	// Naming `at` normalises `at=` and NOTHING else: the id beside it on the
	// same line is still compared as written.
	run[0].Stdout = "BUS POST id=000000 at=2026-09-19T14:55:01Z topic=pit\n"
	if problems := CompareTranscript(parse(t, documented), run, []Field{{Name: "at"}}); len(problems) != 1 {
		t.Fatalf("a norm declared for `at` swallowed the id beside it: %d problem(s), want 1:\n%s", len(problems), joinProblems(problems))
	}
}

// The one table entry a pattern cannot match is a path this run made: the test
// supplies both spellings, and a missing one is refused rather than applied as a
// pattern that would match every path in the transcript.
func TestTheRunsTemporaryDirectoryIsNamedWithBothItsSpellings(t *testing.T) {
	doc := []string{
		"$ nova-bus read --root /tmp/nova-bus-1",
		"BUS READ root=/tmp/nova-bus-1 n=0",
	}
	run := []Result{{Code: 0, Stdout: "BUS READ root=/var/folders/q5/T/nova-bus-9f3 n=0\n"}}

	if problems := CompareTranscript(parse(t, doc), run, []Field{{Name: "tmpdir", Doc: "/tmp/nova-bus-1", Run: "/var/folders/q5/T/nova-bus-9f3"}}); len(problems) != 0 {
		t.Fatalf("the run's directory named with both spellings still drew %d problem(s):\n%s", len(problems), joinProblems(problems))
	}
	problems := CompareTranscript(parse(t, doc), run, []Field{{Name: "tmpdir"}})
	if len(problems) != 1 {
		t.Fatalf("`tmpdir` named with no path drew %d problem(s), want 1:\n%s", len(problems), joinProblems(problems))
	}
	if !strings.Contains(problems[0].Message, "tmpdir") {
		t.Errorf("the refusal does not name the field:\n%s", problems[0].Message)
	}
}

// A shape field carries no path, and handing it one is refused: it would mean
// the test believes the table entry is something other than what it is.
func TestAShapeFieldGivenAPathIsRefused(t *testing.T) {
	problems := CompareTranscript(parse(t, documented), theRun(), []Field{{Name: "at", Doc: "/tmp/x", Run: "/tmp/y"}})
	if len(problems) != 1 {
		t.Fatalf("a shape field handed a path drew %d problem(s), want 1:\n%s", len(problems), joinProblems(problems))
	}
}

// A comparison over no command passes by comparing nothing, so it is a problem
// and not a green.
func TestCompareRefusesATranscriptWithNoCommand(t *testing.T) {
	if problems := CompareTranscript(nil, nil, nil); len(problems) != 1 {
		t.Fatalf("an empty transcript drew %d problem(s), want 1", len(problems))
	}
}

// A run that produced fewer results than the document has commands is a sitting
// that stopped, and is reported as that rather than compared pairwise until the
// slice runs out.
func TestCompareRefusesARunThatIsShorterThanTheDocument(t *testing.T) {
	problems := CompareTranscript(parse(t, documented), theRun()[:1], nil)
	if len(problems) != 1 {
		t.Fatalf("a short run drew %d problem(s), want 1:\n%s", len(problems), joinProblems(problems))
	}
	if !strings.Contains(problems[0].Message, "2 command(s)") {
		t.Errorf("the short run's problem does not count the commands:\n%s", problems[0].Message)
	}
}

func joinProblems(problems []Problem) string {
	var b strings.Builder
	for _, p := range problems {
		b.WriteString(p.Message)
		b.WriteString("\n")
	}
	return b.String()
}
