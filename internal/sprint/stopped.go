package sprint

import (
	"errors"
	"strings"
	"time"
)

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

// clockForms are the clock times --until takes, in now's zone.
var clockForms = []string{"3:04 PM", "3:04PM", "15:04"}

// StopArgs is a stop by hand's --reason and --until (docs/SPEC-SPRINT.md
// section 14): both are wanted, and one refusal names every one missing.
// --until is a duration from now (90m), a clock time (2:04 PM, 14:04: today's,
// or tomorrow's once today's has passed) or an RFC 3339 time; it returns the
// time, which must be after now.
func StopArgs(reason, until string, now time.Time) (time.Time, error) {
	var missing []string
	if strings.TrimSpace(reason) == "" {
		missing = append(missing, "--reason <text>: why the machine stops, shown with it")
	}
	if strings.TrimSpace(until) == "" {
		missing = append(missing, "--until <time or duration>: when the machine starts itself again (90m, 2:04 PM, or RFC 3339)")
	}
	if len(missing) > 0 {
		return time.Time{}, errors.New("wants " + strings.Join(missing, ", and "))
	}
	at, ok := untilOf(strings.TrimSpace(until), now)
	if !ok {
		return time.Time{}, errors.New("--until wants a duration from now (90m), a clock time (2:04 PM, 14:04) or an RFC 3339 time, found " + until)
	}
	if !at.After(now) {
		return time.Time{}, errors.New("--until wants a time after now, found " + until)
	}
	return at, nil
}

// untilOf reads --until's three forms at the clock's reading (docs/SPEC-SPRINT.md section 14).
func untilOf(s string, now time.Time) (time.Time, bool) {
	if d, err := time.ParseDuration(s); err == nil {
		return now.Add(d), true
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	for _, f := range clockForms {
		c, err := time.ParseInLocation(f, strings.ToUpper(s), now.Location())
		if err != nil {
			continue
		}
		at := time.Date(now.Year(), now.Month(), now.Day(), c.Hour(), c.Minute(), 0, 0, now.Location())
		if !at.After(now) {
			at = at.AddDate(0, 0, 1)
		}
		return at, true
	}
	return time.Time{}, false
}

// StoppedText is the machine line's state for a stop by hand
// (docs/SPEC-SPRINT.md section 14): "STOPPED by <who>: <reason>, back by
// <time>", the time on the clock's day as 2:04 PM, on another day with its
// date.
func StoppedText(who, reason string, until, now time.Time) string {
	at := until.In(now.Location())
	when := at.Format("3:04 PM")
	if y, m, d := at.Date(); y != now.Year() || m != now.Month() || d != now.Day() {
		when = at.Format("Mon Jan 2 3:04 PM")
	}
	return "STOPPED by " + who + ": " + strings.Join(strings.Fields(reason), " ") + ", back by " + when
}
