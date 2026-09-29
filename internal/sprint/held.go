package sprint

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// The no-stall rule (docs/SPEC-SPRINT.md section 9, rule 12): every primary
// that has not landed and is on the table is held by something that will move
// it or tell the coordinator about it. Exactly what holds it is one of:
//
//	(a) an outside actor, before its deadline: a live work card in an up
//	    member's ready or working cell; a read card asked or reading; its
//	    merge card queued in a stream that merges;
//	(b) the next tick: a part of the tick, called on the state, moves it or
//	    writes a judgment naming it;
//	(c) an open judgment names it (or its stream, when the stream is stopped
//	    or it is merging there), or the coordinator acknowledged the tick's
//	    judgment that names it;
//	(d) it waits on something that is itself held, followed through the chain
//	    (a need not landed, a sentinel not released, a place in the ready
//	    queues); a chain that ends in nothing, or in a cycle, holds nothing;
//	(e) the machine is STOPPED and the next tick would move it: (b), and what
//	    is visible is that the sprint is stopped.
//
// Anything else is stalled, and so is a judgment past its due time that no
// tick marks overdue, a stopped stream with no open judgment, and an
// operation pending past its grace that a tick since has not finished. The
// tick's check part writes one judgment for each stall ("stalled"), so a stall
// the design missed raises its own interrupt.

// The holders, as Holder names them.
const (
	HeldByActor    = "a"    // an outside actor, before its deadline
	HeldByTick     = "b"    // the next tick moves it or writes its judgment
	HeldByJudgment = "c"    // an open judgment names it
	HeldByWaiting  = "d"    // it waits on something held
	HeldByStopped  = "e"    // the machine is STOPPED; the next tick would move it
	HeldDone       = "done" // landed, or off the table: nothing is owed
)

// NStalled is the judgment of a stall: something nothing holds.
const NStalled = "stalled"

// PendingOp is the operation the sprint's fence holds, as the no-stall rule
// reads it.
type PendingOp struct {
	ID, Verb string
	At       time.Time
}

// HeldState is what the no-stall rule reads: the four tables with the open
// judgments and the tick's acknowledged conditions (Snap), the machine's
// state, and the operation the fence holds.
type HeldState struct {
	Snap    *Snapshot
	Running bool // the machine is RUNNING
	// Stopped is the time the machine was STOPPED between two clock readings:
	// deadlines count running time. nil is none.
	Stopped func(from, to time.Time) time.Duration
	// Pending is the operation the fence holds (nil none), Grace how long it
	// is taken as in flight, and LastTick when the machine last ticked.
	Pending  *PendingOp
	Grace    time.Duration
	LastTick time.Time
}

// Hold is what holds one primary: the holder (HeldBy*, "" when nothing does:
// it is stalled), its place, and why. Root, for a stall, is the stalled card
// it waits on ("" when it is its own).
type Hold struct {
	ID    string `json:"id"`
	By    string `json:"by"`
	Place string `json:"place"`
	Why   string `json:"why"`
	Root  string `json:"root,omitempty"`
}

// Stalled says nothing holds it.
func (h Hold) Stalled() bool { return h.By == "" }

func (h Hold) String() string {
	if h.Stalled() {
		return "stalled: " + h.Place + ": " + h.Why
	}
	if h.By == HeldDone {
		return h.Place
	}
	return "(" + h.By + ") " + h.Why
}

// Finding is one stall: its subject (a primary, or a stream's subject, or the
// sprint's for a pending operation), what it is and why nothing holds it, and
// the decisions open to the coordinator. Root, when set, is the stall it waits
// behind: the tick writes one judgment per root.
type Finding struct {
	Subject   string   `json:"subject"`
	Stream    string   `json:"stream,omitempty"`
	What      string   `json:"what"`
	Why       string   `json:"why"`
	Decisions []string `json:"decisions,omitempty"`
	Root      string   `json:"root,omitempty"`
}

func (f Finding) String() string { return "stalled: " + f.What + ": " + f.Why }

// CheckHeld is the no-stall rule as a rule of check: each stall is one
// violation of rule 12.
func CheckHeld(h HeldState, now time.Time) []Violation {
	var out []Violation
	for _, f := range Unheld(h, now) {
		out = append(out, Violation{Rule: 12, Detail: f.String()})
	}
	return out
}

