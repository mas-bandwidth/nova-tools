package sprint

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// Answered by rule (docs/SPEC-SPRINT.md section 8; the owner, 2026-10-04, at 1:36 PM: "I want
// this sort of oh no fleet is idle, do judgement, release more cards thing -- i want this
// more automated."). That afternoon 49 judgments were open besides the drop-blocked ones, some
// 3h39m old, each a mechanical answer nobody gave: work came back failed, a card reached its
// bound, a work card past its deadline, a stream stopped on a conflict. The tick answers the
// mechanical judgments by rule, without the coordinator: each answer is one verb the
// judgment's own decisions name (rework, rework on a tier up, wait, return, resume, twin),
// applied by the machine, recorded on the log and on the card as "answered by rule <name>". A
// judgment that needs a mind stays one: a reader's finding at its brief's bound, a sentinel,
// a brief that is wrong. Every rule can be turned off: run --answer-rules=false turns them all off, and
// nova-config's sprint row answer_rules_off names the ones off (RulesOff). One pure function
// decides (RuleAnswers); the tick's parts apply it (TickRules), and `rules` prints it. The
// model is tla/SprintRules.tla (RuleAnswersBounded, LadderClimbs, WaitOnce).

// The rules, by name: the names nova-config's answer_rules_off takes (config.AnswerRules).
const (
	RuleBaseGate    = "base-gate"    // the lander's base tree gate, retried before a stream stops, and its streams resumed when a land pass finds it green (landgo.go, land_base.go)
	RuleBound       = "bound"        // a card reached its bound: a new attempt a tier up, heavy to a friend
	RuleBriefDefect = "brief-defect" // the same finding twice: the card marked a brief defect, held
	RuleConflict    = "conflict"     // a head the lander refused (a file conflict, its PATHS, the tree gate): returned, redone on the tip at flash, resumed; the same refusal twice a brief defect
	RuleFailed      = "failed"       // work came back failed or with no result: redealt, then a tier up
	RuleFriendTake  = "friend-take"  // a friend's work card past its bound that she has not started: taken back, dealt again (judgment_rules.go)
	RuleHoldNeed    = "hold-need"    // failed work whose report HOLDs naming a card that has not landed: waits for it, reworked once it lands (judgment_rules.go)
	RuleLate        = "late"         // a work card past its deadline: a wait once with progress; returned and redealt only once its holder stamped and went silent
	RuleReadLate    = "read-late"    // a read past its deadline: taken back and asked of another reader, once an attempt (judgment_rules.go)
	// RulePaths: a failed attempt whose report proposes PATHS (PATHS-PROPOSED): its widened
	// twin, or one judgment with the twin's command when a proposed file is shared
	// (paths_proposed.go). Not in RuleNames: nova-config's answer_rules_off enum
	// (config.AnswerRules, held equal to RuleNames) does not name it yet, so only run
	// --answer-rules=false turns it off.
	RulePaths = "paths"
)

// RuleNames is every rule nova-config's answer_rules_off names, in name order.
var RuleNames = []string{RuleBaseGate, RuleBound, RuleBriefDefect, RuleConflict, RuleFailed, RuleFriendTake, RuleHoldNeed, RuleLate, RuleReadBroken, RuleReadLate}

// The fields the rules write.
const (
	// FieldRuleAnswer is the last rule answer on a card: "<rule>: <what> at <time>".
	FieldRuleAnswer = "rule_answer"
	// FieldRuleTier and FieldRuleFails are the failed rule's count on the primary: the tier
	// it counts on and the attempts that came back failed there.
	FieldRuleTier  = "rule_tier"
	FieldRuleFails = "rule_fails"
	// FieldRuleRedo is the attempt the conflict rule returned to be redone on the tip.
	FieldRuleRedo = "rule_redo"
	// FieldRuleRefused and FieldRuleRefusal are the last refusal the conflict rule returned
	// the card on: its way (RefusalWay) and the lander's words. A second refusal of the same
	// way is a brief defect.
	FieldRuleRefused = "rule_refused"
	FieldRuleRefusal = "rule_refusal"
	// FieldRuleWaited is the work card's generation the late rule waited on, once.
	FieldRuleWaited = "rule_waited"
	// FieldBriefDefect is the stamp the brief-defect rule marked the primary with.
	FieldBriefDefect = "brief_defect"
	// FieldProgress is a work card's last progress: the server's time of its holder's last
	// `progress` verb (Progress), which the member and the friend daemon send every
	// ProgressEvery while the child or the turn prints. The late rule's sign that a late card
	// is moving; a stamp from before the card's take is another holder's and counts as none.
	FieldProgress = "progress"
	// FieldConflictKind and FieldConflictPaths are a conflict stop's facts on the stream's
	// control card, as the lander reported them (MergeReq).
	FieldConflictKind  = "conflict_kind"
	FieldConflictPaths = "conflict_paths"
)

