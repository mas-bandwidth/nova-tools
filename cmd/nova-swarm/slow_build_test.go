//go:build slow || functional

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
	"github.com/stretchr/testify/require"
)

// THE BUILT BINARIES ARE THE SLOW AND FUNCTIONAL TIERS' ONLY. A test that runs a built
// nova-swarm, the fake harness or the real wall is running a real program, so by the house
// rule it is a functional test (docs/TESTING.md, "The two tiers"); the unit tier compiles
// nothing, and these helpers sit behind the same tags as every caller (slow_helpers_test.go
// says why: untagged they are dead code to the unit tier and staticcheck's U1000).

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
	builtTool        string
	builtTaggedTool  string
	builtPath        string
	builtPathBin     string
	builtHarness     string
	builtSandbox     string
	builtFakeSandbox string
	buildErr         error
)

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

// nativeHarness is the fake harness binary, built once for the package.
func nativeHarness(t *testing.T) string {
	t.Helper()
	require.NoError(t, buildShared(), "building the binaries these tests run")
	return builtHarness
}

// nativeStore is the one-seat bench slot store a `native` launch now needs: since
// nova-tools#1546 a launch without a lease is REFUSED, so every test in this package that
// runs native passes `--slots-store nativeStore(t) --owner fake-1`. It is a function
// rather than a fixture so that each test gets a store of its own under its own
// t.TempDir(), and two tests running in parallel never contend for the same seat.
func nativeStore(t *testing.T) string {
	t.Helper()
	return slotShares(t, "capacity\t1\nreserve\t0\nfake-1\t1\n")
}
