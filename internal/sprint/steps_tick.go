package sprint

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// The machine's tick (docs/SPEC-SPRINT.md, "The machine"). While the machine
// is RUNNING, one tick a second performs every mechanical move that is due,
// in a fixed order, each part bounded so one tick stays short, and writes the
// judgments the coordinator needs, each once. Every part is a pure function
// of an observed snapshot: it calls the existing steps (resolve, resume,
// start's dealing, level, ask, check) and returns their plan. The store
// binding runs each part as one operation of the engine, on a fresh read, so
// a later part sees what an earlier part moved; running the tick twice in a
// row changes nothing the second time.

// MachineActor is who the tick's moves and notifications are recorded as.
const MachineActor = "machine"

// The bounds of one tick, and the one queue length the dealing keeps.
const (
	// TickMaxMoves bounds the units one part of a tick applies; the rest are
	// due, and the next tick moves them. It is the deal's bound, TickMaxDeal
	// (width.go): one table-layer write's member candidates, which the step
	// builder cuts into parts under the table layer's entry bounds, so a
	// part plans every row that needs it in the one tick and never lags its
	// own work (the owner's rule, errata 3 amendment 10: every row of every
	// table moves every tick, never a row at a time).
	TickMaxMoves = TickMaxDeal
)

// Deadlines, each against running time: time the machine was STOPPED does
// not count.
const (
	DeadlineUntaken    = 15 * time.Minute // a work card dealt and not taken
	DeadlineUnfinished = 2 * time.Hour    // a work card taken and not finished
	DeadlineUnbegun    = 30 * time.Minute // a read card asked and not begun
	DeadlineUnreported = 2 * time.Hour    // a read card begun and not reported
	DeadlineMergeIdle  = 30 * time.Minute // a stream with cards to merge and no merge step
	// DeadlineJudgment is how long a judgment stays open, in running time,
	// before the tick marks it overdue; a review time the coordinator set
	// (wait) is the judgment's own due time instead.
	DeadlineJudgment = 10 * time.Minute
)

// MaxRedeals is how many times one attempt's work card is dealt again after
// a take of it ended without a finish, its member down or away while the card
// was working (its redeals counter, which no take resets). A card that was
// ready, never taken, is dealt again and keeps its count: a card is retired
// for repeated failure at members, never for members flapping while it sat
// ready. A working card whose member goes down with its count at the bound
// stays withdrawn, and the bound's judgment names it (docs/SPEC-SPRINT.md
// section 2, the work card's redeals; tla/DirtyTick.tla RedealsAreEndedTakes
// and RedealBoundHolds).
const MaxRedeals = 3

// FieldTakeEnded is the work card's mark that a take of it ended without a
// finish and it was withdrawn (no member had room, or it is at its bound):
// the deal that places it again counts that take, and unsets the mark.
const FieldTakeEnded = "take_ended"

// FieldReturnedAttempt is the primary's attempt when the coordinator last
// returned it to review (return): at that attempt the pump does not accept
// it, and the judgment "returned to review" decides it.
const FieldReturnedAttempt = "returned_attempt"

// The tick's own notification types.
const (
	NResumed   = "stream resumed: the card it needed landed"
	NCannotAsk = "cannot ask"
	NNoMember  = "no fleet member is up"
	NInvariant = "an invariant is broken"
	NWorkLate  = "a work card is past its deadline"
	NReadLate  = "a read card is past its deadline"
	NMergeLate = "a stream has had no merge step past its deadline"
	NBound     = "a card reached its bound"
	// NOverdue (notes.go) is the overdue line: a happened note, once per
	// judgment, when the judgment passes its due time.

	NMachineStarted = "the machine started"
	NMachineStopped = "the machine stopped"
)

// Sentinel is the kind of a card that marks a point in a stream: the tick
// never moves it from waiting or ready.
const Sentinel = "sentinel"

// TickDecisions are the decisions open to the tick's judgments.
var TickDecisions = map[string][]string{
	NBound:     {"rework with a fix", "drop", "wait"},
	NCannotAsk: {"reader add", "rework", "drop", "wait"},
	NNoMember:  {"fleet beat", "fleet up", "wait"},
	NInvariant: {"look at the card", "repair", "wait"},
	NWorkLate:  {"fleet down <member>", "wait", "drop"},
	NReadLate:  {"ask --another", "wait", "drop"},
	NMergeLate: {"merge --stream <s>", "look", "wait"},
	NStalled:   {"look at the card", "wait"},
}

// TickReq is what a tick is given beside the snapshot.
type TickReq struct {
	Who string // recorded with every move; "" is MachineActor
	// Stopped is the time the machine was STOPPED between two clock readings:
	// a deadline compares running time only. nil is none.
	Stopped func(from, to time.Time) time.Duration
	// Beats is each fleet member's last beat, read by the binding with the
	// tick: the presence part applies the status it derives. nil is none
	// read, and the presence part does nothing.
	Beats map[string]Beat
	// Started is the machine's first start of the sprint's epoch, the time
	// the done part's note counts from; zero is not known.
	Started time.Time
}

func (r TickReq) who() string {
	if r.Who == "" {
		return MachineActor
	}
	return r.Who
}