// The rules' numbers.
const (
	// RuleAttemptCap is the attempts a card's work may come back failed on one tier before
	// the failed rule raises it a tier: the first is redealt on the next route, the second
	// goes up (cost rule 2: escalate on the second failure, not the third).
	RuleAttemptCap = 2
	// RuleProgressWindow is how recent a late card's progress is to count.
	RuleProgressWindow = 10 * time.Minute
	// ProgressEvery is how often a holder whose child or turn prints stamps progress on its
	// card: inside RuleProgressWindow with room for a stamp that is late or lost.
	ProgressEvery = 3 * time.Minute
	// RuleLateWait is the late rule's one wait.
	RuleLateWait = 30 * time.Minute
	// RuleSameFailureCards is how many cards failing the same way (their failure's class)
	// make the failure the fleet's, not the card's: the failed and bound rules leave each to a
	// mind rather than climb the ladder with every card (sameFailure).
	RuleSameFailureCards = 3
)

// RuleConflictFix is the conflict rule's fix: the attempt's change made again where the
// stream is now.
const RuleConflictFix = "redo the same change on the current tip"

// The ways a head is refused at its landing, as the lander says them (cmd/nova-sprint,
// land.go and landgo.go: mergeHead, checkCard, gateCard): the conflict rule's "the same
// way" (RefusalWay).
const (
	RefusedConflict = "conflict" // the head does not merge: a file no ledger owns conflicts
	RefusedPaths    = "paths"    // the lander's checks: it changes files outside its PATHS (E12)
	RefusedChecks   = "checks"   // the lander's other checks: a stranded fragment (E4)
	RefusedGate     = "gate"     // the merged tree fails the tree gate
)

// RefusalWay is the way the lander refused a head, from the conflict fact it recorded
// (the stream's control card's conflict kind, and the stop's words): "" when the rule
// cannot place it (a conflict in a generated ledger, a head that is no commit or that
// origin does not hold, a conflict the lander did not place), which needs a mind.
func RefusalWay(kind, what string) string {
	switch {
	case kind == "file":
		return RefusedConflict
	case kind != "":
		return ""
	case strings.Contains(what, " fails the lander's checks: ") && strings.Contains(what, "(E12)"):
		return RefusedPaths
	case strings.Contains(what, " fails the lander's checks: "):
		return RefusedChecks
	case strings.Contains(what, " fails the tree gate: "):
		return RefusedGate
	}
	return ""
}

// NRuleAnswered is the happened note of a move a rule made that no judgment's close says (a
// late card returned to be dealt again).
const NRuleAnswered = "answered by rule"

// The acts of a rule answer.
const (
	ActRework = "rework"                    // the next attempt, on the next route of its tier
	ActUp     = "rework on a tier up"       // the next attempt, pinned a tier up
	ActFriend = "rework as a friend's card" // the next attempt, a friend's (above heavy)
	ActWait   = "wait 30m"                  // the late card made progress: held once
	ActHold   = "hold"                      // the late card's holder has not had its own deadline yet, or never stamped progress
	ActRedeal = "return and redeal"         // the late card withdrawn, dealt again
	ActReturn = "return"                    // the conflict card back to review
	ActResume = "resume"                    // the stream again, its conflict card out
	ActMark   = "mark brief defect"         // the card marked, the judgment kept
	ActTake   = "take back and deal again"  // the friend's card she has not started withdrawn from her row, dealt to another
	ActAsk    = "ask another reader"        // the late read taken back, asked of another reader
	ActNeed   = "wait for the card"         // the HOLD's card has not landed: the judgment waits on it
	ActLeft   = "left"                      // the judgment needs a mind
	ActOff    = "off"                       // its rule is turned off
)

// RuleSaid is the answer a rule records on a judgment: "answered by rule <name>: <what>".
func RuleSaid(rule, said string) string { return NRuleAnswered + " " + rule + ": " + said }

// ruleWho is who a rule's hold is recorded as.
func ruleWho(rule string) string { return "rule " + rule }

// RuleOff says nova-config's sprint row turns the rule off.
func (s *Snapshot) RuleOff(rule string) bool { return slices.Contains(s.RulesOff, rule) }

// RuleAnswer is what the rules do with one open judgment on one subject.
type RuleAnswer struct {
	Judgment string `json:"judgment"`
	Type     string `json:"type"`
	Subject  string `json:"subject"`
	Card     string `json:"card,omitempty"`
	Rule     string `json:"rule,omitempty"`
	Act      string `json:"act"`
	Why      string `json:"why"`
	Tier     string `json:"tier,omitempty"`
	Friend   bool   `json:"friend,omitempty"`
	// Waited is how long the judgment has been open.
	Waited string `json:"waited"`

	fix   string
	files []string // the files outside PATHS a twin widens them by (ruleReadBroken)
	twin  string   // the twin's id (ActTwinWider)
	from  string   // the friend a card is taken back from (ActTake), the reader a read is taken back from (ActAsk)
	set   map[string]string
	until time.Time
	open  Open
	paths *PathsProposal // the paths rule's proposal, its twin and brief
}

