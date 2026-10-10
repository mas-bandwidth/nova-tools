package testbin

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// The guard on a test binary that re-executes itself (docs/TESTS.md, "tests-reexec-guard-everywhere").
//
// A Go test binary takes words it does not know as no flags at all and runs the
// whole suite again. A test that runs os.Executable() with CLI words and no
// dispatch behind them therefore starts the suite inside the child, which
// reaches the same test, which starts the binary again: 289 processes deep on
// 2026-10-04. Every cmd/<tool> whose tests re-execute their binary calls Enter
// before any test runs, and a start of the binary is then one of three things:
//
//   - Suite: a `go test` run (its words are -test.* flags), or a child a test
//     started on purpose with -test.run, or CLI words typed by hand with no test
//     binary above;
//   - Handled: CLI words the package's own TestMain or init answers (a helper
//     process of the package), which is the package's dispatch and not a suite;
//   - Refused, exit ExitRefuse and one loud line: CLI words from a test binary
//     the package does not answer, or a chain of test binaries MaxDepth deep.

const (
	// MaxDepth is how many test binaries of one tool may stand in a chain; the
	// MaxDepth-th start is refused, so a chain is at most MaxDepth-1 deep.
	MaxDepth = 2
	// ExitRefuse is the exit code of a refused start.
	ExitRefuse = 3
)

// Start is what one start of a test binary is.
type Start int

const (
	Suite Start = iota
	Handled
	Refused
)

// Handler reports whether a start is one the package answers itself, given the
// words after the program name and the environment.
type Handler func(args []string, getenv func(string) string) bool

// DepthEnv is the environment variable that counts the test binaries of tool
// standing above a start: NOVA_TEST_DEPTH_<TOOL>, upper case, '-' as '_'.
func DepthEnv(tool string) string {
	return "NOVA_TEST_DEPTH_" + strings.ToUpper(strings.ReplaceAll(tool, "-", "_"))
}

// Decide is what a start of tool's test binary is, given its words and its
// environment: Suite, Handled, or Refused with the reason. depth is how many
// test binaries of tool stand above it. handled may be nil.
func Decide(tool string, args []string, getenv func(string) string, handled Handler) (start Start, depth int, why string) {
	env := DepthEnv(tool)
	if d := getenv(env); d != "" {
		n, err := strconv.Atoi(d)
		if err != nil || n < 0 {
			return Refused, 0, fmt.Sprintf("%s=%q is not a depth", env, d)
		}
		depth = n
	}
	if depth >= MaxDepth {
		return Refused, depth, fmt.Sprintf("%d test binaries of %s already stand above this one (%s), at most %d may", depth, tool, env, MaxDepth-1)
	}
	if len(args) == 0 || strings.HasPrefix(args[0], "-test.") {
		return Suite, depth, ""
	}
	if handled != nil && handled(args, getenv) {
		return Handled, depth, ""
	}
	if depth > 0 {
		return Refused, depth, fmt.Sprintf("started by a test binary with the words %q and nothing in %s answers them: the suite would run again", strings.Join(args, " "), tool)
	}
	return Suite, depth, ""
}

// Enter decides this start of tool's test binary from os.Args and os.Environ,
// before any test runs. A refusal prints one line and exits ExitRefuse; any
// other start counts itself in DepthEnv, so its children see one more test
// binary above them, and Enter returns Suite or Handled. A Handled start is the
// caller's to answer: the package's dispatch runs and exits.
func Enter(tool string, handled Handler) Start {
	start, depth, why := Decide(tool, os.Args[1:], os.Getenv, handled)
	if start == Refused {
		fmt.Fprintf(os.Stderr, "%s test binary REFUSED: %s; refusing to recurse (exit %d)\n", tool, why, ExitRefuse)
		os.Exit(ExitRefuse)
	}
	// ignored: os.Setenv fails only on a name with '=' or NUL, and DepthEnv has neither
	_ = os.Setenv(DepthEnv(tool), strconv.Itoa(depth+1))
	return start
}