// running is the running time since the stamp: the time on the clock less the
// time the machine was STOPPED; ok is false for a stamp that is absent or
// unreadable.
func (r TickReq) running(now time.Time, stampText string) (time.Duration, bool) {
	t, err := time.Parse(time.RFC3339, stampText)
	if err != nil {
		return 0, false
	}
	d := now.Sub(t)
	if r.Stopped != nil {
		d -= r.Stopped(t, now)
	}
	return d, true
}

// TickPartFn is one part of the tick over an observed state: its plan, held
// to the part's bounds, and how many moves and judgments are due past them.
type TickPartFn func(*Snapshot, TickReq) (plan Plan, due int)

// TickPart is one part of a tick: its name, its plan, and what is due past
// its bounds.
type TickPart struct {
	Name string
	Plan Plan
	Due  int
}

// TickPartDef is a part of the tick by name, with its planner.
type TickPartDef struct {
	Name string
	Fn   TickPartFn
}

// TableUpdate is one table's update in a tick: its parts, each an existing
// planner over every row of the table that needs it, run in order.
type TableUpdate struct {
	Table string
	Parts []TickPartDef
}

// PartDrain is the name of the work pump's first part: the work table's
// queue applied in one update (Drain). It needs the queue, which the store
// reads with the part's step: it has no planner here.
const PartDrain = "drain"

// TickTables is the tick's shape, in the owner's words (2026-09-30, errata 3
// amendment 12): "Each table gets one update in turn per-tick. 1. work
// streams, 2. readers, 3. merge, 4. fleet." The work table's update is the
// pump, run once a tick: its queue drained, then its cards advanced (a
// waiting card to ready, a ready card to working by the deal, a card in
// review with two ok reads to merging); "no new work moves from waiting ->
// ready -> working except on the FIRST PASS on the work stream table, once
// per-tick". The readers', the merge's and the fleet's updates each write
// their own table, and queue their changes of the work table for the next
// tick's pump; a table another update wrote is updated again, at once, until
// none is ("the tick doesn't end until all dirty bits are cleared"). The
// model is tla/DirtyTick.tla.
var TickTables = []TableUpdate{
	{Work, []TickPartDef{{PartDrain, nil}, {"resolve", TickResolve}, {"deal", TickDeal}, {"accept", TickAccept}}},
	{Readers, []TickPartDef{{"ask", TickAsk}}},
	{Merge, []TickPartDef{{"resume", TickResume}}},
	{Fleet, []TickPartDef{{"presence", TickPresence}, {"level", TickLevel}}},
}

// TickEnd is the tick's end, once the tables are settled: what is always
// true held, the deadlines and the overdue judgments, and the done part last.
// It writes notes, no table.
var TickEnd = []TickPartDef{
	{"check", TickCheck},
	{"deadlines", TickDeadlines},
	{"overdue", TickOverdue},
	{PartDone, TickDone},
}

// TickParts is every part with a planner in the order a tick first runs them:
// the four tables' updates, then the end.
var TickParts = func() []TickPartDef {
	var out []TickPartDef
	for _, u := range TickTables {
		for _, p := range u.Parts {
			if p.Fn != nil {
				out = append(out, p)
			}
		}
	}
	return append(out, TickEnd...)
}()

// Tick is every part's plan over one observed state. Each part is computed
// from the same state; the binding runs them in order, each on a fresh read.
func Tick(s *Snapshot, r TickReq) []TickPart {
	out := make([]TickPart, 0, len(TickParts))
	for _, p := range TickParts {
		plan, due := p.Fn(s, r)
		out = append(out, TickPart{p.Name, plan, due})
	}
	return out
}

// NReadyToMerge is the note the pump addresses to the coordinator once a
// tick for each stream it queued accepted cards in: the merge is the
// coordinator's ("accept is mechanical, but the merge step is not").
const NReadyToMerge = "ready to merge"

// TickAccept is R9 as the machine's (the owner's ruling of 2026-09-30:
// "accept is mechanical, but the merge step is not"): every primary in review
// with ok reads from two different readers at its head moves to merging and
// into its stream's merge queue, in stream turns from the accept's index, in
// the pump's one plan (Accept). The coordinator is told once for each stream
// the tick queued cards in, "ready to merge" with the cards in order: the
// merge is the coordinator's.
func TickAccept(s *Snapshot, r TickReq) (Plan, int) {
	eligible := func(c *Card) string {
		if c.F("result") == "failed" || len(okReaders(s, c)) < 2 {
			return "not two ok reads"
		}
		if why := AcceptHeld(c); why != "" {
			return why
		}
		if m := s.Merge.Card(c.ID); m != nil && (!m.Placed() || m.Col != Returned) {
			return "its merge record is " + placeWord(m)
		}
		if s.StreamCtl(c.Row) == nil {
			return "no merge row"
		}
		return ""
	}
	var ids []string
	for _, c := range eligibleTurns(s.Work.Column(Review), eligible, streamRound(s, PropAcceptStreamIndex)) {
		ids = append(ids, c.ID)
	}
	if len(ids) == 0 {
		return Plan{}, 0
	}
	p := Accept(s, AcceptReq{Sel: Sel{Only: ids}, Who: r.who()})
	by := map[string][]string{}
	var streams []string
	for _, u := range p.Units {
		if !contains(ids, u.Key) {
			continue
		}
		if _, ok := by[u.Stream]; !ok {
			streams = append(streams, u.Stream)
		}
		by[u.Stream] = append(by[u.Stream], u.Key)
	}
	sort.Strings(streams)
	for _, st := range streams {
		n := happened(NReadyToMerge, st, s.Now, by[st]...)
		n.Who, n.To = r.who(), s.Coordinator
		n.What = fmt.Sprintf("%d accepted and queued to merge: %s; run: nova-sprint merge --stream %s", len(by[st]), Preview(by[st], " "), st)
		p.Notes = append(p.Notes, n)
	}
	return p, 0
}

