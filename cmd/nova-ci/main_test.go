package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// main_test.go is the red-test contract of the nova-ci command line: the
// onboarding door a bare run and `help` open, and the slowtests verb end to
// end through run(). Every fixture is a canned TestEvent string; nothing here
// runs `go test` or reads the clock.

func runCI(t *testing.T, args []string, stdin string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, strings.NewReader(stdin), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// A bare command refuses in one line that names the door.
func TestBareCommandNamesTheDoor(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := runCI(t, nil, "")
	assert.Equal(t, 2, code, "exit = %d, want 2", code)
	assert.Empty(t, stdout, "stdout = %q, want empty; a refusal belongs on stderr", stdout)
	assert.Equal(t, "nova-ci REFUSED: no verb given; the verbs are "+verbs+"; run: nova-ci help\n", stderr)
}

// An unknown verb, and `help` of one, is named and answered with every verb,
// so the next call is a pick, not a search.
func TestAnUnknownVerbIsNamedWithTheVerbs(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"cost"}, {"help", "cost"}} {
		code, stdout, stderr := runCI(t, args, "")
		assert.Equal(t, 2, code, "%v", args)
		assert.Empty(t, stdout, "%v", args)
		assert.Equal(t, `nova-ci REFUSED: unknown verb "cost"; the verbs are `+verbs+"; run: nova-ci help\n", stderr, "%v", args)
	}
}

// Every verb the banner and the refusals name exists: its -h answers at exit
// 0. The banner once named a `cost` verb that had been deleted.
func TestEveryNamedVerbExists(t *testing.T) {
	t.Parallel()

	for _, verb := range strings.Split(verbs, ", ") {
		code, _, stderr := runCI(t, append(strings.Fields(verb), "-h"), "")
		assert.Equal(t, 0, code, "%s -h: stderr %q", verb, stderr)
	}
	_, stdout, _ := runCI(t, []string{"help"}, "")
	for _, line := range strings.Split(stdout, "\n") {
		if f := strings.Fields(line); len(f) > 1 && f[0] == "nova-ci" {
			assert.Contains(t, verbs, f[1], "the banner's line %q names a verb the dispatch does not list", line)
		}
	}
	assert.NotContains(t, stdout, " cost", "the banner names the deleted cost verb")
}

// The usage lists each verb once, in one of two groups: the verbs that work in
// any Go module, then the ones that need this repository's checkout or a
// store. slowtests was once listed twice, its flags split between the two.
func TestTheUsageListsEachVerbOnceInItsGroup(t *testing.T) {
	t.Parallel()

	_, stdout, _ := runCI(t, []string{"help"}, "")
	usage, _, _ := strings.Cut(stdout, "\nexit codes: ")
	anyModule, checkout, found := strings.Cut(usage, "\nusage, in a nova-tools checkout")
	require.True(t, found, "the usage has no group for the verbs that need a nova-tools checkout:\n%s", usage)
	for _, c := range []struct {
		verb, group string
	}{
		{"help", anyModule}, {"version", anyModule}, {"slowtests", anyModule}, {"functional", anyModule},
		{"local", checkout}, {"new-rule", checkout}, {"new-verb", checkout}, {"github receipt", checkout},
	} {
		assert.Equal(t, 1, strings.Count(usage, "\n  nova-ci "+c.verb+" "), "%s is listed %d times in the usage", c.verb, strings.Count(usage, "\n  nova-ci "+c.verb+" "))
		assert.Contains(t, c.group, "\n  nova-ci "+c.verb+" ", "%s is in the wrong group", c.verb)
	}
}

