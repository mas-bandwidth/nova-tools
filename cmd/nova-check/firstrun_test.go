package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The onboarding standard (ONBOARDING.md), pinned for this binary. A newcomer's
// first stumble is the spec for these tests: the usage banner's examples are RUN
// rather than read, every refusal a first run hits must say what the flag WANTS,
// and the README's transcript is compared against what the tool actually prints.
// Guidance nothing checks rots into a claim about a message that has since moved.

// exampleSelf is the fixture self repo that ships with this tool: a five-file
// tree the size of a first run, referenced by nothing outside testdata.
const exampleSelf = "testdata/example-self"

func runCheck(t *testing.T, args ...string) (exit int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	exit = run(args, &out, &errb)
	return exit, out.String(), errb.String()
}

// exampleDogfood is the fixture the dogfood verb's banner example runs
// against: a command reference the size of a first run, and the receipts two
// friends left against it.
const exampleDogfood = "testdata/example-dogfood"

// localize points an example or transcript command at the fixture, so what is
// under test is the command's SHAPE and not the reader's directory layout. The
// banner shows a reader the paths they would type from the repository root
// (./self, ./docs/CLI.md); here those become the fixtures that ship with the
// tool, so the example is run rather than read.
func localize(args []string) []string {
	out := append([]string(nil), args...)
	for i, a := range out {
		switch a {
		case "./self":
			out[i] = exampleSelf
		case "./docs/CLI.md":
			out[i] = filepath.Join(exampleDogfood, "CLI.md")
		case "./dogfood-receipts":
			out[i] = filepath.Join(exampleDogfood, "receipts")
		default:
			if rest, ok := strings.CutPrefix(a, "./self/"); ok {
				out[i] = filepath.Join(exampleSelf, rest)
			}
		}
	}
	return out
}

func examples(t *testing.T) []string {
	t.Helper()
	// The banner is asked for, because a bare invocation no longer IS one: a refusal now
	// costs one line and names the door (`run: nova-check help`). Reading it through that
	// door is also a test that the door opens.
	exit, stdout, stderr := runCheck(t, "help")
	require.EqualValues(t, 0, exit, "`nova-check help` must print the usage and exit 0, got %d; stderr: %s", exit, stderr)
	lines, err := onboarding.ExampleLines(stdout, "nova-check")
	require.NoError(t, err, "%s\n\n%s", err, stdout)
	return lines
}

// (a) The bare command prints usage ending in an `example:` block of lines that
// actually run. They are run here against the fixture: an example that has
// drifted out of the flag set teaches the wrong invocation to exactly the
// reader who cannot tell.
func TestUsageBannerExamplesRun(t *testing.T) {
	t.Parallel()

	for _, ex := range examples(t) {
		exit, stdout, stderr := runCheck(t, localize(strings.Fields(ex)[1:])...)
		if !assert.NotEqualValues(t, 2, exit, "the usage example %q does not run: exit 2 (could not run)\nstderr: %s", ex, stderr) {
			continue
		}
		assert.EqualValues(t, 0, exit, "the usage example %q ran but said NO (exit %d); an example a stranger types should pass on the fixture\nstderr: %s", ex, exit, stderr)
		assert.NotEqualValues(t, "", stdout, "the usage example %q printed nothing on stdout", ex)
	}
}

// Every verb the banner names is one a reader may type first, so every one of
// them appears in the usage block above the examples.
func TestQuickstartIsTheFirstThingTheBannerOffers(t *testing.T) {
	t.Parallel()

	exs := examples(t)
	assert.True(t, strings.HasPrefix(exs[0], "nova-check quickstart "), "the first example is %q; a first run should be offered quickstart first", exs[0])
	assert.Contains(t, novaCheck(seams{}).Banner(), "nova-check quickstart --dir <dir>", "the usage block does not list the quickstart verb")
}

// The seed has kept SEED-CORE.md and SEED.md under docs/ since nova#141. The
// banner's floors line and kernel example used to point a first-time reader at
// the root-level paths the seed no longer keeps; both must name docs/.
func TestHelpNamesTheSeedFilesUnderDocs(t *testing.T) {
	t.Parallel()

	exit, stdout, stderr := runCheck(t, "help")
	require.EqualValues(t, 0, exit, "`nova-check help` must exit 0, got %d; stderr: %s", exit, stderr)
	for _, gone := range []string{
		"--core <SEED-CORE.md>",
		"--source <SEED.md>",
		"./self/SEED-CORE.md",
	} {
		assert.NotContains(t, stdout, gone, "the help still names %q, a root-level seed path the seed has not kept since nova#141", gone)
	}
	for _, want := range []string{
		"--core <docs/SEED-CORE.md>",
		"--source <docs/SEED.md>",
		"./self/docs/SEED-CORE.md",
	} {
		assert.Contains(t, stdout, want, "the help does not name %q, the docs/ path the seed keeps", want)
	}
}