// AcceptHeld is why the pump leaves a primary in review that has ok reads
// from two different readers at its head for the coordinator, "" when it
// accepts it (R9's two holds, docs/SPEC-SPRINT.md section 6):
//   - its CI is red at its head (CIRedAtHead): "ci red on a primary" is the
//     coordinator's to decide (rework, return, drop, ack); the coordinator's
//     accept still takes it;
//   - it was returned to review at its attempt (ReturnedAtAttempt): the
//     coordinator sent it back, and "returned to review" decides it (rework,
//     accept, drop); its reads stand, but only a new attempt's two reads are
//     the pump's to accept.
//
// The holder of a primary in review (reviewAccepts) and reviewJudgment read
// the same holds, so no primary in review is silent. The model is
// tla/DirtyTick.tla Held and AcceptHolds.
func AcceptHeld(c *Card) string {
	switch {
	case CIRedAtHead(c):
		return "its CI is red at its head " + orDash(c.F("head"))
	case ReturnedAtAttempt(c):
		return "it was returned to review at attempt " + c.F("attempt")
	}
	return ""
}

// CIRedAtHead says the primary's last CI observation is red, for its current
// head (a red for an older head is not about the work in review).
func CIRedAtHead(c *Card) bool {
	return c.F("ci") == "red" && c.F("ci_head") == c.F("head")
}

// ReturnedAtAttempt says the coordinator returned the primary to review at
// its current attempt.
func ReturnedAtAttempt(c *Card) bool {
	return c.F(FieldReturnedAttempt) != "" && c.Int(FieldReturnedAttempt) == c.Int("attempt")
}

// Empty says a plan writes nothing.
func (p Plan) Empty() bool {
	return len(p.Units) == 0 && len(p.Notes) == 0 && len(p.Closes) == 0 && len(p.Rows) == 0 && len(p.Updates) == 0
}

// bound keeps the first TickMaxMoves units, and says how many it left out:
// those are due. Notes have no bound but the step's: every judgment a part
// finds is written in its tick (the owner's rule: never a row at a time).
func bound(p Plan) (Plan, int) {
	due := 0
	if len(p.Units) > TickMaxMoves {
		due += len(p.Units) - TickMaxMoves
		p.Units = p.Units[:TickMaxMoves]
	}
	return p, due
}

// T1. TickResolve scans every stream's waiting set in score order, from the
// state, whenever the tick reads the whole sprint: a primary whose every need
// has landed moves to ready; a dropped or missing need is a blocked judgment,
// once. A sentinel is never moved: when everything it needs has landed,
// resolve marks it reached and opens its judgment, and what waits
// behind it stays waiting until the coordinator releases it. No flag says a
// scan is due: what is due is read from the state, so a tick that did not
// finish leaves it due for the next.
//
// The waiting primaries go in stream turns (dealTurns: one from each stream
// in turn, within a stream by work order), so a bound that cuts the plan
// cuts every stream alike (errata 3 amendment 10).
func TickResolve(s *Snapshot, r TickReq) (Plan, int) {
	var ids []string
	for _, c := range dealTurns(s.Work.Column(Waiting), nil) {
		ids = append(ids, c.ID)
	}
	var p Plan
	if len(ids) > 0 {
		p = Resolve(s, ResolveReq{Sel: Sel{Only: ids}, Who: r.who()})
	}
	return bound(p)
}

// dealTurns is the order the tick resolves in (front(s) per stream): one
// card from each stream in turn, in the order of streams, until every stream's
// cards are taken; a stream with none left is skipped, and within a stream the
// cards go in work order (SortCards). A card of a stream not in streams takes
// its turn after them, its streams in name order. So every stream with a
// ready card is worked in parallel: a deal of k cards over n streams gives
// each stream k/n, give or take one, never one stream's backlog first. The
// model is tla/SprintEvents.tla: TurnSorted is this order (one card from each
// stream's front in turn, a stream with none skipped, within a stream by work
// order), PlanDeal takes the room from it, and DealTakesTurns states it from
// the counts of a plan; the witness W28 (MCSprintEventsW28.cfg) is the order of
// the whole table. Errata 3, amendment 4.
func dealTurns(cards []*Card, streams []string) []*Card {
	by := map[string][]*Card{}
	order := append([]string(nil), streams...)
	known := map[string]bool{}
	for _, st := range streams {
		known[st] = true
	}
	var extra []string
	for _, c := range cards {
		if !known[c.Row] {
			known[c.Row] = true
			extra = append(extra, c.Row)
		}
		by[c.Row] = append(by[c.Row], c)
	}
	sort.Strings(extra)
	order = append(order, extra...)
	for _, st := range order {
		SortCards(by[st])
	}
	out := make([]*Card, 0, len(cards))
	for turn := 0; len(out) < len(cards); turn++ {
		for _, st := range order {
			if turn < len(by[st]) {
				out = append(out, by[st][turn])
			}
		}
	}
	return out
}

