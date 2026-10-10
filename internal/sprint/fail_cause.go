package sprint

import (
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// The three causes a failed attempt's end names (docs/SPEC-SPRINT.md section 11,
// ok-percent-names-its-cause: the cause of a failed end). Brief is the brief's: the base
// or the PATHS could not hold the work. Machinery is the harness's: no worktree or remote,
// a lane that died without a report, a launch refused. Work is the worker's: a wrong fix,
// a false claim, a missing test, a reader's broken finding.
const (
	CauseBrief     = "brief"
	CauseMachinery = "machinery"
	CauseWork      = "work"
)

// FailCause names the cause of a failed attempt's end (docs/SPEC-SPRINT.md section 11,
// ok-percent-names-its-cause: the cause of a failed end): "" for an empty end; CauseBrief
// when the end names a brief defect (BriefDefectOf); else CauseMachinery when it names a
// harness fault (HarnessFault), a provider failure, a staging refusal or no result (the
// Is* detectors), or the end begins with cardhdr.EndLaunch; else CauseWork. Brief is tested
// first: a HOLD that names a brief defect is the brief's even when it also mentions an
// infrastructure fault.
func FailCause(end string) string {
	switch {
	case end == "":
		return ""
	case BriefDefectOf(end) != "":
		return CauseBrief
	case HarnessFault(end) != "", IsProviderFailure(end), IsStagingRefusal(end), IsNoResult(end),
		strings.HasPrefix(end, cardhdr.EndLaunch):
		return CauseMachinery
	default:
		return CauseWork
	}
}

// CauseCounts is a row's finished attempts by cause (docs/SPEC-SPRINT.md section 11,
// ok-percent-names-its-cause: the cause of a failed end): OK ok finishes, Work work faults,
// and the brief and machinery counts that do not count in the ok%, beside it.
type CauseCounts struct {
	OK, Brief, Machinery, Work int
}

// Add counts one more end of the given cause: a CauseBrief, CauseMachinery or CauseWork,
// else (an ok finish, the empty cause) OK.
func (c *CauseCounts) Add(cause string) {
	switch cause {
	case CauseBrief:
		c.Brief++
	case CauseMachinery:
		c.Machinery++
	case CauseWork:
		c.Work++
	default:
		c.OK++
	}
}

// OkPct is the ok% the tables show (docs/SPEC-SPRINT.md section 11, ok-percent-names-its-cause:
// the cause of a failed end): 100*OK/(OK+Work) rounded down, counting only work the worker
// could do; false when OK+Work is 0.
func (c CauseCounts) OkPct() (int, bool) {
	if c.OK+c.Work == 0 {
		return 0, false
	}
	return 100 * c.OK / (c.OK + c.Work), true
}

// Cell renders CauseCounts as the tables' ok% cell (docs/SPEC-SPRINT.md section 11,
// ok-percent-names-its-cause: the cause of a failed end): the percent, then the brief and
// machinery counts beside it, each only when non-zero (`75% b3 m2`); `-` when there is no
// percent.
func (c CauseCounts) Cell() string {
	pct, ok := c.OkPct()
	if !ok {
		return "-"
	}
	s := fmt.Sprintf("%d%%", pct)
	if c.Brief > 0 {
		s += fmt.Sprintf(" b%d", c.Brief)
	}
	if c.Machinery > 0 {
		s += fmt.Sprintf(" m%d", c.Machinery)
	}
	return s
}

// DayLine renders CauseCounts as a day's per-cause line (docs/SPEC-SPRINT.md section 11,
// ok-percent-names-its-cause: the cause of a failed end): `causes <YYYY-MM-DD> ok=<n>
// brief=<n> machinery=<n> work=<n>`.
func (c CauseCounts) DayLine(day string) string {
	return fmt.Sprintf("causes %s ok=%d brief=%d machinery=%d work=%d", day, c.OK, c.Brief, c.Machinery, c.Work)
}
