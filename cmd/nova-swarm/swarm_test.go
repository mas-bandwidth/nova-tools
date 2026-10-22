package main

import (
	"fmt"
	"os"
	"path/filepath"

	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE CONTRACT TESTS. Every one of them runs against the FAKE HARNESS binary on PATH,
// inside t.TempDir(), with no network and no provider -- so the dispatcher is tested end to
// end and nothing here costs a token or reaches a key that is worth anything.
//
// CONTRIBUTING.md: test code is code. No test here reaches outside t.TempDir(), none
// touches the network, and none matches a process by its command line.

// builtDir is the one directory buildShared (slow_build_test.go, the slow and functional
// tiers) builds into; TestMain removes it after the last test. The unit tier builds
// nothing, so there it stays empty.
var builtDir string

// TestMain builds the one set of binaries the whole package shares before any test runs,
// and removes the one directory they live in after the last. The compile is charged here
// and never to whichever test asks first. Only the slow and functional tiers build
// (prebuild_slow_test.go); the unit tier starts no built binary (prebuild_unit_test.go).
func TestMain(m *testing.M) {
	// THE PROVIDER RETRY WAIT IS NOT UNDER TEST HERE. A launch that dies on a provider 5xx
	// is retried after 5-20s and then 30-60s (swarm.ProviderRetryDelay); a verdict test
	// that drives FAKE-5XX through all three launches sat up to 80s in those waits and
	// asserted nothing about them. The bands are pkg/swarm's TestProviderRetryDelayBands;
	// here every run pins the wait to zero through the seam the delay already reads, once,
	// for the whole process, so no test needs t.Setenv (which t.Parallel refuses) for it. A
	// test that wants a wait of its own still sets it.
	if os.Getenv("NOVA_SWARM_PROVIDER_BACKOFF") == "" {
		_ = os.Setenv("NOVA_SWARM_PROVIDER_BACKOFF", "0s")
	}
	if err := prebuild(); err != nil {
		fmt.Fprintf(os.Stderr, "building the binaries these tests run: %v\n", err)
		os.Exit(1)
	}
	// THIS BINARY IS A CI LEG, and it runs cmdNative in-process: the step behind CI
	// (nativeToCI) would put it at yield.Nice beside the very children it must beat. It is
	// a no-op here, set once before any test runs; the built binary the launch tests start
	// keeps the real step (TestACardsLaunchRunsBehindCI reads it).
	nativeToCI = func() error { return nil }
	// nor does a unit test make /tmp/.dotnet on its host (swarm.EnsureRuntimeSharedDirs)
	nativeRuntimeShared = func(string) []string { return []string{} }
	code := m.Run()
	if leftover := leftoverChildPIDs(); len(leftover) > 0 {
		for _, pid := range leftover {
			fmt.Fprintf(os.Stderr, "a child of the test binary (pid %d) was still alive at exit\n", pid)
			reapLeftoverPID(pid)
		}
		if code == 0 {
			code = 1
		}
	}
	if builtDir != "" {
		_ = os.RemoveAll(builtDir)
	}
	os.Exit(code)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := repoRootPath()
	require.NoError(t, err)
	return root
}

// repoRootPath is the repository root from the package directory every test runs in.
func repoRootPath() (string, error) {
	return filepath.Abs(filepath.Join("..", ".."))
}

func write(t *testing.T, path, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}

func mustContain(t *testing.T, what, body, want string) {
	t.Helper()
	assert.Contains(t, body, want, "%s does not contain %q:\n%s", what, want, body)
}

// ---------------------------------------------------------------------------------------
