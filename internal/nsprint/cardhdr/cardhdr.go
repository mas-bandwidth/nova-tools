// Package cardhdr is the card header's vocabulary: the kinds and routes a
// card's KIND and ROUTE lines may carry, and the one reader of a `KEY: value`
// header line. It is a leaf (standard library only) so both the card session
// (internal/nsprint/card, which lints and runs cards) and the task record
// store (internal/nsprint/taskcard, which renders a card from a record) read
// the header the same way without importing each other: card drives taskcard
// for copy sessions, so taskcard must not import card (PR #3916 landing).
package cardhdr

import (
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// Runner kinds: a card hash kind that sets no RESULT expectation (card_run.lua
// says the same of them). A card with no KIND line is KindModel.
const (
	KindModel  = "model"
	KindScript = "script"
)

// RunnerKinds are the card kinds besides typedrec.Kinds that card push accepts.
var RunnerKinds = []string{KindModel, KindScript}

// KindMap is the one declared mapping from a classification kind to the RESULT
// kind the card is pushed as (nova-tools#3651). The cutter and the feed apply
// it at cut time (card.MapKind), and card push --map-kind applies it at push.
// A kind already in typedrec.Kinds or RunnerKinds is never in this table and
// is never rewritten.
var KindMap = map[string]string{
	"go-verb":  typedrec.KindFix,
	"go-fix":   typedrec.KindFix,
	"lua-fn":   typedrec.KindFix,
	"bats":     typedrec.KindFix,
	"security": typedrec.KindFix,
	"fleet":    typedrec.KindFix,
	"retire":   typedrec.KindFix,
	"docs":     typedrec.KindDocsGuard,
	"spec":     typedrec.KindReport,
	"probe":    typedrec.KindReport,
}

// IsRunnerKind reports whether kind is one of RunnerKinds.
func IsRunnerKind(kind string) bool {
	for _, k := range RunnerKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// The routes a card may carry: the three model types (Glenn 2026-09-26).
// frontier is the most recent Astra or Fable model only; pro and flash are
// the rungs of internal/nsprint/route/routes.yaml the bench harness picks
// its model from (nova-sprint routes --tier <route>, first allowed route). A
// card with no ROUTE line is flash. Every worker (a friend or a bench)
// advertises the types it runs on its desired record's tiers field
// (nova-sprint capacity friend|bench --tiers ...); the dealer matches a
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

// DefaultTiers is what a worker with no tiers advertised is treated as: the
// two swarm rungs, so a frontier card never goes to a worker that did not
// advertise frontier.
const DefaultTiers = "flash,pro"

// IsRoute reports whether s is one of the three model types.
func IsRoute(s string) bool {
	for _, r := range Routes {
		if r == s {
			return true
		}
	}
	return false
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

// A card is a spec (nova-tools#4313, Glenn 2026-09-26: "how can I make the
// quality of the friends+swarm work as good as, or better than software
// built yourself?"): its TEST line names the one test the change is proved
// by, the class test, and the copy wrapper runs it at BASE (red) and at HEAD
// (green) before it pushes. A card with no test says why, on the same line,
// so the reader sees it: `TEST: none <why>`.

// TestLine is a card's TEST value, read by ParseTest.
type TestLine struct {
	// Package and Name are `TEST: <package> <TestName>`: the Go package
	// path the wrapper runs and the test it selects.
	Package, Name string
	// None is `TEST: none <why>`; Why is the reason the card has no test.
	None bool
	Why  string
}

// String is the line's value as a card carries it.
func (t TestLine) String() string {
	if t.None {
		return "none " + t.Why
	}
	return t.Package + " " + t.Name
}

// TestRemedy is what a card whose DONE-WHEN cannot be turned into a test is
// told, on every refusal.
const TestRemedy = "name `go test <package> -run <TestName>` in DONE-WHEN, or add TEST: <package> <TestName>, or TEST: none <why the change has no test>"

var (
	testPkgRE  = regexp.MustCompile(`^\.(/[A-Za-z0-9_.-]+)*/?(\.\.\.)?$`)
	testNameRE = regexp.MustCompile(`^(Test|Example|Fuzz)[A-Za-z0-9_]*$`)
)

// ParseTest reads a TEST value: `<package> <TestName>` (the package a
// ./-relative Go path with no .., the name a Go test name) or `none <why>`.
// Anything else is refused: why is one line with the remedy, and the line
// is zero. A bare `none` is refused too: the reader must see why.
func ParseTest(v string) (TestLine, string) {
	f := strings.Fields(v)
	switch {
	case len(f) == 0:
		return TestLine{}, "DONE-WHEN cannot be turned into a test (no TEST line): " + TestRemedy
	case isNone(f[0]):
		why := strings.TrimLeft(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "none")), ":-()— ")
		if why == "" {
			return TestLine{}, "TEST: none says no why: write TEST: none <why the change has no test>, or TEST: <package> <TestName>"
		}
		return TestLine{None: true, Why: why}, ""
	case len(f) == 2 && testPkgRE.MatchString(f[0]) && !hasDotDot(f[0]) && testNameRE.MatchString(f[1]):
		return TestLine{Package: f[0], Name: f[1]}, ""
	}
	return TestLine{}, "TEST " + strings.TrimSpace(v) + " is not `<package> <TestName>` or `none <why>`: " + TestRemedy
}

func hasDotDot(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// isNone is the first word of `none <why>`: none, or none with a separator
// glued to it (`none:`, `none-`).
func isNone(w string) bool {
	rest, ok := strings.CutPrefix(w, "none")
	return ok && strings.Trim(rest, ":-()—") == ""
}
