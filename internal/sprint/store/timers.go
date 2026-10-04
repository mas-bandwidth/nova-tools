package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The timer duty of the tick (docs/SPEC-SPRINT.md, "Timers"; tla/Timers.tla).
// Two records: the timers, written by the remind verbs alone, and their ends,
// written by the tick alone, so a verb and a tick never overwrite each other.
// Both are in the store, so a timer survives a restart of the server: the
// first tick after it fires every timer due within its window and expires the
// ones whose window closed while nothing ticked.

// The timers' keys.
const (
	keyTimers    = "timers"     // the timers (JSON): the remind verbs write it
	keyTimerEnds = "timer-ends" // what the tick did with each (JSON): the tick writes it
)

// TimerBook is both records as one read.
type TimerBook struct {
	Timers sprint.Timers
	Ends   sprint.TimerEnds
}

// End is the timer's end; the zero end for a pending one.
func (b TimerBook) End(id string) sprint.TimerEnd { return b.Ends.Ends[id] }

// TimerBook reads both records.
func (st *Store) TimerBook(ctx context.Context) (TimerBook, error) {
	var b TimerBook
	if err := st.getJSON(ctx, keyTimers, &b.Timers); err != nil {
		return b, err
	}
	err := st.getJSON(ctx, keyTimerEnds, &b.Ends)
	return b, err
}

// ended is when the timer reached its end, zero while it has not.
func ended(t sprint.Timer, e sprint.TimerEnd) time.Time {
	switch sprint.TimerState(t, e) {
	case sprint.TimerSeen:
		return later(e.Seen, t.Acked)
	case sprint.TimerExpired:
		if t.Acked.IsZero() {
			return time.Time{} // missed until acknowledged
		}
		return later(e.Expired, t.Acked)
	case sprint.TimerCancelled:
		return t.Cancelled
	}
	return time.Time{}
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// updateTimers reads both records fresh, applies fn to the timers and, unless
// dry, writes the timers back with every timer ended more than
// sprint.TimerKeep ago dropped. fn names the timer it changed ("" none): the
// record is read back, and a change another remind verb overwrote at the same
// moment (the store has no compare-and-set for a record) is made again from a
// fresh read, up to timerWrites times. It never writes the tick's ends.
func (st *Store) updateTimers(ctx context.Context, dry bool, fn func(b *TimerBook, now time.Time) (string, error)) (TimerBook, error) {
	for range timerWrites {
		b, err := st.TimerBook(ctx)
		if err != nil {
			return b, err
		}
		now := st.now()
		id, err := fn(&b, now)
		if err != nil || dry {
			return b, err
		}
		b.Timers.All = slices.DeleteFunc(b.Timers.All, func(t sprint.Timer) bool {
			end := ended(t, b.End(t.ID))
			return !end.IsZero() && now.Sub(end) > sprint.TimerKeep
		})
		if err := st.putJSON(ctx, keyTimers, b.Timers); err != nil || id == "" {
			return b, err
		}
		var back sprint.Timers
		if err := st.getJSON(ctx, keyTimers, &back); err != nil {
			return b, err
		}
		if i, j := back.Find(id), b.Timers.Find(id); i >= 0 && j >= 0 && back.All[i] == b.Timers.All[j] {
			return b, nil
		}
	}
	return TimerBook{}, fmt.Errorf("another remind verb kept overwriting the timers record (%d tries): this change does not stand; run it again", timerWrites)
}

// timerWrites bounds updateTimers' tries.
const timerWrites = 3

// knownActor is why the actor cannot be given a timer, "" when it can: the
// seat's holder or a friend of the sprint.
func (st *Store) knownActor(ctx context.Context, actor string) (string, error) {
	holder, err := st.B.Coordinator(ctx)
	if err != nil || actor == holder {
		return "", err
	}
	friends, err := st.FriendNames(ctx)
	if err != nil || slices.Contains(friends, actor) {
		return "", err
	}
	return fmt.Sprintf("%s holds no seat and is no friend of the sprint (the seat: %s; friends: %d); a timer is for the seat's holder or a friend", actor, holder, len(friends)), nil
}

// ErrTimer is a remind verb's refusal: the input or the timer's state.
var ErrTimer = errors.New("timer")

func timerErr(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrTimer}, a...)...)
}

