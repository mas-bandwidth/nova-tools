package sprint

import (
	"maps"
	"reflect"
	"slices"
	"strings"
)

// Mechanical judgments the tick answers by name (the owner, 2026-10-05: replace the
// coordinator's hand answers with a check at the tick). Each answer is a verb the
// house already has, and the log line is RuleSaid. A name in Snapshot.RulesOff
// turns that one rule off. The names are not in RuleNames: store/rule_answers_test.go:276
// holds RuleNames equal to config.AnswerRules, and internal/config/kind.go:299 is
// outside this card, so nova-config apply cannot list them yet. A snapshot that
// already carries the name still turns the rule off.
//
// Two rules are implemented and covered on the twin, and TickEndWith does not run
// them. reader-broken would rework the first finding that store/rule_answers_test.go:239
// requires to stay a judgment. deadline-no-run would finish the unstamped late card
// that store/rule_answers_test.go:132 requires to stay working. Both files are
// outside this card.
const (
	RuleReaderBroken    = "reader-broken"    // a first finding: rework with it as the fix
	RuleFriendUnstarted = "friend-unstarted" // a friend's card not started, past its bound: take it back
	RuleReturnedRead    = "returned-read"    // a read handed back: ask some other reader
	RuleDeadlineNoRun   = "deadline-no-run"  // past its deadline, no live run: finish failed and rework once
	RuleHoldUnlanded    = "hold-unlanded"    // failed work whose HOLD names a card that has not landed

	// FieldDeadlineRework is set once the deadline-no-run rule has reworked the primary.
	FieldDeadlineRework = "rule_deadline_rework"
	// FieldDeadlinePending marks a primary the deadline rule just finished failed, so the
	// next part reworks that card and no other failure.
	FieldDeadlinePending = "rule_deadline_pending"
	// FieldHoldShield marks a failed primary whose report says HOLD, so the failed rule
	// is not given it. FieldHoldNeed is the unlanded card it is waiting on, when the
	// report names one.
	FieldHoldShield = "rule_hold"
	FieldHoldNeed   = "hold_need"
)

// Part names. The returned-read rule is composed into the readers' one ask
// (steps_tick.go). A second part would take an operation id of its own.
const (
	PartJudgmentReader   = "judgment reader"
	PartJudgmentFriend   = "judgment friend"
	PartJudgmentDeal     = "judgment deal"
	PartJudgmentDeadline = "judgment deadline"
	PartJudgmentRework   = "judgment deadline rework"
	PartJudgmentHold     = "judgment hold"
	PartJudgmentRestore  = "judgment hold restore"
)

// JudgmentRulesBefore runs after the deadlines and before the older rule parts.
// The returned-read rule is not here: it is the readers' ask, in front of the
// machine's plan, and only when it has something to do.
var JudgmentRulesBefore = []TickPartDef{
	{PartJudgmentFriend, TickJudgmentFriend},
	{PartJudgmentDeal, TickJudgmentDealAgain},
	{PartJudgmentHold, TickJudgmentHold},
}

// JudgmentRulesAfter puts a shielded failure back to result failed, so the next
// tick's ask still refuses it. The flip is every tick: rules.go:259 leaves a
// failure only while result is not failed, and that file is outside this card.
var JudgmentRulesAfter = []TickPartDef{
	{PartJudgmentRestore, TickJudgmentHoldRestore},
}

func judgmentRulesOn(s *Snapshot, r TickReq, rule string) bool {
	return r.AnswerRules && !s.RuleOff(rule)
}

// friend_read.go's init replaces the ask with the machine ask wrapped for a
// friend's read (friend_read.go:352). This init runs later (judgment_rules.go
// sorts after that file) and composes the returned-read rule in front of that
// function. One part keeps the name ask, so the store still loads routes and
// reader states (store/tick.go routesPart and the ask reader's flag). When the
// rule's plan is empty the part is the machine's ask alone.
func init() {
	machine := askFn(TickTables)
	composed := composeAsk(machine)
	setAsk(TickTables, composed)
	for i := range TickParts {
		if TickParts[i].Name == "ask" {
			TickParts[i].Fn = composed
			break
		}
	}
	// held.go predicts the next tick with the same ask. Its request has
	// answer-rules off, so the composed part is the machine's ask there.
	mp := reflect.ValueOf(machine).Pointer()
	for i := range heldParts {
		if heldParts[i] != nil && reflect.ValueOf(heldParts[i]).Pointer() == mp {
			heldParts[i] = composed
		}
	}
}