// A misspelled flag, a bad value and a missing value are each answered in the
// house refusal, in the one wording every tool shares (verbflag.Explain):
// the flags the verb does take and the nearest of them, or what the flag
// wants, never the value given; and the verb's own help as the next command.
func TestAFlagMistakeNamesWhatTheVerbTakes(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"slowtests", "--budgt", "3"}, "nova-ci slowtests REFUSED: unknown flag --budgt; the flags of slowtests are --allowlist, --budget, --cpus, --enforce, --example, --json, --load, --package-budget, --sleeps, --test-budget; did you mean --budget?; run: nova-ci slowtests -h\n"},
		{[]string{"slowtests", "--budget", "abc"}, `nova-ci slowtests REFUSED: invalid value for --budget: it wants a whole number (whole seconds a package's tests may take before it is over budget); run: nova-ci slowtests -h` + "\n"},
		{[]string{"slowtests", "--load", "x"}, `nova-ci slowtests REFUSED: invalid value for --load: it wants a number (the host's load average, instead of reading it); run: nova-ci slowtests -h` + "\n"},
		{[]string{"slowtests", "--enforce=maybe"}, "nova-ci slowtests REFUSED: invalid value for --enforce: it wants true or false (fail the run on a CI-SLOW line (the nightly reference leg only); without it the times are printed and only a CI-SLEEPS line fails); run: nova-ci slowtests -h\n"},
		{[]string{"slowtests", "--budget"}, "nova-ci slowtests REFUSED: --budget needs a value: it wants a whole number (whole seconds a package's tests may take before it is over budget); run: nova-ci slowtests -h\n"},
		{[]string{"local", "--bse", "x"}, "nova-ci local REFUSED: unknown flag --bse; the flags of local are --base, --dry-run, --functional; did you mean --base?; run: nova-ci local -h\n"},
		{[]string{"new-rule", "--bogus", "x"}, "nova-ci new-rule REFUSED: unknown flag --bogus; the flags of new-rule are --dry-run, --root; run: nova-ci new-rule -h\n"},
		{[]string{"new-verb", "--bogus", "x"}, "nova-ci new-verb REFUSED: unknown flag --bogus; the flags of new-verb are --dry-run, --root; run: nova-ci new-verb -h\n"},
	} {
		code, stdout, stderr := runCI(t, c.args, "")
		assert.Equal(t, 2, code, "%v", c.args)
		assert.Empty(t, stdout, "%v", c.args)
		assert.Equal(t, c.want, stderr, "%v", c.args)
	}
}

// Each verb's -h quotes its own exit codes and no other verb's: one table
// quoted for every verb told slowtests' reader about a store write.
func TestEveryVerbHelpQuotesOnlyItsOwnExitCodes(t *testing.T) {
	t.Parallel()

	rows := map[string]string{
		"slowtests":      "slowtests: 0 inside budget",
		"local":          "local: 0 green",
		"functional":     "functional: 0 the selection printed",
		"new-rule":       "new-rule: 0 the files written",
		"new-verb":       "new-verb: 0 the files written",
		"github receipt": "github receipt: 0 written",
		"version":        "version: 0 printed",
	}
	for verb, own := range rows {
		code, stdout, _ := runCI(t, append(strings.Fields(verb), "-h"), "")
		require.Equal(t, 0, code, verb)
		assert.Contains(t, stdout, "exit codes: 0 done and 2 usage or could not run", verb)
		assert.Contains(t, stdout, own, verb)
		for other, row := range rows {
			if other != verb {
				assert.NotContains(t, stdout, row, "%s -h quotes %s's exit codes", verb, other)
			}
		}
	}
}

// slowtests with a terminal on stdin would wait for events that never come;
// it refuses at once and names both ways to give it events.
func TestSlowtestsRefusesATerminalOnStdin(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := run([]string{"slowtests", "--budget", "60"}, terminal{}, &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "stdin is a terminal")
	assert.Contains(t, stderr.String(), "--example")

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"slowtests", "--example", "--budget", "120", "--load", "1", "--cpus", "2"}, terminal{}, &stdout, &stderr)
	assert.Equal(t, 0, code, "--example reads the built-in stream, not the terminal: %s", stderr.String())
	assert.Equal(t, "CI-SLOW OK packages=2 slowest=github.com/mas-bandwidth/nova-tools/internal/example:65.1s\n"+loadLine1of2, stdout.String())
}

// terminal is a reader that answers as a terminal through the seam isTerminal
// reads (IsTerminal), and whose Stat says character device, as /dev/null's
// does too: the mode alone must not make it a terminal.
type terminal struct{}

func (terminal) Read([]byte) (int, error)   { return 0, io.EOF }
func (terminal) Stat() (os.FileInfo, error) { return terminalInfo{}, nil }
func (terminal) IsTerminal() bool           { return true }