// (b) A refusal says what the flag or input WANTS, not only what was wrong.
func TestARefusalSaysWhatTheFlagWants(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"quickstart without a directory", []string{"quickstart"}, dirHint},
		{"nocode without a directory", []string{"nocode"}, dirHint},
		{"links without a directory", []string{"links"}, dirHint},
		{"attest without a home", []string{"attest", "--manifest", "MANIFEST"}, homeHint},
		{"attest without a manifest", []string{"attest", "--home", exampleSelf}, manifestHint},
		{"kernel without a file", []string{"kernel", "--max-bytes", "4000"}, fileHint},
		{"kernel without a budget", []string{"kernel", "--file", "k.md"}, budgetHint},
		{"kernel with both budgets", []string{"kernel", "--file", "k.md", "--max-bytes", "1", "--max-tokens", "1"}, budgetHint},
		{"kernel with a token budget and no divisor", []string{"kernel", "--file", "k.md", "--max-tokens", "400"}, budgetHint},
		{"floors without a door", []string{"floors", "--source", "SEED.md"}, coreHint},
		{"floors without a source", []string{"floors", "--core", "SEED-CORE.md"}, sourceHint},
		{"corpus without a ledger", []string{"corpus", "--root", ".", "--min-anchors", "1"}, ledgerHint},
		{"corpus without a root", []string{"corpus", "--ledger", "l.md", "--min-anchors", "1"}, rootHint},
		{"corpus without a row floor", []string{"corpus", "--ledger", "l.md", "--root", "."}, anchorsHint},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exit, stdout, stderr := runCheck(t, tc.args...)
			require.EqualValues(t, 2, exit, "exit = %d, want 2 — guidance must not soften the refusal; stderr: %s", exit, stderr)
			assert.Contains(t, stderr, tc.want, "stderr = %q,\nwant it to contain the hint %q", stderr, tc.want)
			assert.EqualValues(t, "", stdout, "a refusal must print nothing on stdout, got %q", stdout)
		})
	}
}

// (b), the other half: a run reports every problem it can find in one go. These
// flags are independent of each other, so a caller who omitted two learns about
// two — being sent back a second time for something the first run could already
// see is the stumble this pins shut.
func TestIndependentProblemsAreReportedInOneRun(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"kernel with nothing at all", []string{"kernel"}, []string{"--file is required", "--max-bytes or --max-tokens is required"}},
		{"corpus with nothing at all", []string{"corpus"}, []string{"--ledger is required", "--root is required", "--min-anchors is required"}},
		{"attest with nothing at all", []string{"attest"}, []string{"--home is required", "--manifest is required"}},
		{"floors with nothing at all", []string{"floors"}, []string{"--core is required", "--source is required"}},
		{"a token budget of zero with no divisor", []string{"kernel", "--file", "k.md", "--max-tokens", "0"}, []string{"--max-tokens requires --bytes-per-token", "--max-tokens must be a positive"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exit, _, stderr := runCheck(t, tc.args...)
			require.EqualValues(t, 2, exit, "exit = %d, want 2; stderr: %s", exit, stderr)
			for _, want := range tc.want {
				assert.Contains(t, stderr, want, "one run must name every problem it can find; %q is missing from:\n%s", want, stderr)
			}
		})
	}
}

// quickstart runs BOTH checks even when the first says NO, for the same reason:
// a first run should learn everything this pair can tell it in one go.
func TestQuickstartRunsBothChecksAndTakesTheWorstExit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.md"), []byte("[gone](nowhere.md)\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "build.py"), []byte("print('machinery')\n"), 0o644))
	exit, stdout, stderr := runCheck(t, "quickstart", "--dir", dir)
	require.EqualValues(t, 1, exit, "exit = %d, want 1 (both checks ran and said NO); stderr: %s", exit, stderr)
	assert.Contains(t, stderr, "LINKS FAILED", "the broken link is not reported:\n%s", stderr)
	assert.Contains(t, stderr, "NOCODE FAILED", "nocode did not run after links failed; a first run must get both:\n%s", stderr)
	assert.Contains(t, stdout, "QUICKSTART FAILED checks=2 failed=links,nocode worst-exit=1", "the closing line must say FAILED, name both failed checks and report the worst exit:\n%s", stdout)
}

