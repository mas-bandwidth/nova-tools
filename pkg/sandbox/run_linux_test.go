//go:build linux

package sandbox

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The refusals Run answers before the wall goes up, each at ExitRefused with its reason
// and with the command never started. no_sandbox and landlock_abi_unknown have their tests
// in wrap_linux_test.go; net_unenforceable and the root refusal live here because they
// depend on a policy field (NetDeny) and on the process identity respectively.

// Rule 7's linux half: --net-deny below ABI 4 is refused, and the command does not run,
// because Landlock's TCP bind/connect arrived at ABI 4.
func TestNetDenyBelowABI4RefusesOnLinux(t *testing.T) {
	t.Parallel()

	write := t.TempDir()
	home := filepath.Join(write, "home")
	require.NoError(t, os.MkdirAll(home, 0o755))
	p, bad := Build(in(t, write, t.TempDir(), home, "/bin/echo", "tripwire"))
	require.Empty(t, bad, "refused at build: %v", bad)
	p.NetDeny = true
	p.LandlockABI = func() (int, bool) { return 3, true }
	var out bytes.Buffer
	code, err := Run(p, os.Environ(), strings.NewReader(""), &out, &out, nil)
	assert.Equal(t, ExitRefused, code)
	r, ok := err.(Refusal)
	require.True(t, ok, "err = %v, want a Refusal", err)
	assert.Equal(t, "net_unenforceable", r.Reason)
	assert.Empty(t, out.String(), "the command produced output; it must not have run")
}

// Rule 2: a root process is refused (the wall is for an ordinary unprivileged user). The
// test is skipped unless the test binary itself runs as root, so that the case is asserted
// on a machine where it can be.
func TestRunRefusesRoot(t *testing.T) {
	t.Parallel()

	if os.Geteuid() != 0 {
		t.Skip("not running as root; the root refusal cannot be asserted here")
	}
	write := t.TempDir()
	home := filepath.Join(write, "home")
	require.NoError(t, os.MkdirAll(home, 0o755))
	p, bad := Build(in(t, write, t.TempDir(), home, "/bin/echo", "hi"))
	require.Empty(t, bad, "refused at build: %v", bad)
	var out bytes.Buffer
	code, err := Run(p, os.Environ(), strings.NewReader(""), &out, &out, nil)
	assert.Equal(t, ExitRefused, code)
	r, ok := err.(Refusal)
	require.True(t, ok, "err = %v, want a Refusal", err)
	assert.Equal(t, "sandbox_failed", r.Reason)
	assert.Empty(t, out.String(), "the command produced output; it must not have run")
}

// Every pure ABI decision is pinned for every row of the spec's table and one row above
// it (a kernel that reports more than the table clamps to the top). These are the
// functions the wall is built from, and each is a function of one number so it can be
// tested without applying a domain that cannot be lifted.

func TestHandledFSForEveryABIRowAndOneAbove(t *testing.T) {
	t.Parallel()

	rows := []struct {
		abi  int
		want uint64
	}{
		{1, fsABI1},
		{2, fsABI1 | fsRefer},
		{3, fsABI1 | fsRefer | fsTruncate},
		{4, fsABI1 | fsRefer | fsTruncate},
		{5, fsABI1 | fsRefer | fsTruncate | fsIoctlDev},
		{6, fsABI1 | fsRefer | fsTruncate | fsIoctlDev},
		{7, fsABI1 | fsRefer | fsTruncate | fsIoctlDev},
	}
	for _, r := range rows {
		assert.Equal(t, r.want, handledFS(r.abi), "handledFS(%d)", r.abi)
	}
}

