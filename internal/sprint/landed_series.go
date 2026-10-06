package sprint

import (
	"strings"
	"time"
)

// The landed series (docs/SPEC-SPRINT.md, the landed series): the cards landed per
// 10-minute bucket over the last 24 hours, split by who worked them, from the epoch's log.
// What the dashboard's Landings panel drew from a shell loop, read from the store instead.

// LandedBucket is the width of a bucket in seconds, and LandedBuckets how many the series
// holds: 24 hours.
const (
	LandedBucket  = 600
	LandedBuckets = 144
)

// LandedTotals is a count of landings by worker class: a friend's, a fleet machine's, and
// those whose work attempt the log does not hold (a card worked before the epoch began).
type LandedTotals struct {
	Friends int `json:"friends"`
	Fleet   int `json:"fleet"`
	Unknown int `json:"unknown"`
}

// LandedSeries is the landed-per-10-minute series. Friends and Fleet are LandedBuckets counts
// each, oldest first, the last bucket the one containing now; Start is the first bucket's
// start. Totals and LastHour count the window and its last hour; Workers counts the window's
// landings by worker (a friend by name without the friend. prefix).
type LandedSeries struct {
	BucketSeconds int            `json:"bucket_seconds"`
	Buckets       int            `json:"buckets"`
	Start         time.Time      `json:"start"`
	Friends       []int          `json:"friends"`
	Fleet         []int          `json:"fleet"`
	Totals        LandedTotals   `json:"totals"`
	LastHour      LandedTotals   `json:"last_hour"`
	Workers       map[string]int `json:"workers"`
}

// LandedSeriesOf reads the series off the log's lines as of now. A landing is a move of the
// work table to <stream>:landed from any place but a wait (a sentinel's release is not
// work), counted once per card at its first time. Its worker is the member of the last
// <member>:ok move of the card's work attempt (<card>.w<n>); a member named friend.<name>
// is a friend's, any other a fleet machine's, and the lander is never the worker.
func LandedSeriesOf(lines []Line, now time.Time) LandedSeries {
	end := now.Truncate(LandedBucket * time.Second)
	start := end.Add(-(LandedBuckets - 1) * LandedBucket * time.Second)
	s := LandedSeries{BucketSeconds: LandedBucket, Buckets: LandedBuckets, Start: start,
		Friends: make([]int, LandedBuckets), Fleet: make([]int, LandedBuckets), Workers: map[string]int{}}
	worker := map[string]string{} // primary -> member of its last work ok
	type landing struct {
		card string
		at   time.Time
	}
	var landed []landing
	seen := map[string]bool{}
	for _, l := range lines {
		if l.Kind != LineMove {
			continue
		}
		cards := l.Cards
		if len(cards) == 0 {
			cards = []string{l.Card}
		}
		switch {
		case l.Table == Work && strings.HasSuffix(l.To, ":landed") && !strings.HasSuffix(l.From, ":landed") && !strings.HasSuffix(l.From, ":waiting"):
			for _, c := range cards {
				if c != "" && !seen[c] {
					seen[c] = true
					landed = append(landed, landing{c, l.At})
				}
			}
		case strings.HasSuffix(l.To, ":ok") && !l.Removed:
			member := strings.TrimSuffix(l.To, ":ok")
			for _, c := range cards {
				if p, ok := workPrimary(c); ok {
					worker[p] = member
				}
			}
		}
	}
	for _, g := range landed {
		if g.at.Before(start) || !g.at.Before(end.Add(LandedBucket*time.Second)) {
			continue
		}
		who, known := worker[g.card]
		hour := !g.at.Before(now.Add(-time.Hour))
		switch {
		case !known:
			s.Totals.Unknown++
			if hour {
				s.LastHour.Unknown++
			}
			continue
		case strings.HasPrefix(who, "friend."):
			who = strings.TrimPrefix(who, "friend.")
			s.Friends[int(g.at.Sub(start)/(LandedBucket*time.Second))]++
			s.Totals.Friends++
			if hour {
				s.LastHour.Friends++
			}
		default:
			s.Fleet[int(g.at.Sub(start)/(LandedBucket*time.Second))]++
			s.Totals.Fleet++
			if hour {
				s.LastHour.Fleet++
			}
		}
		s.Workers[who]++
	}
	return s
}

// workPrimary is the primary a work attempt card <primary>.w<n> belongs to.
func workPrimary(card string) (string, bool) {
	i := strings.LastIndex(card, ".w")
	if i < 1 || i+2 == len(card) {
		return "", false
	}
	for _, r := range card[i+2:] {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return card[:i], true
}
