package sprint

import (
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// The three causes a failed end names (docs/SPEC-SPRINT.md section 11, Statistics,
// ok-percent-names-its-cause): the brief's, when the card could not be done as cut;
// the machinery's, when the harness, a provider, staging or a launch failed it; and
// the work's, when the work came back wrong.
const (
	CauseBrief     = "brief"
	CauseMachinery = "machinery"
	CauseWork      = "work"
)

// FailCause is the cause a failed end names (docs/SPEC-SPRINT.md section 11,
// Statistics, ok-percent-names-its-cause): "" for an empty end; CauseBrief when
// BriefDefectOf names a brief defect; else CauseMachinery when HarnessFault names a
// class, or IsProviderFailure, IsStagingRefusal or IsNoResult holds, or the end
// begins with cardhdr.EndLaunch; else CauseWork. Brief is tested first, so a HOLD
// that names a brief defect is the brief's even when it also names an infrastructure
// fault.
func FailCause(end string) string {
	switch {
	case end == "":
		return ""
	case BriefDefectOf(end) != "":
		return CauseBrief
	case HarnessFault(end) != "" || IsProviderFailure(end) || IsStagingRefusal(end) || IsNoResult(end) || strings.HasPrefix(end, cardhdr.EndLaunch):
		return CauseMachinery
	default:
		return CauseWork
	}
}

// CauseCounts is a set of ended work counted by cause (docs/SPEC-SPRINT.md section
// 11, Statistics, ok-percent-names-its-cause): OK is the work that ended ok, and
// Brief, Machinery and Work are the failed ends by FailCause. ok% counts only the
// work's faults, so a brief's or the machinery's failure leaves it alone.
type CauseCounts struct {
	OK, Brief, Machinery, Work int
}

// Add counts one ended work's cause (docs/SPEC-SPRINT.md section 11, Statistics,
// ok-percent-names-its-cause): "" is an ok end, CauseBrief and CauseMachinery are
// their own, and any other cause — CauseWork, or a cause FailCause never returns —
// is the work's.
func (c *CauseCounts) Add(cause string) {
	switch cause {
	case "":
		c.OK++
	case CauseBrief:
		c.Brief++
	case CauseMachinery:
		c.Machinery++
	default:
		c.Work++
	}
}

// OkPct is 100*OK/(OK+Work) rounded down, and whether there is a percent
// (docs/SPEC-SPRINT.md section 11, Statistics, ok-percent-names-its-cause): false
// when OK+Work is 0, so a row with no judged work has no ok%.
func (c CauseCounts) OkPct() (int, bool) {
	if c.OK+c.Work == 0 {
		return 0, false
	}
	return 100 * c.OK / (c.OK + c.Work), true
}

// Cell is the table cell a set of causes shows (docs/SPEC-SPRINT.md section 11,
// Statistics, ok-percent-names-its-cause): the ok%, then the brief and machinery
// counts when non-zero, as `75% b4 m2`; `-` when there is no percent.
func (c CauseCounts) Cell() string {
	pct, ok := c.OkPct()
	if !ok {
		return "-"
	}
	cell := fmt.Sprintf("%d%%", pct)
	if c.Brief > 0 {
		cell += fmt.Sprintf(" b%d", c.Brief)
	}
	if c.Machinery > 0 {
		cell += fmt.Sprintf(" m%d", c.Machinery)
	}
	return cell
}

// DayLine is one day's causes line (docs/SPEC-SPRINT.md section 11, Statistics,
// ok-percent-names-its-cause): `causes <YYYY-MM-DD> ok=<n> brief=<n> machinery=<n>
// work=<n>`.
func (c CauseCounts) DayLine(day string) string {
	return fmt.Sprintf("causes %s ok=%d brief=%d machinery=%d work=%d", day, c.OK, c.Brief, c.Machinery, c.Work)
}