// T7. TickResume resumes a stream stopped only because a card needed another
// stream's card, once that card has landed: the stuck cards back to queued,
// the stream merging, its judgment closed, and a happened note.
func TickResume(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	due := 0
	for _, st := range s.Merge.Rows() {
		ctl := s.StreamCtl(st)
		if ctl.F("state") != StreamStopped || ctl.F("cause") != "cross" {
			continue
		}
		stuck := s.Merge.Cell(st, Stuck)
		landed := len(stuck) > 0
		var ids []string
		for _, c := range stuck {
			need := c.F("need_card")
			landed = landed && need != "" && s.StateOf(need) == Landed
			ids = append(ids, c.ID)
		}
		if !landed {
			continue
		}
		if len(p.Units) >= TickMaxMoves {
			due++
			continue
		}
		q := Resume(s, ResumeReq{Stream: st, Did: "the card it needed landed", Who: r.who()})
		if len(q.Units) == 0 {
			continue
		}
		n := happened(NResumed, st, s.Now, ids...)
		n.Who = r.who()
		q.Units[0].Notes = append(q.Units[0].Notes, n)
		p.Units = append(p.Units, q.Units...)
	}
	return p, due
}

// T3. TickDeal deals ready primaries in stream turns (streamTurns: one from
// each stream in turn from the deal's stream index on the work table, a stream with no
// ready card skipped, each stream's oldest first by score; Deal moves the
// index past the stream of the last card dealt, errata 3 amendment 10), each
// to the next up
// member round the fleet with room (Deal: the rolling index of round.go,
// errata 3 amendment 5), every member filled up to its width, its ready and
// working cards together (width.go, errata 3 amendment 9): every ready card
// the fleet has room for goes in the one plan, one step, up to TickMaxDeal;
// a withdrawn card is dealt again at a new generation. With no member up and primaries waiting to be dealt, the
// coordinator is told once (N3), and the judgment closes when a member is up.
func TickDeal(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	due := 0
	var ready []*Card
	var conds []cond
	for _, c := range s.Work.Column(Ready) {
		if wc := AtRedealBound(s, c); wc != nil {
			conds = append(conds, cond{typ: NBound, stream: c.Row, card: wc.ID, primaries: []string{c.ID},
				what: fmt.Sprintf("%s: attempt %s was redealt %d times, its bound, and is not dealt again; its history: nova-sprint log --card %s", wc.ID, wc.F("attempt"), wc.Int("redeals"), c.ID)})
			continue
		}
		if !IsSentinel(c) {
			ready = append(ready, c)
		}
	}
	ready = streamTurns(ready, streamRound(s, PropStreamIndex))
	up := s.UpMembers()
	if len(up) == 0 && len(ready) > 0 {
		conds = append(conds, cond{typ: NNoMember, streamLevel: true,
			what: fmt.Sprintf("%d primaries wait to be dealt and no member is up: start nova-sprint fleet beat <member> on a machine, or release a hold with nova-sprint fleet up <member>", len(ready))})
	}
	if len(up) > 0 {
		room := widthRoom(s, up)
		n := min(room, TickMaxDeal, len(ready))
		due = min(room, len(ready)) - n
		if n > 0 {
			ids := make([]string, n)
			for i := range ids {
				ids[i] = ready[i].ID
			}
			p = Deal(s, DealReq{Sel: Sel{Only: ids}, Who: r.who()})
		}
	}
	due += notify(&p, s, conds, []string{NNoMember, NBound}, r)
	return p, due
}

// AtRedealBound is the primary's withdrawn work card when it is at its
// redeal bound: a take of it ended (FieldTakeEnded) with its count at
// MaxRedeals, so the deal that would place it again would pass the bound. The
// tick deals it no more. nil when it is not: a card withdrawn while ready
// keeps its count and is dealt again (tla/DirtyTick.tla AtRB).
func AtRedealBound(s *Snapshot, pr *Card) *Card {
	if pr == nil || pr.Col != Ready {
		return nil
	}
	wc := s.Fleet.Placed(WorkCardID(pr.ID, pr.Int("attempt")))
	if wc != nil && wc.Col == Withdrawn && redealBound(wc) {
		return wc
	}
	return nil
}

// redealBound says the withdrawn work card's next deal would count a take
// past MaxRedeals.
func redealBound(wc *Card) bool {
	return wc.F(FieldTakeEnded) != "" && wc.Int("redeals") >= MaxRedeals
}

// T4. TickLevel evens the up members' ready queues when two differ by more
// than one: the newest cards of the longest queue go round the fleet from the
// deal's index (level, round.levelTo).
func TickLevel(s *Snapshot, r TickReq) (Plan, int) {
	return bound(FleetStep(s, FleetReq{Op: "level", Who: r.who()}))
}

