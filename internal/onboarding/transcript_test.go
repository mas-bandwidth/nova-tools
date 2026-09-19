package onboarding

import (
	"strings"
	"testing"
)

// These tests are the harness's own negative controls, kept where the harness
// is: each one is a way a transcript can be wrong that the comparison this
// repository used before -- a SET of the shapes a command printed, asked
// whether it contains each documented line -- says nothing about. If any of
// them stops failing, the tests in cmd/nova-*/firstrun_test.go have quietly
// become the weaker check again while still reading as the stronger one.

// transcriptLines is what FirstRun hands Steps: the inside of one fenced block,
// commands and output together, with the blank line the document leaves between
// commands kept.
var transcriptLines = []string{
	"$ nova-alpha put --name gate",
	"PUT OK name=gate id=0f1e2d3c created=2026-09-19T06:29:53Z",
	"",
	"$ nova-alpha list",
	"LIST ENTRY name=gate",
	"LIST OK n=1",
}

func TestStepsCutsCommandsFromTheirOutput(t *testing.T) {
	steps, err := Steps("nova-alpha", transcriptLines)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 {
		t.Fatalf("Steps cut %d steps, want 2: %#v", len(steps), steps)
	}
	if got, want := strings.Join(steps[0].Args, " "), "put --name gate"; got != want {
		t.Errorf("step 0 args = %q, want %q", got, want)
	}
	// The blank line between the two commands belongs to neither: it is the
	// document's spacing, and counting it would make every first block one line
	// longer than the tool prints.
	if len(steps[0].Want) != 1 {
		t.Errorf("step 0 wants %d lines, want 1 (the blank separator is not output): %q", len(steps[0].Want), steps[0].Want)
	}
	if len(steps[1].Want) != 2 {
		t.Errorf("step 1 wants %d lines, want 2: %q", len(steps[1].Want), steps[1].Want)
	}
}

func TestStepsReadsTheOneRedirectTheTranscriptsUse(t *testing.T) {
	steps, err := Steps("nova-alpha", []string{"$ nova-alpha count < testdata/events.jsonl", "COUNT OK n=2"})
	if err != nil {
		t.Fatal(err)
	}
	if steps[0].Stdin != "testdata/events.jsonl" {
		t.Errorf("Stdin = %q, want testdata/events.jsonl", steps[0].Stdin)
	}
	if got, want := strings.Join(steps[0].Args, " "), "count"; got != want {
		t.Errorf("args = %q, want %q; the redirect is not an argument", got, want)
	}
}

// A line this harness cannot run is said so rather than truncated and run
// anyway: a pipeline compared against the first command's output alone would be
// a green that means nothing.
func TestStepsRefusesWhatItCannotRun(t *testing.T) {
	for _, line := range []string{
		"$ nova-alpha list | head -2",
		"$ nova-alpha list > out.txt",
		"$ nova-alpha count <",
		"$ nova-alpha put --name \"gate",
		"$ nova-beta list",
	} {
		if _, err := Steps("nova-alpha", []string{line, "LIST OK n=1"}); err == nil {
			t.Errorf("Steps accepted %q; it cannot run that line", line)
		}
	}
	// Output before any command is the abridgement this harness exists to
	// catch, wearing its other face: a command line that was lost.
	if _, err := Steps("nova-alpha", []string{"LIST OK n=1", "$ nova-alpha list"}); err == nil {
		t.Error("Steps accepted output standing before any command")
	}
}

