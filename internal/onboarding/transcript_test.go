package onboarding

import (
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	t.Parallel()

	steps, err := Steps("nova-alpha", transcriptLines)
	require.NoError(t, err)
	require.Len(t, steps, 2, "Steps cut %d steps, want 2: %#v", len(steps), steps)
	got := strings.Join(steps[0].Args, " ")
	assert.Equal(t, "put --name gate", got, "step 0 args = %q, want %q", got, "put --name gate")
	// The blank line between the two commands belongs to neither: it is the
	// document's spacing, and counting it would make every first block one line
	// longer than the tool prints.
	assert.Len(t, steps[0].Want, 1, "step 0 wants %d lines, want 1 (the blank separator is not output): %q", len(steps[0].Want), steps[0].Want)
	assert.Len(t, steps[1].Want, 2, "step 1 wants %d lines, want 2: %q", len(steps[1].Want), steps[1].Want)
}

func TestStepsReadsTheOneRedirectTheTranscriptsUse(t *testing.T) {
	t.Parallel()

	steps, err := Steps("nova-alpha", []string{"$ nova-alpha count < testdata/events.jsonl", "COUNT OK n=2"})
	require.NoError(t, err)
	assert.Equal(t, "testdata/events.jsonl", steps[0].Stdin, "Stdin = %q, want testdata/events.jsonl", steps[0].Stdin)
	got := strings.Join(steps[0].Args, " ")
	assert.Equal(t, "count", got, "args = %q, want %q; the redirect is not an argument", got, "count")
}

// A line this harness cannot run is said so rather than truncated and run
// anyway: a pipeline compared against the first command's output alone would be
// a green that means nothing.
func TestStepsRefusesWhatItCannotRun(t *testing.T) {
	t.Parallel()

	for _, line := range []string{
		"$ nova-alpha list | head -2",
		"$ nova-alpha list > out.txt",
		"$ nova-alpha count <",
		"$ nova-alpha put --name \"gate",
		"$ nova-beta list",
	} {
		_, err := Steps("nova-alpha", []string{line, "LIST OK n=1"})
		assert.Error(t, err, "Steps accepted %q; it cannot run that line", line)
	}
	// Output before any command is the abridgement this harness exists to
	// catch, wearing its other face: a command line that was lost.
	_, err := Steps("nova-alpha", []string{"LIST OK n=1", "$ nova-alpha list"})
	assert.Error(t, err, "Steps accepted output standing before any command")
}

func TestSplitShellKeepsAQuotedSentenceWhole(t *testing.T) {
	t.Parallel()

	got, err := SplitShell(`nova-alpha say --body "a \"brass\" fitting" --to Emma`)
	require.NoError(t, err)
	want := []string{"nova-alpha", "say", "--body", `a "brass" fitting`, "--to", "Emma"}
	require.Equal(t, len(want), len(got), "SplitShell gave %d fields, want %d: %q", len(got), len(want), got)
	for i := range want {
		assert.Equal(t, want[i], got[i], "field %d = %q, want %q", i, got[i], want[i])
	}
}

// AN ABRIDGED TRANSCRIPT IS RED. This is the first of the two failures the set
// comparison cannot see: every line the document keeps still matches something
// the tool printed, and a set lookup for a line that was deleted is never made.
func TestAnAbridgedTranscriptIsRed(t *testing.T) {
	t.Parallel()

	step := Step{Line: "$ nova-alpha list", Want: []string{"LIST ENTRY name=gate"}}
	res := Result{Stdout: "LIST ENTRY name=gate\nLIST OK n=1\n"}
	problems := Compare(step, res, nil)
	require.Len(t, problems, 1, "Compare found %d problems, want 1: %v", len(problems), problems)
	assert.Contains(t, problems[0].Message, "prints 2 line(s) and the document shows 1", "the failure does not name the count:\n%s", problems[0].Message)
}

// A REORDERED TRANSCRIPT IS RED. The second failure the set comparison cannot
// see: the same lines, the same count, a different order, and every lookup
// succeeds.
func TestAReorderedTranscriptIsRed(t *testing.T) {
	t.Parallel()

	step := Step{Line: "$ nova-alpha list", Want: []string{"LIST OK n=1", "LIST ENTRY name=gate"}}
	res := Result{Stdout: "LIST ENTRY name=gate\nLIST OK n=1\n"}
	assert.Len(t, Compare(step, res, nil), 2, "Compare accepted the documented lines in the wrong order")
}

