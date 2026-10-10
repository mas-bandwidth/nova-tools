package main

import (
	"bytes"
	"context"
	"errors"
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
	"github.com/mas-bandwidth/nova-tools/pkg/testbin"
)

// THE HURT (2026-10-04, 3:21 to 3:37 PM ET, the Studio): a verb under test that
// runs "this binary" (selftest land with no --binary: os.Executable) ran THIS TEST
// BINARY with CLI words, `init --readers reader-a --members m1:1 --redis mem:...`.
// A Go test binary takes words it does not know as no flags at all and runs the
// whole suite again, which reached the same verb, which ran the binary again: a
// chain 289 processes deep, each the parent of the next, until it was killed.
//
// THE GUARD is internal/testbin.Enter (docs/TESTS.md, "tests-reexec-guard-everywhere"),
// which decides every start of this test binary before any test runs:
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
//     binaries testbin.MaxDepth deep (testbin.DepthEnv counts every start).
const (
	reexecCLIEnv     = "NOVA_SPRINT_TEST_CLI"
	reexecTool       = "nova-sprint"
	exitReexecRefuse = testbin.ExitRefuse
)

func TestMain(m *testing.M) {
	// a test arms the push proof for the names it registers (pushproof.go)
	pushArmedDefault = false
	start := testbin.Enter(reexecTool, func(_ []string, getenv func(string) string) bool {
		return getenv(reexecCLIEnv) == "1"
	})
	if start == testbin.Handled {
		os.Exit(runTestCLI(os.Args[1:], os.Getenv))
	}
	// ignored: os.Setenv fails only on a name with '=' or NUL, and this is a constant
	_ = os.Setenv(reexecCLIEnv, "1")
	os.Exit(m.Run())
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
	depthEnv := testbin.DepthEnv(reexecTool)
	marks := []string{reexecCLIEnv, depthEnv}
	base := envWithout(os.Environ(), marks)

	code, out := runChild(t, append(base, reexecCLIEnv+"=1", depthEnv+"=1"), words...)
	assert.Equal(t, 0, code, out)
	assert.Contains(t, out, "INIT OK")
	assert.NotContains(t, out, "=== RUN")
	assert.NotContains(t, out, "PASS")

	live := append(append([]string(nil), words[:6]...), "127.0.0.1:6380", "--actor", "stella")
	code, out = runChild(t, append(base, reexecCLIEnv+"=1", depthEnv+"=1"), live...)
	assert.NotEqual(t, 0, code, out)
	assert.Contains(t, out, "twin (--redis mem:<file>) only")

	code, out = runChild(t, append(base, depthEnv+"=1"), words...)
	assert.Equal(t, exitReexecRefuse, code, out)
	assert.Contains(t, out, "refusing to recurse")
	assert.NotContains(t, out, "PASS")

	code, out = runChild(t, append(base, reexecCLIEnv+"=1", depthEnv+"="+strconv.Itoa(testbin.MaxDepth)), words...)
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
