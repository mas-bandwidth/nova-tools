package sprint

import "time"

// What the ETA stands on (docs/SPEC-SPRINT.md section 1, the ETA): the landing rate is
// stated with its window and its sample, and the cards left are split into the held ones,
// which no tick moves on its own, the executing ones, and the queued rest, so an estimate
// is never read without what it is made of.

// ETABasis is the landing rate the ETA is measured at, with the window it was measured
// over and the landings it counted there (LandingRate's rule).
type ETABasis struct {
	// Window is RateWindowRecent when the rate is the last RateWindow of running time,
	// RateWindowAll when fewer than RateFloor landed there and it is the whole sprint's
	// average, RateWindowNone when there is no rate.
	Window string `json:"window"`
	// Landings is the sample: the landings counted in the window.
	Landings int64 `json:"landings"`
	// Hours is the window's running time (the clock's time less the STOPPED spans).
	Hours float64 `json:"hours"`
	// PerHour is the rate, Landings over Hours; 0 with no rate.
	PerHour float64 `json:"per_hour"`
}

// The windows an ETABasis names.
const (
	RateWindowRecent = "last 1h of running time"
	RateWindowAll    = "since the first start"
	RateWindowNone   = "none"
)

// LandingRateBasis is LandingRate with what it was measured from: the window, its
// landings and its running time, counted by LandingRate's rule. Its PerHour is
// LandingRate's.
func LandingRateBasis(landed []time.Time, total int64, spans []Span, first, now time.Time) ETABasis {
	b := ETABasis{Window: RateWindowNone, PerHour: LandingRate(landed, total, spans, first, now)}
	if b.PerHour <= 0 {
		return b
	}
	running := func(from time.Time) float64 { return (now.Sub(from) - StoppedBetween(spans, from, now)).Hours() }
	from := runningStart(spans, first, now, RateWindow)
	for _, at := range landed {
		if at.After(from) && !at.After(now) {
			b.Landings++
		}
	}
	if h := running(from); b.Landings >= RateFloor && h > 0 {
		b.Window, b.Hours = RateWindowRecent, h
		return b
	}
	b.Window, b.Landings, b.Hours = RateWindowAll, total, running(first)
	return b
}

// ETAWork is the cards the ETA is over, every card on the work table not landed, split
// by what moves them: Held, the cards no tick moves on its own (HeldBack); Executing,
// the ones working, in review or merging; Queued, the rest, waiting or ready and not held.
type ETAWork struct {
	Left      int `json:"left"`
	Held      int `json:"held"`
	Executing int `json:"executing"`
	Queued    int `json:"queued"`
}

// ETAWorkOf is the work table's cards not landed, split (ETAWork).
func ETAWorkOf(s *Snapshot) ETAWork {
	w := ETAWork{Held: HeldBack(s), Executing: len(s.Work.Column(Working, Review, Merging))}
	w.Queued = max(len(s.Work.Column(Waiting, Ready))-w.Held, 0)
	w.Left = w.Held + w.Executing + w.Queued
	return w
}