// A WRONG VALUE IS RED. The third: the shape of the line is what the document
// promised and the number on it is not, which is the whole reason a reader
// checks their screen against a transcript at all.
func TestAWrongValueIsRed(t *testing.T) {
	t.Parallel()

	step := Step{Line: "$ nova-alpha list", Want: []string{"LIST OK n=1"}}
	problems := Compare(step, Result{Stdout: "LIST OK n=2\n"}, nil)
	require.Len(t, problems, 1, "Compare found %d problems, want 1: %v", len(problems), problems)
	assert.Contains(t, problems[0].Message, "no normalisation", "a comparison with no declared norm does not say so:\n%s", problems[0].Message)
}

// A declared norm covers the value it names and NOTHING ELSE. The failure mode
// worth a test here is not a norm that fails to match -- that is red and read --
// but one that matches too much and turns a comparison into a formality.
func TestADeclaredNormCoversOnlyItsOwnField(t *testing.T) {
	t.Parallel()

	step := Step{
		Line: "$ nova-alpha put --name gate",
		Want: []string{"PUT OK name=gate id=0f1e2d3c created=2026-09-19T06:29:53Z seen=2026-09-19T06:29:53Z"},
	}
	res := Result{Stdout: "PUT OK name=gate id=0f1e2d3c created=2026-09-19T11:02:41Z seen=2026-09-19T06:29:53Z\n"}
	problems := Compare(step, res, []Norm{Instant("created")})
	assert.Empty(t, problems, "a declared created= instant was not normalised: %v", problems)
	// `seen=` is another instant on the same line and was not declared, so it
	// is compared as written.
	res.Stdout = "PUT OK name=gate id=0f1e2d3c created=2026-09-19T11:02:41Z seen=2026-09-19T11:02:41Z\n"
	problems = Compare(step, res, []Norm{Instant("created")})
	require.Len(t, problems, 1, "an undeclared instant on the same line was normalised too: %v", problems)
	assert.Contains(t, problems[0].Message, "created= (the instant of this run)", "the failure does not list what was not compared:\n%s", problems[0].Message)
	problems = Compare(step, res, []Norm{Instant("created"), Instant("seen")})
	assert.Empty(t, problems, "declaring every run-owned value still disagreed: %v", problems)
}

func TestPathNormReducesBothSidesToTheDocumentedSpelling(t *testing.T) {
	t.Parallel()

	step := Step{Line: "$ nova-alpha where", Want: []string{"WHERE OK store=./cairns"}}
	res := Result{Stdout: "WHERE OK store=/var/folders/T/x9/cairns\n"}
	problems := Compare(step, res, []Norm{Path("./cairns", "/var/folders/T/x9/cairns")})
	assert.Empty(t, problems, "a declared path was not reduced to what the document writes: %v", problems)
}

func TestPathNormRecognizesACommaAfterThePath(t *testing.T) {
	t.Parallel()

	step := Step{Line: "$ nova-alpha where", Want: []string{"WHERE NOTE store=./cairns, using the documented location"}}
	res := Result{Stdout: "WHERE NOTE store=/var/folders/T/x9/cairns, using the documented location\n"}
	problems := Compare(step, res, []Norm{Path("./cairns", "/var/folders/T/x9/cairns")})
	assert.Empty(t, problems, "a comma immediately after the declared path prevented normalization: %v", problems)
}

// Which stream a line is on is part of what a transcript promises, and a
// command that wrote to both leaves the order a terminal showed them unknown.
// The harness says so instead of picking one, because picking one is how a
// transcript comes to show an interleaving the tool does not keep.
func TestAStepThatWroteToBothStreamsIsReported(t *testing.T) {
	t.Parallel()

	step := Step{Line: "$ nova-alpha list", Want: []string{"LIST OK n=1"}}
	problems := Compare(step, Result{Stdout: "LIST OK n=1\n", Stderr: "nova-alpha: a warning\n"}, nil)
	require.Len(t, problems, 1, "Compare found %d problems, want 1: %v", len(problems), problems)
	assert.Contains(t, problems[0].Message, "stdout AND stderr", "the failure does not name the two streams:\n%s", problems[0].Message)
}

// A refusal is on stderr and is compared there, so a transcript may document
// one without the harness being told which stream to read.
func TestARefusalIsComparedOnStderr(t *testing.T) {
	t.Parallel()

	step := Step{Line: "$ nova-alpha", Want: []string{"nova-alpha: no verb given; run: nova-alpha help"}}
	res := Result{Code: 2, Stderr: "nova-alpha: no verb given; run: nova-alpha help\n"}
	problems := Compare(step, res, nil)
	assert.Empty(t, problems, "a documented refusal on stderr disagreed: %v", problems)
}

