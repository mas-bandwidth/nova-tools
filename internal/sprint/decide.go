package sprint

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/cardtree"
	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

// NOVA-DECIDE'S LAYER 2 in the sprint (docs/SPEC-SPRINT.md sections 2 and 5; the owner,
// 2026-10-02: "the nova-decide is both sides"; 2026-10-03: "Please push Jev wide."). Two
// decisions, each a typed question set in internal/decide:
//
//   - the attempt decision: after a work take ends, its member asks how it ended (done,
//     nothing-to-do, wrong-scope, no-result, needs-pro, provider-failure) over the brief, the
//     child's RESULT.md and the reason line, when the work card carries the sprint row's
//     decide_attempt bar (the deal writes it); the finish carries the class, its p and the op
//     id. A failed finish whose class is at or above the bar is routed by the class where the
//     reason line's prefix routed it (finishKind); below the bar the prefix rule stands.
//   - the grade decision: before a card's first deal, the server's decide lane grades its
//     convergence from the brief alone (script, flash, pro) and writes it on the card
//     (Grade). It is a hint, shown by `card`, until the sprint row's decide_grade bar is set:
//     then a card graded pro at or above it starts on pro instead of flash, up to its
//     ceiling (startTier).
//
// Every decision is recorded by the server's decide lane, and its outcome is attached when
// the card lands or is dropped (DecideDue): the record trains, and calibrate reads the bars.

// The fields of layer 2.
const (
	// FieldDecideAttempt is a work card's attempt bar, the sprint row's decide_attempt as the
	// deal read it: its member asks the attempt decision when it is set, and the finish
	// routes by the decided class at or above it.
	FieldDecideAttempt = "decide_attempt"
	// FieldDecided is a work card's attempt decision as its last finish carried it
	// (decide.Decided: `<class> p=<p> op=<op>`), and FieldDecidedUsed is "yes" when the
	// finish was routed by it.
	FieldDecided     = "decided"
	FieldDecidedUsed = "decided_used"
	// PrefixDecided keys a primary's record of each attempt decision of its takes:
	// `decided:<op>` = `<class> p=<p> used=<yes|no>`, set once, read by DecideDue.
	PrefixDecided = "decided:"
	// FieldGrade is a primary's grade decision (decide.Decided: `<grade> p=<p> op=<op>`).
	FieldGrade = "grade"
)

// attemptBar is the work card fields the deal writes for the attempt decision: the sprint
// row's bar when it is set, nil when it is not.
func (s *Snapshot) attemptBar() map[string]string {
	if s.DecideAttempt == "" {
		return nil
	}
	return map[string]string{FieldDecideAttempt: s.DecideAttempt}
}

// finishKind is how a failed finish of the work card c is routed (finishPlan): the take's
// end kind (cardhdr.EndProvider and cardhdr.EndNoResult are takes that ended with no work to
// judge; "" is failed work) and the failure class failed work is compared by (FailureClass;
// "" for the reason line's own). A staging refusal and a launch refused are the member's, no
// take ran, and nothing overrides them. A decision at or above the card's bar
// (FieldDecideAttempt) routes by its class: provider-failure and no-result end the take,
// nothing-to-do, wrong-scope and needs-pro are failed work of that class; done is never a
// failed finish's route (the machine cannot land work it was not handed), so the prefix
// rule stands for it, as for every decision under the bar.
func finishKind(c *Card, r FinishReq) (kind, class string, used bool) {
	kind = prefixKind(r.Report)
	if kind == cardhdr.EndStaging || strings.HasPrefix(strings.TrimSpace(r.Report), cardhdr.EndLaunch) {
		return kind, "", false
	}
	d, ok := decide.ParseDecided(r.Decided)
	if !ok || !d.Over(c.F(FieldDecideAttempt)) {
		return kind, "", false
	}
	switch d.Value {
	case decide.ClassProviderFailure:
		return cardhdr.EndProvider, "", true
	case decide.ClassNoResult:
		return cardhdr.EndNoResult, "", true
	case decide.ClassNothingToDo, decide.ClassWrongScope, decide.ClassNeedsPro:
		return "", "decided " + d.Value, true
	}
	return kind, "", false
}

// prefixKind is a failed finish's end kind by its reason line's prefix: the rule a
// decision under its bar leaves standing.
func prefixKind(report string) string {
	switch {
	case IsProviderFailure(report):
		return cardhdr.EndProvider
	case IsNoResult(report):
		return cardhdr.EndNoResult
	case IsStagingRefusal(report):
		return cardhdr.EndStaging
	}
	return ""
}

// decidedSets is what a finish carrying an attempt decision writes: on the work card the
// decision and whether it routed the finish, on the primary its record of the decision
// (PrefixDecided), set once. Nothing when the finish carries none.
func decidedSets(r FinishReq, used bool, card, primary map[string]string) {
	d, ok := decide.ParseDecided(r.Decided)
	if !ok {
		return
	}
	word := map[bool]string{true: "yes", false: "no"}[used]
	card[FieldDecided] = d.String()
	if used {
		card[FieldDecidedUsed] = "yes"
	}
	primary[PrefixDecided+d.Op] = fmt.Sprintf("%s p=%s used=%s", d.Value, strconv.FormatFloat(d.P, 'f', 3, 64), word)
}

