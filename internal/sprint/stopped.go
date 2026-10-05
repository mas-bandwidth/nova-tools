package sprint

import "time"

// Span is one time the machine was STOPPED; To is zero while it still is.
type Span struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to,omitempty"`
}

// StoppedBetween is the time the spans were STOPPED between from and to: a
// deadline compares running time, which is the clock's time less this.
func StoppedBetween(spans []Span, from, to time.Time) time.Duration {
	var d time.Duration
	for _, s := range spans {
		end := s.To
		if end.IsZero() || end.After(to) {
			end = to
		}
		start := s.From
		if start.Before(from) {
			start = from
		}
		if end.After(start) {
			d += end.Sub(start)
		}
	}
	return d
}

// DueNow is the tree's one "is this due at now" test: the clock time due is
// reached at now when the running time from set to now has reached due's own
// distance from set, so the time the machine was STOPPED never counts
// (docs/SPEC-SPRINT.md, "Timers" and "Deadlines"). A timer is based at the
// time it was written and a judgment's review time at the time wait set it;
// a due with no base recorded is read against the wall clock. The timer tick
// (timers.go, DueTimers) and every reading of a judgment's review time
// (steps_tick.go TickOverdue and notify, inbox.go due, held.go
// overdueUnmarked) call this, so a later external wait operand
// (`after <time>`) calls the same function.
func DueNow(now, due, set time.Time, stopped func(from, to time.Time) time.Duration) bool {
	if due.IsZero() {
		return false
	}
	if set.IsZero() {
		return now.After(due)
	}
	d := now.Sub(set)
	if stopped != nil {
		d -= stopped(set, now)
	}
	return d >= due.Sub(set)
}