// Execute walks the steps in order, as one sitting, and stops at the first
// command it could not invoke: every line after it would be compared against a
// state that never happened, and a pile of consequent failures buries the one
// that is true.
func TestExecuteStopsAtACommandItCannotInvoke(t *testing.T) {
	t.Parallel()

	steps, err := Steps("nova-alpha", transcriptLines)
	require.NoError(t, err)
	ran := 0
	problems := Execute(steps, func(s Step) (Result, error) {
		ran++
		return Result{}, errNotInvokable
	})
	assert.Equal(t, 1, ran, "Execute invoked %d commands after the first could not run, want 1", ran)
	assert.True(t, len(problems) == 1 && strings.Contains(problems[0].Message, "could not be run"), "Execute did not report the command it could not invoke: %v", problems)
}

type notInvokable struct{}

func (notInvokable) Error() string { return "no such file or directory" }

var errNotInvokable = notInvokable{}

// --- Stella's HOLD on #1629 at 1b22de16: three ways a declared normalisation
// --- hid a difference it had promised to leave visible. Each test below fails
// --- on that head; the comment on each names the one edit that reverts its fix.

// ROW 1. A norm declared for one field must not match a DIFFERENT field whose
// name merely ends in the declared one. `Instant("created")` matched inside
// `last_created=`, so a changed value on a field nobody declared was erased and
// the comparison found nothing.
// Reverting fix: drop `field:` from the Norm that Instant builds (one edit) and
// apply falls back to the unanchored ReplaceAllString.
func TestADeclaredNormDoesNotMatchAFieldWhoseNameEndsInIt(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		what string
		want string
		got  string
		norm Norm
	}{
		{
			what: "an instant field whose name ends in the declared one",
			want: "OK last_created=2026-09-19T01:00:00Z",
			got:  "OK last_created=2026-09-19T11:02:41Z",
			norm: Instant("created"),
		},
	} {
		step := Step{Line: "$ nova-alpha put", Want: []string{c.want}}
		problems := Compare(step, Result{Stdout: c.got + "\n"}, []Norm{c.norm})
		assert.Len(t, problems, 1, "%s: Compare found %d problems, want 1; the declared norm swallowed a field it does not name.\nwant: %s\ngot:  %s", c.what, len(problems), c.want, c.got)
	}
}

// ROW 2. `Instant` says the value is an RFC3339 instant in UTC. A pattern that
// checks only the SHAPE of the digits accepts `2026-99-99T99:99:99Z`, so a tool
// printing an impossible date was normalised away instead of being shown. A
// value the constructor did not promise is left where the comparison sees it.
// Reverting fix: drop `valid: isInstant` from Instant (one edit).
func TestAnInstantNormLeavesAnImpossibleInstantVisible(t *testing.T) {
	t.Parallel()

	for _, got := range []string{
		"OK created=2026-99-99T99:99:99Z",
		"OK created=2026-09-19T25:00:00Z",
		"OK created=2026-02-29T01:00:00Z", // 2026 is not a leap year
		"OK created=2026-13-01T01:00:00Z",
	} {
		step := Step{Line: "$ nova-alpha put", Want: []string{"OK created=2026-09-19T01:00:00Z"}}
		problems := Compare(step, Result{Stdout: got + "\n"}, []Norm{Instant("created")})
		assert.Len(t, problems, 1, "Compare found %d problems, want 1: %q is not an instant and was normalised as one", len(problems), got)
	}
	// A real instant, with and without a fraction, is still the run's.
	step := Step{Line: "$ nova-alpha put", Want: []string{"OK created=2026-09-19T01:00:00Z"}}
	problems := Compare(step, Result{Stdout: "OK created=2026-02-28T23:59:59.5Z\n"}, []Norm{Instant("created")})
	assert.Empty(t, problems, "a real instant of this run was not normalised: %v", problems)
}

