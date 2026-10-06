package sprint

import (
	"regexp"
	"strings"
)

// The cause of a failed attempt (docs/SPEC-SPRINT.md section 1, "The cause of a failed
// attempt"), recorded on its work card (FieldCause) at the moment it fails and counted in the
// column of its member's row it is finished into (CauseColumn). The owner, 2026-10-05, reading
// 72 to 76 percent ok on the friends table: "75% seems really low." Nearly every HOLD of that
// night came back as a twin that landed: the work was right and the brief or the machinery was
// wrong, yet each counted against the friend. ok% counts the work's own faults alone.
const (
	// CauseWork is the worker's own fault: a wrong fix, a false claim, a missing test. The
	// default: a failure that names neither of the others is the work's.
	CauseWork = "work"
	// CauseBrief is a brief the worker could not satisfy as written: a PATHS or STOP it could
	// not hold, a name the guardrail refused, a bound the twin corrects (AtBriefBound).
	CauseBrief = "brief"
	// CauseMachinery is the sprint's machinery failing the attempt: no worktree or remote to
	// push from, a lane that died without a report, a packet with no tier, a daemon that did
	// not deliver.
	CauseMachinery = "machinery"
)

// FieldCause is a failed work card's cause (CauseWork, CauseBrief or CauseMachinery), set
// when it is finished failed or when a reader's broken read makes its ok finish failed work
// (Rework).
const FieldCause = "cause"

// Causes is every cause, in the order the counters and the summary line show them.
var Causes = []string{CauseWork, CauseBrief, CauseMachinery}

// DoneCols is every column of a member's row a finished work card is in: ok, then a column
// per cause (CauseColumn).
var DoneCols = []string{DoneOK, DoneFailed, DoneBrief, DoneMachinery}

// FaultCols is the columns of the failed finished work cards, one per cause.
var FaultCols = []string{DoneFailed, DoneBrief, DoneMachinery}

// CauseColumn is the column a work card failed for cause is finished into: failed for the
// work's own fault (so the column ok% has always read keeps its meaning), brief and
// machinery for the others.
func CauseColumn(cause string) string {
	switch cause {
	case CauseBrief:
		return DoneBrief
	case CauseMachinery:
		return DoneMachinery
	}
	return DoneFailed
}

// IsCause says word is one of the causes.
func IsCause(word string) bool {
	return word == CauseWork || word == CauseBrief || word == CauseMachinery
}

// briefWords are the report's words of a brief the worker could not satisfy as written: files
// outside its PATHS (the lander's E12, a worker's HOLD, its PATHS-PROPOSED line), a STOP the
// PATHS cannot hold, a name a guardrail refused, a brief defect said outright.
var briefWords = regexp.MustCompile(`(?i)outside (of )?(the |its |this |the brief's |the card's )?(card's |brief's )?PATHS|PATHS-PROPOSED:|\(E12\)|PATHS cannot hold|cannot hold its STOP|brief defect|guardrail`)

// machineryWords are the report's words of the machinery failing the attempt: nothing to push
// from or to, a lane or child that left no report, a packet with no tier, a daemon or bus that
// did not deliver. A report with no verdict word is the worker's own (it wrote a report).
var machineryWords = regexp.MustCompile(`(?i)no (git )?(remote|worktree|push)\b|without a report|no report\b|(lane|child|daemon|session) (has )?died|no tier\b|did not deliver|not delivered`)

// FailureCause is the cause of a failed finish: given, when the finish names one; else brief
// at the brief's bound (atBound) or when the report names a brief it could not satisfy; else
// machinery when the report names the machinery failing it; else the work's.
func FailureCause(given, report string, atBound bool) string {
	switch {
	case IsCause(given):
		return given
	case atBound, briefWords.MatchString(report):
		return CauseBrief
	case machineryWords.MatchString(report):
		return CauseMachinery
	}
	return CauseWork
}

// CauseCounts is the failed attempts of a fleet table by cause, every row (machines and
// friends): the counters the summary line totals (CauseLine).
func CauseCounts(fleet *Table) map[string]int {
	out := map[string]int{}
	for _, c := range fleet.Column(FaultCols...) {
		out[causeOfColumn(c.Col)]++
	}
	return out
}

func causeOfColumn(col string) string {
	switch col {
	case DoneBrief:
		return CauseBrief
	case DoneMachinery:
		return CauseMachinery
	}
	return CauseWork
}

// CauseLine is the per-cause totals in one line, as the summary shows them under the sprint's
// line: "failed attempts: work 3 · brief 41 · machinery 20".
func CauseLine(counts map[string]int) string {
	parts := make([]string, 0, len(Causes))
	for _, c := range Causes {
		parts = append(parts, c+" "+itoa(counts[c]))
	}
	return "failed attempts: " + strings.Join(parts, " · ")
}
