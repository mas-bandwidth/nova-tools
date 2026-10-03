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
// <branch>: `). A take the provider failed, a launch refused at staging or at launch, and a
// gate red only on failures the gate decision classed pre-existing (`pre-existing: <test>`)
// are the member's, the base's or the provider's, never the card's (docs/SPEC-SPRINT.md
// section 2), so they have no class, and an empty line (a take that ended with its member down) has
// none: an end with no class is never the same as another.
func FailureClass(line string) string {
	line = strings.TrimSpace(line)
	switch {
	case line == "", IsProviderFailure(line), IsStagingRefusal(line), strings.HasPrefix(line, cardhdr.EndLaunch),
		strings.HasPrefix(line, cardhdr.EndPreExisting+": "):
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

// The primary's record of its failed work, written by the failed finish (Finish) and by the
// rework of an attempt at its redeal bound (Rework: its takes ended with no work to judge,
// BoundClass): the class of the last failed attempt, that attempt and the tier it ran on (its
// work card's tier: a new tier counts its own failures), and the attempt whose failure was
// the same as the attempt before's (rule 2): the tick holds the bound's judgment on the
// primary while it stays in review at that attempt (AtIdenticalFailure).
const (
	FieldFailure     = "failure"
	FieldFailureAt   = "failure_at"
	FieldFailureTier = "failure_tier"
	FieldIdenticalAt = "identical_at"
)

// failureSet is what a failed finish at attempt on tier writes on its primary pr
// (FieldFailure, FieldFailureAt, FieldFailureTier) and whether it is the second identical
// failure: the attempt before failed the same way on the same tier (sameAsBefore). The
// class is the report's (FailureClass), or decided, the take's attempt decision as
// `decided <class>`, when that decision routed the finish.
func failureSet(pr *Card, attempt int, report, decided, tier string, set map[string]string) (identical bool) {
	class := FailureClass(report)
	if decided != "" {
		class = decided // the attempt decision's class, when it routed the finish (decide.go)
	}
	identical = sameAsBefore(pr, attempt, class, tier)
	set[FieldFailure], set[FieldFailureAt], set[FieldFailureTier] = class, itoa(attempt), tier
	if identical {
		set[FieldIdenticalAt] = itoa(attempt)
	}
	return identical
}

// sameAsBefore says the primary pr's attempt, ended with class on tier, failed the way the
// attempt before it did (the record failureSet and Rework write): rule 2 across attempts.
func sameAsBefore(pr *Card, attempt int, class, tier string) bool {
	return class != "" && attempt > 1 && pr.Int(FieldFailureAt) == attempt-1 && pr.F(FieldFailure) == class && pr.F(FieldFailureTier) == tier
}

// BoundClass is the class of the attempt whose withdrawn work card wc reached its redeal bound,
// as rule 2 across attempts compares it (sameAsBefore): the class of its last two takes when
// they ended the same way (identicalEnds), else the class of the take that ended last. A take
// the provider failed has no class inside an attempt (it is the provider's, and is redealt);
// across attempts it is `provider failure` with the provider's class word when its line has
// one (`provider failure: class=out-of-credit`), so an attempt the provider ended counts toward
// the next attempt's identical failure as a take that left no result does (the coordinator's
// finding of 2026-10-03, docs/SPEC-SPRINT.md section 5). "" when the last take kept no record
// (its member went down): never the same as another.
func BoundClass(wc *Card) string {
	if class := identicalEnds(wc); class != "" {
		return class
	}
	v := wc.F(FieldProviderTake + itoa(wc.Int("redeals")+1))
	if v == "" {
		return ""
	}
	line := takeEndLine(parseTake(v))
	if !IsProviderFailure(line) {
		return FailureClass(line)
	}
	rest := strings.TrimSpace(strings.TrimPrefix(line, cardhdr.EndProvider+":"))
	word, _, _ := strings.Cut(strings.TrimSpace(strings.TrimPrefix(rest, "provider:")), " ")
	if strings.HasPrefix(word, "class=") {
		return cardhdr.EndProvider + ": " + word
	}
	return cardhdr.EndProvider
}

// reworkAtTheSameBound is the refusal of a rework of the primary pr at its redeal bound (wc,
// its withdrawn work card) when the attempt before it ended the same way on the same tier
// (sameAsBefore over BoundClass and the work card's tier) and tier, the rework's --tier, names
// no other tier: a rework there would be its third try at one failure. "" when the rework is
// its next attempt. The coordinator's finding of 2026-10-03: an answer loop reworked one card
// at its bound 231 times, each rework a fresh bound (docs/SPEC-SPRINT.md section 5).
func reworkAtTheSameBound(pr, wc *Card, tier string) string {
	attempt, class, on := pr.Int("attempt"), BoundClass(wc), wc.F(FieldTier)
	if !sameAsBefore(pr, attempt, class, on) || tier != "" && tier != on {
		return ""
	}
	return fmt.Sprintf("attempt %d reached its bound the way attempt %d ended (%s) on tier %s, and is not reworked there a third time: rework it with a fix and --tier <another tier>, drop it (nova-sprint drop %s --reason <why>), or wait",
		attempt, attempt-1, class, orDash(on), pr.ID)
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
