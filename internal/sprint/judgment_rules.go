package sprint

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// More judgments answered by rule (docs/SPEC-SPRINT.md section 8, the rules table's rows
// friend-take, read-late and hold-need; the owner, 2026-10-05: "OK what else is like this,
// saving LLM work by replacing it with checks at the right time?"). On the log of epoch 15 the
// coordinator answered 864 reworks and 475 take-backs by hand, almost every one by one rule,
// and a judgment waited up to four hours when the coordinator did not look. Each rule here is
// a case of the one engine (RuleAnswers, rules.go): it decides on the judgment's own facts,
// its tick part applies one verb the house already has (friend take, ask --instead, rework),
// every answer is recorded "answered by rule <name>", and its name in nova-config's
// answer_rules_off turns it off alone (RuleOff).

// The fields these rules write.
const (
	// FieldRuleReread is the primary's attempt at which the read-late rule asked another
	// reader: once an attempt, the second late read of the attempt is a mind's.
	FieldRuleReread = "rule_reread"
	// FieldRuleNeed is the card a HOLD named that the hold-need rule waits for, with the
	// attempt that held: "<card>@<attempt>".
	FieldRuleNeed = "rule_need"
)

// The tick parts of these rules (TickRules).
const (
	PartRuleTake = "rule take"
	PartRuleAsk  = "rule ask"
	PartRuleNeed = "rule need"
)

// ruleFriendTake: a work card on a friend's row past its bound (dealt and never taken, or
// working and not finished). One she has not started (no progress stamp, and her beat does
// not name it running: friendStarted) is taken back to ready and the deal places it again,
// never on her (FieldTakenFrom). One she started stays hers: friends keep the cards they
// started. A card pinned to her alone has nowhere else to go, and a brief defect is a mind's.
func ruleFriendTake(s *Snapshot, r TickReq, a *RuleAnswer, wc *Card) {
	a.Rule = RuleFriendTake
	friend, _ := FriendOfRow(wc.Row)
	if wc.Col != Ready && wc.Col != Working {
		left(a, "not on friend "+friend+"'s row ready or working: the deal places it")
		return
	}
	i := slices.IndexFunc(r.Friends, func(f FriendSeat) bool { return f.Name == friend })
	if i < 0 {
		left(a, "the tick holds no seat of friend "+friend+": whether she started it is not known")
		return
	}
	if friendStarted(s, r.Friends[i], wc) {
		left(a, "friend "+friend+" has started it: friends keep the cards they started")
		return
	}
	if pr := s.Work.Placed(wc.F("primary")); pr != nil && (pinnedTo(pr, friend) || pr.F(FieldBriefDefect) != "") {
		left(a, "pinned to friend "+friend+" alone, or a brief defect: taken back it has nowhere to go")
		return
	}
	a.Act, a.from = ActTake, friend
	a.Why = fmt.Sprintf("friend %s has not started %s past its bound (%s): taken back and dealt again, never to her", friend, wc.ID, wc.Col)
}

// ruleReadLate: a read card past its deadline (asked and not begun, or begun and not
// reported) of the primary's live attempt. Its reader's read is taken back and asked of one
// other reader (ask --instead), once an attempt (FieldRuleReread); the second late read of
// the attempt, and one no other reader can take, are a mind's. A read handed back with no
// verdict needs no rule: the ask places it again itself (Ask, returnedRead).
func ruleReadLate(s *Snapshot, a *RuleAnswer) {
	a.Rule = RuleReadLate
	rc := s.Readers.Placed(a.open.Note.Card)
	if rc == nil || rc.Col != Asked && rc.Col != Reading {
		left(a, "the read card moved")
		return
	}
	pr := s.Work.Placed(rc.F("primary"))
	switch {
	case pr == nil || pr.Col != Review || rc.Int("attempt") != pr.Int("attempt"):
		left(a, "not a read of the primary's attempt in review")
		return
	case pr.F(FieldBriefDefect) != "":
		left(a, mindCard(pr))
		return
	case pr.F(FieldRuleReread) == pr.F("attempt"):
		left(a, "another reader was asked once at attempt "+pr.F("attempt")+" already: a mind's")
		return
	}
	if p := Ask(s, AskReq{Sel: Sel{IDs: []string{pr.ID}}, Instead: rc.F("reader")}); len(p.Refused) > 0 || len(p.Units) == 0 {
		why := "the ask placed nothing"
		if len(p.Refused) > 0 {
			why = p.Refused[0].Why
		}
		left(a, "no other reader can take it: "+why)
		return
	}
	a.Card, a.Act, a.from = pr.ID, ActAsk, rc.F("reader")
	a.Why = fmt.Sprintf("%s's read of attempt %s is past its deadline (%s): taken back and asked of another reader, once an attempt", rc.F("reader"), pr.F("attempt"), rc.Col)
	a.set = map[string]string{FieldRuleReread: pr.F("attempt")}
}