// ROW 3. `SplitShell` handed the runner an argv that is not the command the
// reader typed: a single-quoted sentence split on its spaces into two fields
// carrying the quotes. A quoting form this harness does not read is refused by
// name; it is never guessed at.
// Reverting fix: delete the single-quote arm of SplitShell's bare state (one
// edit) and the sentence splits again.
func TestSplitShellKeepsASingleQuotedSentenceWhole(t *testing.T) {
	t.Parallel()

	got, err := SplitShell(`nova-alpha say --body 'hello world' --to Emma`)
	require.NoError(t, err)
	want := []string{"nova-alpha", "say", "--body", "hello world", "--to", "Emma"}
	assert.Equal(t, strings.Join(want, "\x00"), strings.Join(got, "\x00"), "SplitShell gave %q, want %q", got, want)
	// Inside single quotes nothing is special, as in the shell the reader types
	// into: the backslash and the double quote are the argument's own.
	got, err = SplitShell(`nova-alpha say --body 'a "brass" fitting\n'`)
	require.NoError(t, err)
	last := got[len(got)-1]
	assert.Equal(t, `a "brass" fitting\n`, last, "the single-quoted argument = %q, want %q", last, `a "brass" fitting\n`)
	// A shell keeps a backslash that stands before an ordinary character inside
	// double quotes; the old logic ate every one of them.
	got, err = SplitShell(`nova-alpha say --body "a\tab and a \"quote\""`)
	require.NoError(t, err)
	last = got[len(got)-1]
	assert.Equal(t, `a\tab and a "quote"`, last, "the double-quoted argument = %q, want %q", last, `a\tab and a "quote"`)
}

// ROW 3's refusals. Each of these is a line whose argv this harness cannot know,
// so it says so rather than handing the runner something else.
// Reverting fix: delete the two `return nil, fmt.Errorf` arms for `\` and "`"
// in SplitShell's bare state (one edit) and the altered argv comes back.
func TestSplitShellRefusesQuotingItCannotRead(t *testing.T) {
	t.Parallel()

	for _, cmd := range []string{
		`nova-alpha say --body 'hello`,
		`nova-alpha say --body "hello`,
		`nova-alpha say --body hello\ world`,
		"nova-alpha say --body `hostname`",
	} {
		got, err := SplitShell(cmd)
		assert.Error(t, err, "SplitShell accepted %q and returned %q; it cannot know that argv", cmd, got)
	}
	// What it DOES pass through untouched, and says so: no expansion happens
	// here. `$PWD` reaches the runner as the document writes it, which is the
	// contract the callers' Path norms are declared against.
	got, err := SplitShell(`nova-alpha add --remote "$PWD/rehearsal.git"`)
	require.NoError(t, err)
	last := got[len(got)-1]
	assert.Equal(t, "$PWD/rehearsal.git", last, "the documented remote = %q, want %q; SplitShell expands nothing", last, "$PWD/rehearsal.git")
}

// The build triple belongs to the machine; the version word before it does not,
// and a tool that started answering something else about itself is the kind of
// drift a `version` line is in the transcript to catch.
func TestGoBuildCoversTheMachineAndNotTheVersionWord(t *testing.T) {
	t.Parallel()

	step := Step{Line: "$ nova-alpha version", Want: []string{"nova-alpha devel linux/amd64 go1.26.5"}}
	problems := Compare(step, Result{Stdout: "nova-alpha devel darwin/arm64 go1.27.1\n"}, []Norm{GoBuild()})
	assert.Empty(t, problems, "a declared build triple was not normalised: %v", problems)
	problems = Compare(step, Result{Stdout: "nova-alpha v0.16.0 darwin/arm64 go1.27.1\n"}, []Norm{GoBuild()})
	assert.Len(t, problems, 1, "GoBuild swallowed the version word too: %v", problems)
}

// A PRECONDITION IS A PROPERTY OF A COMMAND, NOT OF A SECTION. The 2026-09-19
// dogfood rerun is the case: `## nova-sandbox`'s section header names its
// platform, and a worker on Linux still reported three steps as defects,
// because a header cannot say that step 1 is platform-bound and step 4 is not.
// The same run had nowhere to state a JEV key, a forge credential or a posting
// credential, and recorded those steps as defects too.
func TestStepsReadsAPreconditionStatedOnTheCommandLine(t *testing.T) {
	t.Parallel()

	steps, err := Steps("nova-alpha", []string{
		"$ nova-alpha check   # Platform: darwin",
		"CHECK OK backend=sandbox-exec",
		"",
		"$ nova-alpha ask --questions ./q.json   # Requires: JEV_API_KEY",
		"ASK OK n=1",
		"",
		"$ nova-alpha keygen --as rowan   # Platform: darwin, linux; Requires: age-keygen",
		"KEYGEN OK as=rowan",
		"",
		"$ nova-alpha say --body \"a # sign\"",
		"SAY OK",
	})
	require.NoError(t, err)
	got := strings.Join(steps[0].Platforms, ",")
	assert.Equal(t, "darwin", got, "step 0 platforms = %q, want darwin", got)
	got = strings.Join(steps[0].Args, " ")
	assert.Equal(t, "check", got, "step 0 args = %q; the declaration is not an argument", got)
	got = strings.Join(steps[1].Requires, ",")
	assert.Equal(t, "JEV_API_KEY", got, "step 1 requires = %q, want JEV_API_KEY", got)
	got = strings.Join(steps[2].Platforms, ",")
	assert.Equal(t, "darwin,linux", got, "step 2 platforms = %q, want darwin,linux", got)
	got = strings.Join(steps[2].Requires, ",")
	assert.Equal(t, "age-keygen", got, "step 2 requires = %q, want age-keygen", got)
	// A `#` that is not a declaration is an argument and is left alone.
	got = strings.Join(steps[3].Args, "|")
	assert.Equal(t, "say|--body|a # sign", got, "step 3 args = %q; a `#` inside an argument was eaten", got)
	// A comment that means to be a declaration and is not one is the document's
	// bug, and is louder than a comment silently ignored would be.
	_, err = Steps("nova-alpha", []string{"$ nova-alpha check # Requires:", "CHECK OK"})
	assert.Error(t, err, "Steps accepted a `Requires:` that requires nothing")
}

