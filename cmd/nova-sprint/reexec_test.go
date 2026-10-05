package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// THE HURT (2026-10-04, 3:21 to 3:37 PM ET, the Studio): a verb under test that
// runs "this binary" (selftest land with no --binary: os.Executable) ran THIS TEST
// BINARY with CLI words, `init --readers reader-a --members m1:1 --redis mem:...`.
// A Go test binary takes words it does not know as no flags at all and runs the
// whole suite again, which reached the same verb, which ran the binary again: a
// chain 289 processes deep, each the parent of the next, until it was killed.
//
// THE GUARD. A start of this test binary is one of three things, decided by
// reexecMode before any test runs:
//
//   - the suite: a `go test` run (its words are -test.* flags), or a child a test
//     started on purpose with -test.run;
//   - the CLI: a child started with CLI words by a test binary of this package,
//     which TestMain marks for its children (reexecCLIEnv) before the suite runs.
//     It runs the nova-sprint verbs, as main would, on a twin only: it sees no NOVA_
//     environment and refuses any store that is not mem:<file>, so a test can never
//     reach a live sprint (127.0.0.1:6380 or any other);
//   - a refusal, exit 3 and one loud line: CLI words from a test binary with no CLI
//     mark (the suite would run again, the recursion above), or a chain of test
//     binaries reexecMaxDepth deep (reexecDepthEnv counts every start).
const (
	reexecCLIEnv     = "NOVA_SPRINT_TEST_CLI"
	reexecDepthEnv   = "NOVA_SPRINT_TEST_DEPTH"
	reexecMaxDepth   = 2
	exitReexecRefuse = 3
)

type reexecStart int

const (
	startSuite reexecStart = iota
	startCLI
	startRefused
)

func TestMain(m *testing.M) {
	mode, depth, why := reexecMode(os.Args[1:], os.Getenv)
	// ignored, both: os.Setenv fails only on a name with '=' or NUL, and these are constants
	_ = os.Setenv(reexecDepthEnv, strconv.Itoa(depth+1))
	switch mode {
	case startRefused:
		fmt.Fprintf(os.Stderr, "nova-sprint test binary REFUSED: %s; refusing to recurse (exit %d)\n", why, exitReexecRefuse)
		os.Exit(exitReexecRefuse)
	case startCLI:
		os.Exit(runTestCLI(os.Args[1:], os.Getenv))
	}
	_ = os.Setenv(reexecCLIEnv, "1")
	os.Exit(m.Run())
}

// reexecMode is what this start of the test binary is, given its words and its
// environment: the suite, the CLI, or a refusal with its reason. depth is how
// many test binaries of this package stand above this one.
func reexecMode(args []string, getenv func(string) string) (mode reexecStart, depth int, why string) {
	if d := getenv(reexecDepthEnv); d != "" {
		n, err := strconv.Atoi(d)
		if err != nil || n < 0 {
			return startRefused, 0, fmt.Sprintf("%s=%q is not a depth", reexecDepthEnv, d)
		}
		depth = n
	}
	if depth >= reexecMaxDepth {
		return startRefused, depth, fmt.Sprintf("%d test binaries of nova-sprint already stand above this one (%s), at most %d may", depth, reexecDepthEnv, reexecMaxDepth-1)
	}
	suite := len(args) == 0 || strings.HasPrefix(args[0], "-test.")
	switch {
	case suite:
		return startSuite, depth, ""
	case getenv(reexecCLIEnv) == "1":
		return startCLI, depth, ""
	case depth > 0:
		return startRefused, depth, fmt.Sprintf("started by a test binary with the words %q and no %s mark: the suite would run again", strings.Join(args, " "), reexecCLIEnv)
	}
	return startSuite, depth, ""
}

// runTestCLI is main, as a test binary's child runs it: on a twin only.
func runTestCLI(args []string, getenv func(string) string) int {
	a := newApp(testCLIGetenv(getenv))
	open := a.backend
	a.backend = func(ctx context.Context, addr string, names sprint.Names) (store.Backend, error) {
		if err := twinOnly(addr); err != nil {
			return nil, err
		}
		return open(ctx, addr, names)
	}
	defer a.close()
	return a.run(args, os.Stdout, os.Stderr)
}

// testCLIGetenv hides every NOVA_ variable from a test binary's CLI: no store
// address, server, actor, password or prefix of the machine it runs on reaches it.
func testCLIGetenv(getenv func(string) string) func(string) string {
	return func(k string) string {
		if strings.HasPrefix(k, "NOVA_") {
			return ""
		}
		return getenv(k)
	}
}

// twinOnly refuses a store a test binary's CLI may not open: anything but a twin.
func twinOnly(addr string) error {
	if isTwin(addr) {
		return nil
	}
	return errors.New("a nova-sprint test binary runs its CLI on a twin (--redis mem:<file>) only, never on " + addr)
}