type terminalInfo struct{ os.FileInfo }

func (terminalInfo) Mode() os.FileMode { return os.ModeDevice | os.ModeCharDevice }

// The null device is a character device and not a terminal, and an empty
// stream is nothing judged: slowtests refuses it at exit 2, with or without
// --enforce, in lines and in JSON, never an OK over zero packages. A stream
// holding events but no package's result (a pipe cut before any package
// finished) is refused the same way.
func TestSlowtestsRefusesAStreamWithNoPackage(t *testing.T) {
	t.Parallel()

	const why = "stdin held no package's result (events=%d): a gate over zero packages cannot decide"
	for _, tc := range []struct {
		name  string
		args  []string
		input string
	}{
		{"the null device", nil, ""},
		{"under --enforce", []string{"--enforce"}, ""},
		{"events but no package result", nil, `{"Action":"start","Package":"example.com/pkg"}` + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var in io.Reader = strings.NewReader(tc.input)
			if tc.input == "" {
				null, err := os.Open(os.DevNull)
				require.NoError(t, err)
				t.Cleanup(func() { _ = null.Close() }) // ignored: a read-only handle
				in = null
			}
			args := append([]string{"slowtests", "--budget", "60", "--load", "1", "--cpus", "2"}, tc.args...)
			events := strings.Count(tc.input, "\n")
			var stdout, stderr bytes.Buffer
			code := run(args, in, &stdout, &stderr)
			assert.Equal(t, 2, code, "stdout %q", stdout.String())
			assert.Empty(t, stdout.String())
			assert.Equal(t, "nova-ci slowtests REFUSED: "+fmt.Sprintf(why, events)+"; run: go test -json <packages> | nova-ci slowtests --budget 60\n", stderr.String())
		})
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"slowtests", "--json", "--load", "1", "--cpus", "2"}, strings.NewReader(""), &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Contains(t, stdout.String(), `"status":"refused"`)
	assert.Contains(t, stdout.String(), "no package's result (events=0)")
}

// A float flag that is not a finite number (NaN, +Inf, -Inf) is refused
// before anything is judged or rendered, naming what the flag wants, as a
// line without --json and as the JSON refusal with it.
func TestSlowtestsRefusesANonFiniteFloat(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"--load", "--package-budget", "--test-budget"} {
		for _, value := range []string{"NaN", "+Inf", "-Inf"} {
			args := []string{"slowtests", "--example", "--budget", "120", "--cpus", "2", flag, value}
			want := flag + " wants a finite number, got " + value
			code, stdout, stderr := runCI(t, args, "")
			assert.Equal(t, 2, code, "%v", args)
			assert.Empty(t, stdout, "%v", args)
			assert.Regexp(t, `^nova-ci slowtests REFUSED: .*`+regexp.QuoteMeta(want)+`.*; run: nova-ci slowtests -h\n$`, stderr, "%v", args)

			code, stdout, stderr = runCI(t, append(args, "--json"), "")
			assert.Equal(t, 2, code, "%v --json", args)
			assert.Empty(t, stderr, "%v --json", args)
			var got struct {
				Result struct {
					Verb, Status, Remedy string
					Exit                 int
					Why                  []string
				}
			}
			require.NoError(t, json.Unmarshal([]byte(stdout), &got), "%v --json printed %q", args, stdout)
			assert.Equal(t, "slowtests", got.Result.Verb)
			assert.Equal(t, "refused", got.Result.Status)
			assert.Equal(t, 2, got.Result.Exit)
			assert.Equal(t, "nova-ci slowtests -h", got.Result.Remedy)
			assert.Contains(t, got.Result.Why, want, "%v --json", args)
		}
	}
}