// holdsFor is the card the primary's failed work HOLDs for: the one the hold-need rule
// recorded at this attempt, else the first card its report names that has not landed when
// the report says HOLD; "" when none. A HOLD that names no such card (a lane at its cap, no
// push, a red gate: every friend's HOLD reads "friend <name> HOLD: ...") stays the failed
// rule's, as before.
func holdsFor(s *Snapshot, pr *Card) string {
	if need, at, _ := strings.Cut(pr.F(FieldRuleNeed), "@"); need != "" && at == pr.F("attempt") {
		return need
	}
	if report := workReport(s, pr); reportHolds(report) {
		return namedUnlanded(s, pr, report)
	}
	return ""
}

// ruleHoldNeed: work came back failed and its report HOLDs naming a card on the table that
// has not landed (holdsFor). It waits for that card: the judgment stays open, marked
// "waiting on <card> by rule hold-need", and once the card lands the primary is reworked on
// the current tip. A card dropped since is a mind's.
func ruleHoldNeed(s *Snapshot, a *RuleAnswer, pr *Card, need string) {
	a.Rule = RuleHoldNeed
	switch {
	case s.StateOf(need) == Landed:
		a.Card, a.Act = pr.ID, ActRework
		a.fix = cutText(need+" has landed: do the brief again on the current tip; attempt "+pr.F("attempt")+" held for it: "+workReport(s, pr), MaxCardTextBytes)
		a.Why = fmt.Sprintf("attempt %s held for %s, which has landed: reworked on the current tip", pr.F("attempt"), need)
	case s.Work.Placed(need) == nil:
		left(a, "its report HOLDs for "+need+", which is off the table: a mind's")
	default:
		a.Card, a.Act, a.from = pr.ID, ActNeed, need
		a.Why = fmt.Sprintf("attempt %s held for %s, which has not landed (%s): waits for it", pr.F("attempt"), need, s.StateOf(need))
	}
}

// workReport is the report of the primary's last work card.
func workReport(s *Snapshot, pr *Card) string {
	wc := s.Fleet.Card(pr.F("work"))
	if wc == nil {
		wc = s.Fleet.Card(WorkCardID(pr.ID, pr.Int("attempt")))
	}
	if wc == nil {
		return ""
	}
	return wc.F("report")
}

// reportHolds says a report gives the verdict HOLD: the word, whole, in capitals.
func reportHolds(report string) bool { return hasWord(report, "HOLD") }

