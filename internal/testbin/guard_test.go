package testbin

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// guardChildEnv makes this test binary run Enter as a tool's test binary and
// print what it decided: the guard proved on a real re-exec, in a child.
const guardChildEnv = "TESTBIN_GUARD_CHILD"

func init() {
	if os.Getenv(guardChildEnv) == "1" {
		start := Enter("nova-guardtool", func(args []string, getenv func(string) string) bool {
			return getenv("TESTBIN_GUARD_HANDLED") == "1"
		})
		if start == Handled {
			_, _ = os.Stdout.WriteString("HANDLED\n") // ignored: the parent reads this word, so a lost write fails there
		} else {
			_, _ = os.Stdout.WriteString("SUITE\n") // ignored: as above
		}
		os.Exit(0)
	}
}

// TestDecideRunsTheSuiteHandlesItsOwnWordsOrRefuses is the decision table of the
// guard (docs/TESTS.md, "tests-reexec-guard-everywhere").
func TestDecideRunsTheSuiteHandlesItsOwnWordsOrRefuses(t *testing.T) {
	t.Parallel()
	const tool = "nova-x"
	depthEnv := DepthEnv(tool)
	ours := func(args []string, getenv func(string) string) bool { return getenv("MARK") == "1" }
	words := []string{"init", "--redis", "mem:/tmp/x"}
	cases := []struct {
		name  string
		args  []string
		env   map[string]string
		start Start
		depth int
		why   string
	}{
		{"go test", []string{"-test.paniconexit0", "-test.timeout=10m0s"}, nil, Suite, 0, ""},
		{"no words", nil, nil, Suite, 0, ""},
		{"CLI words by hand, no test binary above", words, nil, Suite, 0, ""},
		{"a child the package answers", words, map[string]string{"MARK": "1", depthEnv: "1"}, Handled, 1, ""},
		{"a child asked for the suite", []string{"-test.run=^TestX$"}, map[string]string{"MARK": "1", depthEnv: "1"}, Suite, 1, ""},
		{"the recursion: CLI words nothing answers", words, map[string]string{depthEnv: "1"}, Refused, 1, "the suite would run again"},
		{"a chain too deep, answered words", words, map[string]string{"MARK": "1", depthEnv: "2"}, Refused, 2, "already stand above"},
		{"a chain too deep, suite", []string{"-test.run=^TestX$"}, map[string]string{depthEnv: "2"}, Refused, 2, "already stand above"},
		{"a depth that is not one", words, map[string]string{depthEnv: "x"}, Refused, 0, "is not a depth"},
		{"a depth below zero", words, map[string]string{depthEnv: "-1"}, Refused, 0, "is not a depth"},
	}
	for _, c := range cases {
		start, depth, why := Decide(tool, c.args, func(k string) string { return c.env[k] }, ours)
		assert.Equal(t, c.start, start, c.name)
		assert.Equal(t, c.depth, depth, c.name)
		if c.why == "" {
			assert.Empty(t, why, c.name)
		} else {
			assert.Contains(t, why, c.why, c.name)
		}
	}
	start, _, _ := Decide(tool, words, func(string) string { return "" }, nil)
	assert.Equal(t, Suite, start, "no handler, no test binary above: the suite")
}

// TestDepthEnvIsPerToolAndShellSafe: the name is one tool's own, so a chain of
// one tool's test binaries is never counted against another's.
func TestDepthEnvIsPerToolAndShellSafe(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "NOVA_TEST_DEPTH_NOVA_SELF_TALK", DepthEnv("nova-self-talk"))
	assert.NotEqual(t, DepthEnv("nova-sprint"), DepthEnv("nova-swarm"))
}

// TestEnterRefusesARecursionAndAChainTooDeepInAChild runs Enter in a real child
// of this test binary: CLI words below a test binary that nothing answers are
// refused at exit 3 and never reach a suite; a handled start is answered; a
// chain MaxDepth deep is refused.
func TestEnterRefusesARecursionAndAChainTooDeepInAChild(t *testing.T) {
	t.Parallel()
	run := func(env []string, args ...string) (int, string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), os.Args[0], args...)
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.CombinedOutput()
		if err == nil {
			return 0, string(out)
		}
		exit, ok := err.(*exec.ExitError)
		require.True(t, ok, "%v", err)
		return exit.ExitCode(), string(out)
	}
	depth := DepthEnv("nova-guardtool")
	base := []string{guardChildEnv + "=1"}

	code, out := run(append(base, depth+"=1"), "init", "--readers", "a")
	assert.Equal(t, ExitRefuse, code, out)
	assert.Contains(t, out, "refusing to recurse")
	assert.NotContains(t, out, "PASS")

	code, out = run(append(base, depth+"=1", "TESTBIN_GUARD_HANDLED=1"), "init", "--readers", "a")
	assert.Equal(t, 0, code, out)
	assert.Contains(t, out, "HANDLED")

	code, out = run(append(base, depth+"=2", "TESTBIN_GUARD_HANDLED=1"), "init", "--readers", "a")
	assert.Equal(t, ExitRefuse, code, out)
	assert.Contains(t, out, "already stand above")

	code, out = run(base, "-test.run=^TestNothing$")
	assert.Equal(t, 0, code, out)
	assert.Contains(t, out, "SUITE")
}
