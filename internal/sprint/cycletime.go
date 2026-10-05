package sprint

import (
	"math"
	"slices"
	"time"
)

// Where a card's wall time goes (docs/SPEC-SPRINT.md, cycle-time-breakdownb.w1).
// A card records its stage times on itself as the steps that move it write them,
// never from the log: the primary carries ready_at (first ready), dealt_at and
// taken_at (the final attempt's work card, the deal and take it ran on), finished_at
// (its last finish), rework_s (the seconds from each finish to the deal of the next
// attempt, summed), read_asked_at and read_done_at (the first ask and the last ok of
// the readers its accept counted), accepted and landed; the merge card carries
// queued. CycleTimes reads them from the snapshot: the median and p90 of each stage
// over the primaries landed in the last CycleWindow, per stream and overall.

// Stage names, in the order wall time passes through them.
const (
	StageNeeds    = "needs"     // added to ready
	StageDeal     = "deal"      // ready to dealt (a card never reworked)
	StageTake     = "take"      // dealt to taken
	StageWork     = "work"      // taken to finished
	StageRework   = "rework"    // finished to dealt again, over every attempt (a reworked card)
	StageReadWait = "read_wait" // finished to read asked
	StageRead     = "read"      // read asked to read done
	StageAccept   = "accept"    // read done to accepted
	StageMerge    = "merge"     // merge queued to landed
)

// StageOrder is the stages in the order a card meets them.
var StageOrder = []string{StageNeeds, StageDeal, StageTake, StageWork, StageRework, StageReadWait, StageRead, StageAccept, StageMerge}

// The stamps a card keeps on itself, beside admitted, accepted and landed.
const (
	FieldReadyAt     = "ready_at"
	FieldDealtAt     = "dealt_at"
	FieldTakenAt     = "taken_at"
	FieldFinishedAt  = "finished_at"
	FieldReworkS     = "rework_s"
	FieldReadAskedAt = "read_asked_at"
	FieldReadDoneAt  = "read_done_at"
	FieldQueued      = "queued" // on the merge card
)

// CycleWindow is how far back a landing counts.
const CycleWindow = 24 * time.Hour

// StageStat is one stage's median and p90, in seconds, over N cards.
type StageStat struct {
	Median float64 `json:"median_s"`
	P90    float64 `json:"p90_s"`
	N      int     `json:"n"`
}

// StageTimes is the stage stats over every stream and per stream; a stage no card
// has a span for is absent.
type StageTimes struct {
	All     map[string]StageStat            `json:"all"`
	Streams map[string]map[string]StageStat `json:"streams"`
}

// readyStamp is the fields of a primary's move to ready: ready_at, once, the first
// time it is ready.
func readyStamp(c *Card, now time.Time) map[string]string {
	if c.F(FieldReadyAt) != "" {
		return nil
	}
	return map[string]string{FieldReadyAt: stamp(now)}
}

// finishStamps is the fields a finish of work card w writes on its primary pr: its
// finish, the work card's deal and take it ran on (takeStamps), and the rework the
// attempt closes (its deal after the primary's last finish).
func finishStamps(pr, w *Card, now time.Time) map[string]string {
	dealt, taken := takeStamps(w)
	set := map[string]string{FieldFinishedAt: stamp(now)}
	if dealt != "" {
		set[FieldDealtAt] = dealt
	}
	if taken != "" {
		set[FieldTakenAt] = taken
	}
	d, _ := time.Parse(time.RFC3339, dealt)
	if prev := stampAt(pr, FieldFinishedAt); !prev.IsZero() && d.After(prev) {
		set[FieldReworkS] = itoa(pr.Int(FieldReworkS) + int(d.Sub(prev)/time.Second))
	}
	return set
}

// CycleTimes is the stage times of the primaries landed in the CycleWindow before now.
func CycleTimes(s *Snapshot, now time.Time) StageTimes {
	all := map[string][]float64{}
	by := map[string]map[string][]float64{}
	for _, c := range s.Work.Column(Landed) {
		if IsSentinel(c) {
			continue
		}
		landed := stampAt(c, "landed")
		if landed.IsZero() || landed.After(now) || now.Sub(landed) > CycleWindow {
			continue
		}
		for stage, d := range stageSpans(s, c, landed) {
			all[stage] = append(all[stage], d)
			if by[c.Row] == nil {
				by[c.Row] = map[string][]float64{}
			}
			by[c.Row][stage] = append(by[c.Row][stage], d)
		}
	}
	out := StageTimes{All: stageStats(all), Streams: map[string]map[string]StageStat{}}
	if len(out.All) == 0 {
		out.All = nil
	}
	for stream, m := range by {
		out.Streams[stream] = stageStats(m)
	}
	if len(out.Streams) == 0 {
		out.Streams = nil
	}
	return out
}

// stageSpans is the card's stage durations in seconds, a stage only where both of
// its stamps are on the card and in order.
func stageSpans(s *Snapshot, c *Card, landed time.Time) map[string]float64 {
	out := map[string]float64{}
	span := func(stage string, a, b time.Time) {
		if !a.IsZero() && !b.IsZero() && !b.Before(a) {
			out[stage] = b.Sub(a).Seconds()
		}
	}
	ready, dealt, taken, finished := stampAt(c, FieldReadyAt), stampAt(c, FieldDealtAt), stampAt(c, FieldTakenAt), stampAt(c, FieldFinishedAt)
	asked, done := stampAt(c, FieldReadAskedAt), stampAt(c, FieldReadDoneAt)
	span(StageNeeds, stampAt(c, "admitted"), ready)
	if c.F(FieldReworkS) == "" {
		span(StageDeal, ready, dealt)
	} else {
		out[StageRework] = float64(c.Int(FieldReworkS))
	}
	span(StageTake, dealt, taken)
	span(StageWork, taken, finished)
	span(StageReadWait, finished, asked)
	span(StageRead, asked, done)
	span(StageAccept, done, stampAt(c, "accepted"))
	if m := s.Merge.Placed(c.ID); m != nil {
		span(StageMerge, stampAt(m, FieldQueued), landed)
	}
	return out
}

// stageStats is each stage's median and p90 over its samples.
func stageStats(m map[string][]float64) map[string]StageStat {
	out := map[string]StageStat{}
	for _, stage := range StageOrder {
		if xs := m[stage]; len(xs) > 0 {
			out[stage] = statOf(xs)
		}
	}
	return out
}

// statOf is the median (the mean of the middle two of an even count) and the p90
// (nearest rank) of xs.
func statOf(xs []float64) StageStat {
	xs = slices.Sorted(slices.Values(xs))
	n := len(xs)
	med := xs[n/2]
	if n%2 == 0 {
		med = (xs[n/2-1] + xs[n/2]) / 2
	}
	return StageStat{Median: med, P90: xs[int(math.Ceil(0.9*float64(n)))-1], N: n}
}

// readStamps is the fields an accept writes on the primary from the reads it counts:
// the first ask and the last ok.
func readStamps(oks []*Card) map[string]string {
	var asked, done time.Time
	for _, o := range oks {
		if a := stampAt(o, "asked"); !a.IsZero() && (asked.IsZero() || a.Before(asked)) {
			asked = a
		}
		if r := stampAt(o, "read"); r.After(done) {
			done = r
		}
	}
	set := map[string]string{}
	if !asked.IsZero() {
		set[FieldReadAskedAt] = stamp(asked)
	}
	if !done.IsZero() {
		set[FieldReadDoneAt] = stamp(done)
	}
	return set
}