// startTier is the tier a card with no tier yet (its first deal on a route) is drawn from:
// flash first, or pro when the sprint row's decide_grade bar is set, the card's grade is pro
// at or above it, and its ceiling is pro (a grade never raises a card above the tier its
// brief or the coordinator gave it).
func (s *Snapshot) startTier(c *Card, m cardhdr.Model) string {
	g, ok := decide.ParseDecided(c.F(FieldGrade))
	if ok && g.Value == decide.GradePro && g.Over(s.DecideGrade) && ceilingTier(c, m) == cardhdr.RoutePro {
		return cardhdr.RoutePro
	}
	return cardhdr.RouteFlash
}

// GradeReq is the decide lane's grades: each card's grade decision as it rides on the card.
type GradeReq struct {
	Grades map[string]decide.Decided
	Who    string
}

// Grade writes each grade on its primary, when the card is still ungraded and never dealt
// (Gradable): a card dealt meanwhile, or graded by an earlier write, is skipped silently,
// since the lane grades what it read a moment before.
func Grade(s *Snapshot, r GradeReq) Plan {
	var p Plan
	for _, id := range slices.Sorted(maps.Keys(r.Grades)) {
		c := s.Work.Placed(id)
		if c == nil || !Gradable(c) {
			continue
		}
		g := r.Grades[id]
		p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.Row, Changes: []Change{change(Work, setEntry(c, map[string]string{FieldGrade: g.String()}))},
			Moved: fmt.Sprintf("%s graded %s p=%s by nova-decide (%s)", c.ID, g.Value, strconv.FormatFloat(g.P, 'f', 2, 64), g.Op)})
	}
	return p
}

// Gradable says the primary c is graded by the decide lane: a card with a brief, not a
// sentinel, never dealt (attempt 0), waiting or ready, and not graded yet.
func Gradable(c *Card) bool {
	return c.Placed() && !IsSentinel(c) && c.F("brief") != "" && c.Int("attempt") == 0 && (c.Col == Waiting || c.Col == Ready) && c.F(FieldGrade) == ""
}

// DecideOutcome is one outcome the decide lane attaches: the decision's record (the
// attempt's or the grade's), its op id, the label and why.
type DecideOutcome struct {
	Decision, Op, Label, Note string
}

// GradeAsk is one card the decide lane grades: its id and its brief.
type GradeAsk struct {
	Card, Brief string
}

// DecideDue is the decide lane's work in the snapshot: every primary to grade (Gradable),
// and the outcome of every decision of a primary that landed or was dropped, read from the
// card (the attempt decisions it records, PrefixDecided, and its grade): a card landed at
// attempt n on tier t labels an attempt decided at n `landed` and one decided earlier
// `later-<t>`, and its grade t; a card dropped labels every decision `dropped`
// (decide.AttemptLabel, decide.GradeLabel). The lane attaches each once.
func DecideDue(s *Snapshot) (grades []GradeAsk, outcomes []DecideOutcome) {
	for _, c := range s.Work.Cards() {
		if c.Placed() && Gradable(c) {
			grades = append(grades, GradeAsk{Card: c.ID, Brief: c.F("brief")})
			continue
		}
		landed, dropped := c.Placed() && c.Col == Landed, !c.Placed() && c.F("outcome") == "dropped"
		if !landed && !dropped {
			continue
		}
		at, tier, note := 0, "", "dropped: "+c.F("reason")
		if landed {
			at, tier = c.Int("attempt"), LandedTier(c)
			note = fmt.Sprintf("landed at attempt %d on %s", at, tier)
		}
		for _, k := range slices.Sorted(maps.Keys(c.Fields)) {
			op, ok := strings.CutPrefix(k, PrefixDecided)
			if !ok {
				continue
			}
			outcomes = append(outcomes, DecideOutcome{Decision: decide.AttemptName, Op: op, Label: decide.AttemptLabel(opAttempt(op), at, tier), Note: c.ID + " " + note})
		}
		if g, ok := decide.ParseDecided(c.F(FieldGrade)); ok {
			outcomes = append(outcomes, DecideOutcome{Decision: decide.GradeName, Op: g.Op, Label: decide.GradeLabel(tier), Note: c.ID + " " + note})
		}
	}
	return grades, outcomes
}

// LandedTier is the tier a landed primary was on: script for a card of script steps alone
// (cardtree), else the tier it was on when it landed (cardTier).
func LandedTier(c *Card) string {
	if cardtree.Parse(c.F("brief")).AllScript() {
		return decide.GradeScript
	}
	now, _ := CardTiers(c)
	return now
}

// opAttempt is the attempt an attempt decision's op id names (decide.AttemptOp:
// `<card>@<attempt>.<hex>`); 0 when it names none.
func opAttempt(op string) int {
	_, rest, _ := strings.Cut(op, "@")
	n, _ := strconv.Atoi(strings.SplitN(rest, ".", 2)[0])
	return n
}
