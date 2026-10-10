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
//     child's RESULT.md and the reason line, whenever it holds the key; the finish carries
//     the class, its p and the op id, which must name the take's own card and attempt
//     (decidedFor). Two classes may route a failed finish, each from its own bar, which the
//     deal writes on the work card from the sprint row: no-result at or above
//     decide_attempt_no_result ends the take, nothing-to-do at or above
//     decide_attempt_nothing_to_do is failed work of that class (finishKind). No other class
//     has a bar (needs-pro had four positives in the calibration): those decisions are
//     recorded and shown only. A report the provider failed, and a launch refused, are never
//     overridden by a decision.
//   - the grade decision: before a card's first deal, the server's decide lane grades its
//     convergence from the brief alone (script, flash, pro) and writes it on the card
//     (Grade). It is a hint, shown by `card`, until the sprint row's decide_grade bar is set:
//     then a card graded pro at or above it starts on pro instead of flash, up to its
//     ceiling (startTier).
//
// Every bar is empty by default (the coordinator, 2026-10-03: nothing routes on a decision
// until a review round labels cards independently): every decision is asked, recorded and
// shown, and none routes; 0.7 is the starting point the calibration supports. Every
// decision is recorded by the server's decide lane, and its outcome is attached when the
// card lands or is dropped (DecideDue): the record trains, and calibrate reads the bars.

// The fields of layer 2.
const (
	// FieldDecideAttemptNoResult and FieldDecideAttemptNothingToDo are a work card's attempt
	// bars, the sprint row's as the deal read them: a failed finish whose decision is that
	// class at or above its bar is routed by the class (finishKind).
	FieldDecideAttemptNoResult    = "decide_attempt_no_result"
	FieldDecideAttemptNothingToDo = "decide_attempt_nothing_to_do"
	// FieldDecided is a work card's attempt decision as its last finish carried it
	// (decide.Decided: `<class> p=<p> op=<op>`), and FieldDecidedUsed is "yes" when the
	// finish was routed by it.
	FieldDecided     = "decided"
	FieldDecidedUsed = "decided_used"
	// PrefixDecided keys a primary's record of each attempt decision of its takes:
	// `decided:<op>` = `<class> p=<p> used=<yes|no>`, set once (the op is per attempt and
	// state, so two takes that ended alike share it), read by DecideDue.
	PrefixDecided = "decided:"
	// FieldGrade is a primary's grade decision (decide.Decided: `<grade> p=<p> op=<op>`).
	FieldGrade = "grade"
)

// attemptBars is the work card fields the deal writes for the attempt decision: each of the
// sprint row's two bars that is set, and the fields of those that are not (a redeal unsets
// them).
func (s *Snapshot) attemptBars() (set map[string]string, unset []string) {
	set = map[string]string{}
	for f, bar := range map[string]string{FieldDecideAttemptNoResult: s.DecideAttemptNoResult, FieldDecideAttemptNothingToDo: s.DecideAttemptNothingToDo} {
		if bar == "" {
			unset = append(unset, f)
		} else {
			set[f] = bar
		}
	}
	slices.Sort(unset)
	return set, unset
}

// decidedFor is why the attempt decision a finish carries (line) cannot route or ride with
// the finish of the work card c: its op id must name c's primary and attempt
// (decide.AttemptOf), so a finish is only ever routed by a decision of its own take. "" when
// it does, or when the finish carries none.
func decidedFor(c *Card, line string) string {
	if line == "" {
		return ""
	}
	d, ok := decide.ParseDecided(line)
	if !ok {
		return fmt.Sprintf("the attempt decision %q is not `<class> p=<p> op=<op>`", line)
	}
	card, attempt, ok := decide.AttemptOf(d.Op)
	if ok && card == c.F("primary") && attempt == c.Int("attempt") {
		return ""
	}
	named := d.Op
	if ok {
		named = card + "@" + strconv.Itoa(attempt)
	}
	return fmt.Sprintf("the attempt decision names %s (op=%s), not this take's %s@%d (%s); finish with the take's own decision, or none",
		named, d.Op, c.F("primary"), c.Int("attempt"), c.ID)
}

// finishKind is how a failed finish of the work card c is routed (finishPlan): the take's
// end kind (cardhdr.EndProvider and cardhdr.EndNoResult are takes that ended with no work to
// judge; "" is failed work) and the failure class failed work is compared by (FailureClass;
// "" for the reason line's own). A staging refusal and a launch refused are the member's, no
// take ran, and a report the provider failed is native's own hard end: no decision
// overrides them. Otherwise two classes route, each at or above its own bar on the card:
// no-result ends the take (FieldDecideAttemptNoResult), nothing-to-do is failed work of the
// class `decided nothing-to-do` (FieldDecideAttemptNothingToDo). Every other class, and
// every decision under its bar or with no bar, leaves the prefix rule standing.
func finishKind(c *Card, r FinishReq) (kind, class string, used bool) {
	kind = prefixKind(r.Report)
	if kind == cardhdr.EndStaging || kind == cardhdr.EndProvider || strings.HasPrefix(strings.TrimSpace(r.Report), cardhdr.EndLaunch) {
		return kind, "", false
	}
	d, ok := decide.ParseDecided(r.Decided)
	switch {
	case !ok:
	case d.Value == decide.ClassNoResult && d.Over(c.F(FieldDecideAttemptNoResult)):
		return cardhdr.EndNoResult, "", true
	case d.Value == decide.ClassNothingToDo && d.Over(c.F(FieldDecideAttemptNothingToDo)):
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
	case strings.HasPrefix(strings.TrimSpace(report), cardhdr.EndLaunch):
		return cardhdr.EndLaunch
	}
	return ""
}

// decidedSets is what a finish carrying an attempt decision writes: on the work card the
// decision and whether it routed the finish, on the primary pr its record of the decision
// (PrefixDecided), set once: a second take with the same op leaves the first record. Nothing
// when the finish carries none.
func decidedSets(r FinishReq, used bool, pr *Card, card, primary map[string]string) {
	d, ok := decide.ParseDecided(r.Decided)
	if !ok {
		return
	}
	card[FieldDecided] = d.String()
	if used {
		card[FieldDecidedUsed] = "yes"
	}
	if pr.F(PrefixDecided+d.Op) == "" {
		word := map[bool]string{true: "yes", false: "no"}[used]
		primary[PrefixDecided+d.Op] = fmt.Sprintf("%s p=%s used=%s", d.Value, strconv.FormatFloat(d.P, 'f', 3, 64), word)
	}
}

// startTier is the tier a card with no tier yet (its first deal on a route) is drawn from:
// the tier its brief's line 1 names when that is pro (the owner, 2026-10-03, after seven of
// seven first flash attempts of pro cards died at the budget or the deadline with no result,
// one dead attempt per card: a brief's tier is the card's starting tier, not only its
// ceiling, and a card that says pro is never dealt below it), else flash first (cost rule 1
// of nova-tools#5174, for briefs that say flash or say no tier). The grade (decide.go, Grade)
// is recorded on the card and raises nothing: line 1 decides the start.
func (s *Snapshot) startTier(c *Card, m cardhdr.Model) string {
	if ceilingTier(c, m) == cardhdr.RoutePro {
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
			_, n, _ := decide.AttemptOf(op)
			outcomes = append(outcomes, DecideOutcome{Decision: decide.AttemptName, Op: op, Label: decide.AttemptLabel(n, at, tier), Note: c.ID + " " + note})
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
