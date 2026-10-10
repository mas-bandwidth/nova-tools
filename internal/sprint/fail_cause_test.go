package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A failed end names its cause (docs/SPEC-SPRINT.md section 11, Statistics,
// ok-percent-names-its-cause): the brief's when a brief defect is named, the
// machinery's when the harness, a provider, staging or a launch failed it, and the
// work's otherwise. Brief is tested first, so a HOLD naming a brief defect is the
// brief's even when it also names an infrastructure fault.
func TestAFailedEndNamesItsCause(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, end, want string
	}{
		{"a brief defect named", "friend amy HOLD: **HOLD: brief defect. The card's PATHS cannot hold its STOP.**", CauseBrief},
		{"a launch refused", "launch refused: card x.w5's route heavy-opus-claude runs under claude, which is on no PATH entry of this member", CauseMachinery},
		{"a lane that died", "the runner ended job x.w1 with no report", CauseMachinery},
		{"a cost line", "friend amy HOLD: Cost: $0.19 (list price, route flash-deepseek41-direct)", CauseMachinery},
		{"brief tested first over a machinery word", "friend amy HOLD: The last attempt ended on an infrastructure fault: launch refused. The brief's PATHS do not hold the code it names.", CauseBrief},
		{"a finding about the work", "internal/sprint/x.go:12 the fix does not change the behaviour", CauseWork},
		{"an empty end", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, FailCause(tc.end))
		})
	}
}

// ok% counts the work's faults only (docs/SPEC-SPRINT.md section 11, Statistics,
// ok-percent-names-its-cause): a brief's or the machinery's failure leaves ok%
// alone. The cell carries the ok% and the brief and machinery counts, and the day
// line carries all four.
func TestCauseCountsOkPctCountsOnlyWorkFaults(t *testing.T) {
	t.Parallel()
	var c CauseCounts
	for i := 0; i < 3; i++ {
		c.Add("")
	}
	c.Add(CauseWork)
	for i := 0; i < 4; i++ {
		c.Add(CauseBrief)
	}
	for i := 0; i < 2; i++ {
		c.Add(CauseMachinery)
	}
	pct, ok := c.OkPct()
	assert.True(t, ok)
	assert.Equal(t, 75, pct)
	assert.Equal(t, "75% b4 m2", c.Cell())
	assert.Equal(t, "causes 2026-10-02 ok=3 brief=4 machinery=2 work=1", c.DayLine("2026-10-02"))

	var zero CauseCounts
	_, ok = zero.OkPct()
	assert.False(t, ok)
	assert.Equal(t, "-", zero.Cell())
	assert.Equal(t, "causes 2026-10-02 ok=0 brief=0 machinery=0 work=0", zero.DayLine("2026-10-02"))
}
