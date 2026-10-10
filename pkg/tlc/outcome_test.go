package tlc

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixture is a real TLC log cut from a bench run: pass (MCMemberFixedPoint),
// invariant (MCTableOnePlace), action (MCEpochMemberBrokenStale), temporal-new
// and temporal-old (MCTableSessionBrokenTermLive, current TLC and 2.19),
// parsefail, initial-new and initial-old (MCFuseBoxBrokenAbsentClear).
func fixture(t *testing.T, name string) string {
	t.Helper()
	return testkit.ReadFile(t, filepath.Join("testdata", name))
}

const termCfg = "SPECIFICATION Spec\nPROPERTY TermEnds\n"

func TestParseReadsTheStatisticsAndTheViolation(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
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
	} {
		t.Run(c.file, func(t *testing.T) {
			t.Parallel()
			o := Parse(fixture(t, c.file))
			assert.Equal(t, c.completed, o.Completed, "%s: completed = %v, want %v", c.file, o.Completed, c.completed)
			if c.generated != "" {
				assert.Equal(t, c.generated, o.Generated, "%s: stats = %s/%s, want %s/%s", c.file, o.Generated, o.Distinct, c.generated, c.distinct)
				assert.Equal(t, c.distinct, o.Distinct, "%s: stats = %s/%s, want %s/%s", c.file, o.Generated, o.Distinct, c.generated, c.distinct)
			}
			assert.Equal(t, c.violations, o.Violations, "%s: violations = %v, want %v", c.file, o.Violations, c.violations)
		})
	}
}

func TestAnInitialStateViolationCountsTheOneStateTLCPrinted(t *testing.T) {
	t.Parallel()
	o := Parse(fixture(t, "initial-new.log"))
	require.True(t, o.InitialState, "outcome = %+v", o)
	require.True(t, o.HasStats(), "outcome = %+v", o)
	later := Parse(fixture(t, "invariant.log"))
	require.False(t, later.InitialState, "outcome = %+v", later)
	require.Equal(t, "3", later.Generated, "outcome = %+v", later)
}

func TestParseKeepsTheTotalsNotTheProgressLine(t *testing.T) {
	t.Parallel()
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
	for _, tc := range []struct {
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
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Accepts(tc.c, tc.code, tc.log, tc.cfg)
			assert.Equal(t, tc.want, got, "%s: Accepts = %v, want %v", tc.name, got, tc.want)
		})
	}
}

func TestBudgetNotesAreNeverAResult(t *testing.T) {
	t.Parallel()
	for _, note := range []string{
		"TLC suite budget exhausted before starting this case\n",
		"TLC suite budget exhausted during this case\n",
	} {
		t.Run(strings.TrimSpace(note), func(t *testing.T) {
			t.Parallel()
			assert.False(t, Accepts(Case{Expected: "pass", Property: "-"}, ExitTimeout, note, ""), "%q was read as a result", strings.TrimSpace(note))
			assert.False(t, Parse(note).HasStats(), "%q was read as a result", strings.TrimSpace(note))
		})
	}
}