// --json is the same verdict as one object: the findings typed, the exit the
// lines' exit, and status failed where the run fails.
func TestSlowtestsJSONIsTheSameVerdict(t *testing.T) {
	t.Parallel()

	stdin := `{"Action":"pass","Package":"example.com/pkg","Test":"TestA","Elapsed":3.2}
{"Action":"pass","Package":"example.com/pkg","Elapsed":75.3}
`
	code, stdout, stderr := runCI(t, []string{"slowtests", "--budget", "60", "--load", "1", "--cpus", "2", "--json"}, stdin)
	require.Equal(t, 0, code, stderr)
	assert.JSONEq(t, `{"result":{"verb":"slowtests","status":"ok","exit":0},
		"facts":{"packages":1,"slowest":"example.com/pkg","slowest_seconds":75.3,"enforce":false,"load":1,"cpus":2},
		"items":[{"kind":"slow-package","fields":{"package":"example.com/pkg","seconds":75.3,"budget":60,"slowest":"TestA:3.2s"}}]}`, stdout)

	code, stdout, _ = runCI(t, []string{"slowtests", "--budget", "60", "--load", "1", "--cpus", "2", "--json", "--enforce"}, stdin)
	assert.Equal(t, 1, code)
	assert.Contains(t, stdout, `"result":{"verb":"slowtests","status":"failed","exit":1,"why":["1 CI-SLOW finding(s) under --enforce"]}`)
}

// One run names every problem with the flags and the files they name.
func TestSlowtestsRefusesEveryProblemAtOnce(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "absent.txt")
	code, stdout, stderr := runCI(t, []string{"slowtests", "--budget", "0", "--cpus", "-1", "--test-budget", "-1", "--allowlist", missing, "--sleeps", missing, "extra"}, "")
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	for _, want := range []string{`unexpected argument "extra"`, "--budget must be", "--test-budget want seconds", "--cpus must not be negative", "--allowlist open", "--sleeps open"} {
		assert.Contains(t, stderr, want)
	}
	assert.Equal(t, 1, strings.Count(stderr, "\n"), "one refusal line: %q", stderr)
}

// help opens the door on stdout at exit 0, and ends in an example block whose
// lines are commands a stranger can paste.
func TestHelpOpensTheDoor(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := runCI(t, []string{"help"}, "")
	require.Equal(t, 0, code, "exit = %d, want 0; stderr: %s", code, stderr)
	assert.Contains(t, stdout, "\nexample:\n", "help has no example: block:\n%s", stdout)
}

// slowtests under budget prints the OK line on stdout and exits 0.
func TestSlowtestsUnderBudgetIsOK(t *testing.T) {
	t.Parallel()

	stdin := `{"Action":"pass","Package":"example.com/pkg","Test":"TestA","Elapsed":3.2}
{"Action":"pass","Package":"example.com/pkg","Elapsed":3.2}
`
	code, stdout, stderr := runCI(t, []string{"slowtests", "--budget", "60", "--load", "1", "--cpus", "2"}, stdin)
	require.Equal(t, 0, code, "exit = %d, want 0; stderr: %s", code, stderr)
	want := "CI-SLOW OK packages=1 slowest=example.com/pkg:3.2s\n" + loadLine1of2
	assert.Equal(t, want, stdout, "stdout = %q, want %q", stdout, want)
}

// loadLine1of2 is the CI-LOAD line of a run handed --load 1 --cpus 2.
const loadLine1of2 = "CI-LOAD load=1.00 cpus=2 per-cpu=0.50: measured, not a verdict\n"

// slowtests over budget prints one line per offending package; it exits 0 (a
// measurement) without --enforce and 1 with it (the check ran and said no;
// 2 is a run that could not read its input).
func TestSlowtestsOverBudgetExitsOneOnlyUnderEnforce(t *testing.T) {
	t.Parallel()

	stdin := `{"Action":"pass","Package":"example.com/pkg","Test":"TestA","Elapsed":3.2}
{"Action":"pass","Package":"example.com/pkg","Elapsed":75.3}
`
	want := "CI-SLOW package=example.com/pkg seconds=75.3s budget=60s slowest=TestA:3.2s\n" + loadLine1of2
	for _, c := range []struct {
		args []string
		code int
	}{{nil, 0}, {[]string{"--enforce"}, 1}} {
		code, stdout, _ := runCI(t, append([]string{"slowtests", "--budget", "60", "--load", "1", "--cpus", "2"}, c.args...), stdin)
		assert.Equal(t, c.code, code, "%v: exit %d stdout %q, want %d and %q", c.args, code, stdout, c.code, want)
		assert.Equal(t, want, stdout, "%v: exit %d stdout %q, want %d and %q", c.args, code, stdout, c.code, want)
	}
}