func TestFileWriteSubsetForEveryABIRowAndOneAbove(t *testing.T) {
	t.Parallel()

	rows := []struct {
		abi  int
		want uint64
	}{
		{1, fsExecute | fsReadFile | fsWriteFile},
		{2, fsExecute | fsReadFile | fsWriteFile},
		{3, fsExecute | fsReadFile | fsWriteFile | fsTruncate},
		{4, fsExecute | fsReadFile | fsWriteFile | fsTruncate},
		{5, fsExecute | fsReadFile | fsWriteFile | fsTruncate | fsIoctlDev},
		{6, fsExecute | fsReadFile | fsWriteFile | fsTruncate | fsIoctlDev},
		{7, fsExecute | fsReadFile | fsWriteFile | fsTruncate | fsIoctlDev},
	}
	for _, r := range rows {
		assert.Equal(t, r.want, fileWriteSubset(r.abi), "fileWriteSubset(%d)", r.abi)
	}
	// A file rule never carries a directory-only right: a rule on a non-directory with one
	// is EINVAL and adds nothing (fsFileSubset's reason for existing).
	dirOnly := uint64(fsReadDir | fsRemoveDir | fsRemoveFile | fsMakeDir | fsMakeReg | fsMakeSym)
	for abi := minKnownABI; abi <= maxKnownABI+1; abi++ {
		assert.Zero(t, fileWriteSubset(abi)&dirOnly, "fileWriteSubset(%d) carries a directory-only right", abi)
	}
}

func TestNetHandledForEveryABIRowAndOneAbove(t *testing.T) {
	t.Parallel()

	for abi := minKnownABI; abi <= maxKnownABI+1; abi++ {
		if abi < 4 {
			assert.Zero(t, netHandled(abi, true), "netHandled(%d, true) is not zero below ABI 4", abi)
		} else {
			assert.Equal(t, uint64(netBindTCP|netConnectTCP), netHandled(abi, true), "netHandled(%d, true)", abi)
		}
		// Without --net-deny nothing is handled: Landlock denies by handling a right and
		// adding no rule for it, so handling nothing is leaving the network alone.
		assert.Zero(t, netHandled(abi, false), "netHandled(%d, false) is not zero", abi)
	}
}

func TestScopedForEveryABIRowAndOneAbove(t *testing.T) {
	t.Parallel()

	for abi := minKnownABI; abi <= maxKnownABI+1; abi++ {
		if abi < 6 {
			assert.Zero(t, scopedFor(abi), "scopedFor(%d) is not zero below ABI 6", abi)
		} else {
			assert.Equal(t, uint64(scopeAbstractUnixSocket|scopeSignal), scopedFor(abi), "scopedFor(%d)", abi)
		}
	}
}

func TestRulesetAttrSizeForEveryABIRowAndOneAbove(t *testing.T) {
	t.Parallel()

	for abi := minKnownABI; abi <= maxKnownABI+1; abi++ {
		var want uintptr
		switch {
		case abi >= 6:
			want = 24
		case abi >= 4:
			want = 16
		default:
			want = 8
		}
		assert.Equal(t, want, rulesetAttrSize(abi), "rulesetAttrSize(%d)", abi)
	}
}

// linuxRootsAt grants the directory the system resolver config resolves to, when that
// directory is outside the static table. An absent resolver config adds nothing: a machine
// with no resolver config is its own shape, not a caller's mistake. (The symlinked half is
// TestLinuxWallGrantsTheResolvedResolverConfigDirectory in systemreads_test.go.)
func TestLinuxRootsAtAddsNothingWhenResolverConfigIsAbsent(t *testing.T) {
	t.Parallel()

	roots := linuxRootsAt(filepath.Join(t.TempDir(), "does-not-exist"))
	assert.Equal(t, linuxReadRoots, roots, "an absent resolver config adds nothing")
}

// writePaths is every directory the wall grants writing in: the --write set, plus the
// temp directory and the cwd when they are a directory of their own. These are the pure
// string-list decisions the card names beside writePaths, so they pin it without applying
// a domain that cannot be lifted.
func TestWritePathsAddsTheTmpAndTheCwdWhenTheyAreTheirOwnDirectory(t *testing.T) {
	t.Parallel()

	p := &Policy{Writes: []string{"/w", "/home"}, Tmp: "/w/tmp", Cwd: "/w/work"}
	assert.Equal(t, []string{"/w", "/home", "/w/tmp", "/w/work"}, writePaths(p))
}

