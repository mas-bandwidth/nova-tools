package onboarding

import (
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	require.Len(t, steps, 2)
	assert.Equal(t, "put --name gate", strings.Join(steps[0].Args, " "))
	assert.Len(t, steps[0].Want, 1)
	assert.Len(t, steps[1].Want, 2)
}

func TestStepsReadsTheOneRedirectTheTranscriptsUse(t *testing.T) {
	t.Parallel()

	steps, err := Steps("nova-alpha", []string{"$ nova-alpha count < testdata/events.jsonl", "COUNT OK n=2"})
	require.NoError(t, err)
	assert.Equal(t, "testdata/events.jsonl", steps[0].Stdin)
	assert.Equal(t, "count", strings.Join(steps[0].Args, " "))
}

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
		assert.Error(t, err)
	}
	_, err := Steps("nova-alpha", []string{"LIST OK n=1", "$ nova-alpha list"})
	assert.Error(t, err)
}

func TestSplitShellKeepsAQuotedSentenceWhole(t *testing.T) {
	t.Parallel()

	got, err := SplitShell(`nova-alpha say --body "a \"brass\" fitting" --to Emma`)
	require.NoError(t, err)
	want := []string{"nova-alpha", "say", "--body", `a "brass" fitting`, "--to", "Emma"}
	require.Equal(t, len(want), len(got))
	for i := range want {
		assert.Equal(t, want[i], got[i])
	}
}

func TestAnAbridgedTranscriptIsRed(t *testing.T) {
	t.Parallel()

	step := Step{Line: "$ nova-alpha list", Want: []string{"LIST ENTRY name=gate"}}
	res := Result{Stdout: "LIST ENTRY name=gate\nLIST OK n=1\n"}
	problems := Compare(step, res, nil)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0].Message, "prints 2 line(s) and the document shows 1")
}

func TestAReorderedTranscriptIsRed(t *testing.T) {
	t.Parallel()

	step := Step{Line: "$ nova-alpha list", Want: []string{"LIST OK n=1", "LIST ENTRY name=gate"}}
	res := Result{Stdout: "LIST ENTRY name=gate\nLIST OK n=1\n"}
	assert.Len(t, Compare(step, res, nil), 2)
}

func TestAWrongValueIsRed(t *testing.T) {
	t.Parallel()

	step := Step{Line: "$ nova-alpha list", Want: []string{"LIST OK n=1"}}
	problems := Compare(step, Result{Stdout: "LIST OK n=2\n"}, nil)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0].Message, "no normalisation")
}

func TestADeclaredNormCoversOnlyItsOwnField(t *testing.T) {
	t.Parallel()

	step := Step{
		Line: "$ nova-alpha put --name gate",
		Want: []string{"PUT OK name=gate id=0f1e2d3c created=2026-09-19T06:29:53Z seen=2026-09-19T06:29:53Z"},
	}
	res := Result{Stdout: "PUT OK name=gate id=0f1e2d3c created=2026-09-19T11:02:41Z seen=2026-09-19T06:29:53Z\n"}
	problems := Compare(step, res, []Norm{Instant("created")})
	assert.Empty(t, problems)
	res.Stdout = "PUT OK name=gate id=0f1e2d3c created=2026-09-19T11:02:41Z seen=2026-09-19T11:02:41Z\n"
	problems = Compare(step, res, []Norm{Instant("created")})
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0].Message, "created= (the instant of this run)")
	problems = Compare(step, res, []Norm{Instant("created"), Instant("seen")})
	assert.Empty(t, problems)
}

func TestPathNormReducesBothSidesToTheDocumentedSpelling(t *testing.T) {
	t.Parallel()

	step := Step{Line: "$ nova-alpha where", Want: []string{"WHERE OK store=./cairns"}}
	res := Result{Stdout: "WHERE OK store=/var/folders/T/x9/cairns\n"}
	problems := Compare(step, res, []Norm{Path("./cairns", "/var/folders/T/x9/cairns")})
	assert.Empty(t, problems)
}