// A line that is not a TestEvent is a refusal on stderr at exit 2 with no OK
// line, naming its line: text, and JSON
// that is not a TestEvent (null, a number, an array, an object with no Action),
// so neither a truncated pipe nor some other JSON reads as a clean run. The
// bookkeeping events (start, run, output, build-output) carry an Action and
// pass.
func TestSlowtestsMalformedLineRefuses(t *testing.T) {
	t.Parallel()

	const pass = `{"Action":"pass","Package":"example.com/p","Elapsed":0.1}` + "\n"
	for _, c := range []struct {
		name, stdin, line string
	}{
		{"text", "not json\n", "line 1"},
		{"null", "null\n", "line 1: not a JSON object"},
		{"a number", "42\n", "line 1: not a JSON object"},
		{"an array", "[]\n", "line 1: not a JSON object"},
		{"an empty object", "{}\n", "line 1: a JSON object with no Action"},
		{"another object", pass + `{"foo":1}` + "\n", "line 2: a JSON object with no Action"},
		{"null after an event", pass + "null\n", "line 2: not a JSON object"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			code, stdout, stderr := runCI(t, []string{"slowtests", "--budget", "60"}, c.stdin)
			assert.Equal(t, 2, code, "exit = %d, want 2", code)
			assert.Empty(t, stdout, "stdout = %q, want empty on a refusal", stdout)
			assert.Contains(t, stderr, "nova-ci slowtests REFUSED: ", "stderr = %q, want the refusal to name the verb", stderr)
			assert.Contains(t, stderr, c.line, "stderr = %q, want it to name the offending line", stderr)
			assert.Contains(t, stderr, "; run: go test -json <packages> | nova-ci slowtests --budget 60\n", "stderr = %q, want the command that makes the input", stderr)
		})
	}
	bookkeeping := `{"Action":"start","Package":"example.com/p"}
{"Action":"run","Package":"example.com/p","Test":"TestA"}
{"Action":"output","Package":"example.com/p","Test":"TestA","Output":"=== RUN TestA\n"}
{"ImportPath":"example.com/q [example.com/q.test]","Action":"build-output","Output":"# example.com/q\n"}
` + pass
	code, stdout, stderr := runCI(t, []string{"slowtests", "--budget", "60", "--load", "1", "--cpus", "2"}, bookkeeping)
	assert.Equal(t, 0, code, "the bookkeeping events were refused: %s", stderr)
	assert.Contains(t, stdout, "CI-SLOW OK packages=1")
}

// A budget of zero or less is refused rather than read as unlimited.
func TestSlowtestsRefusesANonPositiveBudget(t *testing.T) {
	t.Parallel()

	for _, budget := range []string{"0", "-1"} {
		code, _, stderr := runCI(t, []string{"slowtests", "--budget", budget}, "")
		assert.Equal(t, 2, code, "--budget %s: exit = %d, want 2", budget, code)
		assert.Contains(t, stderr, "budget", "--budget %s: stderr = %q, want it to name the budget", budget, stderr)
	}
}

// The unit tier's invocation: two seconds a package, one a test, and the
// allowlist row that raises exactly one of them.
func TestSlowtestsUnitTierBudgetsReadTheAllowlist(t *testing.T) {
	t.Parallel()

	allow := filepath.Join(t.TempDir(), "allow.txt")
	require.NoError(t, os.WriteFile(allow, []byte("pkg\tTestA\t4.5\t3s@run1\n"), 0o644))
	stdin := `{"Action":"pass","Package":"example.com/pkg","Test":"TestA","Elapsed":3.2}
{"Action":"pass","Package":"example.com/pkg","Test":"TestB","Elapsed":1.3}
{"Action":"pass","Package":"example.com/pkg","Elapsed":4.6}
`
	code, stdout, stderr := runCI(t, []string{"slowtests", "--package-budget", "2", "--test-budget", "1", "--allowlist", allow, "--enforce", "--load", "1", "--cpus", "2"}, stdin)
	require.Equal(t, 1, code, "exit = %d, want 1; stderr: %s", code, stderr)
	want := "CI-SLOW package=example.com/pkg seconds=4.6s budget=2s slowest=TestA:3.2s,TestB:1.3s\n" +
		"CI-SLOW test=TestB package=example.com/pkg seconds=1.3s budget=1s\n" + loadLine1of2
	assert.Equal(t, want, stdout, "stdout = %q, want %q", stdout, want)

	code, _, stderr = runCI(t, []string{"slowtests", "--package-budget", "2", "--allowlist", filepath.Join(t.TempDir(), "absent")}, "")
	assert.Equal(t, 2, code, "a missing allowlist: exit %d stderr %q, want a refusal naming --allowlist", code, stderr)
	assert.Contains(t, stderr, "--allowlist", "a missing allowlist: exit %d stderr %q, want a refusal naming --allowlist", code, stderr)
}

