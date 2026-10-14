// Package onboardtest is the onboarding standard's checks for one command
// (docs/STANDARD.md section 3, ONBOARDING points 1, 5 and 6) as one call, so a
// tool's own test stops carrying its copy of them: the bare command refuses in
// one line naming its door at exit 2; `help` exits 0 with its banner on stdout;
// the banner's first line names in one sentence what the tool does; every verb
// answers -h and --help with its usage on stdout at exit 0, nothing on stderr;
// and the `example:` block's lines run and answer 0 or 1. The functional walker
// (internal/ci's TestEveryCommandMeetsTheOnboardingStandard) holds every
// shipped command over the built binaries; this module serves the
// per-command copies, in process, and the walker stays as it is.
//
// The tool is handed in as a pkg/testkit.Main, the repo's existing harness
// (internal/testkit/verb is not at this card's base), and the banner is read
// through pkg/onboarding, the shared parsing. Nothing here opens a socket,
// reads the clock or starts a process: it is a unit-tier check, and testify
// holds every check (require for the spec, assert inside the rows).
package onboardtest

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/require"
)

// Spec is one command's onboarding surface: the name it is run by and the
// verbs it answers, each verb as its own words ("send", "slots take").
type Spec struct {
	Name  string
	Verbs []string
}

// leg is one named check and the problems it found: Check runs each as its
// own subtest, Hold and Problems run them all in one pass.
type leg struct {
	name     string
	problems func(m testkit.Main, spec Spec) []string
}

// legs is the onboarding standard's checks, in the order a stranger meets
// them: the refusal, the door, what the door opens with, and the first run it
// teaches.
func legs(dir string) []leg {
	return []leg{
		{"bare", bareProblems},
		{"help", helpProblems},
		{"banner", bannerProblems},
		{"verbs -h", func(m testkit.Main, spec Spec) []string { return verbProblems(m, spec, dir) }},
		{"example", exampleProblems},
	}
}

// Check runs every onboarding check for one command, each as its own t.Run
// subtest, in parallel: one broken check does not hide the rest.
//
//	onboardtest.Check(t, main, onboardtest.Spec{Name: "nova-x", Verbs: []string{"list", "show"}})
func Check(t *testing.T, m testkit.Main, spec Spec) {
	t.Helper()
	requireSpec(t, spec)
	for _, l := range legs(t.TempDir()) {
		l := l
		t.Run(l.name, func(t *testing.T) {
			t.Parallel()
			report(t, l.name, l.problems(m, spec))
		})
	}
}

// Hold runs every onboarding check for one command and hands each problem to
// t as it is found. It is Check without the subtests, for a testing.TB a test
// owns: a recording fake reads every broken check back.
func Hold(t testing.TB, m testkit.Main, spec Spec) {
	t.Helper()
	requireSpec(t, spec)
	for _, l := range legs(t.TempDir()) {
		report(t, l.name, l.problems(m, spec))
	}
}

// VerbHelp is the verb-help check alone (ONBOARDING point 1): every verb
// answers -h and --help with its usage on stdout at exit 0, nothing on stderr,
// and writes nothing under the check's temp dir.
func VerbHelp(t testing.TB, m testkit.Main, spec Spec) {
	t.Helper()
	require.NotEmpty(t, spec.Verbs, "no verbs named: a check over no verbs would pass by checking nothing")
	report(t, "verbs -h", verbProblems(m, spec, t.TempDir()))
}

// Problems runs every onboarding check and returns each way the command broke
// the standard, in the legs' order; an empty return is a pass. The verb-help
// write-nothing check is omitted (no dir supplied).
func Problems(m testkit.Main, spec Spec) []string {
	var problems []string
	for _, l := range legs("") {
		problems = append(problems, l.problems(m, spec)...)
	}
	return problems
}

// requireSpec refuses a check that would pass by checking nothing.
func requireSpec(t testing.TB, spec Spec) {
	t.Helper()
	require.NotEmpty(t, spec.Name, "no tool named: every check names the tool it runs")
	require.NotEmpty(t, spec.Verbs, "no verbs named: a check over no verbs would pass by checking nothing")
}

// report hands every problem to t as an Error, named by its check, so one
// failing check does not hide the rest.
func report(t testing.TB, check string, problems []string) {
	t.Helper()
	for _, p := range problems {
		t.Errorf("%s: %s", check, p)
	}
}