func TestPathNormRecognizesACommaAfterThePath(t *testing.T) {
	t.Parallel()

	step := Step{Line: "$ nova-alpha where", Want: []string{"WHERE NOTE store=./cairns, using the documented location"}}
	res := Result{Stdout: "WHERE NOTE store=/var/folders/T/x9/cairns, using the documented location\n"}
	problems := Compare(step, res, []Norm{Path("./cairns", "/var/folders/T/x9/cairns")})
	assert.Empty(t, problems)
}

func TestAStepThatWroteToBothStreamsIsReported(t *testing.T) {
	t.Parallel()

	step := Step{Line: "$ nova-alpha list", Want: []string{"LIST OK n=1"}}
	problems := Compare(step, Result{Stdout: "LIST OK n=1\n", Stderr: "nova-alpha: a warning\n"}, nil)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0].Message, "stdout AND stderr")
}

func TestARefusalIsComparedOnStderr(t *testing.T) {
	t.Parallel()

	step := Step{Line: "$ nova-alpha", Want: []string{"nova-alpha: no verb given; run: nova-alpha help"}}
	res := Result{Code: 2, Stderr: "nova-alpha: no verb given; run: nova-alpha help\n"}
	problems := Compare(step, res, nil)
	assert.Empty(t, problems)
}

func TestExecuteStopsAtACommandItCannotInvoke(t *testing.T) {
	t.Parallel()

	steps, err := Steps("nova-alpha", transcriptLines)
	require.NoError(t, err)
	ran := 0
	problems := Execute(steps, func(s Step) (Result, error) {
		ran++
		return Result{}, errNotInvokable
	})
	assert.Equal(t, 1, ran)
	assert.True(t, len(problems) == 1 && strings.Contains(problems[0].Message, "could not be run"))
}

type notInvokable struct{}

func (notInvokable) Error() string { return "no such file or directory" }

var errNotInvokable = notInvokable{}

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
		assert.Len(t, problems, 1)
	}
}

func TestAnInstantNormLeavesAnImpossibleInstantVisible(t *testing.T) {
	t.Parallel()

	for _, got := range []string{
		"OK created=2026-99-99T99:99:99Z",
		"OK created=2026-09-19T25:00:00Z",
		"OK created=2026-02-29T01:00:00Z",
		"OK created=2026-13-01T01:00:00Z",
	} {
		step := Step{Line: "$ nova-alpha put", Want: []string{"OK created=2026-09-19T01:00:00Z"}}
		problems := Compare(step, Result{Stdout: got + "\n"}, []Norm{Instant("created")})
		assert.Len(t, problems, 1)
	}
	step := Step{Line: "$ nova-alpha put", Want: []string{"OK created=2026-09-19T01:00:00Z"}}
	problems := Compare(step, Result{Stdout: "OK created=2026-02-28T23:59:59.5Z\n"}, []Norm{Instant("created")})
	assert.Empty(t, problems)
}

func TestSplitShellKeepsASingleQuotedSentenceWhole(t *testing.T) {
	t.Parallel()

	got, err := SplitShell(`nova-alpha say --body 'hello world' --to Emma`)
	require.NoError(t, err)
	want := []string{"nova-alpha", "say", "--body", "hello world", "--to", "Emma"}
	assert.Equal(t, strings.Join(want, "\x00"), strings.Join(got, "\x00"))
	got, err = SplitShell(`nova-alpha say --body 'a "brass" fitting\n'`)
	require.NoError(t, err)
	assert.Equal(t, `a "brass" fitting\n`, got[len(got)-1])
	got, err = SplitShell(`nova-alpha say --body "a\tab and a \"quote\""`)
	require.NoError(t, err)
	assert.Equal(t, `a\tab and a "quote"`, got[len(got)-1])
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
		_, err := SplitShell(cmd)
		assert.Error(t, err)
	}
	got, err := SplitShell(`nova-alpha add --remote "$PWD/rehearsal.git"`)
	require.NoError(t, err)
	assert.Equal(t, "$PWD/rehearsal.git", got[len(got)-1])
}