func TestSplitShellKeepsAQuotedSentenceWhole(t *testing.T) {
	got, err := SplitShell(`nova-alpha say --body "a \"brass\" fitting" --to Emma`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"nova-alpha", "say", "--body", `a "brass" fitting`, "--to", "Emma"}
	if len(got) != len(want) {
		t.Fatalf("SplitShell gave %d fields, want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("field %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// AN ABRIDGED TRANSCRIPT IS RED. This is the first of the two failures the set
// comparison cannot see: every line the document keeps still matches something
// the tool printed, and a set lookup for a line that was deleted is never made.
func TestAnAbridgedTranscriptIsRed(t *testing.T) {
	step := Step{Line: "$ nova-alpha list", Want: []string{"LIST ENTRY name=gate"}}
	res := Result{Stdout: "LIST ENTRY name=gate\nLIST OK n=1\n"}
	problems := Compare(step, res, nil)
	if len(problems) != 1 {
		t.Fatalf("Compare found %d problems, want 1: %v", len(problems), problems)
	}
	if !strings.Contains(problems[0].Message, "prints 2 line(s) and the document shows 1") {
		t.Errorf("the failure does not name the count:\n%s", problems[0].Message)
	}
}

// A REORDERED TRANSCRIPT IS RED. The second failure the set comparison cannot
// see: the same lines, the same count, a different order, and every lookup
// succeeds.
func TestAReorderedTranscriptIsRed(t *testing.T) {
	step := Step{Line: "$ nova-alpha list", Want: []string{"LIST OK n=1", "LIST ENTRY name=gate"}}
	res := Result{Stdout: "LIST ENTRY name=gate\nLIST OK n=1\n"}
	if len(Compare(step, res, nil)) != 2 {
		t.Error("Compare accepted the documented lines in the wrong order")
	}
}

// A WRONG VALUE IS RED. The third: the shape of the line is what the document
// promised and the number on it is not, which is the whole reason a reader
// checks their screen against a transcript at all.
func TestAWrongValueIsRed(t *testing.T) {
	step := Step{Line: "$ nova-alpha list", Want: []string{"LIST OK n=1"}}
	problems := Compare(step, Result{Stdout: "LIST OK n=2\n"}, nil)
	if len(problems) != 1 {
		t.Fatalf("Compare found %d problems, want 1: %v", len(problems), problems)
	}
	if !strings.Contains(problems[0].Message, "no normalisation") {
		t.Errorf("a comparison with no declared norm does not say so:\n%s", problems[0].Message)
	}
}

// A declared norm covers the value it names and NOTHING ELSE. The failure mode
// worth a test here is not a norm that fails to match -- that is red and read --
// but one that matches too much and turns a comparison into a formality.
func TestADeclaredNormCoversOnlyItsOwnField(t *testing.T) {
	step := Step{
		Line: "$ nova-alpha put --name gate",
		Want: []string{"PUT OK name=gate id=0f1e2d3c created=2026-09-19T06:29:53Z seen=2026-09-19T06:29:53Z"},
	}
	res := Result{Stdout: "PUT OK name=gate id=0f1e2d3c created=2026-09-19T11:02:41Z seen=2026-09-19T06:29:53Z\n"}
	if problems := Compare(step, res, []Norm{Instant("created")}); len(problems) != 0 {
		t.Errorf("a declared created= instant was not normalised: %v", problems)
	}
	// `seen=` is another instant on the same line and was not declared, so it
	// is compared as written.
	res.Stdout = "PUT OK name=gate id=0f1e2d3c created=2026-09-19T11:02:41Z seen=2026-09-19T11:02:41Z\n"
	problems := Compare(step, res, []Norm{Instant("created")})
	if len(problems) != 1 {
		t.Fatalf("an undeclared instant on the same line was normalised too: %v", problems)
	}
	if !strings.Contains(problems[0].Message, "created= (the instant of this run)") {
		t.Errorf("the failure does not list what was not compared:\n%s", problems[0].Message)
	}
	// An id the tool derives from its content reproduces, so HexID is declared
	// only where a run really invents one -- and then it covers that field only.
	if problems := Compare(step, res, []Norm{Instant("created"), Instant("seen"), HexID("id", 8)}); len(problems) != 0 {
		t.Errorf("declaring every run-owned value still disagreed: %v", problems)
	}
}

func TestPathNormReducesBothSidesToTheDocumentedSpelling(t *testing.T) {
	step := Step{Line: "$ nova-alpha where", Want: []string{"WHERE OK store=./cairns"}}
	res := Result{Stdout: "WHERE OK store=/var/folders/T/x9/cairns\n"}
	if problems := Compare(step, res, []Norm{Path("./cairns", "/var/folders/T/x9/cairns")}); len(problems) != 0 {
		t.Errorf("a declared path was not reduced to what the document writes: %v", problems)
	}
}

// Which stream a line is on is part of what a transcript promises, and a
// command that wrote to both leaves the order a terminal showed them unknown.
// The harness says so instead of picking one, because picking one is how a
// transcript comes to show an interleaving the tool does not keep.
func TestAStepThatWroteToBothStreamsIsReported(t *testing.T) {
	step := Step{Line: "$ nova-alpha list", Want: []string{"LIST OK n=1"}}
	problems := Compare(step, Result{Stdout: "LIST OK n=1\n", Stderr: "nova-alpha: a warning\n"}, nil)
	if len(problems) != 1 {
		t.Fatalf("Compare found %d problems, want 1: %v", len(problems), problems)
	}
	if !strings.Contains(problems[0].Message, "stdout AND stderr") {
		t.Errorf("the failure does not name the two streams:\n%s", problems[0].Message)
	}
}

// A refusal is on stderr and is compared there, so a transcript may document
// one without the harness being told which stream to read.
func TestARefusalIsComparedOnStderr(t *testing.T) {
	step := Step{Line: "$ nova-alpha", Want: []string{"nova-alpha: no verb given; run: nova-alpha help"}}
	res := Result{Code: 2, Stderr: "nova-alpha: no verb given; run: nova-alpha help\n"}
	if problems := Compare(step, res, nil); len(problems) != 0 {
		t.Errorf("a documented refusal on stderr disagreed: %v", problems)
	}
}

// Execute walks the steps in order, as one sitting, and stops at the first
// command it could not invoke: every line after it would be compared against a
// state that never happened, and a pile of consequent failures buries the one
// that is true.
func TestExecuteStopsAtACommandItCannotInvoke(t *testing.T) {
	steps, err := Steps("nova-alpha", transcriptLines)
	if err != nil {
		t.Fatal(err)
	}
	ran := 0
	problems := Execute(steps, func(s Step) (Result, error) {
		ran++
		return Result{}, errNotInvokable
	})
	if ran != 1 {
		t.Errorf("Execute invoked %d commands after the first could not run, want 1", ran)
	}
	if len(problems) != 1 || !strings.Contains(problems[0].Message, "could not be run") {
		t.Errorf("Execute did not report the command it could not invoke: %v", problems)
	}
}

type notInvokable struct{}

func (notInvokable) Error() string { return "no such file or directory" }

var errNotInvokable = notInvokable{}