// Answers says the answer acts: a rule answers it and the rule is on.
func (a RuleAnswer) Answers() bool { return a.Rule != "" && a.Act != ActLeft && a.Act != ActOff }

// RuleAnswers is what the rules do with every open judgment, one line per judgment and
// subject, in the order they are open: the rule and its act, or left (it needs a mind) or
// off (its rule is turned off), with why. It is pure: the tick's rule parts apply it
// (TickRules) and `rules` prints it.
func RuleAnswers(s *Snapshot, r TickReq) []RuleAnswer {
	var out []RuleAnswer
	for _, o := range s.Open {
		if o.Note.Kind != Judgment {
			continue
		}
		a := RuleAnswer{Judgment: o.Note.ID, Type: o.Note.Type, Subject: o.Subject(), Card: o.Note.Card, open: o,
			Waited: s.Now.Sub(o.Note.At).Round(time.Second).String()}
		switch {
		case rulePaths(s, &a):
			// a failed attempt that proposed PATHS: never the same brief again
		default:
			ruleByType(s, r, &a)
		}
		if a.Rule != "" && a.Act != ActLeft && s.RuleOff(a.Rule) {
			a.Act, a.Why = ActOff, "nova-config's sprint row answer_rules_off turns the rule "+a.Rule+" off: "+a.Why
		}
		out = append(out, a)
	}
	return out
}

// ruleByType is the rule of the judgment's type answering it.
func ruleByType(s *Snapshot, r TickReq, a *RuleAnswer) {
	switch a.open.Note.Type {
	case NWorkFailed:
		ruleFailed(s, a)
	case NBound:
		ruleBound(s, a)
	case NWorkLate:
		ruleLate(s, r, a)
	case NConflict:
		ruleConflict(s, a)
	case NReturned:
		ruleRedo(s, a)
	case NBriefWrong:
		ruleBrief(s, a)
	case NReadBroken:
		ruleReadBroken(s, a)
	case NReadLate:
		ruleReadLate(s, a)
	case NBaseRed:
		ruleBaseGate(s, a)
	default:
		a.Act, a.Why = ActLeft, "no rule answers it"
	}
}

func left(a *RuleAnswer, why string) { a.Act, a.Why = ActLeft, why }

// escalation is the tier a primary goes up to by rule: the first tier of the ladder above
// the one it is on that a deal draws and a route serves; past heavy, a friend's card.
func escalation(s *Snapshot, pr *Card) (tier string, friend bool) {
	for _, t := range dealtAbove(cardTierOf(pr)) {
		if _, why := s.noRoute(withField(withField(pr, FieldTier, t), FieldTierNow, t)); why == "" {
			return t, false
		}
	}
	return "", true
}

// up is the answer that raises the primary a tier, or makes it a friend's card.
func up(s *Snapshot, a *RuleAnswer, pr *Card, why string) {
	tier, friend := escalation(s, pr)
	a.set = map[string]string{FieldRuleTier: tier, FieldRuleFails: "0"}
	if friend {
		a.Act, a.Friend, a.Why = ActFriend, true, why+": no tier of the ladder above "+cardTierOf(pr)+" is dealt, so a friend's card"
		a.set[FieldRuleTier] = WhoFriend
		return
	}
	a.Act, a.Tier, a.Why = ActUp, tier, why+": a new attempt on "+tier
}

// mindCard is why a primary's judgment needs a mind whatever its type: a friend's card, a
// brief defect, a model the brief pins; "" when none.
func mindCard(pr *Card) string {
	if _, f := FriendCard(pr); f {
		return "a friend's card: her work needs a mind"
	}
	if pr.F(FieldBriefDefect) != "" {
		return "a brief defect: its brief needs a mind"
	}
	if m, _ := cardhdr.ReadModel(pr.F("brief")); m.Pin != "" || m.Tier == cardhdr.RouteFrontier {
		return "its brief pins a model or the frontier tier: no ladder to climb"
	}
	return ""
}