func TestWritePathsDropsATmpOrCwdAlreadyInTheWriteSet(t *testing.T) {
	t.Parallel()

	p := &Policy{Writes: []string{"/w"}, Tmp: "/w", Cwd: ""}
	assert.Equal(t, []string{"/w"}, writePaths(p))
}

// execRoots is every root the wall grants EXECUTE on, in the order addRules applies them:
// the static roots first, then the optional roots, every --read, then the write set (with
// the tmp and the cwd). A --read-noexec path beneath any of them is executable in spite
// of its own rule, which is exactly why addRules refuses it; this pins the set and the
// order that refusal reads.
func TestExecRootsListsEveryExecuteRootInOrder(t *testing.T) {
	t.Parallel()

	p := &Policy{
		OptRoots: []string{"/opt/bin"},
		Reads:    []string{"/r"},
		Writes:   []string{"/w"},
		Tmp:      "/w/tmp",
		Cwd:      "/w/work",
	}
	got := execRoots(p)
	for _, want := range []string{"/opt/bin", "/r", "/w", "/w/tmp", "/w/work"} {
		assert.Contains(t, got, want, "the execute roots %v do not list %s", got, want)
	}
	// The caller's roots keep their order relative to one another: optional roots, then
	// reads, then the write set with the tmp and the cwd after it.
	last := -1
	for _, want := range []string{"/opt/bin", "/r", "/w", "/w/tmp", "/w/work"} {
		i := slices.Index(got, want)
		if i < 0 {
			continue
		}
		assert.Greater(t, i, last, "%s is out of order in the execute roots %v", want, got)
		last = i
	}
}

// RunawayLine is the end of the SANDBOX RUNAWAY line for what KillAndAwait answered:
// "killed" only when a count found nothing of the tree left.
func TestRunawayLine(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "the process tree was killed", RunawayLine(0))
	assert.Equal(t, "the process tree was sent SIGKILL and could not be counted after", RunawayLine(-1))
	assert.Equal(t, "the process tree was sent SIGKILL and 3 of its processes are still running", RunawayLine(3))
}

// The tree counts and kills the process tree the test actually starts: a sleep child, its
// pid the top, killed only by the PID the test started. This runs on a real /proc.
func TestTreeUsageAndKillAndAwaitOverARealProcessTree(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("sleep", "60")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		if cmd.Process != nil {
			// ignored: the process may already be gone; the cleanup only makes sure nothing of the tree survives
			_ = cmd.Process.Kill()
		}
		// ignored: the process was killed above; the wait's error is the kill's, and the tree is over either way
		_ = cmd.Wait()
	})
	self := os.Getpid()
	tr := &Tree{
		top:     cmd.Process.Pid,
		keepTop: true,
		pgid:    mustPgid(t, cmd.Process.Pid),
		list: func() ([]procRow, error) {
			return procRows(self, cmd.Process.Pid)
		},
	}

	u, err := tr.Usage()
	require.NoError(t, err)
	assert.Equal(t, 1, u.Procs, "the sleep is the one member")

	left := tr.KillAndAwait()
	assert.Equal(t, 0, left, "the tree the test started was killed; %d still running", left)
}

func mustPgid(t *testing.T, pid int) int {
	t.Helper()
	pgid, err := syscall.Getpgid(pid)
	require.NoError(t, err)
	return pgid
}

// The wall itself: Run applies a Landlock domain to the calling process that cannot be
// lifted (wrap_linux.go:202), so every case that gets past the refusals runs in a
// re-executed copy of the test binary (os.Args[0] with -test.run naming this test and an
// env marker), never in the test process. The parent asserts on the child's exit code and
// output. Every parent branch skips when the running kernel has no landlock, or when it is
// root, so this file never fails on a machine where the wall cannot be built.
const wallChildEnv = "NOVA_TEST_SANDBOX_WALL_CHILD"
const wallWorkEnv = "NOVA_TEST_SANDBOX_WALL_WORK"
const wallPortEnv = "NOVA_TEST_SANDBOX_WALL_PORT"