// T2. TickAsk asks two different readers of every primary in review whose
// work did not fail and that has no read card at its attempt (the readers
// named on the primary first). One that cannot be asked, for want of two
// different readers, is a judgment once (N1), closed when it is asked. The
// primaries go in stream turns from the ask's stream index on the work table
// (streamTurns, as the deal's; Ask moves the index), so the readers
// serve every stream alike and no stream's backlog waits behind another's
// (errata 3 amendment 10).
func TickAsk(s *Snapshot, r TickReq) (Plan, int) {
	var ids []string
	due := 0
	askable := func(c *Card) string {
		if c.F("result") != "failed" && len(readsAt(s, c, c.Int("attempt"))) == 0 {
			return ""
		}
		return "asked, or its work failed"
	}
	for _, c := range eligibleTurns(s.Work.Column(Review), askable, askStreamRound(s)) {
		if len(ids) < TickMaxMoves {
			ids = append(ids, c.ID)
		} else {
			due++
		}
	}
	var p Plan
	if len(ids) > 0 {
		p = Ask(s, AskReq{Sel: Sel{Only: ids}, Who: r.who()})
	}
	var conds []cond
	for _, x := range p.Refused {
		if pr := s.Work.Placed(x.Key); pr != nil {
			conds = append(conds, cond{typ: NCannotAsk, stream: pr.Row, primaries: []string{pr.ID}, what: x.Why})
		}
	}
	p.Refused = nil
	due += notify(&p, s, conds, []string{NCannotAsk}, r)
	return p, due
}

// T6. TickCheck holds the state to what is always true (section 9): each
// violation is one judgment (N8), with the rule and the cards, closed by the
// tick when the rule holds again. Its duty is the no-stall rule too (rule 12):
// each stall nothing holds is one judgment "stalled", with the decisions open
// to it, not written again while it stays and closed when it clears; a stall
// that waits behind another is told by the other's.
func TickCheck(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	var conds []cond
	for _, v := range Check(s, nil) {
		c := cond{typ: NInvariant, what: v.String()}
		for _, w := range strings.FieldsFunc(v.Detail, func(x rune) bool { return strings.ContainsRune(" ,():", x) }) {
			if pr := s.Work.Card(w); pr != nil && !contains(c.primaries, w) {
				c.primaries = append(c.primaries, w)
				c.stream = pr.Row
			}
		}
		if len(c.primaries) == 0 {
			c.streamLevel, c.stream = true, ""
		}
		conds = append(conds, c)
	}
	for _, f := range Unheld(HeldState{Snap: s, Running: true, Stopped: r.Stopped}, s.Now) {
		if f.Root != "" {
			continue
		}
		c := cond{typ: NStalled, stream: f.Stream, what: f.What + ": " + f.Why, decisions: f.Decisions}
		if strings.HasPrefix(f.Subject, "stream:") {
			c.streamLevel = true
		} else {
			c.primaries = []string{f.Subject}
		}
		conds = append(conds, c)
	}
	due := notify(&p, s, conds, []string{NInvariant, NStalled}, r)
	return p, due
}

// TickDeadlines writes one judgment for each card or stream past its
// deadline, in running time (N4, N5, N6), and closes it when the card or the
// stream moves.
func TickDeadlines(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	var conds []cond
	late := func(stampField string, c *Card, limit time.Duration) (string, bool) {
		d, ok := r.running(s.Now, c.F(stampField))
		return c.F(stampField), ok && d > limit
	}
	// N4: work cards dealt and not taken, taken and not finished, by the
	// card's state (WorkDeadline): not taken from the first deal since its
	// last take, not finished from the attempt's first take. No redeal or
	// withdrawal rewrites either: a member whose beat lapses again and again
	// cannot reset them, and the time a card spends withdrawn counts.
	for _, c := range s.Fleet.Column(Ready, Working, Withdrawn) {
		field, limit, word, own := WorkDeadline(c)
		if at, ok := late(field, c, limit); ok {
			// fleet down names the member only when it has had its own whole
			// deadline: a card late at the moment it is redealt is not the
			// new member's fault
			decisions := []string{"wait", "drop"}
			if _, mine := late(own, c, limit); own != "" && mine && s.MemberCtl(c.Row).F("status") == Up {
				decisions = append([]string{"fleet down " + c.Row}, decisions...)
			}
			conds = append(conds, cond{typ: NWorkLate, stream: c.F("stream"), card: c.ID, primaries: []string{c.F("primary")},
				what:      fmt.Sprintf("%s %s at %s, %s; at %s", c.ID, strings.TrimPrefix(strings.Replace(field, "untaken_since", "dealt", 1), "first_"), at, word, placeOf(c)),
				decisions: decisions})
		}
	}
	// N5: read cards asked and not begun, begun and not reported.
	for _, c := range s.Readers.Column(Asked, Reading) {
		field, limit, word := "asked", DeadlineUnbegun, "not begun"
		if c.Col == Reading {
			field, limit, word = "begun", DeadlineUnreported, "not reported"
		}
		if at, ok := late(field, c, limit); ok {
			conds = append(conds, cond{typ: NReadLate, stream: c.F("stream"), card: c.ID, primaries: []string{c.F("primary")},
				what: fmt.Sprintf("%s %s of %s at %s, %s; at %s", c.ID, field, c.Row, at, word, placeOf(c))})
		}
	}
	// N6: a stream merging, or waiting with queued cards, with no merge step.
	for _, st := range s.Merge.Rows() {
		ctl := s.StreamCtl(st)
		state := ctl.F("state")
		if state != StreamMerging && !(state == StreamWaiting && s.Merge.Count(st, Queued) > 0) {
			continue
		}
		last := ctl.F("since")
		if ctl.F("moved") > last {
			last = ctl.F("moved")
		}
		if d, ok := r.running(s.Now, last); ok && d > DeadlineMergeIdle {
			conds = append(conds, cond{typ: NMergeLate, stream: st, streamLevel: true,
				what:      fmt.Sprintf("state %s, no merge step since %s", state, last),
				decisions: []string{"merge --stream " + st, "look", "wait"}})
		}
	}
	due := notify(&p, s, conds, []string{NWorkLate, NReadLate, NMergeLate}, r)
	return p, due
}

