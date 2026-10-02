package tlc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fixtures are real TLC outputs, each cut from a run on a bench:
//
//	pass.log          MCMemberFixedPoint, exit 0
//	invariant.log     MCTableOnePlace, exit 12
//	action.log        MCEpochMemberBrokenStale, exit 13
//	temporal-new.log  MCTableSessionBrokenTermLive on a current TLC, exit 13
//	temporal-old.log  the same on TLC 2.19, exit 13
//	parsefail.log     a module TLC could not find, exit 150
//	initial-new.log   MCFuseBoxBrokenAbsentClear on a current TLC, exit 12: the
//	                  invariant is violated by the initial state, and no count is printed
//	initial-old.log   the same on TLC 2.19
func fixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return string(raw)
}

const termCfg = "SPECIFICATION Spec\nPROPERTY TermEnds\n"

func TestParseReadsTheStatisticsAndTheViolation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		file       string
		completed  bool
		generated  string
		distinct   string
		violations []Violation
	}{
		{"pass.log", true, "15518", "263", nil},
		{"invariant.log", false, "3", "3", []Violation{{"Invariant", "OnePlacePerTable"}}},
		{"action.log", false, "10", "5", []Violation{{"Action property", "StaleWritesRefuse"}}},
		{"temporal-new.log", false, "358995", "105110", []Violation{{"Temporal property", "TermEnds"}}},
		{"temporal-old.log", false, "", "", []Violation{{"Temporal property", ""}}},
		{"parsefail.log", false, "-", "-", nil},
		{"initial-new.log", false, "1", "1", []Violation{{"Invariant", "GateAnswersOnlyFromEveryNamedBox"}}},
		{"initial-old.log", false, "1", "1", []Violation{{"Invariant", "GateAnswersOnlyFromEveryNamedBox"}}},
	}
	for _, c := range cases {
		o := Parse(fixture(t, c.file))
		assert.Equal(t, c.completed, o.Completed, "%s: completed = %v, want %v", c.file, o.Completed, c.completed)
		if c.generated != "" && (o.Generated != c.generated || o.Distinct != c.distinct) {
			t.Errorf("%s: stats = %s/%s, want %s/%s", c.file, o.Generated, o.Distinct, c.generated, c.distinct)
		}
		if len(o.Violations) != len(c.violations) {
			t.Errorf("%s: violations = %v, want %v", c.file, o.Violations, c.violations)
			continue
		}
		for i, v := range c.violations {
			if o.Violations[i] != v {
				t.Errorf("%s: violation %d = %v, want %v", c.file, i, o.Violations[i], v)
			}
		}
	}
}

func TestAnInitialStateViolationCountsTheOneStateTLCPrinted(t *testing.T) {
	t.Parallel()
	o := Parse(fixture(t, "initial-new.log"))
	require.True(t, o.InitialState, "outcome = %+v", o)
	require.True(t, o.HasStats(), "outcome = %+v", o)
	// A violation later in the search keeps TLC's own count.
	later := Parse(fixture(t, "invariant.log"))
	require.False(t, later.InitialState, "outcome = %+v", later)
	require.Equal(t, "3", later.Generated, "outcome = %+v", later)
}

func TestParseKeepsTheTotalsNotTheProgressLine(t *testing.T) {
	t.Parallel()
	// temporal-new.log holds a progress line of 358,995 and 105,110 and then
	// the totals. The totals are the last pair; a run that ended between
	// progress lines must report them, not the earlier count.
	out := "Progress(1) at t: 1,000 states generated, 500 distinct states found, 9 states left on queue.\n" +
		"2500 states generated, 700 distinct states found, 0 states left on queue.\n"
	o := Parse(out)
	require.Equal(t, "2500", o.Generated, "stats = %s/%s, want 2500/700", o.Generated, o.Distinct)
	require.Equal(t, "700", o.Distinct, "stats = %s/%s, want 2500/700", o.Generated, o.Distinct)
	got := Parse("no counts here\n")
	require.False(t, got.HasStats(), "a log with no counts has stats %s/%s", got.Generated, got.Distinct)
}