func TestGoBuildCoversTheMachineAndNotTheVersionWord(t *testing.T) {
	t.Parallel()

	step := Step{Line: "$ nova-alpha version", Want: []string{"nova-alpha devel linux/amd64 go1.26.5"}}
	problems := Compare(step, Result{Stdout: "nova-alpha devel darwin/arm64 go1.27.1\n"}, []Norm{GoBuild()})
	assert.Empty(t, problems)
	problems = Compare(step, Result{Stdout: "nova-alpha v0.16.0 darwin/arm64 go1.27.1\n"}, []Norm{GoBuild()})
	assert.Len(t, problems, 1)
}

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
	assert.Equal(t, "darwin", strings.Join(steps[0].Platforms, ","))
	assert.Equal(t, "check", strings.Join(steps[0].Args, " "))
	assert.Equal(t, "JEV_API_KEY", strings.Join(steps[1].Requires, ","))
	assert.Equal(t, "darwin,linux", strings.Join(steps[2].Platforms, ","))
	assert.Equal(t, "age-keygen", strings.Join(steps[2].Requires, ","))
	assert.Equal(t, "say|--body|a # sign", strings.Join(steps[3].Args, "|"))
	_, err = Steps("nova-alpha", []string{"$ nova-alpha check # Requires:", "CHECK OK"})
	assert.Error(t, err)
}

func TestSkipReasonAnswersOnlyWhatTheDocumentStated(t *testing.T) {
	t.Parallel()

	onDarwin := Step{Line: "$ nova-alpha check", Platforms: []string{"darwin"}}
	why := onDarwin.SkipReason("darwin", nil)
	assert.Empty(t, why)
	why = onDarwin.SkipReason("linux", nil)
	assert.NotEmpty(t, why)
	needsKey := Step{Line: "$ nova-alpha ask", Requires: []string{"JEV_API_KEY"}}
	why = needsKey.SkipReason("linux", nil)
	assert.NotEmpty(t, why)
	why = needsKey.SkipReason("linux", func(string) bool { return true })
	assert.Empty(t, why)
	plain := Step{Line: "$ nova-alpha list"}
	why = plain.SkipReason("plan9", nil)
	assert.Empty(t, why)
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
	assert.Empty(t, problems)
	require.Len(t, skips, 1)
	assert.Contains(t, skips[0].Why, "plan9")
	assert.Equal(t, "list", strings.Join(ran, ","))
	assert.Contains(t, skips[0].String(), "SKIP-PRECONDITION")
}

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
	problems := Compare(step, res, nil)
	assert.Empty(t, problems)
	extra := res
	extra.Stdout += "ALPHA NOTE catches known shapes only\n"
	assert.Len(t, Compare(step, extra, nil), 1)
	missing := res
	missing.Stderr = "ALPHA FAIL ./pages/journal.md: STANDING\n"
	assert.Len(t, Compare(step, missing, nil), 1)
	swapped := res
	swapped.Stderr = "ALPHA FAIL ./pages/journal.md: STANDING\nALPHA FAIL ./pages/RULES.md:8: INSTALLATION VERDICT-IDIOM\n"
	assert.Len(t, Compare(step, swapped, nil), 1)
}