// TickOverdue marks each open judgment overdue once, when it passes its due
// time in running time (DeadlineJudgment after it was written, or the review
// time the coordinator set with wait): one overdue line (a happened note of
// NOverdue naming the judgment), and a hold on the judgment's open subjects
// (an acknowledged-kind record of NOverdue, never a judgment and shown in no
// inbox) that stops the tick marking it again. The tick closes the hold when
// the judgment closes or is no longer overdue (a wait moved its review time
// on), so a judgment overdue again is marked again. A coordinator who is
// silent is visible: every judgment waiting on them is named, once, as
// overdue.
func TickOverdue(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	type judg struct {
		note     Note
		subjects []string
	}
	var order []string
	byID := map[string]*judg{}
	for _, o := range s.Open {
		if o.Note.Kind != Judgment || o.Note.Type == NSprintDone {
			continue // the sprint is done has no due time
		}
		j := byID[o.Note.ID]
		if j == nil {
			j = &judg{note: o.Note}
			byID[o.Note.ID] = j
			order = append(order, o.Note.ID)
		}
		j.subjects = append(j.subjects, o.Subject())
	}
	overdue := func(n Note) bool {
		if !n.Review.IsZero() && n.ReviewSet.IsZero() {
			return s.Now.After(n.Review)
		}
		if !n.Review.IsZero() {
			d, ok := r.running(s.Now, stamp(n.ReviewSet))
			return ok && d >= n.Review.Sub(n.ReviewSet)
		}
		d, ok := r.running(s.Now, stamp(n.At))
		return ok && d > DeadlineJudgment
	}
	marked := map[string]bool{} // judgment id + subject, held as overdue
	for _, o := range s.Acked {
		if o.Note.Type != NOverdue {
			continue
		}
		j := byID[o.Note.What]
		if j == nil || !contains(j.subjects, o.Subject()) || !overdue(j.note) {
			p.Closes = append(p.Closes, o)
			continue
		}
		marked[OpenKey(o.Note.What, o.Subject())] = true
	}
	due := 0
	for _, id := range order {
		j := byID[id]
		if !overdue(j.note) {
			continue
		}
		var fresh []string
		for _, sub := range j.subjects {
			if !marked[OpenKey(id, sub)] {
				fresh = append(fresh, sub)
			}
		}
		if len(fresh) == 0 {
			continue
		}
		sort.Strings(fresh)
		n := j.note
		past := fmt.Sprintf("%s of running time", DeadlineJudgment)
		if !n.Review.IsZero() {
			past = "its review time " + stamp(n.Review)
		}
		line := Note{Kind: Happened, Type: NOverdue, Stream: n.Stream, Who: r.who(), At: s.Now,
			What: fmt.Sprintf("%s (%s) open since %s, past %s; run: nova-sprint inbox", id, n.Type, stamp(n.At), past)}
		hold := Note{Kind: Acknowledged, Type: NOverdue, Stream: n.Stream, What: id, Who: r.who(), At: s.Now,
			StreamLevel: n.StreamLevel, SprintLevel: n.SprintLevel}
		if !n.StreamLevel && !n.SprintLevel {
			line.Primaries, line.Count = fresh, len(fresh)
			hold.Primaries, hold.Count = fresh, len(fresh)
		} else {
			line.Primaries, line.Count = n.Primaries, n.Count
		}
		p.Notes = append(p.Notes, line, hold)
	}
	return p, due
}

// cond is a condition the tick tells the coordinator of: a judgment of a type
// on its subjects (primaries, or the stream as a whole; a stream-level
// condition with no stream is about the sprint).
type cond struct {
	typ, stream string
	card        string // the consumer card a late judgment is of: its own cause
	primaries   []string
	streamLevel bool
	what        string
	decisions   []string
}

// condKey identifies a condition on one subject: the type, the subject and
// what it says. N3's count and N1's count of free readers change while each
// stays one condition, so they are keyed by their type and subject only.
func condKey(typ, subject, card, what string) string {
	switch typ {
	case NNoMember, NCannotAsk:
		what = ""
	case NWorkLate, NReadLate:
		// a lateness is one per attempt's card and kind (not taken, not
		// finished, not begun, not reported), whatever its facts say now
		what = card + "\x00" + lateKind(what)
	}
	return typ + "\x00" + subject + "\x00" + what
}

// lateKind is a lateness's kind, from its text: "<card> <stamp> at <time>,
// <kind>; at <place>".
func lateKind(what string) string {
	what, _, _ = strings.Cut(what, "; at ")
	if i := strings.LastIndex(what, ", "); i >= 0 {
		return what[i+2:]
	}
	return what
}

