package testbin

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// THE HURT (2026-10-04): a test that runs "this binary"
// (os.Executable) ran the TEST BINARY with CLI words. A Go test binary takes
// words it does not know as no flags at all and runs the whole suite again,
// which reached the same test, which ran the binary again: a chain 289
// processes deep. docs/TESTS.md, tests-reexec-guard-everywhere.w2.
//
// THE GUARD. A start of a test binary is one of three things, decided by Mode
// before any test runs:
//
//   - the suite: a `go test` run (its words are -test.* flags or none), or a
//     child a test started on purpose with -test.run;
//   - a start the package dispatches itself: CLI words or a helper verb the
//     package's own TestMain answers (its handled func says so);
//   - a refusal, exit ExitRefused and one loud line: CLI words from a test
//     binary that handles none (the suite would run again), or a chain of
//     test binaries MaxDepth deep (DepthEnv counts every start).
const (
	// DepthEnv is how many test binaries stand above this one; every start
	// sets it one higher for its children.
	DepthEnv = "NOVA_TESTBIN_DEPTH"
	// MaxDepth is how many test binaries may stand in one chain, this one
	// included.
	MaxDepth = 2
	// ExitRefused is the exit code of a refused start.
	ExitRefused = 3
)

// Start is what one start of a test binary is.
type Start int

const (
	// StartSuite runs the tests.
	StartSuite Start = iota
	// StartHandled is answered by the package's own dispatch before the suite.
	StartHandled
	// StartRefused exits ExitRefused with the reason.
	StartRefused
)

// Guard decides this start of a test binary and exits ExitRefused, with one
// line on stderr, when it is a refusal. A package calls it from a package-level
// `var _ = testbin.Guard(...)` in reexec_test.go, which runs before TestMain
// and so before any dispatch or setup of the package. handled reports the
// words (os.Args[1:]) the package's own TestMain answers; nil means none. It
// returns the Start for a caller that wants it.
func Guard(tool string, handled func(args []string) bool) Start {
	mode, depth, why := Mode(os.Args[1:], os.Getenv, handled)
	// ignored: os.Setenv fails only on a name with '=' or NUL, and this is a constant
	_ = os.Setenv(DepthEnv, strconv.Itoa(depth+1))
	if mode == StartRefused {
		fmt.Fprintf(os.Stderr, "%s test binary REFUSED: %s; refusing to recurse (exit %d)\n", tool, why, ExitRefused)
		os.Exit(ExitRefused)
	}
	return mode
}

// Mode is what a start of the test binary is, given its words and environment:
// the suite, a start the package handles, or a refusal with its reason. depth
// is how many test binaries stand above this one. It is the pure half of Guard,
// the model docs/TESTS.md states.
func Mode(args []string, getenv func(string) string, handled func(args []string) bool) (mode Start, depth int, why string) {
	if d := getenv(DepthEnv); d != "" {
		n, err := strconv.Atoi(d)
		if err != nil || n < 0 {
			return StartRefused, 0, fmt.Sprintf("%s=%q is not a depth", DepthEnv, d)
		}
		depth = n
	}
	if depth >= MaxDepth {
		return StartRefused, depth, fmt.Sprintf("%d test binaries already stand above this one (%s), at most %d may", depth, DepthEnv, MaxDepth-1)
	}
	switch {
	case len(args) == 0 || strings.HasPrefix(args[0], "-test."):
		return StartSuite, depth, ""
	case handled != nil && handled(args):
		return StartHandled, depth, ""
	case depth > 0:
		return StartRefused, depth, fmt.Sprintf("started by a test binary with the words %q and nothing here answers them: the suite would run again", strings.Join(args, " "))
	}
	return StartSuite, depth, ""
}