func TestAcceptsRequiresTheDeclaredExitAndTheDeclaredName(t *testing.T) {
	t.Parallel()
	pass := Case{Expected: "pass", Property: "-"}
	inv := Case{Expected: "invariant", Property: "OnePlacePerTable"}
	act := Case{Expected: "action", Property: "StaleWritesRefuse"}
	tests := []struct {
		name string
		c    Case
		code int
		log  string
		cfg  string
		want bool
	}{
		{"pass", pass, 0, fixture(t, "pass.log"), "", true},
		{"pass with a violation exit", pass, 12, fixture(t, "pass.log"), "", false},
		{"pass that timed out", pass, 124, fixture(t, "pass.log"), "", false},
		{"pass with no completion line", pass, 0, fixture(t, "parsefail.log"), "", false},
		{"invariant", inv, 12, fixture(t, "invariant.log"), "", true},
		{"invariant with the wrong exit", inv, 13, fixture(t, "invariant.log"), "", false},
		{"invariant with a timeout", inv, 124, fixture(t, "invariant.log"), "", false},
		{"invariant named among several", Case{Expected: "invariant", Property: "Other|OnePlacePerTable"}, 12, fixture(t, "invariant.log"), "", true},
		{"a different invariant", Case{Expected: "invariant", Property: "Different"}, 12, fixture(t, "invariant.log"), "", false},
		{"a prefix of the name is not the name", Case{Expected: "invariant", Property: "OnePlace"}, 12, fixture(t, "invariant.log"), "", false},
		{"invariant violated by the initial state, current TLC", Case{Expected: "invariant", Property: "GateAnswersOnlyFromEveryNamedBox"}, 12, fixture(t, "initial-new.log"), "", true},
		{"invariant violated by the initial state, TLC 2.19", Case{Expected: "invariant", Property: "GateAnswersOnlyFromEveryNamedBox"}, 12, fixture(t, "initial-old.log"), "", true},
		{"a different invariant violated by the initial state", inv, 12, fixture(t, "initial-new.log"), "", false},
		{"invariant named for an action log", inv, 12, fixture(t, "action.log"), "", false},
		{"action", act, 13, fixture(t, "action.log"), "", true},
		{"action with the invariant exit", act, 12, fixture(t, "action.log"), "", false},
		{"a parse failure is never a result", inv, 150, fixture(t, "parsefail.log"), "", false},
		{"temporal, current TLC", Case{Expected: "temporal", Property: "TermEnds"}, 13, fixture(t, "temporal-new.log"), termCfg, true},
		{"temporal, TLC 2.19", Case{Expected: "temporal", Property: "TermEnds"}, 13, fixture(t, "temporal-old.log"), termCfg, true},
		{"temporal with the wrong exit", Case{Expected: "temporal", Property: "TermEnds"}, 12, fixture(t, "temporal-new.log"), termCfg, false},
		{"temporal whose config selects another property", Case{Expected: "temporal", Property: "TermEnds"}, 13, fixture(t, "temporal-new.log"), "PROPERTY Other\n", false},
		{"temporal whose config selects several", Case{Expected: "temporal", Property: "TermEnds"}, 13, fixture(t, "temporal-old.log"), "PROPERTIES TermEnds Another\n", false},
		{"temporal whose log names another property", Case{Expected: "temporal", Property: "Other"}, 13, fixture(t, "temporal-new.log"), "PROPERTY Other\n", false},
		{"temporal with no property line", Case{Expected: "temporal", Property: "TermEnds"}, 13, fixture(t, "temporal-old.log"), "SPECIFICATION Spec\n", false},
		{"temporal with no temporal violation", Case{Expected: "temporal", Property: "TermEnds"}, 13, fixture(t, "action.log"), termCfg, false},
		{"an unknown kind", Case{Expected: "nonsense", Property: "-"}, 0, fixture(t, "pass.log"), "", false},
	}
	for _, tc := range tests {
		got := Accepts(tc.c, tc.code, tc.log, tc.cfg)
		assert.Equal(t, tc.want, got, "%s: Accepts = %v, want %v", tc.name, got, tc.want)
	}
}

func TestBudgetNotesAreNeverAResult(t *testing.T) {
	t.Parallel()
	for _, note := range []string{
		"TLC suite budget exhausted before starting this case\n",
		"TLC suite budget exhausted during this case\n",
	} {
		if Accepts(Case{Expected: "pass", Property: "-"}, ExitTimeout, note, "") || Parse(note).HasStats() {
			t.Errorf("%q was read as a result", strings.TrimSpace(note))
		}
	}
}
