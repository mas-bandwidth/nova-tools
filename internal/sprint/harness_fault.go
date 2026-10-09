package sprint

import (
	"fmt"
	"regexp"
	"strings"
)

// A harness fault is the harness's, never the card's (docs/SPEC-SPRINT.md section 8, the
// rules table's row failed; tla/SprintRules.tla, Part "answers"). The seat's sweep of the
// failed pile on 2026-10-06 found most failed attempts in review were no finding about the
// work at all: a lane killed, a report with no verdict or a placeholder one, a LAND with no
// Head, a provider's 5xx, a push refused, a bench mirror missing. Each waited on the seat,
// whose one answer was always the same: rework it with the failure as its fix. The failed
// rule gives that answer itself, on the card's tier, a friend's card too, up to its brief's
// bound; a HOLD whose report carries findings (a file and line) is reworked with them. A
// failure outside every class stays a judgment.

// harnessFaultClasses is each harness fault's class and the words that say it, in the order
// a report is matched: the first class whose pattern matches names it.
var harnessFaultClasses = []struct {
	class string
	re    *regexp.Regexp
}{
	{"lane died", regexp.MustCompile(`the runner ended job|lane ended with no report|the lane died`)},
	{"no step line", regexp.MustCompile(`carries no line for this step`)},
	{"not started", regexp.MustCompile(`\bHOLD: not started\b`)},
	{"staging", regexp.MustCompile(`(?i)refused at staging|^staging refused`)},
	{"push refused", regexp.MustCompile(`(?i)push (was )?refused|not on the staged commit`)},
	{"staged base tip", regexp.MustCompile(`(?i)staged base tip`)},
	{"no head", regexp.MustCompile(`\bLAND with no Head\b`)},
	{"verdict pending", regexp.MustCompile(`(?i)\bverdict:? pending\b`)},
	{"verdict none", regexp.MustCompile(`\bverdict none\b`)},
	{"cost line", regexp.MustCompile(`(^|: |; )Cost: `)},
	{"provider 5xx", regexp.MustCompile(`(?i)^provider failure|\b5[0-9][0-9] (bad gateway|internal server error|service unavailable|gateway timeout)\b|\b(status|http|code)[ :=]*5[0-9][0-9]\b|internal server error|bad gateway|service unavailable|gateway timeout`)},
	{"deadline", regexp.MustCompile(`(?i)deadline[^.;]*no (result|report)|\bcapped at [0-9]`)},
	{"no result", regexp.MustCompile(`(?i)^no result:|no RESULT\.md`)},
}

// HarnessFault is the class of a failed report that is a harness fault, "" when it is none.
func HarnessFault(report string) string {
	for _, c := range harnessFaultClasses {
		if c.re.MatchString(report) {
			return c.class
		}
	}
	return ""
}

// findingLineRE is a finding in a HOLD: a repository file and a line in it.
var findingLineRE = regexp.MustCompile(`\b[A-Za-z0-9_.-]+(/[A-Za-z0-9_.+-]+)*\.[A-Za-z]{1,8}:[0-9]+\b`)

// HoldFindings says a failed report is a HOLD that carries findings: the verdict HOLD, and a
// file and line it names.
func HoldFindings(report string) bool {
	return reportHolds(report) && findingLineRE.MatchString(report)
}

// HarnessFix is the fix of an attempt that ended on a harness fault: the fault, and the
// attempt done again from the staged tip, where its work may stand already.
func HarnessFix(attempt, class, report string) string {
	return cutText(fmt.Sprintf("attempt %s ended on a harness fault (%s), not a finding: %s; start from the staged tip (the work may be on the branch already), check what the branch holds, complete the card, run its gate, push, and report LAND with its full Head",
		attempt, class, strings.TrimSpace(report)), MaxCardTextBytes)
}

// ruleHarness: the primary's work came back failed on a harness fault, or on a HOLD with
// findings: reworked at once on its tier with the failure as its fix, whoever worked it and
// however many cards failed the same way, up to its brief's bound (then the brief's
// judgment stands). It says whether it answered a; a failure of neither kind is the failed
// rule's as before.
func ruleHarness(s *Snapshot, a *RuleAnswer, pr *Card) bool {
	report := workReport(s, pr)
	if report == "" {
		report = a.open.Note.What
	}
	class := HarnessFault(report)
	if class == "" && !HoldFindings(report) {
		return false
	}
	a.Rule = RuleFailed
	if pr.F(FieldBriefDefect) != "" {
		left(a, mindCard(pr))
		return true
	}
	if bb, ok := AtBriefBound(pr, "", s.AttemptsCap(pr.Row)); ok {
		left(a, bb.String())
		return true
	}
	a.Card, a.Act = pr.ID, ActRework
	if class == "" {
		a.fix = cutText(report, MaxCardTextBytes)
		a.Why = fmt.Sprintf("attempt %s held with findings: they are the fix, on the same tier", pr.F("attempt"))
	} else {
		a.fix = HarnessFix(pr.F("attempt"), class, report)
		a.Why = fmt.Sprintf("attempt %s ended on a harness fault (%s): the failure is the fix, on the same tier", pr.F("attempt"), class)
	}
	a.set = map[string]string{FieldNote: cutText(RuleSaid(RuleFailed, a.Act+": "+a.Why), MaxCardTextBytes)}
	return true
}