// LateStands says the cause of a lateness still stands: no move that resolves
// it has happened. Not finished stands while the attempt's work card is
// ready, working or withdrawn (a redeal or a return to ready does not finish
// it); not taken while the card is not taken (ready or withdrawn); not begun
// while the read is asked; not reported while it is asked or reading. While
// its cause stands a lateness stays raised, whether or not it is late at
// this moment: no judgment flaps closed and open again.
func LateStands(s *Snapshot, n Note) bool {
	kind := lateKind(n.What)
	switch n.Type {
	case NWorkLate:
		c := s.Fleet.Placed(n.Card)
		if c == nil {
			return false
		}
		if kind == "not taken" {
			return c.Col == Ready || c.Col == Withdrawn
		}
		return c.Col == Ready || c.Col == Working || c.Col == Withdrawn
	case NReadLate:
		c := s.Readers.Placed(n.Card)
		if c == nil {
			return false
		}
		if kind == "not begun" {
			return c.Col == Asked
		}
		return c.Col == Asked || c.Col == Reading
	}
	return false
}

// placeOf is where a card is, as a lateness says it: member:column, or
// withdrawn.
func placeOf(c *Card) string {
	if c.Col == Withdrawn {
		return "withdrawn"
	}
	return c.Row + ":" + c.Col
}

func (c cond) subjects() []string {
	if c.streamLevel {
		return []string{StreamSubject(c.stream)}
	}
	return c.primaries
}

// notify writes a judgment for each condition not open already, every one in
// the tick, and closes every open judgment of the types whose condition
// no longer holds: each judgment is written once and never every tick. It
// returns how many conditions it left unwritten past the bound: those are
// due.
func notify(p *Plan, s *Snapshot, conds []cond, types []string, r TickReq) int {
	who := r.who()
	// A condition is open while its judgment is, or while the coordinator's
	// hold of it stands (an acknowledgement, or a wait until its time has
	// passed in running time): either way it is not written again. A wait
	// that has run out is closed, and the condition, when it holds, is raised
	// again.
	var held []Open
	for _, o := range s.Open {
		held = append(held, o)
	}
	for _, o := range s.Acked {
		if !o.Note.Review.IsZero() && contains(types, o.Note.Type) {
			if d, ok := r.running(s.Now, o.Note.At.UTC().Format(time.RFC3339)); ok && d >= o.Note.Review.Sub(o.Note.At) {
				p.Closes = append(p.Closes, o)
				continue
			}
		}
		held = append(held, o)
	}
	open := map[string]bool{}
	judged := map[string]Note{} // the open judgment of a condition, to update in place
	for _, o := range held {
		if contains(types, o.Note.Type) {
			k := condKey(o.Note.Type, o.Subject(), o.Note.Card, o.Note.What)
			open[k] = true
			if o.Note.Kind == Judgment {
				judged[k] = o.Note
			}
		}
	}
	holds := map[string]bool{}
	updated := map[string]bool{}
	update := func(n Note, what string) {
		if n.What == what || updated[n.ID] {
			return
		}
		updated[n.ID] = true
		n.What = what
		p.Updates = append(p.Updates, n)
	}
	due := 0
	for _, c := range conds {
		fresh := false
		for _, sub := range c.subjects() {
			k := condKey(c.typ, sub, c.card, c.what)
			holds[k] = true
			fresh = fresh || !open[k]
			if n, ok := judged[k]; ok && (c.typ == NWorkLate || c.typ == NReadLate) {
				update(n, c.what) // the latest facts, in place
			}
		}
		if !fresh {
			continue
		}
		n := Note{Kind: Judgment, Type: c.typ, Stream: c.stream, Primaries: c.primaries, Count: len(c.primaries), What: c.what,
			Who: who, At: s.Now, StreamLevel: c.streamLevel, Marked: true, Card: c.card}
		n.Decisions = append([]string(nil), c.decisions...)
		if len(n.Decisions) == 0 {
			n.Decisions = append([]string(nil), TickDecisions[c.typ]...)
		}
		p.Notes = append(p.Notes, n)
	}
	closing := map[string]bool{}
	for _, o := range held {
		if !contains(types, o.Note.Type) || holds[condKey(o.Note.Type, o.Subject(), o.Note.Card, o.Note.What)] {
			continue
		}
		if LateStands(s, o.Note) {
			// not late now, and its attempt lives: it stays raised, saying
			// where the card is
			if o.Note.Kind == Judgment {
				c := s.Fleet.Placed(o.Note.Card)
				if o.Note.Type == NReadLate {
					c = s.Readers.Placed(o.Note.Card)
				}
				what, _, _ := strings.Cut(o.Note.What, "; at ")
				update(o.Note, what+"; at "+placeOf(c))
			}
			continue
		}
		p.Closes = append(p.Closes, o)
		closing[o.Note.ID] = true
	}
	// A primary in review whose last judgment the tick closes (its late read
	// reported, say) gets the judgment it needs after it, as every step that
	// leaves a primary in review does.
	seen := map[string]bool{}
	for _, o := range p.Closes {
		pr := s.Work.Placed(o.Subject())
		if pr == nil || seen[pr.ID] {
			continue
		}
		seen[pr.ID] = true
		if j, ok := reviewJudgment(s, pr, reviewStep{closing: closing, writes: p.Notes, who: who}); ok {
			p.Notes = append(p.Notes, j)
		}
	}
	return due
}

