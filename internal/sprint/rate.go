package sprint

import (
	"slices"
	"time"
)

// The landing rate the ETA is measured at (docs/SPEC-SPRINT.md section 1).
const (
	// RateWindow is the running time the rate is measured over: the last hour,
	// the window of the dashboard's throughput tile, so the two agree.
	RateWindow = time.Hour
	// RateFloor is the fewest landings in the window the rate is taken from;
	// with fewer, it is the whole sprint's average.
	RateFloor = 5
)

// LandingRate is the cards landed per hour of running time (the clock's time
// less the STOPPED spans): the landings stamped in the last RateWindow of
// running time before now, over that time; with fewer than RateFloor there, the
// total landed over the running time since the first start. 0 when the machine
// has not started (first zero) or nothing has landed: no rate.
func LandingRate(landed []time.Time, total int64, spans []Span, first, now time.Time) float64 {
	if first.IsZero() || !now.After(first) {
		return 0
	}
	running := func(from time.Time) float64 { return (now.Sub(from) - StoppedBetween(spans, from, now)).Hours() }
	from, n := runningStart(spans, first, now, RateWindow), 0
	for _, at := range landed {
		if at.After(from) && !at.After(now) {
			n++
		}
	}
	if h := running(from); n >= RateFloor && h > 0 {
		return float64(n) / h
	}
	if h := running(first); total > 0 && h > 0 {
		return float64(total) / h
	}
	return 0
}

// runningStart is the clock reading from which the running time to now is w,
// walking the STOPPED spans back from now; first when the machine has run for
// less than w since its first start.
func runningStart(spans []Span, first, now time.Time, w time.Duration) time.Time {
	at := now
	for i := len(spans) - 1; i >= 0 && at.After(first); i-- {
		to := spans[i].To
		if to.IsZero() || to.After(at) {
			to = at
		}
		run := at.Sub(to)
		if run >= w {
			return at.Add(-w)
		}
		w -= max(run, 0)
		if spans[i].From.Before(at) {
			at = spans[i].From
		}
	}
	if from := at.Add(-w); from.After(first) {
		return from
	}
	return first
}

// MaxRecentLandings bounds the landings RecentLandings keeps: the newest. It
// is far above the landings of an hour at any width the fleet has; past it,
// the hour's count, and so the rate, reads low.
const MaxRecentLandings = 10000

// RecentLandings is the landings LandingRate can count at now or at any later
// reading: the stamps from the start of the last RateWindow of running time
// before now, oldest first, at most MaxRecentLandings (the newest). The window
// only moves forward (running time never runs back), so what is before it now
// is before it at every later reading. None before the first start (first
// zero): no landing before it is ever counted.
func RecentLandings(landed []time.Time, spans []Span, first, now time.Time) []time.Time {
	if first.IsZero() {
		return nil
	}
	from := runningStart(spans, first, now, RateWindow)
	var out []time.Time
	for _, at := range landed {
		if !at.Before(from) {
			out = append(out, at)
		}
	}
	slices.SortFunc(out, time.Time.Compare)
	return out[max(0, len(out)-MaxRecentLandings):]
}
