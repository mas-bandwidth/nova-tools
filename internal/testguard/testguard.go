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
// A TEST THAT WANTS A CHILD. A test that installs its own fake `ssh` on PATH
// is not reaching a host, and it says so out loud with
// `defer testguard.AllowHosts()()`. The declaration is the point: a reader of
// the test sees that a child process is expected and that the child is a fake.
//
// Every seam this package guards is held by
// TestNoTestReachesAHostThroughAnUnfakedSeam in internal/ci, which reads the
// tree and refuses a seam that does not call it.
package testguard

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
)

// EnvNoHost is the variable that turns the guard on. `make test` sets it to
// "1" for every tier, and ci.yml reaches the same targets through make, so a
// test running anywhere on the CI path runs under the guard.
const EnvNoHost = "NOVA_TEST_NO_HOST"

// refusing is the guard's state, read once from the environment at start and
// again whenever Reload is called. An atomic bool so the load is free and the
// race detector stays quiet across the goroutines a seam may run on.
var refusing atomic.Bool

// allowed counts the AllowHosts scopes standing open. A counter rather than a
// flag so nested scopes -- a helper that allows, inside a test that allows --
// restore correctly.
var allowed atomic.Int64

func init() { Reload() }

// Reload re-reads the environment. Tests that set the variable with t.Setenv
// after start call it, and call it again on cleanup; nothing on a production
// path needs it.
func Reload() { refusing.Store(os.Getenv(EnvNoHost) == "1") }

// Refusing reports whether the guard is armed. It exists so a test can say
// what it is testing without reading the environment itself.
func Refusing() bool { return refusing.Load() }

// AllowHosts opens a scope in which a seam may run a child, and returns the
// function that closes it. The one honest use is a test that has installed its
// own fake on PATH:
//
//	defer testguard.AllowHosts()()
//
// The scope is process-wide for its duration, so a test that opens one must
// not run in parallel with a test relying on the guard. That is a narrowing,
// written down rather than left to be discovered: the guard catches the
// UNFAKED seam, and a test that fakes a seam declares it.
func AllowHosts() func() {
	allowed.Add(1)
	var once sync.Once
	return func() { once.Do(func() { allowed.Add(-1) }) }
}

// RefuseHosts is what every ssh/scp/rsync seam in this tree calls with the
// command line it is about to run. Under the guard, and outside an AllowHosts
// scope, it panics naming that command line; otherwise it returns immediately.
//
// It panics rather than returning an error on purpose. An error would travel
// up a path that already handles "the bench was unreachable" and would be
// reported as exactly that -- an infrastructure story for a code defect. A
// panic names the test, the seam and the command in one stack, which is the
// cheapest possible read of the hurt above.
func RefuseHosts(program string, args ...string) {
	if !refusing.Load() || allowed.Load() > 0 {
		return
	}
	panic(fmt.Sprintf(
		"%s=1: a test reached a host through an unfaked seam: %s; "+
			"inject the fake the seam takes, or install a fake on PATH and declare it with testguard.AllowHosts()",
		EnvNoHost, commandLine(program, args)))
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
