package testbin

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// the guard on this package's own test binary, so its re-exec children below
// are counted and a chain stops (docs/TESTS.md, tests-reexec-guard-everywhere.w1)
var _ = Guard("testbin", nil)

func TestModeRunsTheSuiteHandlesItsOwnOrRefuses(t *testing.T) {
	t.Parallel()
	words := []string{"init", "--readers", "reader-a", "--redis", "mem:/tmp/x.twin"}
	own := func(a []string) bool { return a[0] == "mine" }
	cases := []struct {
		name    string
		args    []string
		env     map[string]string
		handled func([]string) bool
		mode    Start
		depth   int
		why     string
	}{
		{"go test", []string{"-test.paniconexit0", "-test.timeout=10m0s"}, nil, nil, StartSuite, 0, ""},
		{"no words", nil, nil, nil, StartSuite, 0, ""},
		{"CLI words by hand, no test binary above", words, nil, nil, StartSuite, 0, ""},
		{"a child a test asked for the suite", []string{"-test.run=^TestX$"}, map[string]string{DepthEnv: "1"}, nil, StartSuite, 1, ""},
		{"a verb the package answers itself", []string{"mine", "x"}, map[string]string{DepthEnv: "1"}, own, StartHandled, 1, ""},
		{"the recursion: CLI words, nothing answers", words, map[string]string{DepthEnv: "1"}, nil, StartRefused, 1, "the suite would run again"},
		{"the recursion: words the package does not answer", words, map[string]string{DepthEnv: "1"}, own, StartRefused, 1, "the suite would run again"},
		{"a chain too deep, suite", []string{"-test.run=^TestX$"}, map[string]string{DepthEnv: "2"}, nil, StartRefused, 2, "already stand above"},
		{"a chain too deep, handled", []string{"mine"}, map[string]string{DepthEnv: "2"}, own, StartRefused, 2, "already stand above"},
		{"a depth that is not one", words, map[string]string{DepthEnv: "x"}, nil, StartRefused, 0, "is not a depth"},
		{"a negative depth", words, map[string]string{DepthEnv: "-1"}, nil, StartRefused, 0, "is not a depth"},
	}
	for _, c := range cases {
		mode, depth, why := Mode(c.args, func(k string) string { return c.env[k] }, c.handled)
		assert.Equal(t, c.mode, mode, c.name)
		assert.Equal(t, c.depth, depth, c.name)
		if c.why == "" {
			assert.Empty(t, why, c.name)
		} else {
			assert.Contains(t, why, c.why, c.name)
		}
	}
}

// runSelf runs this test binary with words and exactly env, and returns its
// exit code and output.
func runSelf(t *testing.T, env []string, args ...string) (int, string) {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.CommandContext(t.Context(), self, args...)
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err = cmd.Run()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		require.NoError(t, err)
	}
	return cmd.ProcessState.ExitCode(), out.String()
}

// The incident's words, run the way the verb ran them: a child of a test
// binary started with CLI words is refused loudly, and never runs the suite; a
// child past the depth is refused; a suite child asked for on purpose runs.
func TestAChildStartedWithCLIWordsIsRefusedAndNeverRunsTheSuite(t *testing.T) {
	t.Parallel()
	words := []string{"init", "--readers", "reader-a", "--members", "m1:1", "--redis", "mem:/tmp/x.twin"}
	var base []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, DepthEnv+"=") {
			base = append(base, kv)
		}
	}

	code, out := runSelf(t, append(base, DepthEnv+"=1"), words...)
	assert.Equal(t, ExitRefused, code, out)
	assert.Contains(t, out, "refusing to recurse")
	assert.NotContains(t, out, "PASS")

	code, out = runSelf(t, append(base, DepthEnv+"="+strconv.Itoa(MaxDepth)), "-test.run=^TestModeRunsTheSuiteHandlesItsOwnOrRefuses$", "-test.count=1")
	assert.Equal(t, ExitRefused, code, out)
	assert.Contains(t, out, "already stand above")
	assert.NotContains(t, out, "PASS")

	code, out = runSelf(t, append(base, DepthEnv+"=1"), "-test.run=^TestModeRunsTheSuiteHandlesItsOwnOrRefuses$", "-test.count=1")
	assert.Equal(t, 0, code, out)
}