// wallPath is one of the standard directories under the shared base the parent hands the
// child: w and home are the write set, r is read-only, n is read-noexec, x is outside.
func wallPath(name string) string { return filepath.Join(os.Getenv(wallWorkEnv), name) }

// standardInput is the common policy every wall case builds on.
func standardInput(argv []string) Input {
	return Input{
		Reads:       []string{wallPath("r")},
		ReadsNoExec: []string{wallPath("n")},
		Writes:      []string{wallPath("w"), wallPath("home")},
		Home:        wallPath("home"),
		Argv:        argv,
	}
}

// runWall builds and runs one policy inside the wall, returning the child's exit code.
// Output flows to the child's own stdout/stderr, which the parent captures.
func runWall(inp Input) int {
	p, bad := Build(inp)
	if len(bad) > 0 {
		fmt.Fprintf(os.Stderr, "build refused: %v\n", bad)
		return ExitRefused
	}
	code, err := Run(p, os.Environ(), strings.NewReader(""), os.Stdout, os.Stderr, nil)
	if err != nil {
		if r, ok := err.(Refusal); ok {
			// The reason is printed so a test of a Run-level refusal asserts what it was
			// refused for, not only that the command never ran.
			fmt.Fprintf(os.Stderr, "wall refused: %s\n", r.Error())
			return r.Code()
		}
		fmt.Fprintf(os.Stderr, "run refused: %v\n", err)
		return ExitRefused
	}
	return code
}

// mkWallDirs lays out the standard directories, each a sibling of the others.
func mkWallDirs(t *testing.T, base string) {
	t.Helper()
	for _, d := range []string{"w", "home", "r", "n", "x"} {
		require.NoError(t, os.MkdirAll(filepath.Join(base, d), 0o755))
	}
}

// walledRun re-runs this test binary as the walled child and returns its exit code and
// combined output. It skips on a kernel with no landlock, or when it is root, because
// neither can build the wall.
func walledRun(t *testing.T, base string) (int, string) {
	t.Helper()
	if _, ok := landlockABI(); !ok {
		t.Skip("this kernel has no landlock; the wall cannot be built here")
	}
	if os.Geteuid() == 0 {
		t.Skip("the wall does not run as root (rule 2)")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1")
	cmd.Env = append(os.Environ(), wallChildEnv+"=1", wallWorkEnv+"="+base)
	out, err := cmd.CombinedOutput()
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	} else {
		require.NoError(t, err, "the walled child did not run at all: %s", out)
	}
	return code, string(out)
}

func TestWallAWriteInsideAWriteDirectorySucceeds(t *testing.T) {
	t.Parallel()
	if os.Getenv(wallChildEnv) == "1" {
		os.Exit(runWall(standardInput([]string{"/bin/sh", "-c", "echo hi > " + filepath.Join(wallPath("w"), "inside.txt")})))
	}
	base := t.TempDir()
	mkWallDirs(t, base)
	code, out := walledRun(t, base)
	require.Equal(t, 0, code, "a write inside a --write directory failed: %s", out)
	assert.FileExists(t, filepath.Join(base, "w", "inside.txt"))
}

// walledNetRun re-runs this test binary as the walled child for the net test, with the
// port in the environment, and returns the child's exit code and combined output. The
// caller has already skipped when this machine cannot build the wall or cannot enforce
// --net-deny, so the child is only ever run where the wall can refuse the connect.
func walledNetRun(t *testing.T, base, port string) (int, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1")
	cmd.Env = append(os.Environ(), wallChildEnv+"=1", wallWorkEnv+"="+base, wallPortEnv+"="+port)
	out, err := cmd.CombinedOutput()
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	} else {
		require.NoError(t, err, "the walled child did not run at all: %s", out)
	}
	return code, string(out)
}

