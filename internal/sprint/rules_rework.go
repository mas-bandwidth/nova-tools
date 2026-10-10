package sprint

import (
	"strconv"
	"strings"
)

// The rework engine (docs/SPEC-SPRINT.md section 8, answered by rule; the owner,
// 2026-10-07 at 10:35 PM: "Seems like we have a bottleneck in fix" and "what do we need to do
// to fix the sprint machine so this doesn't happen again?"). On that day 150 of the 171 cards
// in review were failed attempts awaiting a rework: the rules had answered 1 of 294 judgments,
// and every one of them waited on the seat for up to 13 hours. The engine here is what the
// rules use to open the next attempt of a finish that is not ok without a judgment: a failed
// report, a reader's broken verdict, a harness fault, a conflict, a deadline. The next attempt
// carries the finding (the reader's finding or the worker's HOLD or report) as its fix, the
// card goes on its way with it, and the judgment the finish raised is closed in the same tick,
// so the seat never holds a "work came back failed" or "a reader found it broken" judgment
// again. The group judgment is retired: the tick answers each finish on its own card.
//
// At the brief's bound the card is parked in fix instead (ActPark): the same finding by its
// first line, normalised (SameFinding), comes back twice, or the card reaches its rework
// bound (ReworkBound, default ReworkBoundDefault). The machine applies what it can first (the
// widen rule, the paths rule: PATHS-PROPOSED, NEEDS, TIER, GATE-HOST, the lint's own fix
// lines); a finding it cannot apply becomes one judgment per card, never per attempt, its text
// the BRIEF line (BriefParkLine) and its two choices brief in place and drop. The card keeps
// its fix, the BRIEF line, so the seat reads the exact finding it must answer.

// The rework settings (docs/SPEC-SPRINT.md section 8). PropReworkBound is the work table's
// property holding the sprint's rework bound, a stream's control card field (PropReworkBound
// read off the control card) over it; ReworkBoundDefault is the bound when neither is set. It
// is the attempts one brief may run before the card is parked in fix: the card the same
// finding stops is parked at two whatever the bound, and a bound under two is no bound, since
// one finding is never a repeat.
const (
	PropReworkBound    = "rework_bound"
	ReworkBoundDefault = 6
)

// FieldFix is the field a rework's finding rides on the card and its next attempt: the read
// a worker is handed first. It is also what a parked card carries, the BRIEF line.
const FieldFix = "fix"

// ActPark is the rework engine's act at the brief's bound: the card stays in review in fix,
// its fix the BRIEF line, its finding the one judgment left for the seat, and nothing deals
// it again until the brief is changed or the card is dropped. The v1 "rework with a fix" a
// person answers with is denied nothing it does not already deny: a --fix changes the brief
// not at all (brief_bound.go).
const ActPark = "park in fix"

// The finishes the engine answers are the judgment types a finish that is not ok raises, in
// the order the seat reads them: a failed report or a harness fault (NWorkFailed), a reader's
// broken verdict (NReadBroken), a head the lander returned (NReturned, a conflict), a deadline
// (NWorkLate), and the bound (NBound, NBriefWrong). Each is answered by a rule
// (RuleAnswers); none is left as the seat's group judgment.

// ReworkBound is the attempts one brief may run in the stream before its card is parked in
// fix: the stream's control card field, else the sprint's work table property, else
// ReworkBoundDefault. A bound under two is no bound, one finding being no repeat.
func (s *Snapshot) ReworkBound(stream string) int {
	if s != nil && s.Merge != nil {
		if n := s.StreamCtl(stream).Int(PropReworkBound); n >= 2 {
			return min(n, AttemptsMax)
		}
	}
	if s != nil && s.Work != nil {
		if v, ok := s.Work.Prop(PropReworkBound); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 2 {
				return min(n, AttemptsMax)
			}
		}
	}
	return ReworkBoundDefault
}

// BriefParkLine is the line a card parked in fix carries and the seat reads: the brief's id
// and the finding that stopped it, its first sentence, normalised. A parked card whose
// finding is unreadable still says the brief is wrong, not the worker.
func BriefParkLine(id, finding string) string {
	if line := firstSentence(finding); line != "" {
		return "BRIEF " + id + ": " + line
	}
	return "BRIEF " + id + ": the brief is wrong, not the worker"
}

// ReworkFinding is the finding a non-ok finish of typ carries on pr, the text the next attempt
// is handed as its fix: a reader's finding for NReadBroken, else the card's own fix (a failed
// work card's report, or the HOLD), else the failure class the finish recorded. "" when the
// finish left none, and the caller falls back to the card's own fix.
func ReworkFinding(s *Snapshot, typ string, pr *Card) string {
	if pr == nil {
		return ""
	}
	if typ == NReadBroken {
		if f := brokenFindings(s, pr); f != "" {
			return f
		}
	}
	if f := ownFix(s, pr); f != "" {
		return f
	}
	return pr.F(FieldFailure)
}

// boundFinding is the finding a bound's park carries: the repeated finding the bound names,
// else the card's own finding (the last attempt's), else the finish's failure class.
func boundFinding(s *Snapshot, pr *Card, bb BriefBound) string {
	if bb.Finding != "" {
		return bb.Finding
	}
	if f := pr.F("finding"); f != "" {
		return f
	}
	return ReworkFinding(s, NWorkFailed, pr)
}

// parkAnswer fills a with the park: the card in fix, its fix the BRIEF line, and the rule's
// why. The tick's brief part applies it (TickRuleBrief), keeping one judgment per card.
func parkAnswer(a *RuleAnswer, pr *Card, finding, why string) {
	a.Card = pr.ID
	a.fix = BriefParkLine(pr.ID, finding)
	a.Act, a.Why = ActPark, why
}