// SetTimer stores a timer for t.Actor at t.Due with the note, set by
// t.Setter, and gives it its id; dry checks it and writes nothing. It refuses
// a due time not after now, an empty note, an actor not in the sprint, and a
// full record.
func (st *Store) SetTimer(ctx context.Context, t sprint.Timer, dry bool) (sprint.Timer, error) {
	if t.Within <= 0 {
		t.Within = sprint.TimerWithin
	}
	if t.Note == "" {
		return t, timerErr("the note is empty: --note <text> says what the timer is for")
	}
	if err := sprint.ValidGoalText(t.Note, MaxCardTextBytes); err != nil {
		return t, timerErr("%v", err)
	}
	why, err := st.knownActor(ctx, t.Actor)
	if err != nil {
		return t, err
	}
	if why != "" {
		return t, timerErr("%s", why)
	}
	_, err = st.updateTimers(ctx, dry, func(b *TimerBook, now time.Time) (string, error) {
		if !t.Due.After(now) {
			return "", timerErr("the due time %s is not after now (%s)", t.Due.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339))
		}
		live := 0
		for _, x := range b.Timers.All {
			if ended(x, b.End(x.ID)).IsZero() {
				live++
			}
		}
		if live >= sprint.MaxTimers {
			return "", timerErr("%d timers are pending or missed, the most the record keeps; cancel or ack some (remind --list)", live)
		}
		t.Set = now
		t.ID = "t" + strconv.Itoa(b.Timers.Seq+1)
		b.Timers.Seq++
		b.Timers.All = append(b.Timers.All, t)
		return t.ID, nil
	})
	return t, err
}

// CancelTimer ends a pending timer unfired; a timer that fired, ended or does
// not exist is refused, naming its state. dry writes nothing.
func (st *Store) CancelTimer(ctx context.Context, id string, dry bool) (sprint.Timer, error) {
	var out sprint.Timer
	_, err := st.updateTimers(ctx, dry, func(b *TimerBook, now time.Time) (string, error) {
		i := b.Timers.Find(id)
		if i < 0 {
			return "", timerErr("no timer %s (remind --list lists them)", id)
		}
		t := &b.Timers.All[i]
		if s := sprint.TimerState(*t, b.End(id)); s != sprint.TimerPending {
			return "", timerErr("timer %s is %s, not pending: only a pending timer is cancelled", id, s)
		}
		t.Cancelled = now
		out = *t
		return id, nil
	})
	return out, err
}

// AckTimer marks a fired timer seen, and an expired one acknowledged (it
// leaves the missed list); a pending timer is refused (cancel it), and a seen
// or cancelled one is left as it is (changed false). dry writes nothing.
func (st *Store) AckTimer(ctx context.Context, id string, dry bool) (sprint.Timer, sprint.TimerEnd, bool, error) {
	var out sprint.Timer
	var end sprint.TimerEnd
	changed := false
	_, err := st.updateTimers(ctx, dry, func(b *TimerBook, now time.Time) (string, error) {
		i := b.Timers.Find(id)
		if i < 0 {
			return "", timerErr("no timer %s (remind --list lists them)", id)
		}
		t := &b.Timers.All[i]
		end, changed = b.End(id), false
		switch sprint.TimerState(*t, end) {
		case sprint.TimerPending:
			return "", timerErr("timer %s has not fired (due %s): nothing to see yet; remind --cancel %s ends it", id, t.Due.UTC().Format(time.RFC3339), id)
		case sprint.TimerFired:
			t.Acked, changed = now, true
		case sprint.TimerExpired:
			if t.Acked.IsZero() {
				t.Acked, changed = now, true
			}
		}
		out = *t
		if !changed {
			return "", nil
		}
		return id, nil
	})
	return out, end, changed, err
}

