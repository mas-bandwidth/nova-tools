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