func TestSkipReasonAnswersOnlyWhatTheDocumentStated(t *testing.T) {
	t.Parallel()

	onDarwin := Step{Line: "$ nova-alpha check", Platforms: []string{"darwin"}}
	why := onDarwin.SkipReason("darwin", nil)
	assert.Empty(t, why, "a darwin step on darwin was skipped: %s", why)
	why = onDarwin.SkipReason("linux", nil)
	assert.NotEmpty(t, why, "a darwin step ran on linux")
	needsKey := Step{Line: "$ nova-alpha ask", Requires: []string{"JEV_API_KEY"}}
	why = needsKey.SkipReason("linux", nil)
	assert.NotEmpty(t, why, "a step requiring a key ran on a bench that has none")
	why = needsKey.SkipReason("linux", func(string) bool { return true })
	assert.Empty(t, why, "a step whose requirement is met was skipped: %s", why)
	// #1570's last sentence: a step skipped for a reason the document does not
	// state is a defect in the document, not a pass. An undeclared step runs.
	plain := Step{Line: "$ nova-alpha list"}
	why = plain.SkipReason("plan9", nil)
	assert.Empty(t, why, "a step that states no precondition was skipped: %s", why)
}

func TestExecuteWithSkipsAStatedPreconditionAndRunsTheRest(t *testing.T) {
	t.Parallel()

	steps, err := Steps("nova-alpha", []string{
		"$ nova-alpha check   # Platform: plan9",
		"CHECK OK",
		"",
		"$ nova-alpha list",
		"LIST OK n=1",
	})
	require.NoError(t, err)
	var ran []string
	problems, skips := ExecuteWith(steps, func(s Step) (Result, error) {
		ran = append(ran, s.Args[0])
		return Result{Stdout: "LIST OK n=1\n"}, nil
	}, Conditions{GOOS: "darwin"})
	assert.Empty(t, problems, "the runnable step disagreed: %v", problems)
	require.True(t, len(skips) == 1 && strings.Contains(skips[0].Why, "plan9"), "the plan9 step was not skipped with the document's reason: %v", skips)
	assert.Equal(t, "list", strings.Join(ran, ","), "ExecuteWith ran %q; only the runnable step should have run", ran)
	// The skip is RETURNED, never swallowed: a run whose skips are invisible is
	// a green that means less than it looks like.
	assert.Contains(t, skips[0].String(), "SKIP-PRECONDITION", "a skip does not print as one: %s", skips[0])
}