func askFn(tables []TableUpdate) TickPartFn {
	for i := range tables {
		for j := range tables[i].Parts {
			if tables[i].Parts[j].Name == "ask" && tables[i].Parts[j].Fn != nil {
				return tables[i].Parts[j].Fn
			}
		}
	}
	return TickAsk
}

func setAsk(tables []TableUpdate, fn TickPartFn) {
	for i := range tables {
		for j := range tables[i].Parts {
			if tables[i].Parts[j].Name == "ask" {
				tables[i].Parts[j].Fn = fn
				return
			}
		}
	}
}

// composeAsk runs the returned-read rule, then the machine ask. A rule plan is
// the whole part: the machine ask sees the committed hand-back on the next tick.
// No rule plan, and the part is the machine ask, including its due.
func composeAsk(machine TickPartFn) TickPartFn {
	return func(s *Snapshot, r TickReq) (Plan, int) {
		rp, rdue := TickJudgmentReturn(s, r)
		if !rp.Empty() || rdue != 0 {
			return rp, rdue
		}
		if machine == nil {
			return Plan{}, 0
		}
		return machine(s, r)
	}
}

// TickJudgmentReader reworks a primary whose open judgment is a reader finding,
// the first time for that finding. The same finding twice is a brief defect and
// stays. Not installed in the tick: store/rule_answers_test.go:239.
func TickJudgmentReader(s *Snapshot, r TickReq) (Plan, int) {
	if !judgmentRulesOn(s, r, RuleReaderBroken) {
		return Plan{}, 0
	}
	per := map[string]ReworkCard{}
	var ids []string
	for _, o := range s.Open {
		if o.Note.Kind != Judgment || o.Note.Type != NReadBroken {
			continue
		}
		pr := s.Work.Placed(o.Subject())
		if pr == nil || pr.Col != Review {
			continue
		}
		found := brokenFindings(s, pr)
		if _, bound := AtBriefBound(pr, found, s.AttemptsCap(pr.Row)); bound {
			continue
		}
		if _, ok := per[pr.ID]; ok {
			continue
		}
		fix := found
		if fix == "" {
			fix = ownFix(s, pr)
		}
		per[pr.ID] = ReworkCard{
			Fix:  fix,
			Set:  map[string]string{FieldRuleAnswer: RuleReaderBroken + ": " + ActRework + " at " + stamp(s.Now)},
			Rule: RuleReaderBroken,
			Said: ActRework + ": the finding, the first time",
		}
		ids = append(ids, pr.ID)
	}
	if len(ids) == 0 {
		return Plan{}, 0
	}
	return Rework(s, ReworkReq{Sel: Sel{Only: ids}, PerCard: per, Who: r.who()}), 0
}