// PROBES 1, 5 and 6 of the #4413 ruling at the verb, with the load and CPUs
// given by hand. The same go test -json -- a 1.4 s test with a row measured at
// 0.4 s (budget 1.2 s, three times it), and cmd/nova-bus at 58.8 s with no row
// in the repository's own internal/ci/slow-tests_allowlist.txt -- exits 0 at
// load 2 and at load 20 on a pull request leg, printing both CI-SLOW lines and
// the CI-LOAD line, and exits 1 at both loads with --enforce (the nightly leg).
// A SLEEPS skip missing from --sleeps exits 1 on both legs at both loads.
func TestSlowtestsVerdictIsTheSameAtAnyLoadEndToEnd(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	ledger := filepath.Join(dir, "sleeps.txt")
	require.NoError(t, os.WriteFile(ledger, []byte("pkg\tTestKnown\t#4221\n"), 0o644))
	allow := filepath.Join(dir, "allow.txt")
	repoRows, err := os.ReadFile(filepath.Join("..", "..", "internal", "ci", "slow-tests_allowlist.txt"))
	require.NoError(t, err)
	require.NotContains(t, string(repoRows), "cmd/nova-bus\t", "internal/ci/slow-tests_allowlist.txt has a cmd/nova-bus row; probe 6 wants none")
	require.NoError(t, os.WriteFile(allow, append(repoRows, []byte("pkg\tTestA\t1.2\t0.4s@run36264290984\n")...), 0o644))
	slow := `{"Action":"pass","Package":"example.com/pkg","Test":"TestA","Elapsed":1.4}
{"Action":"pass","Package":"example.com/pkg","Elapsed":1.5}
{"Action":"pass","Package":"github.com/mas-bandwidth/nova-tools/cmd/nova-bus","Test":"TestWait","Elapsed":0.9}
{"Action":"pass","Package":"github.com/mas-bandwidth/nova-tools/cmd/nova-bus","Elapsed":58.8}
`
	sleeps := `{"Action":"output","Package":"example.com/pkg","Test":"TestKnown","Output":"SLEEPS: x\n"}
{"Action":"skip","Package":"example.com/pkg","Test":"TestKnown","Elapsed":0}
{"Action":"output","Package":"example.com/pkg","Test":"TestNew","Output":"SLEEPS: x\n"}
{"Action":"skip","Package":"example.com/pkg","Test":"TestNew","Elapsed":0}
{"Action":"pass","Package":"example.com/pkg","Elapsed":0.1}
`
	for _, load := range []string{"2", "20"} {
		for _, leg := range []struct {
			name string
			args []string
			code int
		}{{"pull request", nil, 0}, {"nightly", []string{"--enforce"}, 1}} {
			args := append([]string{"slowtests", "--package-budget", "2", "--test-budget", "1", "--allowlist", allow, "--sleeps", ledger, "--load", load, "--cpus", "32"}, leg.args...)
			code, stdout, stderr := runCI(t, args, slow)
			want := "CI-SLOW package=github.com/mas-bandwidth/nova-tools/cmd/nova-bus seconds=58.8s budget=2s slowest=TestWait:0.9s\n" +
				"CI-SLOW test=TestA package=example.com/pkg seconds=1.4s budget=1.2s\n" +
				"CI-LOAD load=" + load + ".00 cpus=32 per-cpu="
			assert.Equal(t, leg.code, code, "load %s, %s leg: exit %d stdout %q stderr %q, want %d and %q...", load, leg.name, code, stdout, stderr, leg.code, want)
			assert.True(t, strings.HasPrefix(stdout, want), "load %s, %s leg: exit %d stdout %q stderr %q, want %d and %q...", load, leg.name, code, stdout, stderr, leg.code, want)
			assert.True(t, strings.HasSuffix(stdout, ": measured, not a verdict\n"), "load %s, %s leg: exit %d stdout %q stderr %q, want %d and %q...", load, leg.name, code, stdout, stderr, leg.code, want)
			code, stdout, _ = runCI(t, args, sleeps)
			assert.Equal(t, 1, code, "an unledgered SLEEPS skip at load %s, %s leg: exit %d stdout %q, want 1 naming TestNew only", load, leg.name, code, stdout)
			assert.Contains(t, stdout, "CI-SLEEPS test=TestNew package=example.com/pkg", "an unledgered SLEEPS skip at load %s, %s leg: exit %d stdout %q, want 1 naming TestNew only", load, leg.name, code, stdout)
			assert.NotContains(t, stdout, "TestKnown", "an unledgered SLEEPS skip at load %s, %s leg: exit %d stdout %q, want 1 naming TestNew only", load, leg.name, code, stdout)
		}
	}
}