// #1570's stream convention, on the real block that needs it. nova-self-talk's
// second first-run command prints seven lines out of TWO streams (issue #1639):
// the protocol lines on standard output, the findings on standard error. Under
// the convention the marked lines are stderr's, standard output is compared
// whole, and stderr is compared only for the lines the document shows.
func TestAMarkedBlockComparesEachStreamOnItsOwnTerms(t *testing.T) {
	t.Parallel()

	step := Step{
		Line: "$ nova-alpha check ./pages/RULES.md ./pages/journal.md",
		Want: []string{
			"ALPHA RULEDOC ./pages/RULES.md: rule documents",
			"! ALPHA FAIL ./pages/RULES.md:8: INSTALLATION VERDICT-IDIOM",
			"! ALPHA FAIL ./pages/journal.md: STANDING",
			"ALPHA DATED n=1 files=2",
		},
	}
	res := Result{
		Code:   1,
		Stdout: "ALPHA RULEDOC ./pages/RULES.md: rule documents\nALPHA DATED n=1 files=2\n",
		Stderr: "ALPHA FAIL ./pages/RULES.md:8: INSTALLATION VERDICT-IDIOM\nALPHA FAIL ./pages/journal.md: STANDING\nALPHA FAIL ./pages/journal.md:10: INSTALLATION RANKING\n",
	}
	// The third FAIL is narration the document has no room for: stderr may
	// carry more than is shown.
	problems := Compare(step, res, nil)
	assert.Empty(t, problems, "a marked block disagreed: %v", problems)
	// Standard output is still compared WHOLE: a line the tool prints there and
	// the document does not show is red, marker or no marker.
	extra := res
	extra.Stdout += "ALPHA NOTE catches known shapes only\n"
	assert.Len(t, Compare(step, extra, nil), 1, "an unshown standard-output line passed under the stream convention")
	// A shown stderr line the tool did not print is red.
	missing := res
	missing.Stderr = "ALPHA FAIL ./pages/journal.md: STANDING\n"
	assert.Len(t, Compare(step, missing, nil), 1, "a documented standard-error line that was never printed passed")
	// And so is a shown pair printed in the other order.
	swapped := res
	swapped.Stderr = "ALPHA FAIL ./pages/journal.md: STANDING\nALPHA FAIL ./pages/RULES.md:8: INSTALLATION VERDICT-IDIOM\n"
	assert.Len(t, Compare(step, swapped, nil), 1, "two documented standard-error lines passed in the wrong order")
}

// Shape must see through the stream marker. #1570 names this as owed by the
// first section swept to the convention: without it Shape returns "" for a
// marked line -- `!` is not an event token -- so marking a line would silently
// reduce its coverage to NOTHING in every caller that compares shapes, which is
// the opposite of what marking it is for.
func TestShapeSeesThroughTheStreamMarker(t *testing.T) {
	t.Parallel()

	const line = "SELFTALK FAIL ./pages/journal.md: STANDING: I cannot check my own work."
	want := Shape(line)
	require.NotEmpty(t, want, "Shape(%q) is empty; this test cannot say anything", line)
	got := Shape(StderrMarker + line)
	assert.Equal(t, want, got, "Shape of the marked line = %q, want %q", got, want)
}

// `# Stderr: whole` is the other half of the stream convention, and the reason
// it had to exist. #1570 compares standard error only for the lines SHOWN,
// which is right for a tool that narrates there and wrong for one whose
// findings live there: under the asymmetry alone, deleting every finding from
// nova-self-talk's transcript is green, and that deletion is issue #1639.
func TestStderrWholeMakesADroppedFindingRed(t *testing.T) {
	t.Parallel()

	lines := []string{
		"$ nova-alpha check ./pages/journal.md",
		"! ALPHA FAIL ./pages/journal.md: STANDING",
		"! ALPHA FAIL ./pages/journal.md:10: RANKING",
		"ALPHA DATED n=1 files=1",
	}
	res := Result{
		Code:   1,
		Stdout: "ALPHA DATED n=1 files=1\n",
		Stderr: "ALPHA FAIL ./pages/journal.md: STANDING\nALPHA FAIL ./pages/journal.md:10: RANKING\n",
	}

	shown, err := Steps("nova-alpha", lines)
	require.NoError(t, err)
	problems := Compare(shown[0], res, nil)
	assert.Empty(t, problems, "the whole block disagreed: %v", problems)

	// Drop the second finding from the document. Without the declaration this
	// is green, because stderr may carry more than is shown.
	abridged := append([]string(nil), lines[:2]...)
	abridged = append(abridged, lines[3])
	loose, err := Steps("nova-alpha", abridged)
	require.NoError(t, err)
	assert.Empty(t, Compare(loose[0], res, nil), "the unqualified convention already caught a dropped standard-error line; this test is now saying nothing")

	// With it, the same abridgement is red.
	abridged[0] += "   # Stderr: whole"
	strict, err := Steps("nova-alpha", abridged)
	require.NoError(t, err)
	require.True(t, strict[0].StderrWhole, "`# Stderr: whole` was not read off the command line")
	problems = Compare(strict[0], res, nil)
	require.Len(t, problems, 1, "Compare found %d problems, want 1: %v", len(problems), problems)
	assert.Contains(t, problems[0].Message, "on standard error", "the failure does not say which stream it is about:\n%s", problems[0].Message)
	// And the only thing a step may say about that stream is `whole`.
	_, err = Steps("nova-alpha", []string{"$ nova-alpha check   # Stderr: quiet", "ALPHA OK"})
	assert.Error(t, err, "Steps accepted a `Stderr:` value it does not understand")
}