func TestWallAWriteOutsideEveryWriteRootIsDenied(t *testing.T) {
	t.Parallel()
	if os.Getenv(wallChildEnv) == "1" {
		os.Exit(runWall(standardInput([]string{"/bin/sh", "-c", "echo hi > " + filepath.Join(wallPath("x"), "outside.txt")})))
	}
	base := t.TempDir()
	mkWallDirs(t, base)
	code, out := walledRun(t, base)
	require.NotEqual(t, 0, code, "a write outside every --write succeeded: %s", out)
	assert.Contains(t, out, "Permission denied", "the denial was not EACCES: %s", out)
	assert.NoFileExists(t, filepath.Join(base, "x", "outside.txt"))
}

func TestWallACreateOutsideEveryWriteRootIsDenied(t *testing.T) {
	t.Parallel()
	if os.Getenv(wallChildEnv) == "1" {
		os.Exit(runWall(standardInput([]string{"/bin/sh", "-c", "mkdir " + filepath.Join(wallPath("x"), "made")})))
	}
	base := t.TempDir()
	mkWallDirs(t, base)
	code, out := walledRun(t, base)
	require.NotEqual(t, 0, code, "a mkdir outside every --write succeeded: %s", out)
	assert.Contains(t, out, "Permission denied", "the denial was not EACCES: %s", out)
	assert.NoDirExists(t, filepath.Join(base, "x", "made"))
}

func TestWallARenameOutsideEveryWriteRootIsDenied(t *testing.T) {
	t.Parallel()
	if os.Getenv(wallChildEnv) == "1" {
		os.Exit(runWall(standardInput([]string{"/bin/sh", "-c", "mv " + filepath.Join(wallPath("x"), "from.txt") + " " + filepath.Join(wallPath("w"), "to.txt")})))
	}
	base := t.TempDir()
	mkWallDirs(t, base)
	require.NoError(t, os.WriteFile(filepath.Join(base, "x", "from.txt"), []byte("keep"), 0o644))
	code, out := walledRun(t, base)
	require.NotEqual(t, 0, code, "a rename out of a directory outside every --write succeeded: %s", out)
	assert.Contains(t, out, "Permission denied", "the denial was not EACCES: %s", out)
	assert.FileExists(t, filepath.Join(base, "x", "from.txt"), "the source file was renamed away from an outside directory")
	assert.NoFileExists(t, filepath.Join(base, "w", "to.txt"), "a file was renamed into the write set from outside")
}

func TestWallAnUnlinkOutsideEveryWriteRootIsDenied(t *testing.T) {
	t.Parallel()
	if os.Getenv(wallChildEnv) == "1" {
		os.Exit(runWall(standardInput([]string{"/bin/sh", "-c", "rm " + filepath.Join(wallPath("x"), "victim.txt")})))
	}
	base := t.TempDir()
	mkWallDirs(t, base)
	require.NoError(t, os.WriteFile(filepath.Join(base, "x", "victim.txt"), []byte("keep"), 0o644))
	code, out := walledRun(t, base)
	require.NotEqual(t, 0, code, "an unlink outside every --write succeeded: %s", out)
	assert.Contains(t, out, "Permission denied", "the denial was not EACCES: %s", out)
	assert.FileExists(t, filepath.Join(base, "x", "victim.txt"), "the outside file was unlinked")
}

func TestWallASymlinkInsideAWriteRootCannotEscape(t *testing.T) {
	t.Parallel()
	if os.Getenv(wallChildEnv) == "1" {
		os.Exit(runWall(standardInput([]string{"/bin/sh", "-c", "echo hi > " + filepath.Join(wallPath("w"), "link.txt")})))
	}
	base := t.TempDir()
	mkWallDirs(t, base)
	outside := filepath.Join(base, "x", "outside.txt")
	require.NoError(t, os.WriteFile(outside, []byte("keep"), 0o644))
	require.NoError(t, os.Symlink(outside, filepath.Join(base, "w", "link.txt")))
	code, out := walledRun(t, base)
	require.NotEqual(t, 0, code, "a write through a symlink inside a --write escaped to a file outside: %s", out)
	assert.Contains(t, out, "Permission denied", "the denial was not EACCES: %s", out)
	got, err := os.ReadFile(outside)
	require.NoError(t, err)
	assert.Equal(t, "keep", string(got), "the file the symlink points at was overwritten")
}

