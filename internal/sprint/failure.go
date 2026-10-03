package sprint

import (
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// Rule 2 of the sprint's cost rules (nova-tools#5174; the owner, 2026-10-02): "Escalate on
// the second identical failure, not the third." A card whose second try fails the way its
// first did is not tried a third time on its tier: the judgment "a card reached its bound"
// is raised at once. Two places count tries: the takes of one attempt's work card that
// ended with no work to judge (the redeals, redealBound) and the attempts of a primary
// whose work came back failed (Finish, AtIdenticalFailure). Identical is one function,
// SameFailure.

// FailureClass is what a failed end is compared by: a take whose child left no result is
// the class `no result` whatever its line says; any other end is its reason, the first
// line up to the member's "; " (member.Judge puts its reason first and the child's report
// after it: `verdict not-done; <the child's line>`), trimmed and cut to
// MaxProviderErrorBytes. A verdict's reason is broad, so its class takes the first
// VerdictWords words of the child's line too (after the member's `pushed=<sha> to
// <branch>: `). A take the provider failed, a launch refused at staging or at launch are
// the member's or the provider's, never the card's (docs/SPEC-SPRINT.md section 2), so
// they have no class, and an empty line (a take that ended with its member down) has
// none: an end with no class is never the same as another.
func FailureClass(line string) string {
	line = strings.TrimSpace(line)
	switch {
	case line == "", IsProviderFailure(line), IsStagingRefusal(line), strings.HasPrefix(line, cardhdr.EndLaunch):
		return ""
	case IsNoResult(line):
		return cardhdr.EndNoResult
	}
	line, _, _ = strings.Cut(line, "\n")
	reason, child, _ := strings.Cut(line, "; ")
	reason = strings.TrimSpace(strings.TrimSuffix(reason, ";")) // a "; " with nothing after it, trimmed
	if strings.HasPrefix(reason, "verdict ") {
		if strings.HasPrefix(child, "pushed=") {
			_, child, _ = strings.Cut(child, ": ")
		}
		if words := strings.Fields(child); len(words) > 0 {
			reason += "; " + strings.Join(words[:min(len(words), VerdictWords)], " ")
		}
	}
	return cutText(reason, MaxProviderErrorBytes)
}

// VerdictWords is how many words of the child's line a verdict's class keeps (FailureClass).
const VerdictWords = 3

// SameFailure says two failed ends are the same failure: each has a class and it is the
// same one (FailureClass).
func SameFailure(a, b string) bool {
	c := FailureClass(a)
	return c != "" && c == FailureClass(b)
}

// takeEndLine is an ended take's record as a finish report said it: a take whose child
// left no result keeps its kind in its line (takeEnded); a take the provider failed keeps
// the provider's line alone, so its kind is put back.
func takeEndLine(t ProviderTake) string {
	if IsNoResult(t.Error) {
		return t.Error
	}
	return cardhdr.EndProvider + ": " + t.Error
}

// identicalEnds is the class of the withdrawn work card's last two ended takes when they
// ended the same way (SameFailure), "" when they did not: the take that ended last (its
// record is the card's redeals plus one, takeEnded) and the one before it. A take that
// ended with its member down keeps no record, so it is never the same as another.
func identicalEnds(wc *Card) string {
	if wc.F(FieldTakeEnded) == "" {
		return ""
	}
	n := wc.Int("redeals") + 1
	last, before := wc.F(FieldProviderTake+itoa(n)), wc.F(FieldProviderTake+itoa(n-1))
	if n < 2 || last == "" || before == "" {
		return ""
	}
	a, b := parseTake(last), parseTake(before)
	if !SameFailure(takeEndLine(a), takeEndLine(b)) {
		return ""
	}
	return FailureClass(takeEndLine(a))
}

// parseTake is one ended take's record (ProviderTake.String).
func parseTake(v string) ProviderTake {
	f := append(strings.SplitN(v, "\t", 7), "", "", "", "", "", "", "")
	return ProviderTake{Route: f[0], Model: f[1], Member: f[2], Finished: f[3], Usage: f[4], Error: f[5], Taken: f[6]}
}

// The primary's record of its failed work, written by the failed finish (Finish): the
// class of the last failed attempt (FailureClass of its report) and that attempt, and the
// attempt whose failure was the same as the attempt before's (rule 2): the tick holds the
// bound's judgment on the primary while it stays in review at that attempt
// (AtIdenticalFailure).
const (
	FieldFailure     = "failure"
	FieldFailureAt   = "failure_at"
	FieldIdenticalAt = "identical_at"
)

// failureSet is what a failed finish at attempt writes on its primary pr (FieldFailure,
// FieldFailureAt) and whether it is the second identical failure: the attempt before
// failed with the same class.
func failureSet(pr *Card, attempt int, report string, set map[string]string) (identical bool) {
	class := FailureClass(report)
	identical = class != "" && attempt > 1 && pr.Int(FieldFailureAt) == attempt-1 && pr.F(FieldFailure) == class
	set[FieldFailure], set[FieldFailureAt] = class, itoa(attempt)
	if identical {
		set[FieldIdenticalAt] = itoa(attempt)
	}
	return identical
}

// AtIdenticalFailure is the primary's failed work card when its attempt failed the way the
// attempt before did (FieldIdenticalAt): it is in review, its work came back failed, and
// the bound's judgment holds it until a rework (a new attempt) or a drop. nil when not.
func AtIdenticalFailure(s *Snapshot, pr *Card) *Card {
	if pr == nil || !pr.Placed() || pr.Col != Review || pr.F("result") != "failed" || pr.F(FieldIdenticalAt) == "" || pr.F(FieldIdenticalAt) != pr.F("attempt") {
		return nil
	}
	return s.Fleet.Placed(WorkCardID(pr.ID, pr.Int("attempt")))
}

// identicalWorkWhat is the bound's judgment on a primary whose attempt failed the way the
// one before did: the finish writes it and the tick holds it, so both say it in the same
// words (a condition is keyed by what it says).
func identicalWorkWhat(card string, attempt int, class, primary string) string {
	return fmt.Sprintf("%s: attempts %d and %d failed the same way (%s), the second identical failure: not reworked on the same tier a third time; rework it with a fix on the next tier, or drop it; its history: nova-sprint log --card %s",
		card, attempt-1, attempt, class, primary)
}

// boundWhat is the bound's judgment on a ready primary whose withdrawn work card wc the
// deal places no more (AtRedealBound): its last two takes ended the same way (rule 2), or
// it was redealt MaxRedeals times.
func boundWhat(wc *Card, primary string) string {
	if class := identicalEnds(wc); class != "" {
		return fmt.Sprintf("%s: attempt %s: its last two takes ended the same way (%s), the second identical failure: not dealt a third time on its tier; its history: nova-sprint log --card %s",
			wc.ID, wc.F("attempt"), class, primary)
	}
	return fmt.Sprintf("%s: attempt %s was redealt %d times, its bound, and is not dealt again%s; its history: nova-sprint log --card %s", wc.ID, wc.F("attempt"), wc.Int("redeals"), providerWhy(wc), primary)
}
