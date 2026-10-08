package sprint

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// The merge queue's age check (docs/SPEC-RELEASE.md section 16, subsection
// release-check-merge-queue-p90-b.w7; docs/SPEC-SPRINT.md section 11, the same
// subsection): merges must not fall behind, so over the last window the p90 of
// the time each card spent in merging, by the nearest rank, is held under a
// bar. The bar is half an hour because the stream merges in batches and a
// single card's merge should be a push and a green gate, not an afternoon: at
// thirty minutes a card is visibly waiting on the queue and a release should
// see it. The check reads only the log and the clock, so a unit test fakes it.

// CheckMergeQueueP90 is the merge queue's check name.
const CheckMergeQueueP90 = "merge-queue-p90"

// MergeQueueWindowDefault is how far back a merge spell counts: the last day,
// the same day the landings and the cycle-time breakdown read.
const MergeQueueWindowDefault = 24 * time.Hour

// MergeQueueP90Default is the bar on the merge queue's p90.
const MergeQueueP90Default = 30 * time.Minute

// PercentileNearestRank is the nearest-rank percentile of xs: order the
// samples and take the one at rank ceil(q*n), one-based, so p90 of n samples
// is the value at or above 90% of them. q is a fraction (0.9 is the p90).
// ok is false when xs is empty: no sample has no percentile. xs is not
// reordered.
func PercentileNearestRank(xs []time.Duration, q float64) (time.Duration, bool) {
	n := len(xs)
	if n == 0 {
		return 0, false
	}
	sorted := append([]time.Duration(nil), xs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	rank := int(math.Ceil(q * float64(n)))
	if rank < 1 {
		rank = 1
	}
	if rank > n {
		rank = n
	}
	return sorted[rank-1], true
}

// MergeQueueP90: over the last MergeWindow, the p90 (nearest rank) of the time
// each card spent in merging, from the log's work-table moves: entering
// merging to landing or to leaving the column. A card still merging at now
// counts with its age now. A card counts when any of its merging overlapped
// the window, and its age is the whole time it spent merging, so a card left
// in the queue before the window and still there is not hidden by the window.
// Above the bar the evidence prints the p90, the count of cards and the oldest
// card still merging; with no merge in the window it is ok and says n=0.
func MergeQueueP90(f ReleaseFacts) ReleaseResult {
	now := f.Now()
	window := f.MergeWindow()
	bar := f.MergeP90()
	ages, still := mergeQueueAges(f.Log(), now.Add(-window), now)

	ids := make([]string, 0, len(ages))
	for id := range ages {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	samples := make([]time.Duration, 0, len(ids))
	for _, id := range ids {
		samples = append(samples, ages[id])
	}
	oldest, oldestAge := "", time.Duration(0)
	for _, id := range ids {
		if _, open := still[id]; open && (oldest == "" || ages[id] > oldestAge) {
			oldest, oldestAge = id, ages[id]
		}
	}
	if _, ok := PercentileNearestRank(samples, 0.9); !ok {
		return ReleaseResult{Name: CheckMergeQueueP90, OK: true,
			Evidence: fmt.Sprintf("no card spent time merging in the last %s (n=0 cards); the bar is %s", dur(window), dur(bar))}
	}
	p90, _ := PercentileNearestRank(samples, 0.9)
	stillWords := "no card is still merging"
	if oldest != "" {
		stillWords = fmt.Sprintf("oldest still merging: %s for %s", oldest, dur(oldestAge))
	}
	if p90 <= bar {
		return ReleaseResult{Name: CheckMergeQueueP90, OK: true,
			Evidence: fmt.Sprintf("p90 %s over n=%d cards merging in the last %s, under the bar of %s; %s",
				dur(p90), len(samples), dur(window), dur(bar), stillWords)}
	}
	look := "nova-sprint where --all"
	if oldest != "" {
		look = fmt.Sprintf("nova-sprint where --all, nova-sprint log --card %s", oldest)
	}
	return ReleaseResult{Name: CheckMergeQueueP90, OK: false,
		Evidence: fmt.Sprintf("p90 %s over n=%d cards merging in the last %s is over the bar of %s; %s; look at: %s",
			dur(p90), len(samples), dur(window), dur(bar), stillWords, look)}
}

// dur is a duration as the evidence prints it, whole seconds.
func dur(d time.Duration) string { return d.Round(time.Second).String() }

// mergeQueueAges replays the log's work-table moves and returns, keyed by
// card, the whole time each card spent in the merging column, but only for the
// cards whose merging overlapped [from, now]; and, for the cards still merging
// at now, the moment each entered. An epoch jump or a clear ends every open
// spell at that line's moment, since the tables are wiped there.
func mergeQueueAges(lines []Line, from, now time.Time) (ages map[string]time.Duration, still map[string]time.Time) {
	total := map[string]time.Duration{}
	overlapped := map[string]bool{}
	started := map[string]time.Time{}
	closeSpell := func(id string, at time.Time) {
		start, open := started[id]
		if !open {
			return
		}
		if at.After(start) {
			total[id] += at.Sub(start)
		}
		if at.After(from) && start.Before(now) {
			overlapped[id] = true
		}
		delete(started, id)
	}
	var epoch uint64
	haveEpoch := false
	for _, l := range lines {
		switch {
		case !haveEpoch && l.Epoch > 0:
			haveEpoch, epoch = true, l.Epoch
		case haveEpoch && l.Epoch > epoch:
			for id := range started {
				closeSpell(id, l.At)
			}
			epoch = l.Epoch
		}
		if l.Verb == "clear" {
			for id := range started {
				closeSpell(id, l.At)
			}
			continue
		}
		if l.Kind != LineMove || l.Table != Work {
			continue
		}
		_, fromCol, fromPlaced := strings.Cut(l.From, ":")
		_, toCol, toPlaced := strings.Cut(l.To, ":")
		stays := fromPlaced && toPlaced && fromCol == Merging && toCol == Merging
		leaves := fromPlaced && fromCol == Merging && !stays
		enters := !l.Removed && toPlaced && toCol == Merging && !stays
		ids := l.Cards
		if len(ids) == 0 {
			ids = []string{l.Card}
		}
		for _, id := range ids {
			if id == "" {
				continue
			}
			if leaves {
				closeSpell(id, l.At)
			}
			if enters {
				if _, open := started[id]; !open {
					started[id] = l.At
				}
			}
		}
	}
	still = map[string]time.Time{}
	for id, start := range started {
		closeSpell(id, now)
		still[id] = start
	}
	ages = map[string]time.Duration{}
	for id, d := range total {
		if overlapped[id] {
			ages[id] = d
		}
	}
	return ages, still
}