// TickJudgmentFriend takes back a friend's work card that is past its bound and
// that she has not started (no progress, and her beat does not name it running).
// The late rule leaves her cards; this part runs first and closes the lateness.
func TickJudgmentFriend(s *Snapshot, r TickReq) (Plan, int) {
	if !judgmentRulesOn(s, r, RuleFriendUnstarted) {
		return Plan{}, 0
	}
	by := map[string][]string{}
	for _, o := range s.Open {
		if o.Note.Kind != Judgment || o.Note.Type != NWorkLate {
			continue
		}
		wc := lateWork(s, o)
		if wc == nil || !IsFriendRow(wc.Row) || (wc.Col != Ready && wc.Col != Working) {
			continue
		}
		if ruleFriendStarted(s, r, wc) != "" {
			continue
		}
		name, ok := FriendOfRow(wc.Row)
		if !ok || slices.Contains(by[name], wc.ID) {
			continue
		}
		by[name] = append(by[name], wc.ID)
	}
	var p Plan
	for _, name := range slices.Sorted(maps.Keys(by)) {
		q := FriendTake(s, FriendTakeReq{Friend: name, IDs: by[name], Reason: RuleSaid(RuleFriendUnstarted, "not started past its bound"), Who: r.who()})
		p.Refused = append(p.Refused, q.Refused...)
		p.Rows = append(p.Rows, q.Rows...)
		for _, u := range q.Units {
			prID := ""
			if wc := s.Fleet.Card(u.Key); wc != nil {
				prID = wc.F("primary")
			}
			said := RuleSaid(RuleFriendUnstarted, "take back and deal again")
			for _, o := range closesFor(s.Open, []string{NWorkLate}, prID) {
				u.Closes = append(u.Closes, o)
				u.Notes = append(u.Notes, decided(o, said, r.who(), s.Now, prID))
			}
			for j := range u.Changes {
				if u.Changes[j].Table != Work {
					continue
				}
				if u.Changes[j].Entry.Set == nil {
					u.Changes[j].Entry.Set = map[string]string{}
				}
				u.Changes[j].Entry.Set[FieldRuleAnswer] = RuleFriendUnstarted + ": take back at " + stamp(s.Now)
			}
			u.Moved += "; answered by rule " + RuleFriendUnstarted
			p.Units = append(p.Units, u)
		}
	}
	return p, 0
}

// TickJudgmentDealAgain places a friend's card the take put back to ready, on a
// friend other than the one it was taken from. The tick reads the roster at the
// start of every tick while it has a friend (store/friends.go friendSeats), so
// the request already holds it. With no roster on the request the next tick's
// deal places the card.
func TickJudgmentDealAgain(s *Snapshot, r TickReq) (Plan, int) {
	if !r.AnswerRules || len(r.Friends) == 0 {
		return Plan{}, 0
	}
	var cards []*Card
	for _, c := range s.Work.Column(Ready) {
		if _, ok := FriendCard(c); !ok {
			continue
		}
		wc := s.Fleet.Placed(WorkCardID(c.ID, c.Int("attempt")))
		if wc == nil || wc.Col != Withdrawn || wc.F(FieldTakenFrom) == "" {
			continue
		}
		cards = append(cards, c)
	}
	if len(cards) == 0 {
		return Plan{}, 0
	}
	return FriendDeal(s, cards, r.Friends), 0
}

// TickJudgmentReturn asks a read handed back of some other reader. One primary
// a tick. No other reader free, and the read stays for the ask that follows.
func TickJudgmentReturn(s *Snapshot, r TickReq) (Plan, int) {
	if !judgmentRulesOn(s, r, RuleReturnedRead) {
		return Plan{}, 0
	}
	for _, rc := range s.Readers.Column(Asked, Reading) {
		if !returnedRead(rc) {
			continue
		}
		pr := s.Work.Placed(rc.F("primary"))
		if pr == nil || pr.Col != Review || pr.F("result") == "failed" {
			continue
		}
		p := Ask(s, AskReq{Sel: Sel{IDs: []string{pr.ID}}, Instead: rc.F("reader"), Who: r.who()})
		if len(p.Refused) > 0 || len(p.Units) == 0 {
			continue
		}
		n := happened(NReadReturned, pr.Row, s.Now, pr.ID)
		n.Who = r.who()
		n.What = RuleSaid(RuleReturnedRead, "ask another reader, not "+rc.F("reader"))
		p.Notes = append(p.Notes, n)
		return p, 0
	}
	return Plan{}, 0
}

