//go:build functional || slow || perf

// Helpers the functional, slow and perf tiers share. Every test that calls them starts
// git over a real bus checkout, so the unit tier builds none of them; the constraint is
// wider than functional because slow_test.go and timing_test.go call them too.

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

const rosterJSON = `{
  "participants": [
    {"name": "Ada", "lane": "from-ada", "aliases": ["Ada Vale", "the archivist"],
     "git_name": "Ada", "git_email": "ada@example.com"},
    {"name": "Bo", "lane": "from-bo", "aliases": ["Bo Quill"],
     "git_name": "Bo", "git_email": "bo@example.com"},
    {"name": "Dana"}
  ],
  "groups": [{"name": "Everybody on the bus", "members": ["Ada", "Bo", "Dana"]}]
}`

// TestMain sets the hermetic git environment ONCE, for the whole process, and hermetic is
// what each test says to declare that it depends on it.
//
// It used to be four t.Setenv calls inside hermetic, which is the right shape for a test
// that runs alone and the one shape a parallel test may not have: t.Setenv panics in any
// test that has called t.Parallel, because the environment is process-wide and restoring it
// per test is not a thing that can be done while another test is reading it. Every test in
// this package shells out to git, so that one call made the whole package serial -- 96
// tests, each paying a git init, a clone, a commit and a push, one after another. The
// environment set here is identical and set in the one place where nothing is running yet.
//
// What it is FOR is unchanged: git ignores the machine's own configuration, so these tests
// do not depend on the ~/.gitconfig of whoever runs them -- including a commit.gpgsign that
// would otherwise make them hang on a key. The global config is a file of our own rather
// than a missing one, and it turns off git's auto-maintenance: `git receive-pack` starts
// `git gc --auto` after every push and does not wait for it, and a git that detaches that
// child before deciding whether there is work to do leaves it running in a fixture the test
// is about to remove. See the same comment on internal/bus's, where the race was found.
//
// The directory holding that config is the ONE thing in this package outside t.TempDir, and
// it has to be: it must exist before the first test starts and outlive the last one. It is
// created under the same TMPDIR t.TempDir uses, it holds one file this process wrote, and
// it is removed before the process exits on every path including a failing run.
func TestMain(m *testing.M) {
	os.Exit(func() int {
		dir, err := os.MkdirTemp("", "nova-bus-test-gitconfig-")
		if err != nil {
			fmt.Fprintf(os.Stderr, "hermetic git config: %v\n", err)
			return 2
		}
		defer os.RemoveAll(dir)
		cfg := filepath.Join(dir, "gitconfig")
		if err := os.WriteFile(cfg, []byte(noMaintenanceConfig), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "hermetic git config: %v\n", err)
			return 2
		}
		os.Setenv("GIT_CONFIG_GLOBAL", cfg)
		os.Setenv("GIT_CONFIG_SYSTEM", filepath.Join(dir, "no-such-gitconfig"))
		os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
		os.Setenv("GIT_TERMINAL_PROMPT", "0")
		// The bus fixture busDir copies is built under this directory too, so it is
		// removed with it on every path out of this function.
		busFixtureRoot = dir
		return m.Run()
	}())
}

// noMaintenanceConfig is the whole of that global config: no auto-gc anywhere, and if some
// git runs one anyway it runs in the foreground, where the call that started it waits for
// it and nothing outlives the test.
const noMaintenanceConfig = "[gc]\n\tauto = 0\n\tautoDetach = false\n" +
	"[maintenance]\n\tauto = false\n" +
	"[receive]\n\tautoGc = false\n" +
	// Durability is not a property of a fixture that lives inside t.TempDir and is
	// deleted at the end of the test. core.fsync=none stops git calling fsync once per
	// loose object, and compression=0 stops it deflating notes that are a few hundred
	// bytes each -- which between them is most of the cost of `git add` plus `git
	// commit` plus `git push` over the ten-thousand-note fixture, on every platform and
	// most of all on the one where a file operation is expensive. A git too old to know
	// either key ignores it; nothing here depends on the setting taking.
	"[core]\n\tfsync = none\n\tcompression = 0\n" +
	"[pack]\n\tcompression = 0\n"

// WHICH TESTS IN THIS PACKAGE ARE SERIAL, and why the rest are not.
//
// A top-level test that does not call t.Parallel is WALL TIME, alone, one after another --
// and on a two-core hosted runner that is most of what the package costs, because the
// parallel ones are only ever two at a time while a serial one is one at a time. Measured
// 2026-09-15: cmd/nova-bus took 567 s on windows-latest against the house rule of a package
// under a minute, and some two hundred seconds of it was tests that were serial for no
// reason at all -- bodies, continuation and the read half, each of which builds its own bus
// under its own t.TempDir and shares nothing. Those now say t.Parallel.
//
// What stays serial, and must: a test that writes PROCESS-WIDE state. That is the whole
// list, and every one of them is serial for a named reason --
//
//	refreshCheckout,        package variables taken out at the seam and put back
//	publishDraft,           (withoutFetch, and the two tests that stand in for a
//	checkoutLockWait,       filesystem, a held lock and a stamp)
//	version
//	t.Setenv, t.Chdir       process-wide by construction, and testing panics if a test
//	                        that has called t.Parallel calls either
//
// The rule for a new test here: it may be parallel unless it writes one of those.
//
// hermetic is the call each git-running test keeps, and it asserts what TestMain set rather
// than setting it. Kept as a call rather than deleted so that the dependency stays written
// at every site that has it, and so that a future TestMain that stopped doing this would
// fail loudly here instead of silently reading the runner's ~/.gitconfig.
func hermetic(t *testing.T) {
	t.Helper()
	// All four, not just the first: three of them are what keeps a machine's system
	// config, its ~/.gitconfig and its credential prompt out of these tests, and an
	// assertion on one of four would pass over a TestMain that set one of four.
	for _, key := range []string{"GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM", "GIT_CONFIG_NOSYSTEM", "GIT_TERMINAL_PROMPT"} {
		require.NotEmptyf(t, os.Getenv(key), "%s is not set: the hermetic git environment is TestMain's, in this package, and it sets four", key)
	}
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %s: %v\n%s", strings.Join(args, " "), err, out)
	return string(out)
}

