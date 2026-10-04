// Package cardhdr is the card header's vocabulary: the kinds and routes a
// card's KIND and ROUTE lines may carry, and the one reader of a `KEY: value`
// header line. It is a leaf (standard library only) so every reader of a card
// header reads it the same way without importing another.
package cardhdr

import (
	"regexp"
	"slices"
	"strings"
)

// The routes a card may carry: the three model types.
// frontier is the most recent Astra or Fable model only; pro and flash are
// the rungs the bench harness picks its model from (nova-sprint routes --tier <route>, first allowed route). A
// card with no ROUTE line is flash. Every worker (a friend or a bench)
// advertises the types it runs on its desired record's tiers field
// (nova-sprint capacity bench --tiers ..., nova-config for a friend); the dealer matches a
// card's route against that, and a worker advertising nothing is flash,pro.
// Nothing in code names a worker.
const (
	RouteFrontier = "frontier"
	RoutePro      = "pro"
	RouteFlash    = "flash"
)

// Routes is the three model types in rank order, the set every ROUTE parse
// and list draws on.
var Routes = []string{RouteFrontier, RoutePro, RouteFlash}

// RouteList is the three types as a refusal names them.
const RouteList = "frontier, pro or flash"

// IsRoute reports whether s is one of the three model types.
func IsRoute(s string) bool {
	return slices.Contains(Routes, s)
}

// KeyRE is the header line's shape: a key word at column 0, then a colon.
var KeyRE = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9-]*):\s*(.*)$`)

// KeyValue reads one `KEY: value` line by the header's own rule, so the issue
// parser that fills a task record (taskcard.ParseIssue) and card push read a
// line the same way.
func KeyValue(line string) (key, value string, ok bool) {
	m := KeyRE.FindStringSubmatch(strings.TrimRight(line, "\r"))
	if m == nil {
		return "", "", false
	}
	return m[1], strings.TrimSpace(m[2]), true
}

// A card is a spec: the work it asks of the friends+swarm must come out as
// good as, or better than, software built yourself, so its TEST line names
// the one test the change is proved
// by, the class test, and the copy wrapper runs it at BASE (red) and at HEAD
// (green) before it pushes. A card with no test says why, on the same line,
// so the reader sees it: `TEST: none <why>`.

// TestLine is a card's TEST value, read by ParseTest.
type TestLine struct {
	// Package and Name are `TEST: <package> <TestName>`: the Go package
	// path the wrapper runs and the test it selects. Tags are the build
	// tags the test needs (`TEST: -tags functional <package> <TestName>`),
	// "" for none.
	Package, Name, Tags string
	// None is `TEST: none <why>`; Why is the reason the card has no test.
	None bool
	Why  string
}

// TestRemedy is what a card whose DONE-WHEN cannot be turned into a test is
// told, on every refusal.
const TestRemedy = "name `go test <package> -run <TestName>` in DONE-WHEN, or add TEST: [-tags <tags>] <package> <TestName>, or TEST: none <why the change has no test>"

var (
	// testPkgRE is a ./-relative or repository-relative Go package path.
	// A `...` pattern is not one package: a red at base from any package
	// it matches would read as the named test's (nova-tools#4401 read).
	testPkgRE  = regexp.MustCompile(`^(\.|\./[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*|[A-Za-z0-9_][A-Za-z0-9_.-]*(/[A-Za-z0-9_.-]+)*)/?$`)
	testNameRE = regexp.MustCompile(`^(Test|Example|Fuzz)[A-Za-z0-9_]*$`)
	testTagsRE = regexp.MustCompile(`^[A-Za-z0-9_.]+(,[A-Za-z0-9_.]+)*$`)
)

// ParseTest reads a TEST value: `[-tags <tags>] <package> <TestName>` (the
// package one ./-relative or repository-relative Go package with no .. and
// no `...` pattern, the name a Go test name, the tags go test's -tags list)
// or `none <why>`. Anything
// else is refused: why is one line with the remedy, and the line is zero. A
// bare `none` is refused too: the reader must see why.
func ParseTest(v string) (TestLine, string) {
	f := strings.Fields(v)
	refused := "TEST " + strings.TrimSpace(v) + " is not `[-tags <tags>] <package> <TestName>` or `none <why>`: " + TestRemedy
	switch {
	case len(f) == 0:
		return TestLine{}, "DONE-WHEN cannot be turned into a test (no TEST line): " + TestRemedy
	case isNone(f[0]):
		why := strings.TrimLeft(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "none")), ":-()— ")
		if why == "" {
			return TestLine{}, "TEST: none says no why: write TEST: none <why the change has no test>, or TEST: <package> <TestName>"
		}
		return TestLine{None: true, Why: why}, ""
	}
	var tags string
	flagged := strings.HasPrefix(f[0], "-")
	switch {
	case strings.HasPrefix(f[0], "-tags="):
		tags, f = strings.TrimPrefix(f[0], "-tags="), f[1:]
	case f[0] == "-tags" && len(f) > 1:
		tags, f = f[1], f[2:]
	}
	if flagged && !testTagsRE.MatchString(tags) {
		return TestLine{}, refused
	}
	if len(f) == 2 && strings.Contains(f[0], "...") {
		return TestLine{}, "TEST " + strings.TrimSpace(v) + " names a package pattern (" + f[0] + "), not one package: a red at base must be the named test's own, so name the package the test is in: " + TestRemedy
	}
	if len(f) == 2 && testPkgRE.MatchString(f[0]) && !hasDotDot(f[0]) && testNameRE.MatchString(f[1]) {
		return TestLine{Package: f[0], Name: f[1], Tags: tags}, ""
	}
	return TestLine{}, refused
}

func hasDotDot(p string) bool {
	return slices.Contains(strings.Split(p, "/"), "..")
}

// isNone is the first word of `none <why>`: none, or none with a separator
// glued to it (`none:`, `none-`).
func isNone(w string) bool {
	rest, ok := strings.CutPrefix(w, "none")
	return ok && strings.Trim(rest, ":-()—") == ""
}
