package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
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
	assert.Contains(t, stderr, "run: nova-ci help", "stderr = %q, want it to name `run: nova-ci help`", stderr)
	if n := len(strings.Split(strings.TrimSuffix(stderr, "\n"), "\n")); n > 2 {
		t.Errorf("stderr printed %d lines, want at most 2:\n%s", n, stderr)
	}
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
// measurement) without --enforce and 2 with it.
func TestSlowtestsOverBudgetExitsTwoOnlyUnderEnforce(t *testing.T) {
	t.Parallel()

	stdin := `{"Action":"pass","Package":"example.com/pkg","Test":"TestA","Elapsed":3.2}
{"Action":"pass","Package":"example.com/pkg","Elapsed":75.3}
`
	want := "CI-SLOW package=example.com/pkg seconds=75.3s budget=60s slowest=TestA:3.2s\n" + loadLine1of2
	for _, c := range []struct {
		args []string
		code int
	}{{nil, 0}, {[]string{"--enforce"}, 2}} {
		code, stdout, _ := runCI(t, append([]string{"slowtests", "--budget", "60", "--load", "1", "--cpus", "2"}, c.args...), stdin)
		if code != c.code || stdout != want {
			t.Errorf("%v: exit %d stdout %q, want %d and %q", c.args, code, stdout, c.code, want)
		}
	}
}

// A malformed line is a refusal on stderr at exit 2, and prints no OK line.
func TestSlowtestsMalformedLineRefuses(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := runCI(t, []string{"slowtests", "--budget", "60"}, "not json\n")
	assert.Equal(t, 2, code, "exit = %d, want 2", code)
	assert.Empty(t, stdout, "stdout = %q, want empty on a refusal", stdout)
	assert.Contains(t, stderr, "nova-ci slowtests", "stderr = %q, want the refusal to name the verb", stderr)
	assert.Contains(t, stderr, "line 1", "stderr = %q, want it to name the offending line", stderr)
	assert.Contains(t, stderr, "run: nova-ci help", "stderr = %q, want it to name the door", stderr)
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
	require.Equal(t, 2, code, "exit = %d, want 2; stderr: %s", code, stderr)
	want := "CI-SLOW package=example.com/pkg seconds=4.6s budget=2s slowest=TestA:3.2s,TestB:1.3s\n" +
		"CI-SLOW test=TestB package=example.com/pkg seconds=1.3s budget=1s\n" + loadLine1of2
	assert.Equal(t, want, stdout, "stdout = %q, want %q", stdout, want)

	code, _, stderr = runCI(t, []string{"slowtests", "--package-budget", "2", "--allowlist", filepath.Join(t.TempDir(), "absent")}, "")
	if code != 2 || !strings.Contains(stderr, "--allowlist") {
		t.Errorf("a missing allowlist: exit %d stderr %q, want a refusal naming --allowlist", code, stderr)
	}
}