func TestShapeSeesThroughTheStreamMarker(t *testing.T) {
	t.Parallel()

	const line = "SELFTALK FAIL ./pages/journal.md: STANDING: I cannot check my own work."
	want := Shape(line)
	require.NotEmpty(t, want)
	got := Shape(StderrMarker + line)
	assert.Equal(t, want, got)
}

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
	assert.Empty(t, problems)

	abridged := append([]string(nil), lines[:2]...)
	abridged = append(abridged, lines[3])
	loose, err := Steps("nova-alpha", abridged)
	require.NoError(t, err)
	assert.Empty(t, Compare(loose[0], res, nil))

	abridged[0] += "   # Stderr: whole"
	strict, err := Steps("nova-alpha", abridged)
	require.NoError(t, err)
	require.True(t, strict[0].StderrWhole)
	problems = Compare(strict[0], res, nil)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0].Message, "on standard error")
	_, err = Steps("nova-alpha", []string{"$ nova-alpha check   # Stderr: quiet", "ALPHA OK"})
	assert.Error(t, err)
}

func TestANormReplacesLiterally(t *testing.T) {
	t.Parallel()

	step := Step{Line: "$ nova-alpha init", Want: []string{"INIT OK remote=$PWD/rehearsal.git"}}
	res := Result{Stdout: "INIT OK remote=/tmp/T/001/rehearsal.git\n"}
	problems := Compare(step, res, []Norm{Path("$PWD/rehearsal.git", "/tmp/T/001/rehearsal.git")})
	assert.Empty(t, problems)
}

func TestGoBuildCoversTwoWholeTokensAndNothingElse(t *testing.T) {
	t.Parallel()

	step := Step{Line: "$ nova-alpha where", Want: []string{"WHERE OK dir=/srv/linux/amd64 go1.26.5-cache"}}
	res := Result{Stdout: "WHERE OK dir=/srv/darwin/arm64 go1.27.1-cache\n"}
	problems := Compare(step, res, []Norm{GoBuild()})
	assert.Len(t, problems, 1)
	step = Step{Line: "$ nova-alpha version", Want: []string{"nova-alpha devel linux/amd64 go1.26.5"}}
	problems = Compare(step, Result{Stdout: "nova-alpha devel darwin/arm64 go1.27.1\n"}, []Norm{GoBuild()})
	assert.Empty(t, problems)
}

func TestVersionNormCoversTheStampAndDevelAndNothingElse(t *testing.T) {
	t.Parallel()

	step := Step{Line: "$ nova-alpha version", Want: []string{"nova-alpha v0.16.0-dev.c839379e.0.20260919144920-705dd1c92534 darwin/arm64 go1.27.1"}}
	res := Result{Stdout: "nova-alpha devel darwin/arm64 go1.27.1\n"}
	problems := Compare(step, res, []Norm{Version(), GoBuild()})
	assert.Empty(t, problems)
	res.Stdout = "nova-alpha unknown darwin/arm64 go1.27.1\n"
	problems = Compare(step, res, []Norm{Version(), GoBuild()})
	assert.Len(t, problems, 1)
	step = Step{Line: "$ nova-alpha list", Want: []string{"LIST OK tag=v1.2.3-rc1 name=alpha"}}
	problems = Compare(step, Result{Stdout: "LIST OK tag=v9.9.9-rc1 name=alpha\n"}, []Norm{Version()})
	assert.Len(t, problems, 1)
}

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
	assert.Equal(t, 1, ran)
	assert.Empty(t, problems)
}

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
	require.Equal(t, 0, ran)
	require.NotEmpty(t, problems)
	whole := problems[0].Message
	for _, want := range []string{"skipped", "NOPE", "nova-alpha list", "nova-alpha count"} {
		assert.Contains(t, whole, want)
	}
}

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
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0].Message, "NOPE")
}

func TestAHashInsideSingleQuotesIsNotADeclaration(t *testing.T) {
	t.Parallel()

	steps, err := Steps("nova-alpha", []string{"$ nova-alpha say --body 'a # Platform: darwin thing'", "SAY OK"})
	require.NoError(t, err)
	assert.Empty(t, steps[0].Platforms)
	got, want := steps[0].Args[len(steps[0].Args)-1], "a # Platform: darwin thing"
	assert.Equal(t, want, got)
}