// ruleFailed: work came back failed, or with no result. A harness fault, or a HOLD with
// findings, is reworked on its tier with the failure as its fix, a friend's card too
// (ruleHarness). Any other failure: the first on a tier is redealt on the next route of the
// tier (the rework's own fix, the report); the RuleAttemptCap-th goes a tier up; past
// heavy, a friend's card.
func ruleFailed(s *Snapshot, a *RuleAnswer) {
	a.Rule = RuleFailed
	pr := s.Work.Placed(a.Subject)
	switch {
	case pr == nil || pr.Col != Review || pr.F("result") != "failed":
		left(a, "not in review with failed work")
		return
	case holdsFor(s, pr) == "" && ruleHarness(s, a, pr):
		return // a harness fault, or a HOLD with findings: the failure is the fix (harness_fault.go)
	case mindCard(pr) != "":
		left(a, mindCard(pr))
		return
	case holdsFor(s, pr) != "":
		ruleHoldNeed(s, a, pr, holdsFor(s, pr))
		return
	case AtIdenticalFailure(s, pr) != nil:
		left(a, "the second identical failure: the bound rule answers it")
		return
	}
	if bb, ok := AtBriefBound(pr, brokenFindings(s, pr), s.AttemptsCap(pr.Row)); ok {
		left(a, bb.String())
		return
	}
	if why := sameFailure(s, pr.F(FieldFailure)); why != "" {
		left(a, why)
		return
	}
	a.Card = pr.ID
	tier := cardTierOf(pr)
	fails := 1
	if pr.F(FieldRuleTier) == tier {
		fails = pr.Int(FieldRuleFails) + 1
	}
	why := fmt.Sprintf("attempt %s failed, %d of %d on %s", pr.F("attempt"), fails, RuleAttemptCap, tier)
	if fails < RuleAttemptCap {
		a.Act, a.Why = ActRework, why+": redealt on the next route of "+tier
		a.set = map[string]string{FieldRuleTier: tier, FieldRuleFails: itoa(fails)}
		return
	}
	up(s, a, pr, why)
}

// ruleBound: a card reached its bound (its redeal bound at its ceiling, or the second
// identical failure): a new attempt a tier up; past heavy, a friend's card. A card every
// member refused at staging, or whose brief is wrong, needs a mind.
func ruleBound(s *Snapshot, a *RuleAnswer) {
	a.Rule = RuleBound
	pr := s.Work.Placed(a.Subject)
	if pr == nil {
		left(a, "the card is off the table")
		return
	}
	if pr.Col == Review && pr.F("result") == "failed" && holdsFor(s, pr) == "" && ruleHarness(s, a, pr) {
		return // the second identical harness fault: still the harness's, on its tier
	}
	if why := mindCard(pr); why != "" {
		left(a, why)
		return
	}
	if bb, ok := AtBriefBound(pr, "", s.AttemptsCap(pr.Row)); ok {
		left(a, bb.String())
		return
	}
	wc := AtRedealBound(s, pr)
	if wc == nil {
		wc = AtIdenticalFailure(s, pr)
	}
	if wc == nil {
		if w, _ := AtStagingBound(s, pr, s.UpMembers()); w != nil {
			left(a, "every member up refused it at staging: the fleet's, a mind's")
		} else {
			left(a, "not at a bound now")
		}
		return
	}
	class := BoundClass(wc)
	if pr.Col == Review {
		class = pr.F(FieldFailure)
	}
	if why := sameFailure(s, class); why != "" {
		left(a, why)
		return
	}
	a.Card = pr.ID
	a.fix = ownFix(s, pr)
	why := fmt.Sprintf("attempt %s reached its bound on %s (%s)", pr.F("attempt"), cardTierOf(pr), orDash(BoundClass(wc)))
	if a.fix == "" {
		a.fix = cutText(why+"; do the brief again, from the start", MaxCardTextBytes)
	}
	up(s, a, pr, why)
}

