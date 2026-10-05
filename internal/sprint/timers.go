package sprint

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Timers (docs/SPEC-SPRINT.md, "Timers"; tla/Timer.tla): an actor sets a
// timer for itself or for another with `remind`, and the machine's tick
// raises it as one judgment addressed to that actor at its due time, once,
// closing the timer in the same store step. The logic here is pure; the store
// binding keeps the record and does the write.

// NTimer is the judgment a timer whose due time has come is raised as.
const NTimer = "timer"

// MaxTimerNote bounds the note text a timer carries.
const MaxTimerNote = 512

// Timer is one timer: the actor it wakes, the time it is due at, the note it
// carries and who set it.
type Timer struct {
	ID   string    `json:"id"`
	For  string    `json:"for"`
	Due  time.Time `json:"due"`
	Note string    `json:"note"`
	By   string    `json:"by"`
	// Set is when the timer was written: the base its due time counts running
	// time from, as a judgment's review time counts from when wait set it.
	Set time.Time `json:"set"`
}

// Timers is the sprint's open timers, the record `remind` writes and the tick
// reads.
type Timers struct {
	Open []Timer `json:"open,omitempty"`
}

// TimerSubject is the subject a timer's judgment is open on: its own id, so
// one timer is one judgment and no card's judgment is it.
func TimerSubject(id string) string { return "timer:" + id }

// Find is the timer's index, -1 when there is none.
func (t Timers) Find(id string) int {
	for i, x := range t.Open {
		if x.ID == id {
			return i
		}
	}
	return -1
}

// Sort keeps the timers in due order, then in id order.
func (t *Timers) Sort() {
	sort.Slice(t.Open, func(i, j int) bool {
		if !t.Open[i].Due.Equal(t.Open[j].Due) {
			return t.Open[i].Due.Before(t.Open[j].Due)
		}
		return t.Open[i].ID < t.Open[j].ID
	})
}

// ValidTimerNote is nil for text a timer can carry: the shape a goal's text is
// held to (ValidGoalText), at most MaxTimerNote bytes, refused in a timer's
// own words.
func ValidTimerNote(text string) error {
	if err := ValidGoalText(text, MaxTimerNote); err != nil {
		return errors.New(strings.Replace(err.Error(), "the goal text", "the timer's note", 1))
	}
	return nil
}

// DueTimers is the timers whose due time the clock has reached at now, in the
// order the record keeps them: the one due test the tree has, DueNow, so a
// timer counts running time exactly as a judgment's review time does
// (docs/SPEC-SPRINT.md, "Timers").
func DueTimers(t Timers, now time.Time, stopped func(from, to time.Time) time.Duration) []Timer {
	var out []Timer
	for _, x := range t.Open {
		if DueNow(now, x.Due, x.Set, stopped) {
			out = append(out, x)
		}
	}
	return out
}

// TimerWrite is a step's change to the timer record: the timers it adds and
// the ids it closes. The store's commit applies it to the record as it reads
// it inside that commit, under the fence (Apply), never writing back a copy
// the step read before, so a timer set or cancelled by another step is not
// overwritten (tla/Timer.tla, Set, Cancel and Tick are each one step).
type TimerWrite struct {
	Add   []Timer  `json:"add,omitempty"`
	Close []string `json:"close,omitempty"`
}

// Apply is the record t with w applied: the closed ids taken off, the added
// timers put on unless one of that id is already there, in due order. A
// commit applied twice (a writer finishing another's operation) leaves the
// record as once.
func (w TimerWrite) Apply(t Timers) Timers {
	closed := map[string]bool{}
	for _, id := range w.Close {
		closed[id] = true
	}
	var out Timers
	for _, x := range t.Open {
		if !closed[x.ID] {
			out.Open = append(out.Open, x)
		}
	}
	for _, x := range w.Add {
		if !closed[x.ID] && out.Find(x.ID) < 0 {
			out.Open = append(out.Open, x)
		}
	}
	out.Sort()
	return out
}

// SetTimer is the plan that adds the timer t to the record (remind): its
// commit puts it on the record as it stands then (TimerWrite).
func SetTimer(t Timer) Plan {
	return Plan{Timers: &TimerWrite{Add: []Timer{t}}}
}

// CancelTimer is the plan that takes the open timer id off the record
// (remind --cancel), read in the step's own snapshot (s.Timers): a timer the
// tick already raised, or none ever set, is no open timer and is refused.
func CancelTimer(s *Snapshot, id string) Plan {
	var p Plan
	if s.Timers.Find(id) < 0 {
		p.refuse(TimerSubject(id), "no open timer is "+id+"; run: nova-sprint remind --list")
		return p
	}
	p.Timers = &TimerWrite{Close: []string{id}}
	return p
}

// TimerNotes is the plan that raises the timers of the step's own snapshot
// (s.Timers) whose due time has come at s.Now as judgments of kind NTimer,
// each addressed to its actor, and closes them: one timer, one judgment,
// once, in the step that writes it (docs/SPEC-SPRINT.md, "Timers";
// tla/Timer.tla, Tick). who is recorded with each judgment.
func TimerNotes(s *Snapshot, stopped func(from, to time.Time) time.Duration, who string) Plan {
	var p Plan
	w := TimerWrite{}
	for _, x := range DueTimers(s.Timers, s.Now, stopped) {
		p.Notes = append(p.Notes, Note{Kind: Judgment, Type: NTimer, Primaries: []string{TimerSubject(x.ID)},
			Count: 1, What: fmt.Sprintf("the timer %s for %s is due: %s", x.ID, x.For, x.Note),
			Who: who, At: s.Now, To: x.For, Decisions: append([]string(nil), Decisions[NTimer]...)})
		w.Close = append(w.Close, x.ID)
	}
	if len(w.Close) > 0 {
		p.Timers = &w
	}
	return p
}