// TickJudgmentDeadline finishes a working card past its deadline that has no
// live run (no progress inside the late rule's window, and no friend's beat
// names it running). A friend's card she has not started is the take-back, not
// this. A primary already reworked by this rule is left. Not installed in the
// tick: store/rule_answers_test.go:132.
func TickJudgmentDeadline(s *Snapshot, r TickReq) (Plan, int) {
	if !judgmentRulesOn(s, r, RuleDeadlineNoRun) {
		return Plan{}, 0
	}
	var ids []string
	gens := map[string]int{}
	members := map[string]string{}
	for _, o := range s.Open {
		if o.Note.Kind != Judgment || o.Note.Type != NWorkLate {
			continue
		}
		wc := lateWork(s, o)
		if wc == nil || wc.Col != Working || liveRun(s, r, wc) {
			continue
		}
		if IsFriendRow(wc.Row) && ruleFriendStarted(s, r, wc) == "" {
			continue
		}
		pr := s.Work.Placed(wc.F("primary"))
		if pr == nil || pr.F(FieldDeadlineRework) != "" || slices.Contains(ids, wc.ID) {
			continue
		}
		ids = append(ids, wc.ID)
		gens[wc.ID] = wc.Int("gen")
		members[wc.ID] = wc.Row
	}
	if len(ids) == 0 {
		return Plan{}, 0
	}
	var p Plan
	for _, id := range ids {
		q := Finish(s, FinishReq{Sel: Sel{IDs: []string{id}}, As: members[id], Gens: map[string]int{id: gens[id]}, Failed: true, Report: "no live run", Who: r.who()})
		if len(q.Refused) > 0 {
			p.Refused = append(p.Refused, q.Refused...)
			continue
		}
		p.Units = append(p.Units, q.Units...)
		p.Notes = append(p.Notes, q.Notes...)
		p.Rows = append(p.Rows, q.Rows...)
	}
	if len(p.Units) == 0 {
		return p, 0
	}
	said := RuleSaid(RuleDeadlineNoRun, "finish failed")
	for i := range p.Units {
		wc := s.Fleet.Card(p.Units[i].Key)
		if wc == nil {
			continue
		}
		pr := s.Work.Placed(wc.F("primary"))
		if pr == nil {
			continue
		}
		for j := range p.Units[i].Changes {
			e := &p.Units[i].Changes[j]
			if e.Table == Work && e.Entry.ID == pr.ID {
				if e.Entry.Set == nil {
					e.Entry.Set = map[string]string{}
				}
				e.Entry.Set[FieldDeadlinePending] = "1"
			}
		}
		for _, o := range closesFor(s.Open, []string{NWorkLate}, pr.ID) {
			p.Units[i].Closes = append(p.Units[i].Closes, o)
			p.Units[i].Notes = append(p.Units[i].Notes, decided(o, said, r.who(), s.Now, pr.ID))
		}
	}
	return p, 0
}

// TickJudgmentDeadlineRework reworks each primary the deadline rule just finished,
// once. The fix is the finish's report.
func TickJudgmentDeadlineRework(s *Snapshot, r TickReq) (Plan, int) {
	if !judgmentRulesOn(s, r, RuleDeadlineNoRun) {
		return Plan{}, 0
	}
	per := map[string]ReworkCard{}
	var ids []string
	for _, pr := range s.Work.Column(Review) {
		if pr.F("result") != "failed" || pr.F(FieldDeadlinePending) != "1" || pr.F(FieldDeadlineRework) != "" {
			continue
		}
		per[pr.ID] = ReworkCard{
			Fix: ownFix(s, pr),
			Set: map[string]string{
				FieldDeadlineRework:  "1",
				FieldDeadlinePending: "",
				FieldRuleAnswer:      RuleDeadlineNoRun + ": " + ActRework + " at " + stamp(s.Now),
			},
			Rule: RuleDeadlineNoRun,
			Said: ActRework + ": no live run, once",
		}
		ids = append(ids, pr.ID)
	}
	if len(ids) == 0 {
		return Plan{}, 0
	}
	return Rework(s, ReworkReq{Sel: Sel{Only: ids}, PerCard: per, Who: r.who()}), 0
}