// ruleLate: a work card past its deadline (docs/SPEC-SPRINT.md section 8, the rules table's
// row late; tla/SprintRules.tla, Part "late": WaitOnce, NeverStampedNeverReturned). With
// progress in the last RuleProgressWindow it is waited on RuleLateWait, once a generation;
// without, or after its wait, it is returned and dealt again, once its holder has had its
// own whole deadline (a card just dealt again is held until then). The default is wait
// only: a working card whose holder has stamped no progress since its take is held
// RuleLateWait, again and again, and never returned by this rule, so a member that does
// not stamp never loses an honest long child to it. A friend's card stays the
// coordinator's: a friend keeps her cards.
func ruleLate(s *Snapshot, r TickReq, a *RuleAnswer) {
	a.Rule = RuleLate
	wc := s.Fleet.Placed(a.open.Note.Card)
	if wc == nil {
		left(a, "the work card moved")
		return
	}
	a.Card = wc.ID
	if IsFriendRow(wc.Row) {
		ruleFriendTake(s, r, a, wc)
		return
	}
	if pr := s.Work.Placed(wc.F("primary")); pr != nil && mindCard(pr) != "" {
		left(a, "a friend's card, or one that needs a mind: friends keep their cards")
		return
	}
	if wc.Col == Withdrawn {
		left(a, "withdrawn: the deal places it")
		return
	}
	field, limit, _, own := WorkDeadline(s, wc)
	_ = field
	ownFor, ok := r.running(s.Now, wc.F(own))
	mine := own != "" && ok && ownFor > limit
	progressAt := stampAt(wc, FieldProgress)
	// a stamp before the take is another holder's: this holder has stamped none
	stamped := !progressAt.IsZero() && !progressAt.Before(stampAt(wc, "taken"))
	progress := stamped && s.Now.Sub(progressAt) <= RuleProgressWindow
	waited := wc.F(FieldRuleWaited) != "" && wc.F(FieldRuleWaited) == wc.F("gen")
	switch {
	case wc.Col == Working && progress && !waited:
		a.Act, a.until = ActWait, s.Now.Add(RuleLateWait)
		a.Why = "progress at " + stamp(progressAt) + ": waited " + RuleLateWait.String() + ", once"
	case wc.Col == Working && mine && !stamped:
		a.Act, a.until = ActHold, s.Now.Add(RuleLateWait)
		a.Why = fmt.Sprintf("%s has stamped no progress since its take: waited, never returned by this rule; held until %s", wc.Row, stamp(a.until))
	case mine || waited:
		a.Act = ActRedeal
		a.Why = fmt.Sprintf("%s held it past its own deadline %s with no progress since %s: returned and dealt again", wc.Row, limit, orDash(stampOrEmpty(progressAt)))
		if waited {
			a.Why = "its one wait is spent: returned and dealt again"
		}
		a.until = s.Now.Add(limit)
	default:
		rest := limit - ownFor
		if !ok {
			rest = limit
		}
		a.Act, a.until = ActHold, s.Now.Add(rest)
		a.Why = fmt.Sprintf("%s has held it %s of its own %s: held until %s", wc.Row, ownFor.Round(time.Second), limit, stamp(a.until))
	}
}

func stampOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return stamp(t)
}

// ruleConflict: a stream stopped on a head the lander refused (docs/SPEC-SPRINT.md section
// 8, the rules table's row conflict): a conflict in a file no generated ledger owns
// (FieldConflictKind file), files outside its PATHS or another of the lander's checks, or
// the merged tree failing the tree gate. The card is returned, then the stream resumed, so
// it lands the rest of its batch, then the card redone on the tip at flash (ruleRedo), all
// in one tick. The same card refused the same way again is a brief defect: marked, the
// stream left stopped and its judgment kept for a mind. A conflict in a ledger, or a
// refusal the rule cannot place (RefusalWay ""), needs a mind.
func ruleConflict(s *Snapshot, a *RuleAnswer) {
	a.Rule = RuleConflict
	ctl := s.StreamCtl(a.open.Note.Stream)
	if ctl == nil || ctl.F("state") != StreamStopped || ctl.F("cause") != "conflict" {
		left(a, "the stream is not stopped on a conflict")
		return
	}
	card := ctl.F("card")
	a.Card = card
	what := a.open.Note.What
	way := RefusalWay(ctl.F(FieldConflictKind), what)
	pr, m := s.Work.Placed(card), s.Merge.Placed(card)
	stuck := pr != nil && pr.Col == Merging && m != nil && m.Col == Stuck
	switch kind := ctl.F(FieldConflictKind); {
	case stuck && way == "" && kind == "":
		left(a, "the lander did not say which files conflicted: a mind's")
		return
	case stuck && way == "":
		left(a, "the conflict is in a "+kind+" ("+orDash(ctl.F(FieldConflictPaths))+"): a mind's")
		return
	}
	switch {
	case stuck:
		if why := mindCard(pr); why != "" {
			left(a, why)
			return
		}
		if pr.F(FieldRuleRefused) == way {
			a.Rule, a.Act = RuleBriefDefect, ActMark
			a.Why = card + " refused the same way twice (" + way + "): the brief is wrong, not the worker; marked a brief defect, the stream held for a mind"
			return
		}
		a.Act, a.Why = ActReturn, refusalSaid(way, ctl)+": "+card+" returned to be redone on the current tip at flash"
		a.set = map[string]string{FieldRuleRefused: way, FieldRuleRefusal: cutText(what, MaxCardTextBytes), FieldTierNow: cardhdr.RouteFlash}
	case pr != nil && pr.F(FieldRuleRedo) != "" && (m == nil || m.Col != Stuck):
		a.Act, a.Why = ActResume, card+" is out of the stream to be redone on the tip: the stream goes on"
	default:
		left(a, "the conflict card is not where the rule left it")
	}
}

// refusalSaid is a refusal of the way, in the rule's answer.
func refusalSaid(way string, ctl *Card) string {
	switch way {
	case RefusedConflict:
		return "a conflict in " + orDash(ctl.F(FieldConflictPaths)) + ", no ledger"
	case RefusedPaths:
		return "files outside its PATHS"
	case RefusedChecks:
		return "the lander's checks failed"
	}
	return "the merged tree fails the tree gate"
}

