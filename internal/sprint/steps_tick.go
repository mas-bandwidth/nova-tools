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
	// MaxReadyPerMember is the longest ready queue the tick deals a member:
	// work is dealt late and little, so a member going down takes little
	// with it and a later primary never waits behind a long queue.
	MaxReadyPerMember = 2
	// TickMaxMoves bounds the units one part of a tick applies; the rest are
	// due, and the next tick reads the whole sprint and moves them.
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

// The tick's own notification types.
const (
	NResumed   = "stream resumed: the card it needed landed"
	NCannotAsk = "cannot ask: fewer than two different readers are free"
	NNoMember  = "no fleet member is up"
	NInvariant = "an invariant is broken"
	NWorkLate  = "a work card is past its deadline"
	NReadLate  = "a read card is past its deadline"
	NMergeLate = "a stream has had no merge step past its deadline"
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
	NCannotAsk: {"reader add", "rework", "drop", "wait"},
	NNoMember:  {"fleet beat", "fleet up", "wait"},
	NInvariant: {"look at the card", "repair", "wait"},
	NWorkLate:  {"fleet down <member>", "wait", "drop"},
	NReadLate:  {"ask --another", "wait", "drop"},
	NMergeLate: {"merge --stream <s>", "look", "wait"},
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
	return len(p.Units) == 0 && len(p.Notes) == 0 && len(p.Closes) == 0 && len(p.Rows) == 0
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
// has landed moves to ready; a need that was dropped is the blocked judgment,
// once. A sentinel is never moved: when everything it needs has landed the
// tick marks it reached and opens its judgment (SentinelsDue), and what waits
// behind it stays waiting until the coordinator releases it. No flag says a
// scan is due: what is due is read from the state, so a tick that did not
// finish leaves it due for the next.
func TickResolve(s *Snapshot, r TickReq) (Plan, int) {
	var ids []string
	for _, c := range s.Work.Column(Waiting) {
		if !IsSentinel(c) {
			ids = append(ids, c.ID)
		}
	}
	var p Plan
	if len(ids) > 0 {
		p = Resolve(s, ResolveReq{Sel: Sel{Only: ids}, Who: r.who()})
	}
	due := SentinelsDue(s, r.who())
	p.Units = append(p.Units, due.Units...)
	p.Notes = append(p.Notes, due.Notes...)
	return bound(p)
}

// T7. TickResume resumes a stream stopped only because a card needed another
// stream's card, once that card has landed: the stuck cards back to queued,
// the stream merging, its judgment closed, and a happened note.
func TickResume(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	due := 0
	for _, st := range s.Merge.Rows {
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

// T3. TickDeal deals ready primaries, oldest first by score, each to the up
// member with the shortest ready queue, keeping every ready queue no longer
// than MaxReadyPerMember; a withdrawn card is dealt again at a new
// generation. With no member up and primaries waiting to be dealt, the
// coordinator is told once (N3), and the judgment closes when a member is up.
func TickDeal(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	due := 0
	var ready []*Card
	for _, c := range s.Work.Column(Ready) {
		if !IsSentinel(c) {
			ready = append(ready, c)
		}
	}
	up := s.UpMembers()
	var conds []cond
	if len(up) == 0 && len(ready) > 0 {
		conds = append(conds, cond{typ: NNoMember, streamLevel: true,
			what: fmt.Sprintf("%d primaries wait to be dealt and no member is up: start nova-sprint fleet beat <member> on a machine, or release a hold with nova-sprint fleet up <member>", len(ready))})
	}
	if len(up) > 0 {
		room := 0
		for _, m := range up {
			room += max(0, MaxReadyPerMember-s.Fleet.Count(m, Ready))
		}
		n := min(room, TickMaxMoves, len(ready))
		due = min(room, len(ready)) - n
		if n > 0 {
			ids := make([]string, n)
			for i := range ids {
				ids[i] = ready[i].ID
			}
			p = Deal(s, DealReq{Sel: Sel{Only: ids}, Who: r.who()})
		}
	}
	due += notify(&p, s, conds, []string{NNoMember}, r)
	return p, due
}

// T4. TickLevel evens the up members' ready queues when two differ by more
// than one: the newest cards go to the shortest queue.
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
// tick when the rule holds again.
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
	due := notify(&p, s, conds, []string{NInvariant}, r)
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
	// N4: work cards dealt and not taken, taken and not finished.
	for _, c := range s.Fleet.Column(Ready, Working) {
		field, limit, word := "dealt", DeadlineUntaken, "not taken"
		if c.Col == Working {
			field, limit, word = "taken", DeadlineUnfinished, "not finished"
		}
		if at, ok := late(field, c, limit); ok {
			conds = append(conds, cond{typ: NWorkLate, stream: c.F("stream"), primaries: []string{c.F("primary")},
				what:      fmt.Sprintf("%s@%s %s to %s at %s, %s", c.ID, c.F("gen"), field, c.Row, at, word),
				decisions: []string{"fleet down " + c.Row, "wait", "drop"}})
		}
	}
	// N5: read cards asked and not begun, begun and not reported.
	for _, c := range s.Readers.Column(Asked, Reading) {
		field, limit, word := "asked", DeadlineUnbegun, "not begun"
		if c.Col == Reading {
			field, limit, word = "begun", DeadlineUnreported, "not reported"
		}
		if at, ok := late(field, c, limit); ok {
			conds = append(conds, cond{typ: NReadLate, stream: c.F("stream"), primaries: []string{c.F("primary")},
				what: fmt.Sprintf("%s %s of %s at %s, %s", c.ID, field, c.Row, at, word)})
		}
	}
	// N6: a stream merging, or waiting with queued cards, with no merge step.
	for _, st := range s.Merge.Rows {
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
	primaries   []string
	streamLevel bool
	what        string
	decisions   []string
}

// condKey identifies a condition on one subject: the type, the subject and
// what it says. N3's count and N1's count of free readers change while each
// stays one condition, so they are keyed by their type and subject only.
func condKey(typ, subject, what string) string {
	if typ == NNoMember || typ == NCannotAsk {
		what = ""
	}
	return typ + "\x00" + subject + "\x00" + what
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
	for _, o := range held {
		if contains(types, o.Note.Type) {
			open[condKey(o.Note.Type, o.Subject(), o.Note.What)] = true
		}
	}
	holds := map[string]bool{}
	written, due := 0, 0
	for _, c := range conds {
		fresh := false
		for _, sub := range c.subjects() {
			k := condKey(c.typ, sub, c.what)
			holds[k] = true
			fresh = fresh || !open[k]
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
			Who: who, At: s.Now, StreamLevel: c.streamLevel, Marked: true}
		n.Decisions = append([]string(nil), c.decisions...)
		if len(n.Decisions) == 0 {
			n.Decisions = append([]string(nil), TickDecisions[c.typ]...)
		}
		p.Notes = append(p.Notes, n)
	}
	for _, o := range held {
		if contains(types, o.Note.Type) && !holds[condKey(o.Note.Type, o.Subject(), o.Note.What)] {
			p.Closes = append(p.Closes, o)
		}
	}
	return due
}

// MovesDue is how many moves the tick would make on the snapshot's work and
// fleet: primaries ready to deal, work cards withdrawn, and waiting primaries
// whose needs have all landed (a sentinel is the coordinator's release).
func MovesDue(s *Snapshot) int {
	n := len(s.Fleet.Column(Withdrawn))
	for _, c := range s.Work.Column(Ready) {
		if !IsSentinel(c) {
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
