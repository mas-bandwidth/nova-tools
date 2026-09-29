// Package testguard refuses to let a unit test reach a real host.
//
// THE HURT (2026-09-18). A unit test in the certify verb's first cut ran the
// REAL workloads on hulk and reached redis on space, because the local-runner
// seam defaulted to the real thing when nothing injected a fake. Nobody wrote
// a hostname in the test: the test constructed production code, production
// code constructed its own default, and the default was an
// `exec.Command("ssh", …)`. The `net` class test reads test files for a real
// hostname and could not see this, because the host was never in the test's
// text -- it was in the default two packages away.
//
// THE GUARD. Every seam in this tree that reaches a host calls RefuseHosts
// with the command line it is about to run. When NOVA_TEST_NO_HOST is 1 --
// which `make test` sets, so every run of the suite carries it -- that call
// panics and names the command, so the defect is the test that constructed the
// real thing rather than a bench that was busy, a key that was missing or a
// flake nobody could reproduce. When the variable is unset the call is one
// atomic load and a return: no allocation, no lookup, nothing to pay on a
// production path that is about to spawn ssh anyway.
//
// A TEST THAT WANTS A CHILD. A fake `ssh` written into t.TempDir() and put on
// PATH is not a host, and this package can see that for itself: a program that
// resolves INSIDE a temp directory (GOTMPDIR included) is a fake, a program that resolves to
// /usr/bin/ssh is the fleet. So the tests that already fake the seam that way
// -- and the tools they run as child processes, which inherit the variable --
// keep working untouched. A test whose fake lives anywhere else says so out
// loud with `defer testguard.AllowHosts()()`.
//
// Every seam this package guards is held by
// TestNoTestReachesAHostThroughAnUnfakedSeam in internal/ci, which reads the
// tree and refuses a seam that does not call it.
package testguard

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
)

// EnvNoHost is the variable that turns the guard on. `make test` sets it to
// "1" for every tier, and ci.yml reaches the same targets through make, so a
// test running anywhere on the CI path runs under the guard.
const EnvNoHost = "NOVA_TEST_NO_HOST"

// Guard is an instance of the host guard. The package-level functions operate
// on the process-default guard; tests can construct an isolated Guard so they
// can run in parallel without mutating the process environment.
type Guard struct {
	refusing  atomic.Bool
	forced    atomic.Int64
	allowed   atomic.Int64
	lookPath  func(string) (string, error)
	tempRoots func() []string
}

var defaultGuard = &Guard{
	lookPath:  exec.LookPath,
	tempRoots: tempRoots,
}

func init() { Reload() }

// Reload re-reads the environment. Tests that set the variable with t.Setenv
// after start call it, and call it again on cleanup; nothing on a production
// path needs it.
func Reload() { defaultGuard.refusing.Store(os.Getenv(EnvNoHost) == "1") }

// Refusing reports whether the guard is armed. It exists so a test can say
// what it is testing without reading the environment itself.
func Refusing() bool { return defaultGuard.Refusing() }

// Arm forces the guard to refuse host access until the returned function is called.
// It allows tests asserting guard behavior to run in parallel without mutating process environment.
func Arm() func() { return defaultGuard.Arm() }

// AllowHosts opens a scope in which a seam may run a child, and returns the
// function that closes it.
func AllowHosts() func() { return defaultGuard.AllowHosts() }

// RefuseHosts is what every ssh/scp/rsync seam in this tree calls with the
// command line it is about to run.
func RefuseHosts(program string, args ...string) { defaultGuard.RefuseHosts(program, args...) }

// NewGuard constructs an isolated guard with its armed state explicitly configured.
func NewGuard(armed bool) *Guard {
	g := &Guard{
		lookPath:  exec.LookPath,
		tempRoots: tempRoots,
	}
	g.refusing.Store(armed)
	return g
}

// Refusing reports whether the guard is armed.
func (g *Guard) Refusing() bool { return g.refusing.Load() || g.forced.Load() > 0 }

