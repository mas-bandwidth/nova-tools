package sprint

import (
	"fmt"
	"time"
)

// Timers (docs/SPEC-SPRINT.md, "Timers"; tla/Timers.tla): remind sets a
// timer, a note for an actor at a time, and the tick raises it once when its
// time comes. The logic here is pure; the store binding keeps the two records
// (the timers the verbs write, the ends the tick alone writes) and writes the
// notes.
//
// A timer is pending, then fired (raised, unseen), then seen (remind --ack,
// or its judgment answered), or it expires: the window (its due time plus
// Within) closed with it unfired (no tick ran in it, or its actor left the
// sprint) or fired and unseen. Seen, expired and cancelled are its ends; it
// never fires before its due time and fires at most once.

// Timer judgment and note types.
const (
	NTimer        = "timer"         // the coordinator's judgment: its own timer fired
	NTimerFired   = "timer fired"   // a note: to a friend whose timer fired, or to the setter of another's
	NTimerExpired = "timer expired" // a note to the setter: the timer ended unseen
)

// Timer states, as remind --list prints them.
const (
	TimerPending   = "pending"
	TimerFired     = "fired" // fired and unseen
	TimerSeen      = "seen"
	TimerExpired   = "expired"
	TimerCancelled = "cancelled"
)

// TimerWithin is the window a timer has when --within is not given: the
// lateness after which it is no longer worth firing, and the time it has to
// be seen in.
const TimerWithin = time.Hour

// MaxTimers bounds the timers the record keeps; a timer ended a TimerKeep ago
// and acknowledged is dropped by the next write of the record.
const (
	MaxTimers = 500
	TimerKeep = 7 * 24 * time.Hour
)

// Timer is one timer as remind set it: written by the verbs alone.
type Timer struct {
	ID     string        `json:"id"`
	Actor  string        `json:"actor"`
	Setter string        `json:"setter"`
	Note   string        `json:"note"`
	Due    time.Time     `json:"due"`
	Within time.Duration `json:"within_ns"`
	Set    time.Time     `json:"set"`
	// Cancelled is when remind --cancel ended it unfired; Acked when remind
	// --ack saw it.
	Cancelled time.Time `json:"cancelled,omitzero"`
	Acked     time.Time `json:"acked,omitzero"`
}

// Closes is the end of the timer's window: its due time plus Within.
func (t Timer) Closes() time.Time { return t.Due.Add(t.Within) }

// Timers is the verbs' record: the timers and the last number given.
type Timers struct {
	Seq int     `json:"seq"`
	All []Timer `json:"all,omitempty"`
}

// Find is the timer's index, -1 when there is none.
func (r Timers) Find(id string) int {
	for i, t := range r.All {
		if t.ID == id {
			return i
		}
	}
	return -1
}

// TimerEnd is what the tick did with one timer: written by the tick alone.
type TimerEnd struct {
	ID      string    `json:"id"`
	Fired   time.Time `json:"fired,omitzero"`
	Seen    time.Time `json:"seen,omitzero"`
	Expired time.Time `json:"expired,omitzero"`
	Reason  string    `json:"reason,omitempty"`
	// Judged says it fired as the coordinator's judgment; else as a note to
	// its actor.
	Judged bool `json:"judged,omitempty"`
	// Owed is the notes its last change owes and the tick has not written:
	// the record is written first, so a crash between the two writes them
	// on the next tick.
	Owed []string `json:"owed,omitempty"`
}

// Notes a change owes.
const (
	OweFiredActor    = "fired-actor"    // the note to a friend whose timer fired
	OweFiredSetter   = "fired-setter"   // the note to the setter of another's timer
	OweExpiredSetter = "expired-setter" // the note to the setter that it expired
)

// TimerEnds is the tick's record, by timer id.
type TimerEnds struct {
	Ends map[string]TimerEnd `json:"ends,omitempty"`
	// Raised is the coordinator's timers whose judgment the last notes step
	// left open: the step runs again only when that set changes.
	Raised []string `json:"raised,omitempty"`
}

// Reached is the one due-check: the clock reads at or after at. A timer is
// due when Reached(now, its due time); a wait operand "after <time>" holds
// when Reached(now, that time).
func Reached(now, at time.Time) bool { return !now.Before(at) }

// TimerState is the timer's one state, from the two records.
func TimerState(t Timer, e TimerEnd) string {
	switch {
	case !e.Expired.IsZero():
		return TimerExpired
	case !e.Fired.IsZero() && (!e.Seen.IsZero() || !t.Acked.IsZero()):
		return TimerSeen
	case !e.Fired.IsZero():
		return TimerFired
	case !t.Cancelled.IsZero():
		return TimerCancelled
	}
	return TimerPending
}

// Missed says the timer is one a returning session is shown first: fired and
// unseen, or expired and not acknowledged.
func Missed(t Timer, e TimerEnd) bool {
	switch TimerState(t, e) {
	case TimerFired:
		return true
	case TimerExpired:
		return t.Acked.IsZero()
	}
	return false
}