// timers is the tick's timer duty: each pending timer due within its window
// fires once (its actor's judgment when the actor holds the seat, else a note
// to it), a timer whose window closed unfired or unseen expires with its
// reason, and a fired one is seen once acked or its judgment answered. The
// ends are written before the notes they owe, so a crash between the two never
// fires a timer twice; the notes owed are written by the next tick. A second
// tick right after changes nothing.
func (st *Store) timers(ctx context.Context, res *TickResult) error {
	var b TimerBook
	if err := st.getJSON(ctx, keyTimers, &b.Timers); err != nil || len(b.Timers.All) == 0 {
		return err // no timers: one read; the ends of dropped ones go with the next timer's tick
	}
	if err := st.getJSON(ctx, keyTimerEnds, &b.Ends); err != nil {
		return err
	}
	now := st.now()
	holder, err := st.B.Coordinator(ctx)
	if err != nil {
		return err
	}
	var friends []string // read once, when a pending timer of a friend is due
	friendsRead := false
	var answered map[string]bool
	if b.Ends.Ends == nil {
		b.Ends.Ends = map[string]sprint.TimerEnd{}
	}
	part := PartResult{Name: "timers", Result: Result{Verb: "tick timers"}}
	changed := false
	owed := map[string][]string{}
	for _, t := range b.Timers.All {
		e := b.End(t.ID)
		w := sprint.TimerWorld{Holder: holder, Known: t.Actor == holder}
		switch sprint.TimerState(t, e) {
		case sprint.TimerPending:
			if !w.Known && sprint.Reached(now, t.Due) && !friendsRead {
				if friends, err = st.FriendNames(ctx); err != nil {
					return err
				}
				friendsRead = true
			}
			w.Known = w.Known || slices.Contains(friends, t.Actor)
		case sprint.TimerFired:
			if e.Judged && answered == nil {
				if answered, err = st.answeredTimers(ctx); err != nil {
					return err
				}
			}
			w.Answered = answered[sprint.TimerSubject(t.ID)]
		}
		next, moved := sprint.Advance(t, e, now, w)
		if moved {
			b.Ends.Ends[t.ID], changed = next, true
			part.Moved = append(part.Moved, fmt.Sprintf("TIMER %s %s for %s", t.ID, sprint.TimerState(t, next), t.Actor))
		}
		if len(next.Owed) > 0 {
			owed[t.ID] = next.Owed
		}
	}
	for id := range b.Ends.Ends {
		if b.Timers.Find(id) < 0 {
			delete(b.Ends.Ends, id) // dropped from the record: its end goes too
			changed = true
		}
	}
	var raised []string // the coordinator's timers whose judgment stands open
	for _, t := range b.Timers.All {
		if e := b.End(t.ID); e.Judged && sprint.TimerState(t, e) == sprint.TimerFired {
			raised = append(raised, t.ID)
		}
	}
	if changed {
		if err := st.putJSON(ctx, keyTimerEnds, b.Ends); err != nil {
			return err
		}
	}
	if len(owed) > 0 || !slices.Equal(raised, b.Ends.Raised) {
		r, err := st.Run(ctx, Step{Verb: "tick timers", Actor: sprint.MachineActor, Plan: func(s *sprint.Snapshot) sprint.Plan {
			return sprint.TimerNotes(s, b.Timers.All, b.Ends.Ends, owed, sprint.MachineActor)
		}})
		if err != nil {
			return err
		}
		part.Notes = r.Notes
		for id := range owed {
			e := b.Ends.Ends[id]
			e.Owed = nil
			b.Ends.Ends[id] = e
		}
		b.Ends.Raised = raised
		if err := st.putJSON(ctx, keyTimerEnds, b.Ends); err != nil {
			return err
		}
	}
	if len(part.Moved) > 0 || part.Notes > 0 {
		res.Parts = append(res.Parts, part)
	}
	return nil
}

// answeredTimers is the timers whose judgment the coordinator answered (ack):
// the subjects of the acknowledged timer judgments.
func (st *Store) answeredTimers(ctx context.Context) (map[string]bool, error) {
	open, err := st.B.OpenNotes(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, o := range open {
		if o.Note.Type == sprint.NTimer && o.Note.Kind == sprint.Acknowledged {
			out[o.Subject()] = true
		}
	}
	return out, nil
}