// ruleRedo: a card the conflict rule returned, in review at that attempt: reworked with the
// fix "redo the same change on the current tip".
func ruleRedo(s *Snapshot, a *RuleAnswer) {
	pr := s.Work.Placed(a.Subject)
	if pr == nil || pr.Col != Review || pr.F(FieldRuleRedo) == "" || pr.F(FieldRuleRedo) != pr.F("attempt") {
		left(a, "returned by a person: a mind's")
		return
	}
	a.Rule, a.Card = RuleConflict, pr.ID
	a.Act, a.fix, a.Why = ActRework, redoFix(pr), "returned by the conflict rule: "+RuleConflictFix
}

// redoFix is the fix of a card the conflict rule returned: the change made again on the
// current tip, and the refusal it answers when the tip alone does not (its PATHS, the
// lander's checks, the tree gate); a file conflict is the tip's to answer.
func redoFix(pr *Card) string {
	if way := pr.F(FieldRuleRefused); way == "" || way == RefusedConflict {
		return RuleConflictFix
	}
	return cutText(RuleConflictFix+"; the lander refused attempt "+pr.F(FieldRuleRedo)+": "+pr.F(FieldRuleRefusal), MaxCardTextBytes)
}

// ruleBrief: the same finding twice, or too many attempts on one brief (the brief is wrong,
// not the worker): the card is marked a brief defect, once, and the judgment stays, its
// text saying so; nothing deals the card, and no rule answers it again.
func ruleBrief(s *Snapshot, a *RuleAnswer) {
	a.Rule = RuleBriefDefect
	pr := s.Work.Placed(a.Subject)
	switch {
	case pr == nil:
		left(a, "the card is off the table")
	case pr.F(FieldBriefDefect) != "":
		left(a, "a brief defect since "+pr.F(FieldBriefDefect)+": brief or drop, a mind's")
	default:
		a.Card = pr.ID
		a.Act, a.Why = ActMark, "the brief is wrong, not the worker: marked a brief defect and held for a mind"
	}
}

// ruleBaseGate: a stream stopped on its base's red (NBaseRed), the one judgment of every stream
// stopped on that base (land_base.go). Resumed when a land pass found the base's tip green
// again (FieldBaseGatePassed); until then left, the lander re-checking the tip each pass.
func ruleBaseGate(s *Snapshot, a *RuleAnswer) {
	a.Rule = RuleBaseGate
	ctl := s.StreamCtl(a.open.Note.Stream)
	if ctl == nil || ctl.F("state") != StreamStopped || ctl.F("cause") != "base" {
		left(a, "the stream is not stopped on its base's red")
		return
	}
	if sha := basePassed(ctl); sha != "" {
		a.Act, a.Why = ActResume, baseGreenSaid(ctl.F(FieldBaseGateBase), sha)
		return
	}
	left(a, "the base "+orDash(ctl.F(FieldBaseGateBase))+" fails its tree gate; each land pass re-checks its tip")
}

// baseGreenSaid is the base-gate rule's resume, in its answer.
func baseGreenSaid(base, sha string) string {
	return "the base " + orDash(base) + " passes its tree gate again at " + sha
}

// The tick's rule parts, in the order they run: the conflict's return, its resume, a twin
// (read-broken, rules_read.go), every rework (failed, bound, read-broken, hold-need, the
// conflict's redo), the late cards, the friends' cards taken back, a late read asked of
// another reader, the HOLDs waiting on a card, the brief defects (judgment_rules.go). Each is
// a step of its own on a fresh read, so the conflict's three moves can all be made in one
// tick. With TickReq.AnswerRules false each is empty.
const (
	PartRulePaths  = "rule paths"
	PartRuleReturn = "rule return"
	PartRuleResume = "rule resume"
	PartRuleRework = "rule rework"
	PartRuleLate   = "rule late"
	PartRuleBrief  = "rule brief"
)

// TickRules is the rule parts, run at the tick's end before its checks.
var TickRules = []TickPartDef{
	{PartRulePaths, TickRulePaths},
	{PartRuleReturn, TickRuleReturn},
	{PartRuleResume, TickRuleResume},
	{PartRuleTwin, TickRuleTwin},
	{PartRuleRework, TickRuleRework},
	{PartRuleLate, TickRuleLate},
	{PartRuleTake, TickRuleTake},
	{PartRuleAsk, TickRuleAsk},
	{PartRuleNeed, TickRuleNeed},
	{PartRuleBrief, TickRuleBrief},
}

// IsRulePart says the tick part is a rule part: it plans with the routes and the rules
// turned off (nova-config's sprint row).
func IsRulePart(name string) bool { return strings.HasPrefix(name, "rule ") }

