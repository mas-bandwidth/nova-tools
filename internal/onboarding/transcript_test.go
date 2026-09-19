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

// --- Stella's HOLD on #1629 at 1b22de16: three ways a declared normalisation
// --- hid a difference it had promised to leave visible. Each test below fails
// --- on that head; the comment on each names the one edit that reverts its fix.

// ROW 1. A norm declared for one field must not match a DIFFERENT field whose
// name merely ends in the declared one. `HexID("id", 8)` matched inside
// `parent_id=`, and `Instant("created")` inside `last_created=`, so a changed
// value on a field nobody declared was erased and the comparison found nothing.
// Reverting fix: drop `field:` from the Norm that Instant and HexID build (one
// edit each) and apply falls back to the unanchored ReplaceAllString.
func TestADeclaredNormDoesNotMatchAFieldWhoseNameEndsInIt(t *testing.T) {
	for _, c := range []struct {
		what string
		want string
		got  string
		norm Norm
	}{
		{
			what: "an id field whose name ends in the declared one",
			want: "OK parent_id=aaaaaaaa",
			got:  "OK parent_id=bbbbbbbb",
			norm: HexID("id", 8),
		},
		{
			what: "an instant field whose name ends in the declared one",
			want: "OK last_created=2026-09-19T01:00:00Z",
			got:  "OK last_created=2026-09-19T11:02:41Z",
			norm: Instant("created"),
		},
	} {
		step := Step{Line: "$ nova-alpha put", Want: []string{c.want}}
		problems := Compare(step, Result{Stdout: c.got + "\n"}, []Norm{c.norm})
		if len(problems) != 1 {
			t.Errorf("%s: Compare found %d problems, want 1; the declared norm swallowed a field it does not name.\nwant: %s\ngot:  %s", c.what, len(problems), c.want, c.got)
		}
	}
	// And the declared field itself is still normalised, so the fix is a
	// boundary and not a norm that stopped working.
	step := Step{Line: "$ nova-alpha put", Want: []string{"OK parent_id=aaaaaaaa id=0f1e2d3c"}}
	if problems := Compare(step, Result{Stdout: "OK parent_id=aaaaaaaa id=9b8a7c6d\n"}, []Norm{HexID("id", 8)}); len(problems) != 0 {
		t.Errorf("the declared id= was not normalised: %v", problems)
	}
}

// ROW 1, the value's own boundary. `HexID(field, n)` promises EXACTLY n hex
// digits, so an id longer than that is a tool disagreeing with the document and
// not a value the norm declared. THIS ONE WAS ALREADY GREEN at 1b22de16 -- the
// unanchored pattern normalised the first n digits and the remainder still
// disagreed, so nothing was masked -- and it is written down because the fix
// for the rows above REPLACES that pattern: a boundary that covered too much
// must not become one that covers too little.
func TestAHexIDNormCoversExactlyTheDigitsItDeclares(t *testing.T) {
	step := Step{Line: "$ nova-alpha put", Want: []string{"OK id=aaaaaaaa"}}
	if problems := Compare(step, Result{Stdout: "OK id=aaaaaaaabbbb\n"}, []Norm{HexID("id", 8)}); len(problems) != 1 {
		t.Errorf("Compare found %d problems, want 1: an id longer than the %d digits declared was normalised anyway", len(problems), 8)
	}
	step = Step{Line: "$ nova-alpha put", Want: []string{"OK id=aaaaaaaabbbb"}}
	if problems := Compare(step, Result{Stdout: "OK id=aaaaaaaa\n"}, []Norm{HexID("id", 8)}); len(problems) != 1 {
		t.Errorf("Compare found %d problems, want 1: a document showing more digits than the norm declares was accepted", len(problems))
	}
}

