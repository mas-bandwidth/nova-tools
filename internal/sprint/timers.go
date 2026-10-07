package sprint

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
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

// ValidTimerNote is nil for text a timer can carry: not blank, valid UTF-8,
// no more than MaxTimerNote bytes.
func ValidTimerNote(text string) error {
	switch {
	case strings.TrimSpace(text) == "":
		return fmt.Errorf("the timer note is empty")
	case !utf8.ValidString(text) || strings.ContainsRune(text, 0):
		return fmt.Errorf("the timer note is not text (invalid UTF-8 or a NUL byte)")
	case len(text) > MaxTimerNote:
		return fmt.Errorf("the timer note is %d bytes; the bound is %d", len(text), MaxTimerNote)
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

// TimerNotes is the plan that raises the timers due names as judgments of
// kind NTimer, each addressed to its actor, and leaves the record with them
// closed: one timer, one judgment, once, in the step that writes it
// (docs/SPEC-SPRINT.md, "Timers"; tla/Timer.tla, Tick). who is recorded with
// each judgment.
func TimerNotes(s *Snapshot, t Timers, due []Timer, who string) Plan {
	if len(due) == 0 {
		return Plan{}
	}
	raising := map[string]bool{}
	var p Plan
	for _, x := range due {
		if t.Find(x.ID) < 0 || raising[x.ID] {
			continue // cancelled while the tick read, or named twice: no judgment
		}
		raising[x.ID] = true
		p.CloseTimers = append(p.CloseTimers, x.ID)
		p.Notes = append(p.Notes, Note{Kind: Judgment, Type: NTimer, Primaries: []string{TimerSubject(x.ID)},
			Count: 1, What: fmt.Sprintf("the timer %s for %s is due: %s", x.ID, x.For, x.Note),
			Who: who, At: s.Now, To: x.For, Decisions: append([]string(nil), Decisions[NTimer]...)})
	}
	if len(p.Notes) == 0 {
		return Plan{}
	}
	var leave Timers
	for _, x := range t.Open {
		if !raising[x.ID] {
			leave.Open = append(leave.Open, x)
		}
	}
	leave.Sort()
	p.Timers = &leave
	return p
}