// PROBES 1, 5 and 6 of the #4413 ruling at the verb, with the load and CPUs
// given by hand. The same go test -json -- a 1.4 s test with a row measured at
// 0.4 s (budget 1.2 s, three times it), and cmd/nova-bus at 58.8 s with no row
// in the repository's own internal/ci/slow-tests_allowlist.txt -- exits 0 at
// load 2 and at load 20 on a pull request leg, printing both CI-SLOW lines and
// the CI-LOAD line, and exits 2 at both loads with --enforce (the nightly leg).
// A SLEEPS skip missing from --sleeps exits 2 on both legs at both loads.
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
		}{{"pull request", nil, 0}, {"nightly", []string{"--enforce"}, 2}} {
			args := append([]string{"slowtests", "--package-budget", "2", "--test-budget", "1", "--allowlist", allow, "--sleeps", ledger, "--load", load, "--cpus", "32"}, leg.args...)
			code, stdout, stderr := runCI(t, args, slow)
			want := "CI-SLOW package=github.com/mas-bandwidth/nova-tools/cmd/nova-bus seconds=58.8s budget=2s slowest=TestWait:0.9s\n" +
				"CI-SLOW test=TestA package=example.com/pkg seconds=1.4s budget=1.2s\n" +
				"CI-LOAD load=" + load + ".00 cpus=32 per-cpu="
			if code != leg.code || !strings.HasPrefix(stdout, want) || !strings.HasSuffix(stdout, ": measured, not a verdict\n") {
				t.Errorf("load %s, %s leg: exit %d stdout %q stderr %q, want %d and %q...", load, leg.name, code, stdout, stderr, leg.code, want)
			}
			code, stdout, _ = runCI(t, args, sleeps)
			if code != 2 || !strings.Contains(stdout, "CI-SLEEPS test=TestNew package=example.com/pkg") || strings.Contains(stdout, "TestKnown") {
				t.Errorf("an unledgered SLEEPS skip at load %s, %s leg: exit %d stdout %q, want 2 naming TestNew only", load, leg.name, code, stdout)
			}
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
	if failed.Known || failed.CPUs != 32 || !strings.Contains(failed.Why, "sysctl") || !strings.Contains(failed.LoadLine(), "load=unknown") {
		t.Errorf("a failed read = %+v, want unknown, 32 CPUs, and the reason", failed)
	}
	if got := loadFrom("windows", 8, readLoadAvg); got.Known || !strings.Contains(got.Why, "windows has no load average") {
		t.Errorf("windows = %+v, want unknown with its reason", got)
	}
	if got := loadFrom("linux", 4, func(string) (string, error) { return "garbage", nil }); got.Known || got.Why == "" {
		t.Errorf("an unparsable figure = %+v, want unknown with its reason", got)
	}
	for raw, want := range map[string]float64{"{ 17.36 21.31 19.56 }\n": 21.31, "0.52 0.48 0.59 1/467 12345\n": 0.52} {
		got := loadFrom("x", 32, func(string) (string, error) { return raw, nil })
		if !got.Known || got.Avg != want {
			t.Errorf("loadFrom(%q) = %+v, want %g known", raw, got, want)
		}
	}
}

// functional with no package is a refusal; with packages that hold no
// functional test it prints one CI FUNCTIONAL OK packages=0 line and exits 0,
// so make runs nothing and says so.
func TestFunctionalSelectsNothingWithoutTheTag(t *testing.T) {
	t.Parallel()

	code, _, stderr := runCI(t, []string{"functional"}, "")
	if code != 2 || !strings.Contains(stderr, "run: nova-ci help") {
		t.Errorf("bare functional: exit %d stderr %q, want a refusal", code, stderr)
	}
	dir := filepath.Join("..", "..", "internal", "ci", "slowtests")
	code, stdout, stderr := runCI(t, []string{"functional", dir}, "")
	if code != 0 || stdout != "CI FUNCTIONAL OK packages=0 reason=no-functional-tag-in-1-dirs\n" || stderr != "" {
		t.Errorf("functional %s: exit %d stdout %q stderr %q, want 0 and one CI FUNCTIONAL OK line", dir, code, stdout, stderr)
	}
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
		if code != 2 || stdout != "" {
			t.Errorf("%s: exit %d stdout %q, want 2 and nothing on stdout", tc.name, code, stdout)
		}
		if strings.Count(stderr, "\n") != 1 || !strings.HasPrefix(stderr, "nova-ci functional: ") || !strings.HasSuffix(stderr, "; run: nova-ci help\n") {
			t.Errorf("%s: stderr %q, want one line `nova-ci functional: ...; run: nova-ci help`", tc.name, stderr)
		}
		for _, w := range tc.want {
			if !strings.Contains(stderr, w) {
				t.Errorf("%s: stderr %q lacks %q", tc.name, stderr, w)
			}
		}
	}
}
