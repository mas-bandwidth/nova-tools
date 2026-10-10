package sprint

import (
	"cmp"
	"context"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// LandedSeries represents cards landed per 10-minute bucket over 24 hours,
// split between friends and fleet, for the Landings panel.
type LandedSeries struct {
	Generated      string         `json:"generated"`
	GeneratedEpoch int64          `json:"generatedEpoch"`
	BucketSeconds  int            `json:"bucketSeconds"`
	Start          int64          `json:"start"`
	Buckets        int            `json:"buckets"`
	Friends        []int          `json:"friends"`
	Fleet          []int          `json:"fleet"`
	Totals         SeriesTotals   `json:"totals"`
	LastHour       SeriesLastHour `json:"lastHour"`
	Workers        map[string]int `json:"workers"`
}

type SeriesTotals struct {
	Friends int `json:"friends"`
	Fleet   int `json:"fleet"`
	Unknown int `json:"unknown"`
}

type SeriesLastHour struct {
	Friends int `json:"friends"`
	Fleet   int `json:"fleet"`
}

// LogReader is any store or log source that can return the epoch's log lines.
type LogReader interface {
	Log(ctx context.Context) ([]Line, error)
}

// LandedSeriesFrom computes the landed series from any log reader at now.
func LandedSeriesFrom(ctx context.Context, r LogReader, now time.Time) (LandedSeries, error) {
	lines, err := r.Log(ctx)
	if err != nil {
		return LandedSeries{}, err
	}
	return LandedSeriesOf(lines, now), nil
}

var reWorkCard = regexp.MustCompile(`\.w[0-9]+$`)

// LandedSeriesOf computes the landed series from log lines at reference time now.
func LandedSeriesOf(lines []Line, now time.Time) LandedSeries {
	latestWorker := make(map[string]string)
	workerAt := make(map[string]time.Time)

	for _, l := range lines {
		if l.Kind != LineMove && l.Kind != "move" {
			continue
		}
		if !strings.HasSuffix(l.To, ":ok") {
			continue
		}
		for _, card := range l.Names() {
			if !reWorkCard.MatchString(card) {
				continue
			}
			base := reWorkCard.ReplaceAllString(card, "")
			worker := strings.TrimSuffix(l.To, ":ok")
			if at, ok := workerAt[base]; !ok || !l.At.Before(at) {
				latestWorker[base] = worker
				workerAt[base] = l.At
			}
		}
	}

	var landings []Landing
	seenLanded := make(map[string]bool)

	for _, l := range lines {
		if l.Kind != LineMove && l.Kind != "move" {
			continue
		}
		if l.Table != Work && l.Table != "work" {
			continue
		}
		if !strings.HasSuffix(l.To, ":landed") && l.To != "landed" {
			continue
		}
		if strings.HasSuffix(l.From, ":landed") || l.From == "landed" ||
			strings.HasSuffix(l.From, ":waiting") || l.From == "waiting" {
			continue
		}
		for _, card := range l.Names() {
			if card == "" || seenLanded[card] {
				continue
			}
			seenLanded[card] = true
			landings = append(landings, Landing{At: l.At.Unix(), Worker: latestWorker[card]})
		}
	}
	return LandedSeriesOfLandings(landings, now)
}

// Landing is one card's landing as the landed series counts it: when it landed (Unix
// seconds), and the fleet row that finished its latest work card ok ("" when none did).
type Landing struct {
	At     int64  `json:"at"`
	Worker string `json:"worker,omitempty"`
}

// SeriesWindow is how far back the landings the tick keeps for the series reach: the
// series' 24 hours and one bucket more, so a where a bucket after the count still fills
// its first bucket.
const SeriesWindow = 24*time.Hour + seriesBucketSeconds*time.Second

const (
	seriesBucketSeconds = 600
	seriesBuckets       = 144
)

// SeriesLandings is the landings the series counts, from the tables instead of the log
// (the tick's where record, store.WhereRecord): every work card in landed whose landed
// stamp is at or after since, with the row of its latest work card (<card>.w<n>) in a
// fleet row's ok cell, the latest by its finished_at stamp, then by n. It is what
// LandedSeriesOf finds in the log: a landing's time and the row its latest ok move went to.
func SeriesLandings(s *Snapshot, since time.Time) []Landing {
	if s == nil || s.Work == nil {
		return []Landing{}
	}
	type ok struct {
		row      string
		finished time.Time
		n        int
	}
	latest := map[string]ok{}
	if s.Fleet != nil {
		for _, c := range s.Fleet.Column(DoneOK) {
			if !reWorkCard.MatchString(c.ID) {
				continue
			}
			base := reWorkCard.ReplaceAllString(c.ID, "")
			n, _ := strconv.Atoi(c.ID[strings.LastIndex(c.ID, ".w")+2:]) // ignored: the pattern matched digits
			at, _ := time.Parse(time.RFC3339, c.F(FieldFinishedAt))      // unreadable or absent: zero, n decides
			cur, seen := latest[base]
			if !seen || at.After(cur.finished) || at.Equal(cur.finished) && n >= cur.n {
				latest[base] = ok{row: c.Row, finished: at, n: n}
			}
		}
	}
	out := []Landing{}
	for _, c := range s.Work.Column(Landed) {
		at, err := time.Parse(time.RFC3339, c.F("landed"))
		if err != nil || at.Before(since) {
			continue
		}
		out = append(out, Landing{At: at.Unix(), Worker: latest[c.ID].row})
	}
	slices.SortFunc(out, func(a, b Landing) int { return cmp.Or(cmp.Compare(a.At, b.At), cmp.Compare(a.Worker, b.Worker)) })
	return out
}

// LandedSeriesOfLandings buckets landings at reference time now: 144 buckets of 10
// minutes ending with now's, each landing counted to friends (a friend's row), fleet
// (any other row) or unknown (no row).
func LandedSeriesOfLandings(landings []Landing, now time.Time) LandedSeries {
	const (
		bucketSeconds = seriesBucketSeconds
		numBuckets    = seriesBuckets
	)
	nowUnix := now.Unix()
	end := (nowUnix / bucketSeconds) * bucketSeconds
	start := end - int64(numBuckets-1)*bucketSeconds

	friends := make([]int, numBuckets)
	fleet := make([]int, numBuckets)
	totals := SeriesTotals{}
	lastHour := SeriesLastHour{}
	workers := make(map[string]int)

	for _, ld := range landings {
		t := ld.At
		if t < start || t >= end+bucketSeconds {
			continue
		}
		b := int((t - start) / bucketSeconds)
		if b < 0 || b >= numBuckets {
			continue
		}

		worker := ld.Worker
		if worker == "" {
			worker = "-"
		}

		var cls string
		if worker == "-" {
			cls = "unknown"
		} else if strings.HasPrefix(worker, "friend.") {
			cls = "friends"
		} else {
			cls = "fleet"
		}

		switch cls {
		case "friends":
			friends[b]++
			totals.Friends++
			workers[worker]++
		case "fleet":
			fleet[b]++
			totals.Fleet++
			workers[worker]++
		case "unknown":
			totals.Unknown++
		}

		if t >= nowUnix-3600 {
			switch cls {
			case "friends":
				lastHour.Friends++
			case "fleet":
				lastHour.Fleet++
			}
		}
	}

	return LandedSeries{
		Generated:      now.Format(time.RFC3339),
		GeneratedEpoch: nowUnix,
		BucketSeconds:  bucketSeconds,
		Start:          start,
		Buckets:        numBuckets,
		Friends:        friends,
		Fleet:          fleet,
		Totals:         totals,
		LastHour:       lastHour,
		Workers:        workers,
	}
}
