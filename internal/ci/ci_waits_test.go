package ci

import (
	"path/filepath"
	"testing"
)

// ci_waits_test.go is the waits rule's cases, run through the harness in
// ci_templates_test.go. Each top-level test keeps its old name and delegates to
// the one shared helper, so `go test -run` and the docs that name it still find
// it; the rule's inputs and wants are the waitsRule row below.

// waitsRule is the waits row of ciRules.
var waitsRule = ciRule{
	key:           "waits",
	prefix:        "CI-WAITS",
	allowlist:     "fixed-waits-allowlist.txt",
	fixtures:      "waits",
	allowKind:     "sleep",
	allowRemedy:   WaitRemedyAllow,
	verbLine:      WaitsVerbLine,
	check:         runWaits,
	outputFixture: "sleep.go.txt",
	wants: []string{
		"CI-WAITS file=internal/fixture/fixture_test.go line=",
		` kind=sleep remedy="poll for the event up to NOVA_TEST_WAIT, not a fixed sleep"`,
	},
	refusals: []ciRefusal{
		{name: "TestWaitsRefusesFixedSleep", fixture: "sleep.go.txt", kind: "sleep", remedy: WaitRemedySleep, contains: "time.Sleep(150 * time.Millisecond)"},
		{name: "TestWaitsRefusesShortBound", fixture: "bound.go.txt", kind: "bound", remedy: WaitRemedyBound, contains: "context.WithTimeout(context.Background(), 5"},
		{name: "TestWaitsRefusesElapsedAssertion", fixture: "elapsed.go.txt", kind: "elapsed", remedy: WaitRemedyElapsed, contains: "time.Since(start) < 5"},
	},
	allows: []ciAllow{
		{name: "TestWaitsAllowsThePoll", fixture: "poll.go.txt", tests: 1},
		{name: "TestWaitsAllowsFakeBenchAndClock", fixture: "fakeclock.go.txt", tests: -1},
	},
	secondOffender: func(rel, host string) string {
		return "\nfunc TestSecondSleeper(t *testing.T) { time.Sleep(250 * time.Millisecond) }\n"
	},
}

// 1. A test carrying time.Sleep(150 * time.Millisecond) is refused with its
// file and line.
func TestWaitsRefusesFixedSleep(t *testing.T) {
	t.Parallel()
	ciNamedRefusal(t, waitsRule, "TestWaitsRefusesFixedSleep")
}

// 2. A test with context.WithTimeout(ctx, 5*time.Second) used as its pass/fail
// condition is refused as a bound under ten seconds.
func TestWaitsRefusesShortBound(t *testing.T) {
	t.Parallel()
	ciNamedRefusal(t, waitsRule, "TestWaitsRefusesShortBound")
}

// 3. A test asserting time.Since(start) < 5*time.Second is refused as an
// elapsed-time assertion.
func TestWaitsRefusesElapsedAssertion(t *testing.T) {
	t.Parallel()
	ciNamedRefusal(t, waitsRule, "TestWaitsRefusesElapsedAssertion")
}

// 4. A polling test reading NOVA_TEST_WAIT is allowed.
func TestWaitsAllowsThePoll(t *testing.T) {
	t.Parallel()
	ciNamedAllow(t, waitsRule, "TestWaitsAllowsThePoll")
}

// 5. A test whose subprocess bench and clock are fakes passes.
func TestWaitsAllowsFakeBenchAndClock(t *testing.T) {
	t.Parallel()
	ciNamedAllow(t, waitsRule, "TestWaitsAllowsFakeBenchAndClock")
}

// 6. Adding an entry to the allowlist is refused; removing one is allowed.
func TestWaitsAllowlistGrowsRefused(t *testing.T) {
	t.Parallel()
	ciAllowlistGrowsRefused(t, waitsRule)
}

// TestWaitsOutputMatchesTheSpec pins the one-line grammar of the section.
func TestWaitsOutputMatchesTheSpec(t *testing.T) {
	t.Parallel()
	ciOutputMatchesTheSpec(t, waitsRule)
}

// TestWaitsVerbLineMatchesTheSpec pins the help line the class test is entered
// under, word for word, to the section that prints it.
func TestWaitsVerbLineMatchesTheSpec(t *testing.T) {
	t.Parallel()
	ciVerbLineMatchesTheSpec(t, waitsRule)
}

// TestNoFixedWaitsOnTheCIPath is the class test itself: every _test.go under
// internal/ and cmd/ is read, and the only fixed waits that pass are the ones
// the allowlist already names.
func TestNoFixedWaitsOnTheCIPath(t *testing.T) {
	t.Parallel()
	ciClassTest(t, waitsRule, filepath.Join(repoRoot(t), "internal", "ci", "testdata", "fixed-waits-allowlist.txt"))
}

// TestWaitsAllowlistSurvivesShiftedLines: a row allows one offender of its kind
// in its file wherever that offender now stands.
func TestWaitsAllowlistSurvivesShiftedLines(t *testing.T) {
	t.Parallel()
	ciAllowlistShiftedLines(t, waitsRule)
}