func TestReexecModeRunsTheSuiteTheCLIOrRefuses(t *testing.T) {
	t.Parallel()
	incident := []string{"init", "--readers", "reader-a", "--members", "m1:1", "--redis", "mem:/tmp/x.twin"}
	cases := []struct {
		name  string
		args  []string
		env   map[string]string
		mode  reexecStart
		depth int
		why   string
	}{
		{"go test", []string{"-test.paniconexit0", "-test.timeout=10m0s"}, nil, startSuite, 0, ""},
		{"no words", nil, nil, startSuite, 0, ""},
		{"CLI words by hand, no test binary above", incident, nil, startSuite, 0, ""},
		{"a marked child with CLI words", incident, map[string]string{reexecCLIEnv: "1", reexecDepthEnv: "1"}, startCLI, 1, ""},
		{"a marked child a test asked for the suite", []string{"-test.run=^TestX$"}, map[string]string{reexecCLIEnv: "1", reexecDepthEnv: "1"}, startSuite, 1, ""},
		{"the recursion: CLI words, no mark", incident, map[string]string{reexecDepthEnv: "1"}, startRefused, 1, "the suite would run again"},
		{"a chain too deep, CLI", incident, map[string]string{reexecCLIEnv: "1", reexecDepthEnv: "2"}, startRefused, 2, "already stand above"},
		{"a chain too deep, suite", []string{"-test.run=^TestX$"}, map[string]string{reexecDepthEnv: "2"}, startRefused, 2, "already stand above"},
		{"a depth that is not one", incident, map[string]string{reexecDepthEnv: "x"}, startRefused, 0, "is not a depth"},
	}
	for _, c := range cases {
		mode, depth, why := reexecMode(c.args, func(k string) string { return c.env[k] })
		assert.Equal(t, c.mode, mode, c.name)
		assert.Equal(t, c.depth, depth, c.name)
		if c.why == "" {
			assert.Empty(t, why, c.name)
		} else {
			assert.Contains(t, why, c.why, c.name)
		}
	}
}

func TestTheTestCLISeesNoNovaEnvironmentAndOpensATwinOnly(t *testing.T) {
	t.Parallel()
	env := map[string]string{"NOVA_SPRINT_REDIS": "127.0.0.1:6380", "NOVA_REDIS_ADDR": "127.0.0.1:6380", "NOVA_SPRINT_ACTOR": "stella", "NOVA_SPRINT_SERVER": "127.0.0.1:7000", "HOME": "/home/x"}
	get := testCLIGetenv(func(k string) string { return env[k] })
	for _, k := range []string{"NOVA_SPRINT_REDIS", "NOVA_REDIS_ADDR", "NOVA_SPRINT_ACTOR", "NOVA_SPRINT_SERVER"} {
		assert.Empty(t, get(k), k)
	}
	assert.Equal(t, "/home/x", get("HOME"))
	assert.NoError(t, twinOnly("mem:/tmp/a.twin"))
	for _, addr := range []string{"127.0.0.1:6380", "localhost:6380", "studio:6380"} {
		assert.ErrorContains(t, twinOnly(addr), "twin (--redis mem:<file>) only", addr)
	}
}

// runChild runs this test binary with words and an environment, bounded, and
// returns its exit code and its output.
func runChild(t *testing.T, env []string, args ...string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], args...)
	cmd.WaitDelay = 10 * time.Second
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		require.NoError(t, err)
	}
	return cmd.ProcessState.ExitCode(), out.String()
}

// envWithout is the environment with every name in drop removed and the extra
// pairs added.
func envWithout(env []string, drop []string, extra ...string) []string {
	var out []string
	for _, kv := range env {
		keep := true
		for _, d := range drop {
			if strings.HasPrefix(kv, d+"=") {
				keep = false
			}
		}
		if keep {
			out = append(out, kv)
		}
	}
	return append(out, extra...)
}

// The incident's own words, run by a test the way the verb ran them: the child
// is the CLI, inits the twin and exits; no test runs in it, so nothing recurses.
// Without the mark the same words are refused loudly, and so is a chain too deep.
func TestTheIncidentWordsRunTheCLIAndNeverTheSuite(t *testing.T) {
	t.Parallel()
	twin := "mem:" + filepath.Join(t.TempDir(), "sprint.twin")
	words := []string{"init", "--readers", "reader-a", "--members", "m1:1", "--redis", twin, "--actor", "boss"}
	marks := []string{reexecCLIEnv, reexecDepthEnv}
	base := envWithout(os.Environ(), marks)

	code, out := runChild(t, append(base, reexecCLIEnv+"=1", reexecDepthEnv+"=1"), words...)
	assert.Equal(t, 0, code, out)
	assert.Contains(t, out, "INIT OK")
	assert.NotContains(t, out, "=== RUN")
	assert.NotContains(t, out, "PASS")

	live := append(append([]string(nil), words[:6]...), "127.0.0.1:6380", "--actor", "stella")
	code, out = runChild(t, append(base, reexecCLIEnv+"=1", reexecDepthEnv+"=1"), live...)
	assert.NotEqual(t, 0, code, out)
	assert.Contains(t, out, "twin (--redis mem:<file>) only")

	code, out = runChild(t, append(base, reexecDepthEnv+"=1"), words...)
	assert.Equal(t, exitReexecRefuse, code, out)
	assert.Contains(t, out, "refusing to recurse")
	assert.NotContains(t, out, "PASS")

	code, out = runChild(t, append(base, reexecCLIEnv+"=1", reexecDepthEnv+"="+strconv.Itoa(reexecMaxDepth)), words...)
	assert.Equal(t, exitReexecRefuse, code, out)
	assert.Contains(t, out, "refusing to recurse")
}

// The incident itself, in-process: selftest land with no --binary runs this test
// binary (os.Executable) with its CLI words. Each of them now runs the CLI on the
// selftest's twin, so the verb ends, whatever its verdict, instead of the suite
// starting again under it: init, the incident's own words, answers as the CLI.
func TestSelftestLandWithNoBinaryRunsTheCLIAndNotTheSuite(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	_, out, errs := ta.do("selftest land --scratch-dir " + t.TempDir())
	for _, suite := range []string{"=== RUN", "--- PASS", "--- FAIL", "refusing to recurse"} {
		assert.NotContains(t, out+errs, suite)
	}
	assert.NotContains(t, errs, "command [init ", "init ran as the CLI and inited the twin")
}