// MovesDue is how many moves the tick would make on the snapshot's work and
// fleet: primaries ready to deal, work cards withdrawn, waiting primaries
// whose needs have all landed (a sentinel is the coordinator's release), and
// primaries in review to ask (as TickAsk picks them).
func MovesDue(s *Snapshot) int {
	n := len(s.Fleet.Column(Withdrawn))
	for _, c := range s.Work.Column(Review) {
		if s.Readers != nil && c.F("result") != "failed" && len(readsAt(s, c, c.Int("attempt"))) == 0 {
			n++
		}
	}
	for _, c := range s.Work.Column(Ready) {
		if !IsSentinel(c) && AtRedealBound(s, c) == nil {
			n++
		}
	}
	for _, c := range s.Work.Column(Waiting) {
		if !IsSentinel(c) && len(WaitsFor(s, c, nil)) == 0 {
			n++
		}
	}
	return n
}

// WorkDeadline is the deadline a work card is held to, by its state: a card
// not taken since its last deal (ready, or withdrawn again before a take) is
// late not taken 15 minutes from untaken_since, the first deal since its last
// take, which no later redeal or withdrawal rewrites; a card working, or
// withdrawn from a take, is late not finished 2 hours from first_taken, the
// attempt's first take. The tick's deadline part and the no-stall rule both
// call it, so they speak at the same moment. field is the stamp it counts
// from; own is the stamp of the current member's own deal or take, which
// says whether that member has had its whole deadline ("" when the card is
// withdrawn: no member holds it).
func WorkDeadline(c *Card) (field string, limit time.Duration, word, own string) {
	first := func(fields ...string) string {
		for _, f := range fields[:len(fields)-1] {
			if c.F(f) != "" {
				return f
			}
		}
		return fields[len(fields)-1]
	}
	switch c.Col {
	case Working:
		own = "taken"
	case Ready:
		own = "dealt"
	}
	switch {
	case c.Col == Working:
		return first("first_taken", "taken"), DeadlineUnfinished, "not finished", own
	case c.F("untaken_since") != "":
		return "untaken_since", DeadlineUntaken, "not taken", own
	case c.F("first_taken") != "":
		return "first_taken", DeadlineUnfinished, "not finished", own
	}
	return first("first_dealt", "dealt"), DeadlineUntaken, "not taken", own
}

// PartDone is the name of the tick's last part, the done part.
const PartDone = "done"

// DoneCause is the cause the machine's record carries when the done part
// stopped it, and DoneHint what the coordinator does to go on (errata 3
// amendment 6).
const (
	DoneCause = "done"
	DoneHint  = "to continue: add work, then nova-sprint start"
)

// SprintDoneCounts is the sprint's landed primaries and its dropped ones (the
// streams' control cards' dropped counters), and whether the sprint is done:
// no primary waiting, ready, working, in review or merging, and at least one
// landed or dropped. A sprint that never had a card is not done.
func SprintDoneCounts(s *Snapshot) (landed, dropped int, done bool) {
	for _, st := range []State{Waiting, Ready, Working, Review, Merging} {
		if len(s.Work.Column(st)) > 0 {
			return 0, 0, false
		}
	}
	landed = len(s.Work.Column(Landed))
	for _, row := range s.Work.Rows() {
		dropped += s.StreamCtl(row).Int("dropped")
	}
	return landed, dropped, landed+dropped > 0
}

// DoneWhat is the words of "the sprint is done": the counts and, when the
// first start is known, how long the sprint took from it in wall time.
func DoneWhat(landed, dropped int, now, started time.Time) string {
	w := fmt.Sprintf("%d landed, %d dropped", landed, dropped)
	if !started.IsZero() && !now.Before(started) {
		w += ", took " + TookText(now.Sub(started)) + " from the first start"
	}
	return w
}

// TookText is a duration as the done note and the sprint line say it: to the
// second.
func TookText(d time.Duration) string { return d.Round(time.Second).String() }

// TickDone is R15 of the design as errata 3 amendment 6 amends it
// (design/EVENT-DRIVEN-TICK-v2.1-ERRATA-3.md, amendment 6): the sprint done
// (SprintDoneCounts) is no judgment that waits on the coordinator. The part
// writes one happened note, "the sprint is done", addressed to the
// coordinator, with the counts, the time from the first start and the hint;
// the binding stops the machine as the part's step commits, its record
// STOPPED with the cause DoneCause, so no part and no tick runs after it. It
// is the tick's last part: a tick of a RUNNING machine that finds the sprint
// done ends there. It writes nothing on a sprint that is not done, and it runs
// only while the machine is RUNNING, so it says it once for each run that
// finishes the sprint. The reference model's tick stops the same way
// (refmodel.Tick); tla/SprintEvents.tla does not model R15.
func TickDone(s *Snapshot, r TickReq) (Plan, int) {
	landed, dropped, done := SprintDoneCounts(s)
	if !done {
		return Plan{}, 0
	}
	n := happened(NSprintDone, "", s.Now)
	n.Who, n.To, n.Hint = r.who(), s.Coordinator, DoneHint
	n.What = DoneWhat(landed, dropped, s.Now, r.Started)
	return Plan{Notes: []Note{n}}, 0
}
