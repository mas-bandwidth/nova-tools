package sprint

import (
	"context"
	"regexp"
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

	type landing struct {
		card string
		at   time.Time
	}
	var landings []landing
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
			landings = append(landings, landing{card: card, at: l.At})
		}
	}

	const (
		bucketSeconds = 600
		numBuckets    = 144
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
		t := ld.at.Unix()
		if t < start || t >= end+bucketSeconds {
			continue
		}
		b := int((t - start) / bucketSeconds)
		if b < 0 || b >= numBuckets {
			continue
		}

		worker, hasWorker := latestWorker[ld.card]
		if !hasWorker || worker == "" {
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
		case "fleet":
			fleet[b]++
			totals.Fleet++
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

		workers[worker]++
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
