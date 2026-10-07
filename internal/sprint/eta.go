package sprint

import "time"

// ETARate is the evidence behind LandingRate, using the same running-time
// window and fallback (docs/SPEC-SPRINT.md, ETA rate evidence).
type ETARate struct {
	Rate          float64   `json:"landings_per_hour"`
	Basis         string    `json:"basis"`
	WindowSeconds int64     `json:"window_seconds"`
	Landings      int64     `json:"landings"`
	From          time.Time `json:"from,omitzero"`
	To            time.Time `json:"to,omitzero"`
}

func LandingRateEvidence(landed []time.Time, total int64, spans []Span, first, now time.Time) ETARate {
	r := ETARate{Rate: LandingRate(landed, total, spans, first, now), Basis: "unavailable", To: now}
	if first.IsZero() || !now.After(first) {
		return r
	}
	from := runningStart(spans, first, now, RateWindow)
	for _, at := range landed {
		if at.After(from) && !at.After(now) {
			r.Landings++
		}
	}
	r.Basis = "recent-running-time"
	if r.Landings < RateFloor {
		from = first
		r.Landings = max(total, 0)
		r.Basis = "epoch-running-time"
	}
	r.From = from
	r.WindowSeconds = int64((now.Sub(from) - StoppedBetween(spans, from, now)) / time.Second)
	return r
}