// A norm replaces LITERALLY. `As` is a sentence a reader is shown, not a
// template: docs/TESTS.md's nova-merge block writes the reader's own path as
// `$PWD/rehearsal.git`, and a replacement read as a template would take `$PWD`
// for a capture group, expand it to nothing, and leave the two sides
// disagreeing about a path that had just been normalised.
func TestANormReplacesLiterally(t *testing.T) {
	t.Parallel()

	step := Step{Line: "$ nova-alpha init", Want: []string{"INIT OK remote=$PWD/rehearsal.git"}}
	res := Result{Stdout: "INIT OK remote=/tmp/T/001/rehearsal.git\n"}
	problems := Compare(step, res, []Norm{Path("$PWD/rehearsal.git", "/tmp/T/001/rehearsal.git")})
	assert.Empty(t, problems, "a declared path holding `$` was not normalised: %v", problems)
}

// --- Fable's cold read of #1632 (medium): the repaired `version` line is not
// --- what the shipped verb prints, and GoBuild was an unanchored ReplaceAll.

// A `version` line has TWO declared parts and they have different owners: the
// build triple is the machine, the word before it is the build. GoBuild must
// not reach past its own two tokens, and must not match from the middle of a
// longer one -- it was a plain ReplaceAll over every line of every step.
func TestGoBuildCoversTwoWholeTokensAndNothingElse(t *testing.T) {
	t.Parallel()

	step := Step{Line: "$ nova-alpha where", Want: []string{"WHERE OK dir=/srv/linux/amd64 go1.26.5-cache"}}
	res := Result{Stdout: "WHERE OK dir=/srv/darwin/arm64 go1.27.1-cache\n"}
	problems := Compare(step, res, []Norm{GoBuild()})
	assert.Len(t, problems, 1, "Compare found %d problems, want 1: GoBuild matched inside a path token", len(problems))
	// Its own two tokens, standing alone, are still normalised.
	step = Step{Line: "$ nova-alpha version", Want: []string{"nova-alpha devel linux/amd64 go1.26.5"}}
	problems = Compare(step, Result{Stdout: "nova-alpha devel darwin/arm64 go1.27.1\n"}, []Norm{GoBuild()})
	assert.Empty(t, problems, "a declared build triple was not normalised: %v", problems)
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
	t.Parallel()

	step := Step{Line: "$ nova-alpha version", Want: []string{"nova-alpha v0.16.0-dev.c839379e.0.20260919144920-705dd1c92534 darwin/arm64 go1.27.1"}}
	res := Result{Stdout: "nova-alpha devel darwin/arm64 go1.27.1\n"}
	problems := Compare(step, res, []Norm{Version(), GoBuild()})
	assert.Empty(t, problems, "the document's stamp and the test binary's `devel` disagreed: %v", problems)
	// The tool's NAME is not the version word, and a tool that answered
	// something that is neither a stamp nor `devel` is still a finding.
	res.Stdout = "nova-alpha unknown darwin/arm64 go1.27.1\n"
	problems = Compare(step, res, []Norm{Version(), GoBuild()})
	assert.Len(t, problems, 1, "Compare found %d problems, want 1: a version word that is neither a stamp nor `devel` was normalised", len(problems))
	// And it does not reach inside a longer token.
	step = Step{Line: "$ nova-alpha list", Want: []string{"LIST OK tag=v1.2.3-rc1 name=alpha"}}
	problems = Compare(step, Result{Stdout: "LIST OK tag=v9.9.9-rc1 name=alpha\n"}, []Norm{Version()})
	assert.Len(t, problems, 1, "Compare found %d problems, want 1: Version matched inside `tag=`", len(problems))
}

// --- Fable's cold read of #1674 (HIGH): Execute skipped every step that stated
// --- a precondition, and threw the skips away.

// THE HIGH ONE. `Execute` called `ExecuteWith` with an empty `Conditions` and
// DISCARDED the skips it came back with. An empty GOOS matches no platform and a
// nil Have meets no requirement, so every step that stated a precondition -- the
// whole point of #1674 -- was skipped, and a block could turn its test green
// having run nothing at all. Nothing printed, nothing counted.
//
// A caller that wants to tolerate a skip calls ExecuteWith and decides. Execute
// is the simple entry point, and the simple entry point must never be green on
// a promise it did not check.
func TestExecuteRunsAStepThatStatesAPreconditionThisBenchMeets(t *testing.T) {
	t.Parallel()

	steps, err := Steps("nova-alpha", []string{
		"$ nova-alpha list   # Platform: " + runtime.GOOS,
		"LIST OK n=1",
	})
	require.NoError(t, err)
	ran := 0
	problems := Execute(steps, func(Step) (Result, error) {
		ran++
		return Result{Stdout: "LIST OK n=1\n"}, nil
	})
	assert.Equal(t, 1, ran, "Execute ran %d of 1 steps; a step recorded for THIS platform must run here", ran)
	assert.Empty(t, problems, "Execute reported %d problems on a step it should have run and agreed with: %v", len(problems), problems)
}

