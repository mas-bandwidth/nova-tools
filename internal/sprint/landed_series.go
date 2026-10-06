package sprint

import (
	"regexp"
	"strings"
	"time"
)

// Landed series (where --json's landedSeries). The owner, 2026-10-05 ~9:20 AM ET:
// the dashboard's Landings panel reads one verb, computed in the tool from the
// store, and the shell loop that pulled the log is not the product.
//
// A landing is a work-table move to "<stream>:landed" from any state but
// waiting. A sentinel's release (waiting to landed) is not work. Each card
// counts once, the first landing in log order. A set move counts every card
// on the line. The worker is the last "<who>:ok" of the card's work attempt
// (a card matching `.w<n>`), by the line's time, and a tie keeps the later
// line. "friend.<name>" is a friend; anything else is a fleet machine; a
// missing worker is unknown. The lander is not the worker: the :ok row is.

const (
	// LandedBucketSeconds is one bucket of the series: ten minutes.
	LandedBucketSeconds int64 = 600
	// LandedBuckets is the series over the last 24 hours.
	LandedBuckets = 144
)

// workAttempt is a work card of one attempt: "<primary>.w<n>".
var workAttempt = regexp.MustCompile(`\.w[0-9]+$`)

// LandedTotals is how many cards landed in the window, by the worker's kind.
type LandedTotals struct {
	Friends int `json:"friends"`
	Fleet   int `json:"fleet"`
	Unknown int `json:"unknown"`
}

// LandedHour is how many of those landed in the last hour of the clock.
type LandedHour struct {
	Friends int `json:"friends"`
	Fleet   int `json:"fleet"`
}

// LandedSeries is cards landed per ten minutes over the last 24 hours.
// Friends and Fleet each have LandedBuckets counts. The last bucket is the
// one now is in. Start is the unix time of the first bucket.
type LandedSeries struct {
	BucketSeconds int64        `json:"bucketSeconds"`
	Start         int64        `json:"start"`
	Buckets       int          `json:"buckets"`
	Friends       []int        `json:"friends"`
	Fleet         []int        `json:"fleet"`
	Totals        LandedTotals `json:"totals"`
	LastHour      LandedHour   `json:"lastHour"`
}

type landedWorker struct {
	who string
	at  time.Time
}

// LandedSeriesOf folds lines, the epoch's log in order, at now.
// Buckets align to the wall clock's ten minutes: the last is the one now
// is in, and the first is 143 buckets before it. A landing before that
// window is not counted. Totals count every selected landing from the
// start of the window on, and the series counts those that fall in a bucket.
func LandedSeriesOf(lines []Line, now time.Time) LandedSeries {
	end := now.Unix() / LandedBucketSeconds * LandedBucketSeconds
	start := end - (LandedBuckets-1)*LandedBucketSeconds
	out := LandedSeries{
		BucketSeconds: LandedBucketSeconds,
		Start:         start,
		Buckets:       LandedBuckets,
		Friends:       make([]int, LandedBuckets),
		Fleet:         make([]int, LandedBuckets),
	}
	workers := map[string]landedWorker{}
	first := map[string]int64{}
	var order []string
	for _, l := range lines {
		if l.Kind != LineMove {
			continue
		}
		cards := l.Cards
		if len(cards) == 0 && l.Card != "" {
			cards = []string{l.Card}
		}
		for _, card := range cards {
			if card == "" {
				continue
			}
			if workAttempt.MatchString(card) && strings.HasSuffix(l.To, ":ok") {
				primary := workAttempt.ReplaceAllString(card, "")
				who := strings.TrimSuffix(l.To, ":ok")
				prev, ok := workers[primary]
				if !ok || !l.At.Before(prev.at) {
					workers[primary] = landedWorker{who: who, at: l.At}
				}
			}
			if l.Table != Work || !strings.HasSuffix(l.To, ":landed") {
				continue
			}
			if strings.HasSuffix(l.From, ":waiting") || strings.HasSuffix(l.From, ":landed") {
				continue
			}
			if _, seen := first[card]; seen {
				continue
			}
			first[card] = l.At.Unix()
			order = append(order, card)
		}
	}
	windowEnd := start + int64(LandedBuckets)*LandedBucketSeconds
	hour := now.Unix() - 3600
	for _, card := range order {
		t := first[card]
		if t < start {
			continue
		}
		switch landedClass(workers[card].who) {
		case "friends":
			out.Totals.Friends++
			if t >= hour {
				out.LastHour.Friends++
			}
			if t < windowEnd {
				out.Friends[(t-start)/LandedBucketSeconds]++
			}
		case "fleet":
			out.Totals.Fleet++
			if t >= hour {
				out.LastHour.Fleet++
			}
			if t < windowEnd {
				out.Fleet[(t-start)/LandedBucketSeconds]++
			}
		default:
			out.Totals.Unknown++
		}
	}
	return out
}

// landedClass is the worker's series: a missing worker is unknown, a
// friend.<name> row is friends, and any other row is a fleet machine.
func landedClass(who string) string {
	switch {
	case who == "" || who == "-":
		return "unknown"
	case strings.HasPrefix(who, "friend."):
		return "friends"
	default:
		return "fleet"
	}
}