// ROW 2. `Instant` says the value is an RFC3339 instant in UTC. A pattern that
// checks only the SHAPE of the digits accepts `2026-99-99T99:99:99Z`, so a tool
// printing an impossible date was normalised away instead of being shown. A
// value the constructor did not promise is left where the comparison sees it.
// Reverting fix: drop `valid: isInstant` from Instant (one edit).
func TestAnInstantNormLeavesAnImpossibleInstantVisible(t *testing.T) {
	for _, got := range []string{
		"OK created=2026-99-99T99:99:99Z",
		"OK created=2026-09-19T25:00:00Z",
		"OK created=2026-02-29T01:00:00Z", // 2026 is not a leap year
		"OK created=2026-13-01T01:00:00Z",
	} {
		step := Step{Line: "$ nova-alpha put", Want: []string{"OK created=2026-09-19T01:00:00Z"}}
		if problems := Compare(step, Result{Stdout: got + "\n"}, []Norm{Instant("created")}); len(problems) != 1 {
			t.Errorf("Compare found %d problems, want 1: %q is not an instant and was normalised as one", len(problems), got)
		}
	}
	// A real instant, with and without a fraction, is still the run's.
	step := Step{Line: "$ nova-alpha put", Want: []string{"OK created=2026-09-19T01:00:00Z"}}
	if problems := Compare(step, Result{Stdout: "OK created=2026-02-28T23:59:59.5Z\n"}, []Norm{Instant("created")}); len(problems) != 0 {
		t.Errorf("a real instant of this run was not normalised: %v", problems)
	}
}

// ROW 3. `SplitShell` handed the runner an argv that is not the command the
// reader typed: a single-quoted sentence split on its spaces into two fields
// carrying the quotes. A quoting form this harness does not read is refused by
// name; it is never guessed at.
// Reverting fix: delete the single-quote arm of SplitShell's bare state (one
// edit) and the sentence splits again.
func TestSplitShellKeepsASingleQuotedSentenceWhole(t *testing.T) {
	got, err := SplitShell(`nova-alpha say --body 'hello world' --to Emma`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"nova-alpha", "say", "--body", "hello world", "--to", "Emma"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("SplitShell gave %q, want %q", got, want)
	}
	// Inside single quotes nothing is special, as in the shell the reader types
	// into: the backslash and the double quote are the argument's own.
	got, err = SplitShell(`nova-alpha say --body 'a "brass" fitting\n'`)
	if err != nil {
		t.Fatal(err)
	}
	if last := got[len(got)-1]; last != `a "brass" fitting\n` {
		t.Errorf("the single-quoted argument = %q, want %q", last, `a "brass" fitting\n`)
	}
	// A shell keeps a backslash that stands before an ordinary character inside
	// double quotes; the old logic ate every one of them.
	got, err = SplitShell(`nova-alpha say --body "a\tab and a \"quote\""`)
	if err != nil {
		t.Fatal(err)
	}
	if last := got[len(got)-1]; last != `a\tab and a "quote"` {
		t.Errorf("the double-quoted argument = %q, want %q", last, `a\tab and a "quote"`)
	}
}

// ROW 3's refusals. Each of these is a line whose argv this harness cannot know,
// so it says so rather than handing the runner something else.
// Reverting fix: delete the two `return nil, fmt.Errorf` arms for `\` and "`"
// in SplitShell's bare state (one edit) and the altered argv comes back.
func TestSplitShellRefusesQuotingItCannotRead(t *testing.T) {
	for _, cmd := range []string{
		`nova-alpha say --body 'hello`,
		`nova-alpha say --body "hello`,
		`nova-alpha say --body hello\ world`,
		"nova-alpha say --body `hostname`",
	} {
		if got, err := SplitShell(cmd); err == nil {
			t.Errorf("SplitShell accepted %q and returned %q; it cannot know that argv", cmd, got)
		}
	}
	// What it DOES pass through untouched, and says so: no expansion happens
	// here. `$PWD` reaches the runner as the document writes it, which is the
	// contract the callers' Path norms are declared against.
	got, err := SplitShell(`nova-alpha add --remote "$PWD/rehearsal.git"`)
	if err != nil {
		t.Fatal(err)
	}
	if last := got[len(got)-1]; last != "$PWD/rehearsal.git" {
		t.Errorf("the documented remote = %q, want %q; SplitShell expands nothing", last, "$PWD/rehearsal.git")
	}
}