// bareProblems holds ONBOARDING point 1's first half: a command with no verb
// is not an invocation; it refuses at exit 2, on stderr, in one line (two with
// an indented hint) that names the door, `run: <tool> help`.
func bareProblems(m testkit.Main, spec Spec) []string {
	var problems []string
	fail := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }
	r := m.Run()
	if r.Code != 2 {
		fail("a bare `%s` exits %d, want 2 (could not run -- no arguments is not an invocation); stdout %q stderr %q", spec.Name, r.Code, r.Stdout, r.Stderr)
	}
	if r.Stdout != "" {
		fail("a bare `%s` wrote to stdout: %q; a refusal belongs on stderr", spec.Name, r.Stdout)
	}
	if lines := nonEmpty(strings.Split(strings.TrimSuffix(r.Stderr, "\n"), "\n")); len(lines) > 2 {
		fail("a bare `%s` printed %d lines, want 1 (or 2 with its indented hint); the banner is behind `%s help`:\n%s", spec.Name, len(lines), spec.Name, r.Stderr)
	}
	door := "run: " + spec.Name + " help"
	if !strings.Contains(r.Stderr, door) {
		fail("a bare `%s` names no door; the refusal must carry %q; stderr: %s", spec.Name, door, r.Stderr)
	}
	return problems
}

// helpProblems holds the door: `<tool> help` prints the banner on stdout and
// exits 0, nothing on stderr.
func helpProblems(m testkit.Main, spec Spec) []string {
	var problems []string
	fail := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }
	r := m.Run("help")
	if r.Code != 0 {
		fail("`%s help` exits %d, want 0; stderr: %q", spec.Name, r.Code, r.Stderr)
	}
	if r.Stderr != "" {
		fail("`%s help` wrote to stderr: %q; the banner is not a refusal", spec.Name, r.Stderr)
	}
	if r.Stdout == "" {
		fail("`%s help` printed no banner on stdout", spec.Name)
	}
	return problems
}

// bannerProblems holds ONBOARDING point 6's first question: the banner's
// first line is `<tool>: ` and one sentence saying what the tool does, the
// README's own sentence.
func bannerProblems(m testkit.Main, spec Spec) []string {
	var problems []string
	banner := m.Run("help").Stdout
	if _, err := onboarding.OpeningSentence(banner, spec.Name); err != nil {
		problems = append(problems, fmt.Sprintf("%v", err))
	}
	return problems
}

// verbProblems holds the verb-help rule for every verb (the CLI style's rule
// (b)): `<tool> <verb> -h` and `--help` print that verb's usage on stdout and
// exit 0, before anything is read, dialled or written, so nothing lands under
// dir either.
func verbProblems(m testkit.Main, spec Spec, dir string) []string {
	var problems []string
	fail := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }
	for _, verb := range spec.Verbs {
		for _, spelling := range []string{"-h", "--help"} {
			args := append(strings.Fields(verb), spelling)
			r := m.Run(args...)
			if r.Code != 0 {
				fail("`%s %s` exits %d, want 0; stderr: %q", strings.Join(args, " "), spec.Name, r.Code, r.Stderr)
			}
			if !strings.HasPrefix(r.Stdout, "usage:") {
				fail("`%s %s` prints no usage line: %q", spec.Name, verb, firstLine(r.Stdout))
			}
			if r.Stderr != "" {
				fail("`%s %s` wrote to stderr: %q; help is not a refusal", spec.Name, verb, r.Stderr)
			}
			if dir != "" {
				if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
					fail("`%s %s` left entries under the check's temp dir (err %v); help writes nothing", spec.Name, verb, err)
				}
			}
		}
	}
	return problems
}

// exampleProblems holds the banner's `example:` block (ONBOARDING points 1
// and 6): the block is present, carries no placeholder, and its lines run and
// answer 0 (done) or 1 (the verb ran and said no) -- never 2.
func exampleProblems(m testkit.Main, spec Spec) []string {
	var problems []string
	fail := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }
	banner := m.Run("help").Stdout
	lines, err := onboarding.ExampleLines(banner, spec.Name)
	if err != nil {
		fail("%v", err)
		return problems
	}
	for _, line := range lines {
		if strings.ContainsAny(line, "<>") {
			fail("the example %q still carries a placeholder; the `example:` block is for lines a stranger can paste", line)
		}
		r := m.Run(strings.Fields(line)[1:]...)
		if r.Code != 0 && r.Code != 1 {
			fail("`%s` (an example line) exits %d, want 0 or 1; exit 2 is could-not-run, a broken example; stderr: %q", line, r.Code, r.Stderr)
		}
	}
	return problems
}

// nonEmpty drops the empty lines a split leaves behind.
func nonEmpty(lines []string) []string {
	var out []string
	for _, l := range lines {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// firstLine is a stream's first line, for a problem that names what the tool
// opened with.
func firstLine(s string) string {
	return strings.SplitN(s, "\n", 2)[0]
}