// Holder is what holds the primary id now.
func Holder(h HeldState, now time.Time, id string) Hold { return newHeld(h, now).hold(id) }

// Unheld is every stall, in a fixed order: the primaries (by id), then the
// judgments past due, the stopped streams, and the pending operation. While an
// operation is pending the tables are a partial state of it, and only the
// operation is judged: it is a stall past its grace when a tick since has not
// finished it.
func Unheld(h HeldState, now time.Time) []Finding {
	var out []Finding
	if p := h.Pending; p != nil {
		if h.Grace > 0 && now.Sub(p.At) >= h.Grace && h.LastTick.After(p.At.Add(h.Grace)) {
			out = append(out, Finding{Subject: StreamSubject(""), What: "operation " + p.ID + " (" + p.Verb + ") pending",
				Why: "past its grace, and a tick since has not finished it", Decisions: []string{"repair", "check"}})
		}
		return out
	}
	c := newHeld(h, now)
	s := c.s
	var ids []string
	for _, st := range States[:len(States)-1] {
		for _, pr := range s.Work.Column(st) {
			ids = append(ids, pr.ID)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		hd := c.hold(id)
		if !hd.Stalled() {
			continue
		}
		pr := s.Work.Placed(id)
		out = append(out, Finding{Subject: id, Stream: pr.Row, What: hd.Place, Why: hd.Why, Root: hd.Root, Decisions: c.decisions(pr)})
	}
	out = append(out, c.overdueUnmarked()...)
	for _, st := range s.Merge.Rows {
		ctl := s.StreamCtl(st)
		if ctl.F("state") != StreamStopped || c.openOn(StreamSubject(st)) || c.tickStream[st] != "" {
			continue
		}
		out = append(out, Finding{Subject: StreamSubject(st), Stream: st, What: "stream " + st + " stopped (" + orDash(ctl.F("cause")) + ")",
			Why: "no judgment is open on it", Decisions: []string{"resume"}})
	}
	return out
}

// held is one reading of the rule: the state at now, what the next tick does
// (from the tick's own parts), and the judgments open on each subject.
type held struct {
	h   HeldState
	s   *Snapshot
	req TickReq
	// tick is what the next tick does to a card (a unit keyed by it, or a
	// judgment naming it), by id; tickStream by stream; noMember says the
	// tick writes that no fleet member is up; marks is the judgments the
	// tick marks overdue; due is what the tick leaves past its bounds.
	tick       map[string]string
	tickStream map[string]string
	noMember   bool
	marks      map[string]bool
	due        int
	// judged is the judgments open on a subject, and the tick's conditions
	// the coordinator acknowledged there, the stall judgments aside.
	judged map[string][]string
	memo   map[string]Hold
	on     map[string]bool
}

// heldParts is the tick's parts the rule asks what the next tick does: every
// part but the check, whose duty the rule is.
var heldParts = []TickPartFn{TickResolve, TickResume, TickDeal, TickLevel, TickAsk, TickDeadlines, TickOverdue}

func newHeld(h HeldState, now time.Time) *held {
	s := *h.Snap
	s.Now = now
	c := &held{h: h, s: &s, req: TickReq{Who: MachineActor, Stopped: h.Stopped},
		tick: map[string]string{}, tickStream: map[string]string{}, marks: map[string]bool{},
		judged: map[string][]string{}, memo: map[string]Hold{}, on: map[string]bool{}}
	for _, o := range s.Open {
		if o.Note.Kind == Judgment && o.Note.Type != NStalled {
			c.judged[o.Subject()] = append(c.judged[o.Subject()], o.Note.Type+" "+o.Note.ID)
		}
	}
	for _, o := range s.Acked {
		if _, tick := TickDecisions[o.Note.Type]; tick && o.Note.Type != NStalled {
			c.judged[o.Subject()] = append(c.judged[o.Subject()], o.Note.Type+" (acknowledged)")
		}
	}
	for _, fn := range heldParts {
		p, due := fn(c.s, c.req)
		c.due += due
		for _, u := range p.Units {
			if strings.HasPrefix(u.Key, "ctl-") && u.Stream != "" {
				c.tickStream[u.Stream] = u.Moved
			} else {
				c.tick[u.Key] = u.Moved
			}
			c.notes(u.Notes)
		}
		c.notes(p.Notes)
	}
	return c
}

// openOn says a judgment is open on the subject, the stall judgments aside:
// an acknowledgement is not one.
func (c *held) openOn(subject string) bool {
	for _, j := range c.judged[subject] {
		if !strings.HasSuffix(j, " (acknowledged)") {
			return true
		}
	}
	return false
}

// notes records what the tick's notes name.
func (c *held) notes(ns []Note) {
	for _, n := range ns {
		switch {
		case n.Kind == Acknowledged && n.Type == NOverdue:
			c.marks[n.What] = true
		case n.Kind != Judgment:
		case n.Type == NNoMember:
			c.noMember = true
		case n.StreamLevel:
			c.tickStream[n.Stream] = "writes " + n.Type
		default:
			for _, p := range n.Primaries {
				c.tick[p] = "writes " + n.Type
			}
		}
	}
}

// running is the running time since a stamp; ok is false when the stamp is
// absent or unreadable.
func (c *held) running(stampText string) (time.Duration, bool) {
	return c.req.running(c.s.Now, stampText)
}

// byTick is (b), or (e) when the machine is STOPPED.
func (c *held) byTick(what string) (string, string) {
	if !c.h.Running {
		return HeldByStopped, "the machine is STOPPED; the next tick: " + what
	}
	return HeldByTick, "the next tick: " + what
}

func (c *held) hold(id string) Hold {
	if hd, ok := c.memo[id]; ok {
		return hd
	}
	hd := c.judge(id)
	c.memo[id] = hd
	return hd
}

func (c *held) judge(id string) Hold {
	s := c.s
	pr := s.Work.Card(id)
	switch {
	case pr == nil:
		return Hold{ID: id, Place: id, Why: "not on the table"}
	case !pr.Placed():
		return Hold{ID: id, By: HeldDone, Place: id + " off the table (" + orDash(pr.F("outcome")) + ")"}
	case pr.Col == Landed:
		return Hold{ID: id, By: HeldDone, Place: id + " landed"}
	}
	place := id + " " + pr.Col
	if IsSentinel(pr) {
		place = "sentinel " + place
	}
	hd := Hold{ID: id, Place: place}
	if why := c.actor(pr); why != "" {
		hd.By, hd.Why = HeldByActor, why
		return hd
	}
	if what := c.tickOn(pr); what != "" {
		hd.By, hd.Why = c.byTick(what)
		return hd
	}
	if why := c.judgment(pr); why != "" {
		hd.By, hd.Why = HeldByJudgment, why
		return hd
	}
	why, root, ok := c.waits(pr)
	if ok {
		hd.By, hd.Why = HeldByWaiting, why
		return hd
	}
	hd.Why, hd.Root = why, root
	return hd
}

// actor is (a): the outside actor that holds the primary before its deadline.
func (c *held) actor(pr *Card) string {
	s := c.s
	switch pr.Col {
	case Working:
		wc := s.Fleet.Placed(pr.F("work"))
		if wc == nil || wc.F("primary") != pr.ID || (wc.Col != Ready && wc.Col != Working) || s.MemberCtl(wc.Row).F("status") != Up {
			return ""
		}
		field, limit, _, _ := WorkDeadline(wc) // the tick's own deadline
		if d, ok := c.running(wc.F(field)); ok && d <= limit {
			return fmt.Sprintf("member %s holds %s@%s (%s), %s of %s running", wc.Row, wc.ID, wc.F("gen"), wc.Col, d.Round(time.Second), limit)
		}
	case Review:
		for _, rc := range readsAt(s, pr, pr.Int("attempt")) {
			if (rc.Col != Asked && rc.Col != Reading) || !ReadCardAgrees(rc) {
				continue
			}
			field, limit := "asked", DeadlineUnbegun
			if rc.Col == Reading {
				field, limit = "begun", DeadlineUnreported
			}
			if d, ok := c.running(rc.F(field)); ok && d <= limit {
				return fmt.Sprintf("reader %s holds %s (%s), %s of %s running", rc.Row, rc.ID, rc.Col, d.Round(time.Second), limit)
			}
		}
	case Merging:
		m := s.Merge.Placed(pr.ID)
		ctl := s.StreamCtl(pr.Row)
		state := ctl.F("state")
		if m == nil || m.Row != pr.Row || m.Col != Queued || (state != StreamMerging && state != StreamWaiting) {
			return ""
		}
		for _, x := range s.Merge.Cell(pr.Row, Stuck) {
			if x.Score < m.Score || x.Score == m.Score && x.ID < m.ID {
				return "" // behind a stuck card: the merge step never passes it
			}
		}
		last := ctl.F("since")
		if ctl.F("moved") > last {
			last = ctl.F("moved")
		}
		if d, ok := c.running(last); ok && d <= DeadlineMergeIdle {
			return fmt.Sprintf("queued in stream %s (%s) for its merge step, %s of %s running", pr.Row, state, d.Round(time.Second), DeadlineMergeIdle)
		}
	}
	return ""
}

// tickOn is what the next tick does to the primary, "" when nothing.
func (c *held) tickOn(pr *Card) string {
	if w := c.tick[pr.ID]; w != "" {
		return w
	}
	if pr.Col == Merging {
		if w := c.tickStream[pr.Row]; w != "" {
			return w
		}
	}
	if pr.Col == Ready && !IsSentinel(pr) && c.noMember {
		return "writes " + NNoMember
	}
	if c.due > 0 && pr.Col != Merging {
		return fmt.Sprintf("%d moves and judgments are due past its bounds", c.due)
	}
	return ""
}

// judgment is (c): the open judgment, or the acknowledged condition of the
// tick, that names the primary, its stopped stream, or its stream while it
// merges there; "" when none.
func (c *held) judgment(pr *Card) string {
	if j := c.judged[pr.ID]; len(j) > 0 {
		return "open: " + strings.Join(j, ", ")
	}
	st := StreamSubject(pr.Row)
	if j := c.judged[st]; len(j) > 0 {
		if c.s.StreamCtl(pr.Row).F("state") == StreamStopped {
			return "stream " + pr.Row + " is stopped; open: " + strings.Join(j, ", ")
		}
		if pr.Col == Merging {
			return "its stream " + pr.Row + " merging; open: " + strings.Join(j, ", ")
		}
	}
	if pr.Col == Ready && !IsSentinel(pr) && len(c.s.UpMembers()) == 0 {
		for _, j := range c.judged[StreamSubject("")] {
			if strings.HasPrefix(j, NNoMember) {
				return "no fleet member is up; open: " + j
			}
		}
	}
	return ""
}

// waits is (d): what the primary waits on, when each of those is held; else
// why the chain holds nothing, with the stalled card it ends at (root).
func (c *held) waits(pr *Card) (why, root string, ok bool) {
	s := c.s
	switch pr.Col {
	case Waiting:
		w := WaitsFor(s, pr, nil)
		if len(w) == 0 {
			if IsSentinel(pr) && pr.F("reached") != "" {
				return "reached, and no judgment is open on it", "", false
			}
			return "every need has landed or was waived, and nothing moves it", "", false
		}
		c.on[pr.ID] = true
		defer delete(c.on, pr.ID)
		var held []string
		for _, n := range w {
			nc := s.Work.Card(n)
			switch {
			case c.on[n]:
				return "its needs make a cycle through " + n, "", false
			case nc == nil:
				return "waits on " + n + ", which is not on the table", "", false
			case !nc.Placed():
				return "waits on " + n + ", off the table (" + orDash(nc.F("outcome")) + "), and no judgment is open on it", "", false
			}
			hd := c.hold(n)
			if hd.Stalled() {
				r := hd.Root
				if r == "" {
					r = n
				}
				return "waits on " + n + ", which is stalled", r, false
			}
			kind := "needs"
			if IsSentinel(nc) {
				kind = "waits behind sentinel"
			}
			held = append(held, kind+" "+n+" ("+hd.By+")")
		}
		return strings.Join(held, "; "), "", true
	case Ready:
		up := s.UpMembers()
		if len(up) == 0 {
			return "no fleet member is up, and no judgment says so", "", false
		}
		// The ready queues' free places go to the ready primaries in work
		// order: one with as many ahead of it as there are places waits for
		// the workers to take from the queues.
		room := 0
		for _, m := range up {
			room += max(0, MaxReadyPerMember-s.Fleet.Count(m, Ready))
		}
		ahead := 0
		for _, x := range s.Work.Column(Ready) {
			if x.ID == pr.ID {
				break
			}
			if !IsSentinel(x) {
				ahead++
			}
		}
		if ahead < room {
			return "a member has room in its ready queue, and nothing deals it", "", false
		}
		return fmt.Sprintf("waits for a place in a ready queue: %d free, %d ready ahead of it", room, ahead), "", true
	case Working:
		return "no live work card of an up member holds it before its deadline, and no judgment is open on it", "", false
	case Review:
		switch {
		case len(okReaders(s, pr)) >= 2:
			return "acceptable (two different readers said ok at its head), and no judgment is open on it", "", false
		case pr.F("result") == "failed":
			return "its work came back failed, and no judgment is open on it", "", false
		}
		return "no read is outstanding before its deadline, it is not acceptable, and no judgment is open on it", "", false
	case Merging:
		m := s.Merge.Placed(pr.ID)
		return "its merge card is " + placeWord(orEmpty(m, pr.ID)) + " in a stream " + orDash(s.StreamCtl(pr.Row).F("state")) + ", and no judgment is open on it", "", false
	}
	return "nothing holds it", "", false
}

// overdueUnmarked is every judgment past its due time that no overdue mark
// holds and the next tick does not mark.
func (c *held) overdueUnmarked() []Finding {
	s := c.s
	marked := map[string]bool{}
	for _, o := range s.Acked {
		if o.Note.Type == NOverdue {
			marked[OpenKey(o.Note.What, o.Subject())] = true
		}
	}
	var out []Finding
	seen := map[string]bool{}
	for _, o := range s.Open {
		n := o.Note
		if n.Kind != Judgment || n.Type == NSprintDone || seen[n.ID] || marked[OpenKey(n.ID, o.Subject())] || c.marks[n.ID] {
			continue
		}
		past := false
		if !n.Review.IsZero() && n.ReviewSet.IsZero() {
			past = s.Now.After(n.Review)
		} else if !n.Review.IsZero() {
			// a wait counts running time from when it was set
			d, ok := c.running(stamp(n.ReviewSet))
			past = ok && d >= n.Review.Sub(n.ReviewSet)
		} else if d, ok := c.running(stamp(n.At)); ok {
			past = d > DeadlineJudgment
		}
		if !past {
			continue
		}
		seen[n.ID] = true
		f := Finding{Subject: o.Subject(), Stream: n.Stream, What: "judgment " + n.ID + " (" + n.Type + ")",
			Why: "past its due time, and no overdue mark holds it", Decisions: []string{"look at the card", "wait"}}
		out = append(out, f)
	}
	return out
}

// decisions is what the coordinator may do about a stalled primary.
func (c *held) decisions(pr *Card) []string {
	var out []string
	switch {
	case IsSentinel(pr) && pr.F("reached") != "":
		out = []string{"release", "drop"}
	case pr.Col == Ready:
		out = []string{"fleet up", "drop"}
	case pr.Col == Working:
		if wc := c.s.Fleet.Placed(pr.F("work")); wc != nil && wc.Row != "" {
			out = append(out, "fleet down "+wc.Row)
		}
		out = append(out, "drop")
	case pr.Col == Review && len(okReaders(c.s, pr)) >= 2:
		out = []string{"accept", "rework", "drop"}
	case pr.Col == Review && pr.F("result") == "failed":
		out = []string{"rework", "drop"}
	case pr.Col == Review && len(readsAt(c.s, pr, pr.Int("attempt"))) > 0:
		_, free := readersForAsk(c.s, pr, pr.Int("attempt"))
		if len(free) > 0 {
			out = append(out, "ask --another") // ask alone is refused: asked already
		}
		out = append(out, "rework", "drop")
	case pr.Col == Review:
		_, free := readersForAsk(c.s, pr, pr.Int("attempt"))
		if len(free) >= 2 {
			out = append(out, "ask")
		}
		out = append(out, "rework", "drop")
	case pr.Col == Merging:
		out = []string{"return", "drop"}
	default:
		out = []string{"look at the card", "drop"}
	}
	return append(out, "wait")
}