// acting is the rule answers that act, of the acts given.
func acting(s *Snapshot, r TickReq, acts ...string) []RuleAnswer {
	if !r.AnswerRules {
		return nil
	}
	var out []RuleAnswer
	for _, a := range RuleAnswers(s, r) {
		if a.Answers() && slices.Contains(acts, a.Act) {
			out = append(out, a)
		}
	}
	return out
}

// TickRuleReturn returns the conflict cards the conflict rule answers, each marked with the
// refusal it was returned on (FieldRuleRefused, FieldRuleRefusal) and its next attempt's
// tier, flash (FieldTierNow).
func TickRuleReturn(s *Snapshot, r TickReq) (Plan, int) {
	var ids []string
	sets := map[string]map[string]string{}
	for _, a := range acting(s, r, ActReturn) {
		if !slices.Contains(ids, a.Card) {
			ids = append(ids, a.Card)
			sets[a.Card] = a.set
		}
	}
	if len(ids) == 0 {
		return Plan{}, 0
	}
	p := Return(s, ReturnReq{Sel: Sel{Only: ids}, Reason: RuleSaid(RuleConflict, RuleConflictFix), Rule: RuleConflict, Who: r.who()})
	for i := range p.Units {
		for j := range p.Units[i].Changes {
			c := &p.Units[i].Changes[j]
			if set := sets[c.Entry.ID]; c.Table == Work && len(set) > 0 {
				if c.Entry.Set == nil {
					c.Entry.Set = map[string]string{}
				}
				maps.Copy(c.Entry.Set, set)
			}
		}
	}
	return p, 0
}

// TickRuleResume resumes the streams whose conflict card the conflict rule took out, and
// every stream stopped on a base's red that a land pass found green again (the base-gate
// rule): the one with the judgment answers it, and the ones stopped under it resume with it.
func TickRuleResume(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	seen := map[string]bool{}
	resume := func(st, rule, why string) {
		seen[st] = true
		q := Resume(s, ResumeReq{Stream: st, Did: RuleSaid(rule, why), Who: r.who()})
		for _, u := range q.Units {
			if ctl := s.StreamCtl(st); ctl != nil {
				clearBasePassed(&u, ctl)
			}
			for _, o := range u.Closes {
				u.Notes = append(u.Notes, decided(o, RuleSaid(rule, why), r.who(), s.Now))
			}
			p.Units = append(p.Units, u)
		}
		p.Refused = append(p.Refused, q.Refused...)
	}
	for _, a := range acting(s, r, ActResume) {
		if st := a.open.Note.Stream; !seen[st] {
			resume(st, a.Rule, a.Why)
		}
	}
	if r.AnswerRules && !s.RuleOff(RuleBaseGate) {
		for _, st := range BaseGreenStreams(s) {
			if !seen[st] {
				ctl := s.StreamCtl(st)
				resume(st, RuleBaseGate, baseGreenSaid(ctl.F(FieldBaseGateBase), basePassed(ctl)))
			}
		}
	}
	return p, 0
}

// TickRuleRework reworks every card a rule answers with a new attempt, each its own way, in
// one step.
func TickRuleRework(s *Snapshot, r TickReq) (Plan, int) {
	per := map[string]ReworkCard{}
	var ids []string
	for _, a := range acting(s, r, ActRework, ActUp, ActFriend) {
		if _, ok := per[a.Card]; ok {
			continue
		}
		set := map[string]string{FieldRuleAnswer: a.Rule + ": " + a.Act + " at " + stamp(s.Now)}
		for k, v := range a.set {
			set[k] = v
		}
		per[a.Card] = ReworkCard{Fix: a.fix, Tier: a.Tier, Friend: a.Friend, Set: set, Rule: a.Rule, Said: a.Act + ": " + a.Why}
		ids = append(ids, a.Card)
	}
	if len(ids) == 0 {
		return Plan{}, 0
	}
	return Rework(s, ReworkReq{Sel: Sel{Only: ids}, PerCard: per, Who: r.who()}), 0
}

// TickRuleLate answers the late work cards: a wait, a hold until the holder's own deadline,
// or the card returned and dealt again; each closes the judgment and keeps a hold on its
// condition until the time it names, so the tick raises it again then if it still holds.
func TickRuleLate(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	for _, a := range acting(s, r, ActWait, ActHold, ActRedeal) {
		wc := s.Fleet.Placed(a.Card)
		hold := acknowledged(a.open.Note, []Open{a.open}, ruleWho(RuleLate), s.Now)
		hold.Review = a.until
		said := RuleSaid(RuleLate, a.Act+": "+a.Why)
		answer := RuleLate + ": " + a.Act + " at " + stamp(s.Now)
		var u Unit
		switch a.Act {
		case ActWait:
			u = Unit{Key: wc.ID, Stream: wc.F("stream"), Changes: []Change{change(Fleet, setEntry(wc, map[string]string{FieldRuleWaited: wc.F("gen"), FieldRuleAnswer: answer}))},
				Moved: wc.ID + " " + said}
		case ActHold:
			u = Unit{Key: wc.ID, Stream: wc.F("stream"), Moved: wc.ID + " " + said}
		case ActRedeal:
			extra := map[string]string{FieldRuleAnswer: answer}
			if wc.Col == Working {
				extra[FieldTakeEnded] = stamp(s.Now) // the take ended: it spends a redeal
			}
			u = withdrawUnit(s, wc, extra, []string{FieldRuleWaited}, NRuleAnswered, ruleWho(RuleLate), said)
		}
		u.Closes = append(u.Closes, a.open)
		u.Notes = append(u.Notes, decided(a.open, said, r.who(), s.Now, a.Subject), hold)
		p.Units = append(p.Units, u)
	}
	return p, 0
}