// Arm forces the guard to refuse host access until the returned function is called.
func (g *Guard) Arm() func() {
	g.forced.Add(1)
	var once sync.Once
	return func() { once.Do(func() { g.forced.Add(-1) }) }
}

// AllowHosts opens a scope in which a seam may run a child, and returns the
// function that closes it.
func (g *Guard) AllowHosts() func() {
	g.allowed.Add(1)
	var once sync.Once
	return func() { once.Do(func() { g.allowed.Add(-1) }) }
}

// RefuseHosts is what every ssh/scp/rsync seam in this tree calls with the
// command line it is about to run. Under the guard, and outside an AllowHosts
// scope, it panics naming that command line; otherwise it returns immediately.
func (g *Guard) RefuseHosts(program string, args ...string) {
	if (!g.refusing.Load() && g.forced.Load() <= 0) || g.allowed.Load() > 0 {
		return
	}
	if g.isFakeProgram(program) {
		return
	}
	panic(fmt.Sprintf(
		"%s=1: a test reached a host through an unfaked seam: %s; "+
			"inject the fake the seam takes, or install a fake on PATH and declare it with testguard.AllowHosts()",
		EnvNoHost, commandLine(program, args)))
}

func (g *Guard) isFakeProgram(program string) bool {
	look := g.lookPath
	if look == nil {
		look = exec.LookPath
	}
	path, err := look(program)
	if err != nil {
		return false
	}
	if !filepath.IsAbs(path) {
		abs, err := filepath.Abs(path)
		if err != nil {
			return false
		}
		path = abs
	}
	rootsFn := g.tempRoots
	if rootsFn == nil {
		rootsFn = tempRoots
	}
	for _, root := range rootsFn() {
		if under(path, root) {
			return true
		}
	}
	return false
}

// tempRoots are the directories a test's own files live under: the platform's
// temp directory, the temp directory a CI workflow names, and GOTMPDIR.
//
// GOTMPDIR is here because it is where t.TempDir() puts a test's files when it
// is set, and os.TempDir() does not follow it: the testing package makes the
// directory with `os.MkdirTemp(os.Getenv("GOTMPDIR"), pattern)`
// (testing/testing.go, common.makeTempDir, go1.27.1). The root is reachable
// only through the environment -- testing exposes no accessor, and the go command
// starts the test binary with its own original environment, so the variable
// the binary sees is the one t.TempDir() read. A runner whose unit sets
// GOTMPDIR and not TMPDIR otherwise puts every fake a test wrote into its own
// t.TempDir() outside every root here, and the guard calls it the fleet.
//
// A relative root is made absolute, because the program path it is compared
// with has been.
func tempRoots() []string {
	roots := []string{os.TempDir()}
	for _, env := range []string{"TMPDIR", "TMP", "TEMP", "RUNNER_TEMP", "GOTMPDIR"} {
		if v := os.Getenv(env); v != "" {
			roots = append(roots, v)
		}
	}
	for i, r := range roots {
		if !filepath.IsAbs(r) {
			if abs, err := filepath.Abs(r); err == nil {
				roots[i] = abs
			}
		}
	}
	return roots
}

// under reports whether path is inside root, comparing the resolved forms so
// that /var/folders and /private/var/folders (macOS) are the same place.
func under(path, root string) bool {
	if root == "" {
		return false
	}
	path, root = resolve(path), resolve(root)
	if !strings.HasSuffix(root, string(filepath.Separator)) {
		root += string(filepath.Separator)
	}
	if runtime.GOOS == "windows" {
		return strings.HasPrefix(strings.ToLower(path), strings.ToLower(root))
	}
	return strings.HasPrefix(path, root)
}

func resolve(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(r)
	}
	return filepath.Clean(p)
}

// commandLine renders the child as one line, every word quoted, so a script on
// stdin or an argument holding a space cannot break the panic into two lines a
// reader has to reassemble.
func commandLine(program string, args []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%q", program)
	for _, a := range args {
		fmt.Fprintf(&b, " %q", a)
	}
	return b.String()
}
