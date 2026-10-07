package sprint

import (
	"fmt"
	"time"
)

// What the ETA stands on (docs/SPEC-SPRINT.md section 1, the ETA; the 2026-10-06
// nova-sprint review, item 6): the landing rate is stated with the window it was measured over and the
// landings it counted there, and the cards left are split into the held ones, which no tick
// moves on its own, the executing ones and the queued rest, so an estimate is never read
// without what it is made of.

// The windows an ETABasis names.
const (
	RateWindowRecent = "last 1h of running time"
	RateWindowAll    = "since the first start"
	RateWindowNone   = "none"
)

// ETABasis is the landing rate the ETA is measured at, with the window it was measured over
// and its sample: the landings counted there (LandingRate's rule).
type ETABasis struct {
	// Window is RateWindowRecent when the rate is the last RateWindow of running time,
	// RateWindowAll when fewer than RateFloor landed there and it is the whole sprint's
	// average, RateWindowNone when there is no rate.
	Window string `json:"window"`
	// Landings is the sample: the landings counted in the window.
	Landings int64 `json:"landings"`
	// Hours is the window's running time (the clock's time less the STOPPED spans).
	Hours float64 `json:"hours"`
	// PerHour is the rate, Landings over Hours: LandingRate's; 0 with no rate.
	PerHour float64 `json:"per_hour"`
}

// Text is the basis as a summary prints it: "7.0/h over last 1h of running time (7
// landings)", "rate none" with no rate.
func (b ETABasis) Text() string {
	if b.Window == RateWindowNone || b.PerHour <= 0 {
		return "rate none"
	}
	return fmt.Sprintf("%.1f/h over %s (%d landings)", b.PerHour, b.Window, b.Landings)
}

// LandingRateBasis is LandingRate with what it was measured from, counted by LandingRate's
// rule: the window, its landings and its running time.
func LandingRateBasis(landed []time.Time, total int64, spans []Span, first, now time.Time) ETABasis {
	b := ETABasis{Window: RateWindowNone}
	if first.IsZero() || !now.After(first) {
		return b
	}
	running := func(from time.Time) float64 { return (now.Sub(from) - StoppedBetween(spans, from, now)).Hours() }
	from, n := runningStart(spans, first, now, RateWindow), int64(0)
	for _, at := range landed {
		if at.After(from) && !at.After(now) {
			n++
		}
	}
	if h := running(from); n >= RateFloor && h > 0 {
		return ETABasis{Window: RateWindowRecent, Landings: n, Hours: h, PerHour: float64(n) / h}
	}
	if h := running(first); total > 0 && h > 0 {
		return ETABasis{Window: RateWindowAll, Landings: total, Hours: h, PerHour: float64(total) / h}
	}
	return b
}

// ETAWork is the cards the ETA is over, every primary on the work table not landed, split
// by what moves them: Held, the ones no tick moves on its own (HeldBack); Executing, the
// ones working, in review or merging; Queued, the rest, waiting or ready and not held.
type ETAWork struct {
	Left      int `json:"left"`
	Held      int `json:"held"`
	Executing int `json:"executing"`
	Queued    int `json:"queued"`
}

// ETAWorkOf is the work table's primaries not landed, split (ETAWork).
func ETAWorkOf(s *Snapshot) ETAWork {
	if s == nil || s.Work == nil {
		return ETAWork{}
	}
	w := ETAWork{Held: HeldBack(s), Executing: len(s.Work.Column(Working, Review, Merging))}
	w.Queued = max(len(s.Work.Column(Waiting, Ready))-w.Held, 0)
	w.Left = w.Held + w.Executing + w.Queued
	return w
}

// Text is the split as a summary prints it: "left 1182: held 770 executing 12 queued 400".
func (w ETAWork) Text() string {
	return fmt.Sprintf("left %d: held %d executing %d queued %d", w.Left, w.Held, w.Executing, w.Queued)
}