// TickJudgmentHold keeps a HOLD failure off the failed rule for this tick's rule
// parts. A report that names another primary which has not landed records the
// wait once (FieldHoldNeed). A review card cannot move to waiting, and a need
// on it is a break of check rule 11 (lifecycle.go:52, check.go:221), so the
// judgment stays open. Any other HOLD is left for the coordinator the same way:
// shielded, not reworked, not counted as a rule answer.
func TickJudgmentHold(s *Snapshot, r TickReq) (Plan, int) {
	if !judgmentRulesOn(s, r, RuleHoldUnlanded) {
		return Plan{}, 0
	}
	var p Plan
	for _, pr := range s.Work.Column(Review) {
		if pr.F("result") != "failed" || !reportHolds(workReport(s, pr)) {
			continue
		}
		set := map[string]string{"result": "hold", FieldHoldShield: "1"}
		var notes []Note
		if id := namedUnlanded(s, pr, workReport(s, pr)); id != "" && pr.F(FieldHoldNeed) != id {
			set[FieldHoldNeed] = id
			n := happened(NWorkFailed, pr.Row, s.Now, pr.ID)
			n.Who = r.who()
			n.What = RuleSaid(RuleHoldUnlanded, "wait for "+id)
			notes = append(notes, n)
		}
		p.Units = append(p.Units, Unit{Key: pr.ID, Stream: pr.Row, Changes: []Change{change(Work, setEntry(pr, set))}, Notes: notes,
			Moved: pr.ID + " result failed -> hold for the rule parts"})
	}
	return p, 0
}

// TickJudgmentHoldRestore puts a shielded failure back to result failed.
func TickJudgmentHoldRestore(s *Snapshot, r TickReq) (Plan, int) {
	if !r.AnswerRules {
		return Plan{}, 0
	}
	var p Plan
	for _, pr := range s.Work.Column(Review) {
		if pr.F("result") != "hold" || pr.F(FieldHoldShield) != "1" {
			continue
		}
		p.Units = append(p.Units, Unit{Key: pr.ID, Stream: pr.Row, Changes: []Change{change(Work, setEntry(pr, map[string]string{"result": "failed"}))},
			Moved: pr.ID + " result hold -> failed"})
	}
	return p, 0
}

func lateWork(s *Snapshot, o Open) *Card {
	if wc := s.Fleet.Placed(o.Note.Card); wc != nil {
		return wc
	}
	pr := s.Work.Placed(o.Subject())
	if pr == nil {
		return nil
	}
	return s.Fleet.Placed(pr.F("work"))
}

// ruleFriendStarted says why a friend's card counts as started: a progress stamp,
// or her beat names it running. The tick's seats carry Running (friend_deal.go
// friendStarted). A request that carries beats and no seats is read the same way.
func ruleFriendStarted(s *Snapshot, r TickReq, wc *Card) string {
	if wc.F(FieldProgress) != "" {
		return "progress was stamped on it"
	}
	name, _ := FriendOfRow(wc.Row)
	for _, f := range r.Friends {
		if f.Name != name && f.Name != wc.Row {
			continue
		}
		if friendStarted(s, f, wc) {
			return "her beat names it running"
		}
	}
	for _, key := range []string{name, wc.Row} {
		b, ok := r.Beats[key]
		if !ok || b.Friend == nil {
			continue
		}
		for _, run := range b.Friend.Running {
			if run == wc.ID || run == wc.F("primary") {
				return "her beat names it running"
			}
		}
	}
	return ""
}

func liveRun(s *Snapshot, r TickReq, wc *Card) bool {
	if ruleFriendStarted(s, r, wc) == "her beat names it running" {
		return true
	}
	at := stampAt(wc, FieldProgress)
	taken := stampAt(wc, "taken")
	return !at.IsZero() && !at.Before(taken) && s.Now.Sub(at) <= RuleProgressWindow
}

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

func reportHolds(report string) bool {
	return tokenHas(report, "HOLD")
}

func namedUnlanded(s *Snapshot, self *Card, report string) string {
	var ids []string
	for _, c := range s.Work.Cards() {
		if c.ID == self.ID || !c.Placed() || c.Col == Landed || c.F("kind") == "control" {
			continue
		}
		if tokenHas(report, c.ID) {
			ids = append(ids, c.ID)
		}
	}
	slices.Sort(ids)
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

func tokenHas(text, token string) bool {
	if token == "" || text == "" {
		return false
	}
	rest := text
	for {
		i := strings.Index(rest, token)
		if i < 0 {
			return false
		}
		before := i == 0 || !idByte(rest[i-1])
		end := i + len(token)
		after := end == len(rest) || !idByte(rest[end])
		if before && after {
			return true
		}
		rest = rest[i+1:]
	}
}

func idByte(b byte) bool {
	return b == '_' || b == '-' || b == '.' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}
