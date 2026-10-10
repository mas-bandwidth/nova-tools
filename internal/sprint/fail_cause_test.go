package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A failed end names its cause (docs/SPEC-SPRINT.md section 11, ok-percent-names-its-cause:
// the cause of a failed end): brief, machinery or work, tested in that order, so a HOLD that
// names a brief defect is the brief's even when it also names an infrastructure fault.
func TestAFailedEndNamesItsCause(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name, end, cause string
	}{
		{"a HOLD naming a brief defect", "friend amy HOLD: **HOLD: brief defect. The card's PATHS cannot hold its STOP.**", CauseBrief},
		{"a launch refused", "launch refused: card x.w5's route heavy-opus-claude runs under claude, which is on no PATH entry of this member", CauseMachinery},
		{"a lane that died without a report", "the runner ended job x.w1 with no report", CauseMachinery},
		{"a HOLD naming only a cost line", "friend amy HOLD: Cost: $0.19 (list price, route flash-deepseek41-direct)", CauseMachinery},
		{"a HOLD naming a brief defect and an infrastructure fault", "friend amy HOLD: The last attempt ended on an infrastructure fault: launch refused. The brief's PATHS do not hold the code it names.", CauseBrief},
		{"a wrong fix", "internal/sprint/x.go:12 the fix does not change the behaviour", CauseWork},
		{"an empty end", "", ""},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.cause, FailCause(tc.end))
		})
	}
}

// CauseCounts turns a row's counts into the ok% the tables show (docs/SPEC-SPRINT.md section 11,
// ok-percent-names-its-cause: the cause of a failed end): ok over ok plus work faults, the brief
// and machinery counts beside it, and a day's per-cause line.
func TestCauseCountsOkPctCountsOnlyWorkFaults(t *testing.T) {
	t.Parallel()
	c := CauseCounts{OK: 3, Work: 1, Brief: 4, Machinery: 2}
	pct, ok := c.OkPct()
	require.True(t, ok)
	assert.Equal(t, 75, pct)
	assert.Equal(t, "75% b4 m2", c.Cell())
	assert.Equal(t, "causes 2026-10-06 ok=3 brief=4 machinery=2 work=1", c.DayLine("2026-10-06"))

	zero := CauseCounts{}
	pct, ok = zero.OkPct()
	assert.False(t, ok)
	assert.Equal(t, 0, pct)
	assert.Equal(t, "-", zero.Cell())

	onlyFaults := CauseCounts{Brief: 4, Machinery: 2}
	_, ok = onlyFaults.OkPct()
	assert.False(t, ok)
	assert.Equal(t, "-", onlyFaults.Cell())
}

// Add counts one more end by its cause: an ok finish (the empty cause) counts OK.
func TestCauseCountsAdd(t *testing.T) {
	t.Parallel()
	var c CauseCounts
	c.Add("ok")
	c.Add(CauseBrief)
	c.Add(CauseMachinery)
	c.Add(CauseWork)
	c.Add(CauseWork)
	assert.Equal(t, CauseCounts{OK: 1, Brief: 1, Machinery: 1, Work: 2}, c)
}
