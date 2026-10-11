package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/goenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE CONTRACT TESTS. Every one of them runs against the FAKE HARNESS binary on PATH,
// inside t.TempDir(), with no network and no provider -- so the dispatcher is tested end to
// end and nothing here costs a token or reaches a key that is worth anything.
//
// CONTRIBUTING.md: test code is code. No test here reaches outside t.TempDir(), none
// touches the network, and none matches a process by its command line.

// buildShared builds every binary and fixture the whole package shares: the two nova-swarm
// builds, the fake harness, the fake sqlite3, the real sandbox and its stand-in, and a
// PATH directory naming the stand-in `nova-sandbox`. It runs once, from TestMain, so the
// compile is charged to the package and never to whichever test happens to ask first.
func buildShared() error {
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "nova-swarm-binaries")
		if err != nil {
			buildErr = err
			return
		}
		builtDir = dir
		builtTool, buildErr = build(dir, "nova-swarm", "./cmd/nova-swarm")
		if buildErr != nil {
			return
		}
		// THE TAGGED BUILD. The same binary with -tags swarmtest, which is the only build
		// that honours the NOVA_SWARM_* injection variables. The recovery tests that plant
		// a kill or a pause run this one; every other test runs the release build above.
		if builtTaggedTool, buildErr = buildTagged(dir, "nova-swarm-swarmtest", "./cmd/nova-swarm"); buildErr != nil {
			return
		}
		harnessDir := filepath.Join(dir, "bin")
		if buildErr = os.MkdirAll(harnessDir, 0o755); buildErr != nil {
			return
		}
		builtHarness, buildErr = build(harnessDir, "fake-harness", "./cmd/nova-swarm/testdata/fakeharness")
		if buildErr != nil {
			return
		}
		// The one program the usage source runs. It is a stand-in, on the same PATH as the
		// fake harness, so the dispatcher reads a database end to end with no sqlite3 of
		// the machine's and no provider (SPEC-SWARM rule 12).
		if _, buildErr = build(harnessDir, "sqlite3", "./pkg/swarm/testdata/fakesqlite"); buildErr != nil {
			return
		}
		// THE WALL AND THE STAND-IN. nova-sandbox is the binary every job now runs inside
		// (docs/SPEC-SANDBOX.md); it is built here, from this repository, so that no test
		// depends on what is installed on the machine. The fake beside it records the argv
		// the dispatcher built and enforces nothing, which is how the seam is tested on a
		// platform whose sandbox body is not built.
		if builtSandbox, buildErr = build(harnessDir, "nova-sandbox", "./cmd/nova-sandbox"); buildErr != nil {
			return
		}
		if builtFakeSandbox, buildErr = build(harnessDir, "fake-sandbox", "./cmd/nova-swarm/testdata/fakesandbox"); buildErr != nil {
			return
		}
		// THE STAND-IN ON PATH. nativeSandboxOnPath resolves `nova-sandbox` through PATH
		// rather than a --sandbox flag, and the real sandbox of the build above already
		// owns that name in harnessDir; a link under its own name in a second directory
		// keeps both without a per-test compile.
		pathBin := filepath.Join(dir, "pathbin")
		if buildErr = os.MkdirAll(pathBin, 0o755); buildErr != nil {
			return
		}
		if buildErr = linkExecutable(builtFakeSandbox, filepath.Join(pathBin, "nova-sandbox"+exeSuffix())); buildErr != nil {
			return
		}
		builtPathBin = pathBin
		builtPath = harnessDir + string(os.PathListSeparator) + os.Getenv("PATH")
	})
	return buildErr
}

var (
	buildOnce        sync.Once
	builtDir         string
	builtTool        string
	builtTaggedTool  string
	builtPath        string
	builtPathBin     string
	builtHarness     string
	builtSandbox     string
	builtFakeSandbox string
	buildErr         error
)

// TestMain builds the one set of binaries the whole package shares before any test runs,
// and removes the one directory they live in after the last. The compile is charged here
// and never to whichever test asks first.
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

// exeSuffix is the name a Windows binary carries and a unix one does not.
func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// linkExecutable places src under name in the same tree. A hard link needs no privilege and
// leaves one inode; a symlink is the fallback where the filesystem refuses a link.
func linkExecutable(src, name string) error {
	if err := os.Link(src, name); err == nil {
		return nil
	}
	return os.Symlink(src, name)
}

func build(into, name, pkg string) (string, error) {
	return buildWith(into, name, pkg)
}

// buildTagged builds with -tags swarmtest, the build whose injection functions read the
// NOVA_SWARM_* environment variables. It is the binary the recovery tests run.
func buildTagged(into, name, pkg string) (string, error) {
	return buildWith(into, name, pkg, "swarmtest")
}

func buildWith(into, name, pkg string, tags ...string) (string, error) {
	bin := filepath.Join(into, name)
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	args := []string{"build", "-o", bin}
	if len(tags) > 0 {
		args = append(args, "-tags", strings.Join(tags, ","))
	}
	args = append(args, pkg)
	root, err := repoRootPath()
	if err != nil {
		return "", err
	}
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	cmd.Env = goenv.Clean(os.Environ())
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("building %s: %v\n%s", pkg, err, out)
	}
	return bin, nil
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