func TestWallAReadRootIsReadableAndNotWritable(t *testing.T) {
	t.Parallel()
	if os.Getenv(wallChildEnv) == "1" {
		os.Exit(runWall(standardInput([]string{"/bin/sh", "-c", "cat " + filepath.Join(wallPath("r"), "readme.txt") + " >/dev/null && echo nope > " + filepath.Join(wallPath("r"), "readme.txt")})))
	}
	base := t.TempDir()
	mkWallDirs(t, base)
	readme := filepath.Join(base, "r", "readme.txt")
	require.NoError(t, os.WriteFile(readme, []byte("hello"), 0o644))
	code, out := walledRun(t, base)
	require.NotEqual(t, 0, code, "a write to a --read root succeeded: %s", out)
	assert.Contains(t, out, "Permission denied", "the denial was not EACCES: %s", out)
	got, err := os.ReadFile(readme)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(got), "a --read file was overwritten")
}

func TestWallAReadNoexecBinaryCannotBeExecuted(t *testing.T) {
	t.Parallel()
	if os.Getenv(wallChildEnv) == "1" {
		os.Exit(runWall(standardInput([]string{filepath.Join(wallPath("n"), "run.sh")})))
	}
	base := t.TempDir()
	mkWallDirs(t, base)
	sentinel := filepath.Join(base, "w", "executed.txt")
	require.NoError(t, os.WriteFile(filepath.Join(base, "n", "run.sh"), []byte("#!/bin/sh\necho ran > "+sentinel+"\n"), 0o755))
	code, out := walledRun(t, base)
	require.NotEqual(t, 0, code, "a --read-noexec script executed inside the wall: %s", out)
	// The command's own directory is an execute root, so the no-exec path sits under an
	// execute-granting root and the wall refuses it at build time, naming the reason: a
	// wall that silently let the execution through is the hole this test is red about.
	noexec, err := filepath.EvalSymlinks(filepath.Join(base, "n"))
	require.NoError(t, err)
	assert.Contains(t, out, "sandbox_failed", "the refusal does not name reason=sandbox_failed: %s", out)
	assert.Contains(t, out, "read-noexec", "the refusal does not name the flag: %s", out)
	assert.Contains(t, out, noexec, "the refusal does not name the --read-noexec path %s: %s", noexec, out)
	assert.NoFileExists(t, sentinel, "the script wrote its sentinel; it executed under --read-noexec")
}

// The no-exec path that is NOT under an execute-granting root is enforced by Landlock
// itself: the wall builds, and executing a binary there is denied with EACCES, not
// refused at build time. The command is /bin/sh (its own directory /bin is not the
// no-exec path), and the script it runs sits in the no-exec directory.
func TestWallAReadNoexecNotUnderAnExecRootIsDeniedByLandlock(t *testing.T) {
	t.Parallel()
	if os.Getenv(wallChildEnv) == "1" {
		os.Exit(runWall(standardInput([]string{"/bin/sh", "-c", filepath.Join(wallPath("n"), "run.sh")})))
	}
	base := t.TempDir()
	mkWallDirs(t, base)
	sentinel := filepath.Join(base, "w", "executed.txt")
	require.NoError(t, os.WriteFile(filepath.Join(base, "n", "run.sh"), []byte("#!/bin/sh\necho ran > "+sentinel+"\n"), 0o755))
	code, out := walledRun(t, base)
	require.NotEqual(t, 0, code, "a --read-noexec script executed inside the wall: %s", out)
	assert.Contains(t, out, "Permission denied", "the denial was not EACCES: %s", out)
	assert.NoFileExists(t, sentinel, "the script wrote its sentinel; it executed under --read-noexec")
}