// AN ALL-SKIPPED BLOCK IS RED, IN THE HARNESS. #1677's caller checks this for
// itself; every other caller did not, and a caller cannot be asked to remember.
// A sitting that ran nothing proved nothing, and it is the harness that knows.
func TestExecuteRefusesABlockItSkippedEntirely(t *testing.T) {
	t.Parallel()

	steps, err := Steps("nova-alpha", []string{
		"$ nova-alpha list   # Requires: NOPE",
		"LIST OK n=1",
		"",
		"$ nova-alpha count   # Requires: NOPE",
		"COUNT OK n=0",
	})
	require.NoError(t, err)
	ran := 0
	problems := Execute(steps, func(Step) (Result, error) {
		ran++
		return Result{Stdout: "LIST OK n=1\n"}, nil
	})
	require.Equal(t, 0, ran, "Execute ran %d steps whose requirement this bench does not have", ran)
	require.NotEmpty(t, problems, "Execute found NO problem in a block it skipped from end to end; that is a green that proved nothing")
	whole := problems[0].Message
	for _, want := range []string{"skipped", "NOPE", "nova-alpha list", "nova-alpha count"} {
		assert.Contains(t, whole, want, "the refusal does not name %q:\n%s", want, whole)
	}
}

// A skip that is NOT the whole block is still counted and printed rather than
// swallowed: the block ran less than the document promises, and the caller is
// told which step and in the document's own words.
func TestExecuteReportsASkipItDidNotRun(t *testing.T) {
	t.Parallel()

	steps, err := Steps("nova-alpha", []string{
		"$ nova-alpha list",
		"LIST OK n=1",
		"",
		"$ nova-alpha count   # Requires: NOPE",
		"COUNT OK n=0",
	})
	require.NoError(t, err)
	problems := Execute(steps, func(Step) (Result, error) {
		return Result{Stdout: "LIST OK n=1\n"}, nil
	})
	require.Len(t, problems, 1, "Execute reported %d problems, want 1 (the skipped step): %v", len(problems), problems)
	assert.Contains(t, problems[0].Message, "NOPE", "the skip does not carry the document's own reason:\n%s", problems[0].Message)
}

// `# Platform:` inside SINGLE quotes is part of the argument, not a declaration.
// lastComment tracked only the double quote, so this line was cut in half and
// then failed as an unterminated quote -- a defect reported as the wrong defect.
func TestAHashInsideSingleQuotesIsNotADeclaration(t *testing.T) {
	t.Parallel()

	steps, err := Steps("nova-alpha", []string{"$ nova-alpha say --body 'a # Platform: darwin thing'", "SAY OK"})
	require.NoError(t, err)
	assert.Empty(t, steps[0].Platforms, "a `# Platform:` inside single quotes was read as a declaration: %v", steps[0].Platforms)
	got, want := steps[0].Args[len(steps[0].Args)-1], "a # Platform: darwin thing"
	assert.Equal(t, want, got, "the quoted argument = %q, want %q", got, want)
}

// A `# Platform:` value that is not a GOOS is a step that skips on every bench
// for ever and is never seen again. `macOS` is the one a person writes.
func TestAPlatformDeclarationMustNameAGOOS(t *testing.T) {
	t.Parallel()

	for _, line := range []string{
		"$ nova-alpha list   # Platform: macOS",
		"$ nova-alpha list   # Platform: mac",
		"$ nova-alpha list   # Platform: darwin,Linux",
	} {
		_, err := Steps("nova-alpha", []string{line, "LIST OK n=1"})
		assert.Error(t, err, "Steps accepted %q; that value is no GOOS and the step would skip everywhere for ever", line)
	}
	for _, line := range []string{
		"$ nova-alpha list   # Platform: darwin",
		"$ nova-alpha list   # Platform: darwin,linux",
	} {
		_, err := Steps("nova-alpha", []string{line, "LIST OK n=1"})
		assert.NoError(t, err, "Steps refused %q, which names real platforms: %v", line, err)
	}
}