// The `### First run` block of docs/TESTS.md is EXECUTED: every documented
// command is run, in order, and its whole output is compared with the block
// written under it -- same number of lines, same lines, same order -- through
// the one comparator, onboarding.CompareTranscript (docs/SPEC-TOOLWORK.md
// documents rule 3; internal/ci/testdata/transcripts_allowlist.txt is the
// shrink-only ledger of the sections that are not yet compared this way).
//
// NOTHING IS NORMALISED. The fixture is on disk and every count on every line
// is of it, so all of them reproduce; the comparator is handed no field of the
// onboarding.Volatile table and says so under any line that disagrees. Under a
// shape comparison `LINKS OK files=4 links=3 excluded=0` and `LINKS OK files=0
// links=0 excluded=0` are the same line, so a quickstart that had stopped
// finding the fixture's files would read as green.
//
// The fixture is typed as written. The documented `./self` is what a reader
// types and what the tool PRINTS BACK on `dir=`, so the fixture is copied to
// that name in a directory of the test's own rather than the path being
// rewritten, which is what the old `localize` did and why it could not have
// compared the line the document promised.
func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	require.NoError(t, err)
	fixture, err := filepath.Abs(exampleSelf)
	require.NoError(t, err)
	lines, err := onboarding.FirstRun(string(raw), "nova-check")
	require.NoError(t, err)
	steps, err := onboarding.Steps("nova-check", lines)
	require.NoError(t, err)
	require.NotEmpty(t, steps, "the `### First run` block holds no nova-check command; this test would pass by running nothing")

	dir := t.TempDir()
	copyTree(t, fixture, filepath.Join(dir, "self"))
	got := make([]onboarding.Result, 0, len(steps))
	runner := runInDir(dir)
	for _, s := range steps {
		res, err := runner(s)
		require.NoError(t, err, "the documented command\n  %s\ncould not be run: %v", s.Line, err)
		got = append(got, res)
	}
	for _, p := range onboarding.CompareTranscript(steps, got, nil) {
		assert.Fail(t, "check failed", p.Error())
	}
}

// runInDir calls this binary's own entry point with the documented arguments,
// in a child of this test binary whose working directory is dir, so the
// documented relative paths (`./self`) resolve as written without a
// process-wide Chdir. nova-check reads nothing on stdin.
func runInDir(dir string) onboarding.Runner {
	return func(s onboarding.Step) (onboarding.Result, error) {
		if s.Stdin != "" {
			return onboarding.Result{}, errReadsNothing
		}
		cmd := exec.Command(os.Args[0], s.Args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "NOVA_CHECK_CHILD_MAIN=1")
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		err := cmd.Run()
		if err != nil && cmd.ProcessState == nil {
			return onboarding.Result{}, err
		}
		return onboarding.Result{Code: cmd.ProcessState.ExitCode(), Stdout: out.String(), Stderr: errb.String()}, nil
	}
}

// runDocumented calls this binary's own entry point with the documented
// arguments. nova-check reads nothing on stdin.
func runDocumented(s onboarding.Step) (onboarding.Result, error) {
	if s.Stdin != "" {
		return onboarding.Result{}, errReadsNothing
	}
	var out, errb bytes.Buffer
	code := run(s.Args, &out, &errb)
	return onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}, nil
}

type readsNothing struct{}

func (readsNothing) Error() string {
	return "nova-check reads no stdin; a `< path` in its transcript is the document's bug"
}

var errReadsNothing = readsNothing{}

// copyTree copies the fixture to where the transcript says it is.
func copyTree(t *testing.T, from, to string) {
	t.Helper()
	entries, err := os.ReadDir(from)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(to, 0o755))
	for _, e := range entries {
		if e.IsDir() {
			copyTree(t, filepath.Join(from, e.Name()), filepath.Join(to, e.Name()))
			continue
		}
		body, err := os.ReadFile(filepath.Join(from, e.Name()))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(to, e.Name()), body, 0o644))
	}
}

// THE OK WORD IS A CLAIM THAT EVERY CHECK PASSED (a cold rating of the tools, 2026-09-30:
// `QUICKSTART OK ... worst-exit=1` over two failed checks). With one failing check the run
// prints no OK line at all, closes with FAILED naming exactly the failed check, and exits 1.
func TestQuickstartWithOneFailingCheckPrintsFailAndNoOK(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.md"), []byte("[gone](nowhere.md)\n"), 0o644))
	exit, stdout, stderr := runCheck(t, "quickstart", "--dir", dir)
	require.EqualValues(t, 1, exit, "exit = %d, want 1; stderr: %s", exit, stderr)
	assert.NotContains(t, stdout, "QUICKSTART OK", "an OK line over a failed check:\n%s", stdout)
	assert.Contains(t, stdout, "QUICKSTART FAILED checks=2 failed=links worst-exit=1 ", "the closing line must be FAILED and name only the failed check:\n%s", stdout)
}

// With both checks clean the run closes with OK, and only then.
func TestQuickstartWithEveryCheckPassingPrintsOK(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.md"), []byte("no links here\n"), 0o644))
	exit, stdout, stderr := runCheck(t, "quickstart", "--dir", dir)
	require.EqualValues(t, 0, exit, "exit = %d, want 0; stdout: %s\nstderr: %s", exit, stdout, stderr)
	assert.True(t, strings.Contains(stdout, "QUICKSTART OK done=2 worst-exit=0 ") && !strings.Contains(stdout, "QUICKSTART FAILED"), "a clean run closes with OK:\n%s", stdout)
}