func TestStepsPreservesQuotedSyntaxCharacters(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name      string
		line      string
		args      []string
		stdin     string
		platforms []string
		requires  []string
		stderr    bool
		wantErr   bool
	}{
		{
			name: "a single-quoted literal less-than is an argument, not a redirect",
			line: `$ nova-alpha count --body 'a < b'`,
			args: []string{"count", "--body", "a < b"},
		},
		{
			name: "a double-quoted literal greater-than is an argument",
			line: `$ nova-alpha say --body "a > b"`,
			args: []string{"say", "--body", "a > b"},
		},
		{
			name: "a single-quoted literal pipe is an argument",
			line: `$ nova-alpha say --body 'a | b'`,
			args: []string{"say", "--body", "a | b"},
		},
		{
			name: "a double-quoted literal and-and is an argument",
			line: `$ nova-alpha say --body "a && b"`,
			args: []string{"say", "--body", "a && b"},
		},
		{
			name: "a single-quoted literal semicolon is an argument",
			line: `$ nova-alpha say --body 'a ; b'`,
			args: []string{"say", "--body", "a ; b"},
		},
		{
			name: "an escaped double quote before an in-argument declaration marker",
			line: `$ nova-alpha say --body "a \" # Platform: linux"`,
			args: []string{"say", "--body", `a " # Platform: linux`},
		},
		{
			name: "an in-argument Requires marker is not a declaration",
			line: `$ nova-alpha say --body 'a # Requires: JEV_API_KEY b'`,
			args: []string{"say", "--body", "a # Requires: JEV_API_KEY b"},
		},
		{
			name: "an in-argument Stderr marker is not a declaration",
			line: `$ nova-alpha say --body "a \" # Stderr: whole"`,
			args: []string{"say", "--body", `a " # Stderr: whole`},
		},
		{
			name:      "a real trailing declaration after a quoted argument still parses",
			line:      `$ nova-alpha say --body "a \" # Platform: linux"   # Platform: darwin`,
			args:      []string{"say", "--body", `a " # Platform: linux`},
			platforms: []string{"darwin"},
		},
		{
			name:  "the existing unquoted stdin redirect is unchanged",
			line:  `$ nova-alpha count < testdata/events.jsonl`,
			args:  []string{"count"},
			stdin: "testdata/events.jsonl",
		},
		{
			name:    "an unquoted pipe is still refused",
			line:    `$ nova-alpha list | head -2`,
			wantErr: true,
		},
		{
			name:    "an unquoted output redirect is still refused",
			line:    `$ nova-alpha list > out.txt`,
			wantErr: true,
		},
		{
			name:    "an unquoted separator is still refused",
			line:    `$ nova-alpha list ; echo done`,
			wantErr: true,
		},
		{
			name:    "an unquoted and-and is still refused",
			line:    `$ nova-alpha list && echo done`,
			wantErr: true,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			steps, err := Steps("nova-alpha", []string{c.line, "ALPHA OK"})
			if c.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Len(t, steps, 1)
			assert.Equal(t, c.args, steps[0].Args)
			assert.Equal(t, c.stdin, steps[0].Stdin)
			assert.Equal(t, c.platforms, steps[0].Platforms)
			assert.Equal(t, c.requires, steps[0].Requires)
			assert.Equal(t, c.stderr, steps[0].StderrWhole)
		})
	}
}

func TestAPlatformDeclarationMustNameAGOOS(t *testing.T) {
	t.Parallel()

	for _, line := range []string{
		"$ nova-alpha list   # Platform: macOS",
		"$ nova-alpha list   # Platform: mac",
		"$ nova-alpha list   # Platform: darwin,Linux",
	} {
		_, err := Steps("nova-alpha", []string{line, "LIST OK n=1"})
		assert.Error(t, err)
	}
	for _, line := range []string{
		"$ nova-alpha list   # Platform: darwin",
		"$ nova-alpha list   # Platform: darwin,linux",
	} {
		_, err := Steps("nova-alpha", []string{line, "LIST OK n=1"})
		assert.NoError(t, err)
	}
}