func TestWallRedirectToDevNullWorks(t *testing.T) {
	t.Parallel()
	if os.Getenv(wallChildEnv) == "1" {
		os.Exit(runWall(standardInput([]string{"/bin/sh", "-c", "echo x > /dev/null"})))
	}
	base := t.TempDir()
	mkWallDirs(t, base)
	code, out := walledRun(t, base)
	require.Equal(t, 0, code, "redirecting to /dev/null inside the wall failed: %s", out)
}

func TestWallTmpAndCwdAreWritableOutsideTheWriteSet(t *testing.T) {
	t.Parallel()
	if os.Getenv(wallChildEnv) == "1" {
		tmp := filepath.Join(wallPath("w"), "tmp")
		cwd := filepath.Join(wallPath("w"), "work")
		in := standardInput([]string{"/bin/sh", "-c", "echo a > " + filepath.Join(tmp, "t.txt") + " && echo b > " + filepath.Join(cwd, "c.txt")})
		in.Tmp = tmp
		in.Cwd = cwd
		os.Exit(runWall(in))
	}
	base := t.TempDir()
	mkWallDirs(t, base)
	require.NoError(t, os.MkdirAll(filepath.Join(base, "w", "tmp"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(base, "w", "work"), 0o755))
	code, out := walledRun(t, base)
	require.Equal(t, 0, code, "a write to the job's own --tmp or --cwd failed: %s", out)
	assert.FileExists(t, filepath.Join(base, "w", "tmp", "t.txt"))
	assert.FileExists(t, filepath.Join(base, "w", "work", "c.txt"))
}

// TestWallNetDenyRefusesATCPConnect pins rule 7's linux half: with --net-deny on a
// kernel at ABI >= 4, a TCP connect to 127.0.0.1 is denied with EACCES, and the denial
// is the wall and not a closed port -- the same connect made without the wall to the
// same closed port is refused with ECONNREFUSED, not EACCES. The connect is bash's
// /dev/tcp (no second tool), and the wall applies to a re-executed copy of the test
// binary, so the parent only asserts on the child's exit code and output. The unit tier
// owns no socket (docs/STANDARD.md rule 2), so there is no listener here; the closed
// port's ECONNREFUSED is the control that tells the wall's EACCES apart from a port that
// is simply closed.
func TestWallNetDenyRefusesATCPConnect(t *testing.T) {
	t.Parallel()
	if os.Getenv(wallChildEnv) == "1" {
		in := standardInput([]string{"bash", "-c", "exec 3<>/dev/tcp/127.0.0.1/" + os.Getenv(wallPortEnv)})
		in.NetDeny = true
		os.Exit(runWall(in))
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skipf("no bash on this machine; /dev/tcp is how a connect is made without a second tool: %v", err)
	}
	abi, ok := landlockABI()
	if !ok {
		t.Skip("this kernel has no landlock; the wall cannot be built here")
	}
	if os.Geteuid() == 0 {
		t.Skip("the wall does not run as root (rule 2)")
	}
	if abi < 4 {
		t.Skipf("this kernel reports landlock abi %d; --net-deny needs abi 4 for TCP connect", abi)
	}

	base := t.TempDir()
	mkWallDirs(t, base)
	const port = "45678"

	// THE CONTROL: the same connect made without the wall is refused with ECONNREFUSED
	// ("Connection refused"), proving the port is closed -- so the walled child's
	// "Permission denied" below is the wall and not a closed port.
	ctl := exec.Command("bash", "-c", "exec 3<>/dev/tcp/127.0.0.1/"+port)
	// ignored: the control's non-zero exit is the refused connect itself; the output text is what proves the port is closed
	ctlOut, _ := ctl.CombinedOutput()
	require.Contains(t, string(ctlOut), "Connection refused",
		"the control connect to the closed port %s did not say the port is closed (something listens there?): %s", port, ctlOut)

	// Under --net-deny the same connect is denied by the wall with EACCES, and the walled
	// command never connects.
	code, out := walledNetRun(t, base, port)
	require.NotEqual(t, 0, code, "the walled command CONNECTED under --net-deny: %s", out)
	assert.Contains(t, out, "Permission denied", "the denial was not EACCES: %s", out)
}