// PROBE 1 at the read: a host whose load cannot be read (no sysctl, no
// /proc/loadavg, a figure that does not parse) is Known=false with the reason,
// which the CI-LOAD line prints; a readable one is the larger of its 1- and
// 5-minute figures.
func TestLoadFromAFailedReadIsUnknownWithItsReason(t *testing.T) {
	t.Parallel()

	failed := loadFrom("darwin", 32, func(string) (string, error) {
		return "", errors.New(`sysctl -n vm.loadavg: exec: "sysctl": executable file not found in $PATH`)
	})
	assert.False(t, failed.Known, "a failed read = %+v, want unknown, 32 CPUs, and the reason", failed)
	assert.Equal(t, 32, failed.CPUs, "a failed read = %+v, want unknown, 32 CPUs, and the reason", failed)
	assert.Contains(t, failed.Why, "sysctl", "a failed read = %+v, want unknown, 32 CPUs, and the reason", failed)
	assert.Contains(t, failed.LoadLine(), "load=unknown", "a failed read = %+v, want unknown, 32 CPUs, and the reason", failed)
	got := loadFrom("windows", 8, readLoadAvg)
	assert.False(t, got.Known, "windows = %+v, want unknown with its reason", got)
	assert.Contains(t, got.Why, "windows has no load average", "windows = %+v, want unknown with its reason", got)
	got = loadFrom("linux", 4, func(string) (string, error) { return "garbage", nil })
	assert.False(t, got.Known, "an unparsable figure = %+v, want unknown with its reason", got)
	assert.NotEqual(t, "", got.Why, "an unparsable figure = %+v, want unknown with its reason", got)
	for raw, want := range map[string]float64{"{ 17.36 21.31 19.56 }\n": 21.31, "0.52 0.48 0.59 1/467 12345\n": 0.52} {
		got := loadFrom("x", 32, func(string) (string, error) { return raw, nil })
		assert.True(t, got.Known, "loadFrom(%q) = %+v, want %g known", raw, got, want)
		assert.Equal(t, want, got.Avg, "loadFrom(%q) = %+v, want %g known", raw, got, want)
	}
}

