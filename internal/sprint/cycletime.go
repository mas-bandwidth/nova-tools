package sprint

import (
	"math"
	"slices"
	"strconv"
	"time"
)

// Where a card's wall time goes (docs/SPEC-SPRINT.md, the cycle-time-breakdown
// subsection of section 3): the time from add to landed, per stage. A primary
// records its stage times on itself as the steps that move it run, never from
// the log: ready when it is added or resolved, dealt, taken and finished when
// its work finishes (the work card's own stamps, copied up), asked and read when
// it is accepted (the earliest ask and the last read of the readers it needed),
// queued when the accept places it in the merge queue, then the admitted,
// accepted and landed stamps it has always had. StageTimesOf reads them.

// The primary's stage stamps. The deal, take and finish stamps are the last
// attempt's; the first deal is the first attempt's; the rework time is seconds,
// summed over the attempts.
const (
	StReady      = "st_ready"
	StFirstDealt = "st_first_dealt"
	StDealt      = "st_dealt"
	StTaken      = "st_taken"
	StFinished   = "st_finished"
	StAsked      = "st_asked"
	StRead       = "st_read"
	StQueued     = "st_queued"
	StRework     = "st_rework"
)

// StageWindow is how far back a landing counts: the cards landed in the last day.
const StageWindow = 24 * time.Hour

// StageRework is the rework stage: finished to dealt again, summed over a card's
// attempts. It is measured on the cards that were reworked only.
const StageRework = "rework"

// stages are the stages in the order a card passes them: each is the span between
// two stamps of the primary.
var stages = []struct {
	name     string
	from, to func(*Card) time.Time
}{
	{"add_to_ready", func(c *Card) time.Time { return stampAt(c, "admitted") }, func(c *Card) time.Time { return stampAt(c, StReady) }},
	{"ready_to_dealt", func(c *Card) time.Time { return stampAt(c, StReady) }, func(c *Card) time.Time { return stampAt(c, StFirstDealt) }},
	{"dealt_to_taken", func(c *Card) time.Time { return stampAt(c, StDealt) }, func(c *Card) time.Time { return stampAt(c, StTaken) }},
	{"taken_to_finished", func(c *Card) time.Time { return stampAt(c, StTaken) }, func(c *Card) time.Time { return stampAt(c, StFinished) }},
	{"finished_to_asked", func(c *Card) time.Time { return stampAt(c, StFinished) }, func(c *Card) time.Time { return stampAt(c, StAsked) }},
	{"asked_to_read", func(c *Card) time.Time { return stampAt(c, StAsked) }, func(c *Card) time.Time { return stampAt(c, StRead) }},
	{"read_to_accepted", func(c *Card) time.Time { return stampAt(c, StRead) }, func(c *Card) time.Time { return stampAt(c, "accepted") }},
	{"queued_to_landed", func(c *Card) time.Time { return stampAt(c, StQueued) }, func(c *Card) time.Time { return stampAt(c, "landed") }},
}

// StageNames are the stage names in order, rework last.
func StageNames() []string {
	out := make([]string, 0, len(stages)+1)
	for _, st := range stages {
		out = append(out, st.name)
	}
	return append(out, StageRework)
}

// StageStat is one stage's median and 90th percentile, in seconds, over the N
// cards it was measured on.
type StageStat struct {
	MedianS float64 `json:"median_s"`
	P90S    float64 `json:"p90_s"`
	N       int     `json:"n"`
}

// StageTimes is each stage's numbers over the cards landed in the last StageWindow,
// overall and per stream; a stage no card was measured on is absent.
type StageTimes struct {
	Overall map[string]StageStat            `json:"overall"`
	Streams map[string]map[string]StageStat `json:"streams"`
}

// StageTimesOf is the stage times of a snapshot holding the work table, at now.
// Pure: the cards' stamps in, the numbers out. A card with a stamp missing (one
// landed before the stamps, or moved by a step that does not write them) is left
// out of the stages it cannot give.
func StageTimesOf(s *Snapshot, now time.Time) StageTimes {
	all := map[string][]float64{}
	by := map[string]map[string][]float64{}
	if s.Work != nil {
		for _, c := range s.Work.Column(Landed) {
			landed := stampAt(c, "landed")
			if IsSentinel(c) || landed.IsZero() || now.Sub(landed) > StageWindow {
				continue
			}
			if by[c.Row] == nil {
				by[c.Row] = map[string][]float64{}
			}
			add := func(name string, sec float64) {
				all[name] = append(all[name], sec)
				by[c.Row][name] = append(by[c.Row][name], sec)
			}
			for _, st := range stages {
				if from, to := st.from(c), st.to(c); !from.IsZero() && !to.IsZero() {
					add(st.name, max(0, to.Sub(from).Seconds()))
				}
			}
			if rework, err := strconv.ParseFloat(c.F(StRework), 64); err == nil && rework > 0 {
				add(StageRework, rework)
			}
		}
	}
	out := StageTimes{Overall: statsOf(all), Streams: map[string]map[string]StageStat{}}
	for stream, m := range by {
		out.Streams[stream] = statsOf(m)
	}
	return out
}

// statsOf is the median and p90 of every stage that has a sample.
func statsOf(m map[string][]float64) map[string]StageStat {
	out := map[string]StageStat{}
	for name, xs := range m {
		slices.Sort(xs)
		n := len(xs)
		med := xs[n/2]
		if n%2 == 0 {
			med = (xs[n/2-1] + xs[n/2]) / 2
		}
		out[name] = StageStat{MedianS: med, P90S: xs[int(math.Ceil(0.9*float64(n)))-1], N: n}
	}
	return out
}

// stageFinish adds the finish step's stage stamps to the primary's set: the work
// card's deal and take stamps (takeStamps) and the finish.
func stageFinish(set map[string]string, dealt, taken string, now time.Time) {
	set[StDealt], set[StTaken], set[StFinished] = dealt, taken, stamp(now)
}

// stageDeal adds the deal's stage stamps to the primary's set: the first deal once,
// and, on an attempt after a finish, the rework time since that finish added to the
// seconds already summed.
func stageDeal(set map[string]string, c *Card, now time.Time) {
	if c.F(StFirstDealt) == "" {
		set[StFirstDealt] = stamp(now)
	}
	if prev := stampAt(c, StFinished); !prev.IsZero() && now.After(prev) {
		sum, _ := strconv.ParseFloat(c.F(StRework), 64)
		set[StRework] = strconv.FormatFloat(sum+now.Sub(prev).Seconds(), 'f', -1, 64)
	}
}

// acceptStamps is the accept step's set on the primary: the readers, accepted and
// queued now, and the earliest ask and the last read of the ok reads it stands on.
func acceptStamps(oks []*Card, readers string, now time.Time) map[string]string {
	set := map[string]string{"readers": readers, "accepted": stamp(now), StQueued: stamp(now)}
	var asked, read time.Time
	for _, o := range oks {
		if at := stampAt(o, "asked"); !at.IsZero() && (asked.IsZero() || at.Before(asked)) {
			asked = at
		}
		if at := stampAt(o, "read"); at.After(read) {
			read = at
		}
	}
	if !asked.IsZero() {
		set[StAsked] = stamp(asked)
	}
	if !read.IsZero() {
		set[StRead] = stamp(read)
	}
	return set
}