// namedUnlanded is the first card, by id, that the report names and that is on the table and
// not landed, the primary itself and the stream controls aside; "" when none.
func namedUnlanded(s *Snapshot, self *Card, report string) string {
	var ids []string
	for _, c := range s.Work.Cards() {
		if c.ID != self.ID && c.Placed() && c.Col != Landed && c.F("kind") != "control" && hasWord(report, c.ID) {
			ids = append(ids, c.ID)
		}
	}
	slices.Sort(ids)
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

// hasWord says word stands in text with no id character either side of it.
func hasWord(text, word string) bool {
	if word == "" {
		return false
	}
	for i := 0; ; {
		j := strings.Index(text[i:], word)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(word)
		if (start == 0 || !idByte(text[start-1])) && (end == len(text) || !idByte(text[end])) {
			return true
		}
		i = start + 1
	}
}

func idByte(b byte) bool {
	return b == '_' || b == '-' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

// ruleAnswerSet is the FieldRuleAnswer a rule's move writes: "<rule>: <act> at <time>".
func ruleAnswerSet(a RuleAnswer, now time.Time) map[string]string {
	return map[string]string{FieldRuleAnswer: a.Rule + ": " + a.Act + " at " + stamp(now)}
}

// setOn adds set to the plan's change of the entry id on the table, or a change of its own
// to the unit keyed by key when the plan has none.
func setOn(p *Plan, s *Snapshot, table, id, key string, set map[string]string) {
	for i := range p.Units {
		for j := range p.Units[i].Changes {
			if c := &p.Units[i].Changes[j]; c.Table == table && c.Entry.ID == id {
				if c.Entry.Set == nil {
					c.Entry.Set = map[string]string{}
				}
				maps.Copy(c.Entry.Set, set)
				return
			}
		}
	}
	for i := range p.Units {
		if p.Units[i].Key == key {
			p.Units[i].Changes = append(p.Units[i].Changes, change(table, setEntry(s.T(table).Card(id), set)))
			return
		}
	}
}

// TickRuleTake takes back the friends' cards the friend-take rule answers, each closing its
// late judgment with the rule's decided note; the next tick's deal places each again, never
// on the friend it was taken from.
func TickRuleTake(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	taken := map[string]bool{}
	for _, a := range acting(s, r, ActTake) {
		if taken[a.Card] {
			continue
		}
		taken[a.Card] = true
		said := RuleSaid(RuleFriendTake, a.Act+": "+a.Why)
		q := FriendTake(s, FriendTakeReq{Friend: a.from, IDs: []string{a.Card}, Reason: said, Who: r.who()})
		if len(q.Refused) > 0 || len(q.Units) == 0 {
			continue // refused: the judgment stays
		}
		setOn(&q, s, Fleet, a.Card, a.Card, ruleAnswerSet(a, s.Now))
		q.Units[0].Closes = append(q.Units[0].Closes, a.open)
		q.Units[0].Notes = append(q.Units[0].Notes, decided(a.open, said, r.who(), s.Now, a.Subject))
		q.Units[0].Moved += "; answered by rule " + RuleFriendTake
		p.Units = append(p.Units, q.Units...)
		p.Rows = append(p.Rows, q.Rows...)
	}
	return p, 0
}

// TickRuleAsk asks another reader for the first late read the read-late rule answers: one a
// tick, as the ask's round index is written with each ask; the next tick asks the next.
func TickRuleAsk(s *Snapshot, r TickReq) (Plan, int) {
	for _, a := range acting(s, r, ActAsk) {
		p := Ask(s, AskReq{Sel: Sel{IDs: []string{a.Card}}, Instead: a.from, Who: r.who()})
		if len(p.Refused) > 0 || len(p.Units) == 0 {
			continue
		}
		said := RuleSaid(RuleReadLate, a.Act+": "+a.Why)
		set := ruleAnswerSet(a, s.Now)
		maps.Copy(set, a.set)
		setOn(&p, s, Work, a.Card, a.Card, set)
		u := &p.Units[0]
		u.Closes = append(u.Closes, a.open)
		u.Notes = append(u.Notes, decided(a.open, said, r.who(), s.Now, a.Subject))
		u.Moved += "; answered by rule " + RuleReadLate
		return p, 0
	}
	return Plan{}, 0
}

// TickRuleNeed records, once an attempt, the card a HOLD waits for: FieldRuleNeed on the
// primary, the judgment's text "waiting on <card> by rule hold-need: ...", and one log line.
// The judgment stays open: a primary in review has no move to waiting (Moves), and the
// rework part reworks it by this rule once the card lands.
func TickRuleNeed(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	for _, a := range acting(s, r, ActNeed) {
		pr := s.Work.Placed(a.Card)
		want := a.from + "@" + pr.F("attempt")
		if pr.F(FieldRuleNeed) == want {
			continue
		}
		set := ruleAnswerSet(a, s.Now)
		set[FieldRuleNeed] = want
		p.Units = append(p.Units, Unit{Key: pr.ID, Stream: pr.Row, Changes: []Change{change(Work, setEntry(pr, set))},
			Moved: pr.ID + " " + RuleSaid(RuleHoldNeed, a.Act+": "+a.Why)})
		if n := a.open.Note; !strings.HasPrefix(n.What, "waiting on ") {
			n.What = "waiting on " + a.from + " by rule " + RuleHoldNeed + ": " + n.What
			p.Updates = append(p.Updates, n)
		}
	}
	return p, 0
}

// RuleAnsweredWithin is how many cards a rule answered in the window ending at now, by the
// FieldRuleAnswer each move writes ("<rule>: <act> at <time>"), per rule: the coordinator
// view's count of the answers it did not have to give. A card answered twice in the window
// counts once, by its last answer.
func RuleAnsweredWithin(cards []*Card, now time.Time, window time.Duration) map[string]int {
	out := map[string]int{}
	for _, c := range cards {
		v := c.F(FieldRuleAnswer)
		i := strings.LastIndex(v, " at ")
		rule, _, ok := strings.Cut(v, ": ")
		if i < 0 || !ok {
			continue
		}
		t, err := time.Parse(time.RFC3339, v[i+len(" at "):])
		if err != nil || t.After(now) || now.Sub(t) > window {
			continue
		}
		out[rule]++
	}
	return out
}