// TickRuleBrief marks the brief defects: the card's mark, and the judgment's text.
func TickRuleBrief(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	updated := map[string]bool{}
	for _, a := range acting(s, r, ActMark) {
		pr := s.Work.Placed(a.Card)
		p.Units = append(p.Units, Unit{Key: pr.ID, Stream: pr.Row,
			Changes: []Change{change(Work, setEntry(pr, map[string]string{FieldBriefDefect: stamp(s.Now), FieldRuleAnswer: RuleBriefDefect + ": " + a.Act + " at " + stamp(s.Now)}))},
			Moved:   pr.ID + " marked a brief defect by rule " + RuleBriefDefect})
		if n := a.open.Note; !updated[n.ID] && !strings.HasPrefix(n.What, "brief defect: ") {
			updated[n.ID] = true
			n.What = "brief defect: " + n.What
			p.Updates = append(p.Updates, n)
		}
	}
	return p, 0
}

// BaseGateRetries is the base-gate rule's waits (cmd/nova-sprint, landgo.go): a base that
// fails its tree gate at its tip is gated again after the first, and again after the second;
// the failure after them is its third, and stops the stream (MergeReq.BaseRed, NBaseRed).
var BaseGateRetries = []time.Duration{2 * time.Minute, 5 * time.Minute}

// sameFailure is why a failure is the fleet's and not the card's: RuleSameFailureCards or
// more cards hold it now (in review with their work failed that way, or ready at their
// redeal bound with that class), "" when fewer do or the class is unknown. A toolchain a
// machine cannot run, a provider down: the ladder would raise every card a tier for a
// failure no tier changes, so a mind looks first.
func sameFailure(s *Snapshot, class string) string {
	if class == "" {
		return ""
	}
	n := 0
	for _, c := range s.Work.Column(Review) {
		if c.F("result") == "failed" && c.F(FieldFailure) == class {
			n++
		}
	}
	for _, c := range s.Work.Column(Ready) {
		if wc := AtRedealBound(s, c); wc != nil && BoundClass(wc) == class {
			n++
		}
	}
	if n < RuleSameFailureCards {
		return ""
	}
	return fmt.Sprintf("the same failure on %d cards (%s): the fleet's, not the card's; a mind's", n, cutText(class, 120))
}

// ProgressReq is a holder stamping progress on the work cards it works (the `progress`
// verb, sent by the member and the friend daemon every ProgressEvery while the child or
// the turn prints): As is the fleet row that holds them, Gens the generation held, where
// the card is named at one.
type ProgressReq struct {
	Sel
	As   string
	Gens map[string]int
	Who  string
}

// Progress stamps FieldProgress, at the server's time, on each named work card its holder
// works (docs/SPEC-SPRINT.md section 8, the rules table's row late; tla/SprintRules.tla,
// Stamp): the late rule's sign that the card moves. Only the holder stamps: a card that is
// not working, one held by another row than As, and one named at a generation that is not
// its live one are refused, and nothing else of the card changes.
func Progress(s *Snapshot, r ProgressReq) Plan {
	var p Plan
	if r.As == "" || len(r.Sel.IDs) == 0 {
		p.refuse("progress", "a progress stamp names its holder and its cards: progress --as <worker> <card>[@<gen>]...")
		return p
	}
	for _, id := range r.Sel.IDs {
		c := s.Fleet.Card(id)
		_, atGen := r.Gens[id]
		why := ""
		switch {
		case !c.Placed() || c.Col != Working:
			why = "not working (it is " + placeWord(c) + ")"
		case c.Row != r.As:
			why = "held by " + c.Row + ", not " + r.As + ": only its holder stamps its progress"
		case atGen:
			why = liveGen("progress", c, r.Gens)
		}
		if why != "" {
			p.refuse(id, why)
			continue
		}
		p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.F("stream"),
			Changes: []Change{change(Fleet, setEntry(c, map[string]string{FieldProgress: stamp(s.Now)}))},
			Moved:   c.ID + " progress at " + stamp(s.Now) + " by " + r.As})
	}
	return p
}