// functional with no package is a refusal; with packages that hold no
// functional test it prints one CI FUNCTIONAL OK packages=0 line and exits 0,
// so make runs nothing and says so.
func TestFunctionalSelectsNothingWithoutTheTag(t *testing.T) {
	t.Parallel()

	code, _, stderr := runCI(t, []string{"functional"}, "")
	assert.Equal(t, 2, code, "bare functional: exit %d stderr %q, want a refusal", code, stderr)
	assert.Contains(t, stderr, "run: nova-ci functional -h", "bare functional: exit %d stderr %q, want a refusal", code, stderr)
	dir := filepath.Join("..", "..", "internal", "ci", "slowtests")
	code, stdout, stderr := runCI(t, []string{"functional", dir}, "")
	assert.Equal(t, 0, code, "functional %s: exit %d stdout %q stderr %q, want 0 and one CI FUNCTIONAL OK line", dir, code, stdout, stderr)
	assert.Equal(t, "CI FUNCTIONAL OK packages=0 reason=no-functional-tag-in-1-dirs\n", stdout, "functional %s: exit %d stdout %q stderr %q, want 0 and one CI FUNCTIONAL OK line", dir, code, stdout, stderr)
	assert.Equal(t, "", stderr, "functional %s: exit %d stdout %q stderr %q, want 0 and one CI FUNCTIONAL OK line", dir, code, stdout, stderr)
}

// The four silent exits the cold audit found (a typo, -h, --help, an unknown
// flag) are each one refusal line at exit 2, and nothing on stdout: a typo in
// CI's package list must never skip the functional tier in silence.
func TestFunctionalRefusesWhatItCannotRun(t *testing.T) {
	t.Parallel()

	file := filepath.Join("..", "..", "internal", "ci", "functional", "functional.go")
	empty := t.TempDir()
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"missing dir", []string{"./nope"}, []string{`package pattern "./nope" matches no package (no such directory)`}},
		{"missing tree", []string{"./nope/..."}, []string{`package pattern "./nope/..." matches no package (no such directory)`}},
		{"not a dir", []string{file}, []string{"matches no package (not a directory)"}},
		{"no go files", []string{empty}, []string{"matches no package (the directory holds no .go file)"}},
		{"unknown flag", []string{"--bogus"}, []string{`unknown flag "--bogus" (functional takes no flags`}},
		// Every independent problem in the one refusal.
		{"all at once", []string{"--bogus", "./nope", ".", "./also-nope"}, []string{`unknown flag "--bogus"`, `"./nope" matches no package`, `"./also-nope" matches no package`}},
	} {
		code, stdout, stderr := runCI(t, append([]string{"functional"}, tc.args...), "")
		assert.Equal(t, 2, code, "%s: exit %d stdout %q, want 2 and nothing on stdout", tc.name, code, stdout)
		assert.Equal(t, "", stdout, "%s: exit %d stdout %q, want 2 and nothing on stdout", tc.name, code, stdout)
		assert.Equal(t, 1, strings.Count(stderr, "\n"), "%s: stderr %q, want one line `nova-ci functional REFUSED: ...; run: nova-ci functional -h`", tc.name, stderr)
		assert.True(t, strings.HasPrefix(stderr, "nova-ci functional REFUSED: "), "%s: stderr %q, want one line `nova-ci functional REFUSED: ...; run: nova-ci functional -h`", tc.name, stderr)
		assert.True(t, strings.HasSuffix(stderr, "; run: nova-ci functional -h\n"), "%s: stderr %q, want one line `nova-ci functional REFUSED: ...; run: nova-ci functional -h`", tc.name, stderr)
		for _, w := range tc.want {
			assert.Contains(t, stderr, w, "%s: stderr %q lacks %q", tc.name, stderr, w)
		}
	}
}

// Every verb's -h ends with its effect (docs/STANDARD.md section 2, the tool-answers
// dry-run rule): an inspection, or a write a reader can see before letting it.
func TestEveryVerbHelpStatesItsEffect(t *testing.T) {
	t.Parallel()

	for verb, want := range map[string]string{
		"slowtests":      "effect: inspection: reads, writes nothing\n",
		"functional":     "effect: inspection: reads, writes nothing\n",
		"version":        "effect: inspection: reads, writes nothing\n",
		"local":          "effect: local write: runs this checkout's unit tests, writing only a temp dir\n",
		"new-rule":       "effect: local write: writes files on this machine\n",
		"new-verb":       "effect: local write: writes files on this machine\n",
		"github receipt": "effect: delivery: writes one row of a CI run to a Redis store\n",
	} {
		code, stdout, _ := runCI(t, append(strings.Fields(verb), "-h"), "")
		require.Equal(t, 0, code, verb)
		assert.True(t, strings.HasSuffix(stdout, want), "%s -h does not end with %q:\n%s", verb, want, stdout)
	}
}
