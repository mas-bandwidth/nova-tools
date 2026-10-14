package ci

import (
	"path/filepath"
	"testing"
)

// ci_waits_test.go is the waits rule's cases, run through the harness in
// ci_templates_test.go: TestCIScanRules runs each case as a subtest; the
// rule's inputs and wants are the waitsRule row below.

// waitsRule is the waits row of the harness.
var waitsRule = ciRule{
	key:           "waits",
	prefix:        "CI-WAITS",
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
		{name: "RefusesFixedSleep", fixture: "sleep.go.txt", kind: "sleep", remedy: WaitRemedySleep, contains: "time.Sleep(150 * time.Millisecond)"},
		{name: "RefusesShortBound", fixture: "bound.go.txt", kind: "bound", remedy: WaitRemedyBound, contains: "context.WithTimeout(context.Background(), 5"},
		{name: "RefusesElapsedAssertion", fixture: "elapsed.go.txt", kind: "elapsed", remedy: WaitRemedyElapsed, contains: "time.Since(start) < 5"},
	},
	allows: []ciAllow{
		{name: "AllowsThePoll", fixture: "poll.go.txt", tests: 1},
		{name: "AllowsFakeBenchAndClock", fixture: "fakeclock.go.txt", tests: -1},
	},
	secondOffender: func(rel, host string) string {
		return "\nfunc TestSecondSleeper(t *testing.T) { time.Sleep(250 * time.Millisecond) }\n"
	},
}

// TestNoFixedWaitsOnTheCIPath is the class test itself: every _test.go under
// internal/ and cmd/ is read, and the only fixed waits that pass are the ones
// the allowlist already names.
func TestNoFixedWaitsOnTheCIPath(t *testing.T) {
	t.Parallel()
	ciClassTest(t, waitsRule, filepath.Join(repoRoot(t), "internal", "ci", "testdata", "fixed-waits-allowlist.txt"))
}