// The build triple belongs to the machine; the version word before it does not,
// and a tool that started answering something else about itself is the kind of
// drift a `version` line is in the transcript to catch.
func TestGoBuildCoversTheMachineAndNotTheVersionWord(t *testing.T) {
	step := Step{Line: "$ nova-alpha version", Want: []string{"nova-alpha devel linux/amd64 go1.26.5"}}
	if problems := Compare(step, Result{Stdout: "nova-alpha devel darwin/arm64 go1.27.1\n"}, []Norm{GoBuild()}); len(problems) != 0 {
		t.Errorf("a declared build triple was not normalised: %v", problems)
	}
	if problems := Compare(step, Result{Stdout: "nova-alpha v0.16.0 darwin/arm64 go1.27.1\n"}, []Norm{GoBuild()}); len(problems) != 1 {
		t.Errorf("GoBuild swallowed the version word too: %v", problems)
	}
}

// --- Fable's cold read of #1632 (medium): the repaired `version` line is not
// --- what the shipped verb prints, and GoBuild was an unanchored ReplaceAll.

// A `version` line has TWO declared parts and they have different owners: the
// build triple is the machine, the word before it is the build. GoBuild must
// not reach past its own two tokens, and must not match from the middle of a
// longer one -- it was a plain ReplaceAll over every line of every step.
func TestGoBuildCoversTwoWholeTokensAndNothingElse(t *testing.T) {
	step := Step{Line: "$ nova-alpha where", Want: []string{"WHERE OK dir=/srv/linux/amd64 go1.26.5-cache"}}
	res := Result{Stdout: "WHERE OK dir=/srv/darwin/arm64 go1.27.1-cache\n"}
	if problems := Compare(step, res, []Norm{GoBuild()}); len(problems) != 1 {
		t.Errorf("Compare found %d problems, want 1: GoBuild matched inside a path token", len(problems))
	}
	// Its own two tokens, standing alone, are still normalised.
	step = Step{Line: "$ nova-alpha version", Want: []string{"nova-alpha devel linux/amd64 go1.26.5"}}
	if problems := Compare(step, Result{Stdout: "nova-alpha devel darwin/arm64 go1.27.1\n"}, []Norm{GoBuild()}); len(problems) != 0 {
		t.Errorf("a declared build triple was not normalised: %v", problems)
	}
}

// Version covers the word a build stamps itself with, so that the document can
// show what a READER sees -- `go build ./cmd/nova-review && ./nova-review
// version` prints `v0.16.0-dev.<base>.0.<date>-<sha>` -- while the test, whose
// binary is not stamped, prints `devel` and still agrees.

// Version covers the word a build stamps itself with, so that the document can
// show what a READER sees -- `go build ./cmd/nova-review && ./nova-review
// version` prints `v0.16.0-dev.<base>.0.<date>-<sha>` -- while the test, whose
// binary is not stamped, prints `devel` and still agrees. RED FIRST at
// `705dd1c9` in the only way it can be: `Version` did not exist there, so the
// package did not build.
func TestVersionNormCoversTheStampAndDevelAndNothingElse(t *testing.T) {
	step := Step{Line: "$ nova-alpha version", Want: []string{"nova-alpha v0.16.0-dev.c839379e.0.20260919144920-705dd1c92534 darwin/arm64 go1.27.1"}}
	res := Result{Stdout: "nova-alpha devel darwin/arm64 go1.27.1\n"}
	if problems := Compare(step, res, []Norm{Version(), GoBuild()}); len(problems) != 0 {
		t.Errorf("the document's stamp and the test binary's `devel` disagreed: %v", problems)
	}
	// The tool's NAME is not the version word, and a tool that answered
	// something that is neither a stamp nor `devel` is still a finding.
	res.Stdout = "nova-alpha unknown darwin/arm64 go1.27.1\n"
	if problems := Compare(step, res, []Norm{Version(), GoBuild()}); len(problems) != 1 {
		t.Errorf("Compare found %d problems, want 1: a version word that is neither a stamp nor `devel` was normalised", len(problems))
	}
	// And it does not reach inside a longer token.
	step = Step{Line: "$ nova-alpha list", Want: []string{"LIST OK tag=v1.2.3-rc1 name=alpha"}}
	if problems := Compare(step, Result{Stdout: "LIST OK tag=v9.9.9-rc1 name=alpha\n"}, []Norm{Version()}); len(problems) != 1 {
		t.Errorf("Compare found %d problems, want 1: Version matched inside `tag=`", len(problems))
	}
}
