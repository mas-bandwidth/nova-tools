package sprint

import (
	"fmt"
	"sort"
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
// held to (ValidGoalText), at most MaxTimerNote bytes.
func ValidTimerNote(text string) error { return validText("the timer's note", text, MaxTimerNote) }

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

// TimerChange is what one step does to the timer record: it adds one timer,
// or closes the timers it names. The store's commit applies it to the record
// as that commit reads it, under the step's fence (Timers.With), never by
// writing back a record the step read earlier, so a set or a cancel committed
// between a tick's read and its commit is kept (tla/Timer.tla, NoLapse and
// CancelledNeverFires).
type TimerChange struct {
	Add   *Timer   `json:"add,omitempty"`
	Close []string `json:"close,omitempty"`
}

// With is the record c leaves of t: the timers c closes taken off, the one it
// adds put on, in due order. A closed id the record no longer holds is no
// change.
func (t Timers) With(c TimerChange) Timers {
	closing := map[string]bool{}
	for _, id := range c.Close {
		closing[id] = true
	}
	var out Timers
	for _, x := range t.Open {
		if !closing[x.ID] {
			out.Open = append(out.Open, x)
		}
	}
	if c.Add != nil && out.Find(c.Add.ID) < 0 {
		out.Open = append(out.Open, *c.Add)
	}
	out.Sort()
	return out
}

// TimerNotes is the plan that raises the timers of s.Timers whose due time
// has come at s.Now (DueTimers) as judgments of kind NTimer, each addressed to
// its actor, and closes them: one timer, one judgment, once, in the step that
// writes it (docs/SPEC-SPRINT.md, "Timers"; tla/Timer.tla, Tick). s.Timers is
// the record read under the step's fence, so a timer cancelled before it is
// not raised. who is recorded with each judgment.
func TimerNotes(s *Snapshot, stopped func(from, to time.Time) time.Duration, who string) Plan {
	var p Plan
	var closing []string
	for _, x := range DueTimers(s.Timers, s.Now, stopped) {
		closing = append(closing, x.ID)
		p.Notes = append(p.Notes, Note{Kind: Judgment, Type: NTimer, Primaries: []string{TimerSubject(x.ID)},
			Count: 1, What: fmt.Sprintf("the timer %s for %s is due: %s", x.ID, x.For, x.Note),
			Who: who, At: s.Now, To: x.For, Decisions: append([]string(nil), Decisions[NTimer]...)})
	}
	if len(closing) == 0 {
		return Plan{}
	}
	p.Timers = &TimerChange{Close: closing}
	return p
}
