package sprintfn

import (
	"errors"
	"strconv"
)

// The running clock's functions, as a stub of item IT03 (upper design version
// 2.1, 8.1: package sprint, clock.go). IT03's own branch has not landed, so the
// clock part and the pop and beat parts of this package are built against
// these unexported copies of its four names, with the same fields and the same
// arguments:
//
//	sprint.Clock         is clockRecord    (five int64 fields; 0 stands for "")
//	sprint.Running       is clockRunning
//	sprint.StartClock    is clockStart
//	sprint.StopClock     is clockStop
//
// When IT03 merges, each of these is replaced by its name in package sprint
// and this file is deleted. Nothing outside this package calls them. Where the
// design is silent, the stub takes the narrower reading and says so:
//
//   - start ends a span, so it also clears due_since_ms, which is "in this
//     span" (1.2); stophold_ms and stopraised_ms are left as they are;
//   - init leaves the machine STOPPED since the call's time (3, the verb table:
//     "clock (STOPPED)"), and is refused when a clock exists.

// clockRecord is {p}clock (1.2): STOPPED time before the current span, when
// the current span began (0 while RUNNING), and the three bookkeeping fields
// of R17, all in ms.
type clockRecord struct {
	StoppedMs, StoppedSinceMs, StopHoldMs, DueSinceMs, StopRaisedMs int64
}

// The fields of the clock hash, by their names in 1.2.
const (
	clockFieldStopped    = "stopped_ms"
	clockFieldSince      = "stopped_since_ms"
	clockFieldHold       = "stophold_ms"
	clockFieldDueSince   = "due_since_ms"
	clockFieldStopRaised = "stopraised_ms"
)

// Refusals of the clock's transitions (A2): each is MACHINESTATE.
var (
	errClockRunning = errors.New("the machine is already running")
	errClockStopped = errors.New("the machine is already stopped")
)

// clockRunning is R(t) = t - stopped_ms - (stopped_since_ms == "" ? 0 : t -
// stopped_since_ms) (1.2). While STOPPED it is constant: the t cancels.
func clockRunning(c clockRecord, t int64) int64 {
	r := t - c.StoppedMs
	if c.StoppedSinceMs != 0 {
		r -= t - c.StoppedSinceMs
	}
	return r
}

// clockStart ends the current STOPPED span at t: its length joins stopped_ms
// and R goes on from where it stood. It is refused while RUNNING, so a second
// start can never change the clock (A2).
func clockStart(c clockRecord, t int64) (clockRecord, error) {
	if c.StoppedSinceMs == 0 {
		return c, errClockRunning
	}
	c.StoppedMs += t - c.StoppedSinceMs
	c.StoppedSinceMs = 0
	c.DueSinceMs = 0
	return c, nil
}

// clockStop begins a STOPPED span at t. It is refused while STOPPED, so a
// second stop can never move stopped_since_ms and make every due time early by
// the gap between the two stops (A2).
func clockStop(c clockRecord, t int64) (clockRecord, error) {
	if c.StoppedSinceMs != 0 {
		return c, errClockStopped
	}
	c.StoppedSinceMs = t
	return c, nil
}

// clockFields is the clock as its hash holds it: every field, in a fixed
// order, "" for a zero since, hold, due-since or raised field.
func clockFields(c clockRecord) []string {
	blank := func(n int64) string {
		if n == 0 {
			return ""
		}
		return strconv.FormatInt(n, 10)
	}
	return []string{
		clockFieldStopped, strconv.FormatInt(c.StoppedMs, 10),
		clockFieldSince, blank(c.StoppedSinceMs),
		clockFieldHold, blank(c.StopHoldMs),
		clockFieldDueSince, blank(c.DueSinceMs),
		clockFieldStopRaised, blank(c.StopRaisedMs),
	}
}