func writeFile(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
}

// busDir hands a test its own bare remote and checkout of it, with a roster and two
// notes from Bo already on the bus: one carrying a question, one a bare
// acknowledgement.
//
// The fixture is BUILT ONCE for the process and COPIED per test. It used to be built
// per test, and building it is six git subprocesses -- init, clone, checkout, add,
// commit, push -- which 135 calls in this package turned into some eight hundred git
// spawns and 124 s of `go test ./cmd/nova-bus/` (#516, Glenn's two-minute rule). A
// copy plus one `remote set-url` is far cheaper and hands out the same bytes: same
// roster, same two notes, same INDEX, same commit. Each test still gets its OWN
// directories and may push, rewrite and corrupt them freely -- nothing is shared
// after the copy.
func busDir(t *testing.T) (checkout, bare string) {
	t.Helper()
	busFixtureOnce.Do(func() { busFixtureDir, busFixtureErr = buildBusFixture() })
	require.NoErrorf(t, busFixtureErr, "the bus fixture: %v", busFixtureErr)
	root := t.TempDir()
	{
		err := os.CopyFS(root, os.DirFS(busFixtureDir))
		require.NoErrorf(t, err, "copying the bus fixture: %v", err)
	}
	bare = filepath.Join(root, "bus.git")
	checkout = filepath.Join(root, "checkout")
	// The copied checkout still names the TEMPLATE's bare remote; origin has to be
	// this test's own copy or a push would reach the fixture every other test reads.
	gitIn(t, checkout, "remote", "set-url", "origin", bare)
	return checkout, bare
}

// The process-wide fixture busDir copies. busFixtureDir holds `bus.git` and
// `checkout`; it is made under TestMain's directory and removed with it.
var (
	busFixtureOnce sync.Once
	busFixtureDir  string
	busFixtureErr  error
	busFixtureRoot string // set by TestMain, the parent the fixture is built under
)

// buildBusFixture builds the template once, with the same git calls busDir used to
// make per test. It takes no *testing.T: it runs under sync.Once, where the caller
// that loses the race is not the test whose failure it would be.
func buildBusFixture() (string, error) {
	dir, err := os.MkdirTemp(busFixtureRoot, "bus-fixture-")
	if err != nil {
		return "", err
	}
	var fail error
	git := func(at string, args ...string) {
		if fail != nil {
			return
		}
		out, err := exec.Command("git", append([]string{"-C", at}, args...)...).CombinedOutput()
		if err != nil {
			fail = fmt.Errorf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	write := func(path, content string) {
		if fail != nil {
			return
		}
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			fail = err
			return
		}
		fail = os.WriteFile(full, []byte(content), 0o644)
	}
	// --template= (empty) on both the init and the clone: git otherwise copies its
	// sample hooks into every .git it makes, twenty-eight files nothing reads, and busDir
	// copies this template once per test -- thirty-six files of which those were the
	// twenty-eight, on every test, and on the platform where a file operation is expensive.
	bare := filepath.Join(dir, "bus.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		return "", err
	}
	git(bare, "init", "--bare", "--quiet", "--template=", "--initial-branch=main")
	checkout := filepath.Join(dir, "checkout")
	git(dir, "clone", "--quiet", "--template=", bare, checkout)
	git(checkout, "checkout", "-q", "-B", "main")
	write("checkout/participants.json", rosterJSON)
	write("checkout/from-bo/2026-09-07T0001Z-a-question-abcdef012345.md",
		"From: Bo Quill\nTo: Ada\nDate: Mon Sep  7 00:01:00 UTC 2026\nId: bo-abcdef012345\nSubject: A question about the gate\n\nShould the gate run on the merge queue too?\n")
	write("checkout/from-bo/2026-09-07T0002Z-heard-111111111111.md",
		"From: Bo\nTo: Ada\nDate: Mon Sep  7 00:02:00 UTC 2026\nId: bo-111111111111\nSubject: Heard\n\nHeard, thank you.\n")
	write("checkout/from-bo/INDEX", strings.Join([]string{
		"bo-abcdef012345\tfrom-bo/2026-09-07T0001Z-a-question-abcdef012345.md\t2026-09-07T00:01:00Z\tAda\t-",
		"bo-111111111111\tfrom-bo/2026-09-07T0002Z-heard-111111111111.md\t2026-09-07T00:02:00Z\tAda\t-",
	}, "\n")+"\n")
	git(checkout, "add", "-A")
	git(checkout, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "the bus")
	git(checkout, "push", "-q", "origin", "HEAD:refs/heads/main")
	if fail != nil {
		return "", fail
	}
	return dir, nil
}