// TimerWorld is what the tick knows of the sprint when it advances a timer:
// the seat's holder, whether the actor is in the sprint (the holder or a
// friend), and whether the timer's judgment was answered.
type TimerWorld struct {
	Holder   string
	Known    bool
	Answered bool
}

// Advance is the tick's step for one timer at now: a pending timer fires
// once due (Reached) within its window, or expires when the window closed
// unfired or its actor has left the sprint; a fired one is seen once acked
// or answered, or expires unseen when its window closes. It says whether the
// end changed. It never fires before the due time, and never a timer that
// has fired, ended or been cancelled.
func Advance(t Timer, e TimerEnd, now time.Time, w TimerWorld) (TimerEnd, bool) {
	e.ID = t.ID
	switch TimerState(t, e) {
	case TimerPending:
		switch {
		case !Reached(now, t.Due):
			return e, false
		case now.After(t.Closes()):
			e.Expired, e.Reason = now, "not fired by the end of its window ("+t.Closes().UTC().Format(time.RFC3339)+"): no tick ran from its due time until then"
		case !w.Known:
			e.Expired, e.Reason = now, "its actor "+t.Actor+" is not in the sprint (no seat, no friend row)"
		default:
			e.Fired, e.Judged = now, t.Actor == w.Holder
			if !e.Judged {
				e.Owed = append(e.Owed, OweFiredActor)
			}
			if t.Setter != t.Actor {
				e.Owed = append(e.Owed, OweFiredSetter)
			}
			return e, true
		}
		e.Owed = append(e.Owed, OweExpiredSetter)
		return e, true
	case TimerFired:
		switch {
		case w.Answered:
			e.Seen = now
		case now.After(t.Closes()):
			e.Expired, e.Reason = now, "fired at "+e.Fired.UTC().Format(time.RFC3339)+" and not seen by the end of its window ("+t.Closes().UTC().Format(time.RFC3339)+")"
			e.Owed = append(e.Owed, OweExpiredSetter)
		default:
			return e, false
		}
		return e, true
	}
	return e, false
}

// Lateness is how long after its due time the timer fired; zero unfired.
func (e TimerEnd) Lateness(t Timer) time.Duration {
	if e.Fired.IsZero() {
		return 0
	}
	return e.Fired.Sub(t.Due)
}

// TimerSubject is the open-judgment subject of a timer's judgment.
func TimerSubject(id string) string { return "timer:" + id }

// TimerWhat is a fired timer's words: its note, who set it for whom, its due
// time, when it fired and how late, and the command that marks it seen.
func TimerWhat(t Timer, e TimerEnd) string {
	return fmt.Sprintf("timer %s for %s (set by %s): %s; due %s, fired %s, %s late; run: nova-sprint remind --ack %s",
		t.ID, t.Actor, t.Setter, oneLine(t.Note), t.Due.UTC().Format(time.RFC3339), e.Fired.UTC().Format(time.RFC3339), e.Lateness(t).Round(time.Second), t.ID)
}

// timerExpiredWhat is the setter's words of an expired timer: why.
func timerExpiredWhat(t Timer, e TimerEnd) string {
	return fmt.Sprintf("timer %s for %s expired: %s; its note: %s", t.ID, t.Actor, e.Reason, oneLine(t.Note))
}

// TimerNotes is the plan that makes the inbox agree with the timers: the
// coordinator's judgment open for each of its own timers fired and unseen
// (judged), written once and closed when the timer is seen or expires, and
// the happened notes the ends owe (owed: timer id to the notes), each
// addressed to the one it tells.
func TimerNotes(s *Snapshot, all []Timer, ends map[string]TimerEnd, owed map[string][]string, who string) Plan {
	var conds []cond
	var p Plan
	for _, t := range all {
		e := ends[t.ID]
		if e.Judged && TimerState(t, e) == TimerFired {
			conds = append(conds, cond{typ: NTimer, primaries: []string{TimerSubject(t.ID)}, what: TimerWhat(t, e),
				decisions: []string{"remind --ack " + t.ID, "ack"}})
		}
		for _, o := range owed[t.ID] {
			n := Note{Kind: Happened, Who: who, At: s.Now, Primaries: []string{TimerSubject(t.ID)}, Count: 1}
			switch o {
			case OweFiredActor:
				n.Type, n.To, n.What, n.Hint = NTimerFired, t.Actor, TimerWhat(t, e), "nova-sprint remind --ack "+t.ID
			case OweFiredSetter:
				n.Type, n.To, n.What, n.Hint = NTimerFired, t.Setter, "the "+TimerWhat(t, e), "nova-sprint remind --list --for "+t.Actor
			case OweExpiredSetter:
				n.Type, n.To, n.What, n.Hint = NTimerExpired, t.Setter, timerExpiredWhat(t, e), "nova-sprint remind --ack "+t.ID
			default:
				continue
			}
			p.Notes = append(p.Notes, n)
		}
	}
	notify(&p, s, conds, []string{NTimer}, TickReq{Who: who})
	return p
}
