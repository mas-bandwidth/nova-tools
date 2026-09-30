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
	// due, and the next tick reads the whole sprint and moves them. Levelling
	// moves are not counted in it. The deal's own bound is TickMaxDeal (width.go):
	// it fills every member to its width in one step.
	TickMaxMoves = 200
	// TickMaxNotes bounds the judgments one part of a tick writes; the rest
	// are due, and the next tick writes them.
	TickMaxNotes = 50
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
// its member went down or away (its redeals counter, which no take resets):
// past it the card stays withdrawn, and the bound's judgment names it.
const MaxRedeals = 3

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

// TickParts is the tick's parts in their fixed order. Repair of a pending
// operation (T5) comes first and needs the store: the binding does it.
var TickParts = []struct {
	Name string
	Fn   TickPartFn
}{
	{"presence", TickPresence},
	{"resolve", TickResolve},
	{"resume", TickResume},
	{"deal", TickDeal},
	{"level", TickLevel},
	{"ask", TickAsk},
	{"check", TickCheck},
	{"deadlines", TickDeadlines},
	{"overdue", TickOverdue},
	{PartDone, TickDone},
}

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

// Empty says a plan writes nothing.
func (p Plan) Empty() bool {
	return len(p.Units) == 0 && len(p.Notes) == 0 && len(p.Closes) == 0 && len(p.Rows) == 0 && len(p.Updates) == 0
}

// bound keeps the first TickMaxMoves units and the first TickMaxNotes
// unit-less notes, and says how many it left out: those are due.
func bound(p Plan) (Plan, int) {
	due := 0
	if len(p.Units) > TickMaxMoves {
		due += len(p.Units) - TickMaxMoves
		p.Units = p.Units[:TickMaxMoves]
	}
	if len(p.Notes) > TickMaxNotes {
		due += len(p.Notes) - TickMaxNotes
		p.Notes = p.Notes[:TickMaxNotes]
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
func TickResolve(s *Snapshot, r TickReq) (Plan, int) {
	var ids []string
	for _, c := range s.Work.Column(Waiting) {
		ids = append(ids, c.ID)
	}
	var p Plan
	if len(ids) > 0 {
		p = Resolve(s, ResolveReq{Sel: Sel{Only: ids}, Who: r.who()})
	}
	return bound(p)
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

// T3. TickDeal deals ready primaries in stream turns (dealTurns: one from each
// stream in turn, each stream's oldest first by score), each to the next up
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
	ready = dealTurns(ready, nil)
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
// redeal bound: the tick deals it no more. nil when it is not.
func AtRedealBound(s *Snapshot, pr *Card) *Card {
	if pr == nil || pr.Col != Ready {
		return nil
	}
	wc := s.Fleet.Placed(WorkCardID(pr.ID, pr.Int("attempt")))
	if wc != nil && wc.Col == Withdrawn && wc.Int("redeals") >= MaxRedeals {
		return wc
	}
	return nil
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
// different readers, is a judgment once (N1), closed when it is asked.
func TickAsk(s *Snapshot, r TickReq) (Plan, int) {
	var ids []string
	due := 0
	for _, c := range s.Work.Column(Review) {
		if c.F("result") != "failed" && len(readsAt(s, c, c.Int("attempt"))) == 0 {
			if len(ids) < TickMaxMoves {
				ids = append(ids, c.ID)
			} else {
				due++
			}
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
	written, due := 0, 0
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
		if written >= TickMaxNotes {
			due++
			continue
		}
		written++
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

// notify writes a judgment for each condition not open already (bounded by
// TickMaxNotes), and closes every open judgment of the types whose condition
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
	written, due := 0, 0
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
		if written >= TickMaxNotes {
			due++
			continue
		}
		written++
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
