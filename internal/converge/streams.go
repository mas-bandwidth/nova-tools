package converge

// streams.go builds the seven streams from already-read data. Every function
// here is pure: data in, one Stream out. The order they appear in is the order
// the reading prints them, and it is fixed — a reader who learns the shape of
// one tick can diff it against the next.

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/dogfood"
)

// Order is the fixed order of the reading.
var Order = []string{"LANDING", "CLASSES", "SCRIPTS", "PRS", "EDGES", "FLEET", "LEDGER"}

// ---------------------------------------------------------------------------
// LANDING
// ---------------------------------------------------------------------------

// Batch is one integration batch that landed: the pull request, when it merged,
// and how many gate rounds it took.
type Batch struct {
	Number     int
	MergedAt   time.Time
	Rounds     int
	HaveRounds bool
}

// BatchPrefix is what makes a merged pull request a batch. Glenn, 2026-09-18:
// integration batches only — nothing else enters the queue.
const BatchPrefix = "integration-"

// Batches turns merged pull requests into batches, taking each one's rounds
// from the logs when the logs know it and from the body otherwise. The logs
// win: a body is written by hand, and the hand is the thing this verb is
// replacing.
func Batches(merged []PR, logs map[int]int) []Batch {
	var out []Batch
	for _, pr := range merged {
		if !strings.HasPrefix(strings.TrimSpace(pr.Title), BatchPrefix) {
			continue
		}
		b := Batch{Number: pr.Number, MergedAt: pr.ClosedAt}
		if n, ok := logs[pr.Number]; ok && n > 0 {
			b.Rounds, b.HaveRounds = n, true
		} else if n, ok := RoundsFromBody(pr.Body); ok {
			b.Rounds, b.HaveRounds = n, true
		}
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MergedAt.Before(out[j].MergedAt) })
	return out
}

// Landing is the cost of landing: gate rounds per integration batch in the
// window, against the window of equal length immediately before --since.
func Landing(batches []Batch, since, now time.Time) Stream {
	s := Stream{Name: "LANDING", Measure: "rounds-per-batch", Lower: true}
	width := now.Sub(since)
	prevSince := since.Add(-width)

	inWindow, windowRounds := meanRounds(batches, since, now)
	inPrev, prevRounds := meanRounds(batches, prevSince, since)

	if len(windowRounds) > 0 {
		s.Now, s.HaveNow = mean(windowRounds), true
	}
	if len(prevRounds) > 0 {
		s.Before, s.HaveBefore = mean(prevRounds), true
	}
	if !s.HaveNow {
		s.Want = "--repo (no integration batch in the window carried a round count)"
	}
	s = s.WithInt("batches", inWindow).
		WithNum("per-hour", perHour(inWindow, width), width > 0).
		WithInt("prev-batches", inPrev).
		WithNum("prev-per-hour", perHour(inPrev, width), width > 0).
		WithInt("rounds-read", len(windowRounds))
	return s
}

// meanRounds returns how many batches merged in [from, to) and the rounds of
// the ones whose rounds are known. The two numbers are separate on purpose: a
// batch whose rounds nobody recorded still landed.
func meanRounds(batches []Batch, from, to time.Time) (count int, rounds []float64) {
	for _, b := range batches {
		if b.MergedAt.Before(from) || !b.MergedAt.Before(to) {
			continue
		}
		count++
		if b.HaveRounds {
			rounds = append(rounds, float64(b.Rounds))
		}
	}
	return count, rounds
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	total := 0.0
	for _, x := range xs {
		total += x
	}
	return total / float64(len(xs))
}

func perHour(n int, width time.Duration) float64 {
	if width <= 0 {
		return 0
	}
	return float64(n) / width.Hours()
}

// ---------------------------------------------------------------------------
// CLASSES
// ---------------------------------------------------------------------------

// Classes counts the class-test index at both revisions. It is the one stream
// that converges upwards.
func Classes(nowSpec string, beforeSpec string, haveBefore bool) Stream {
	s := Stream{Name: "CLASSES", Measure: "class-test-index-entries", Lower: false}
	s.Now, s.HaveNow = float64(len(ClassTests(nowSpec))), true
	if haveBefore {
		s.Before, s.HaveBefore = float64(len(ClassTests(beforeSpec))), true
	}
	return s
}

// ---------------------------------------------------------------------------
// SCRIPTS
// ---------------------------------------------------------------------------

// Scripts is the sprint's finish line: what is left in bin, against what was
// left at --since — which is what is left now plus what the window retired.
func Scripts(remaining int, rows []RetiredRow, since, now time.Time) Stream {
	s := Stream{Name: "SCRIPTS", Measure: "scripts-left-in-bin", Lower: true}
	inWindow, undated := RetiredInWindow(rows, since, now)
	s.Now, s.HaveNow = float64(remaining), true
	s.Before, s.HaveBefore = float64(remaining+inWindow), true
	return s.WithInt("retired-in-window", inWindow).
		WithInt("retired-rows", len(rows)).
		WithInt("undated", undated)
}

// ---------------------------------------------------------------------------
// PRS
// ---------------------------------------------------------------------------

// PRs is the queue at both ends: open now, and open at --since. The second is
// arithmetic rather than a query, because a forge answers what is open now and
// nothing answers what was open then: it is the still-open ones created before
// --since, plus the ones created before it and closed inside the window.
func PRs(open, closed []PR, since, now time.Time) Stream {
	s := Stream{Name: "PRS", Measure: "open-pull-requests", Lower: true}
	openedInWindow := 0
	before := 0
	for _, pr := range open {
		if pr.CreatedAt.Before(since) {
			before++
		} else {
			openedInWindow++
		}
	}
	closedInWindow := 0
	for _, pr := range closed {
		if pr.ClosedAt.Before(since) || pr.ClosedAt.After(now) {
			continue
		}
		closedInWindow++
		if pr.CreatedAt.Before(since) {
			before++
		}
	}
	s.Now, s.HaveNow = float64(len(open)), true
	s.Before, s.HaveBefore = float64(before), true
	return s.WithInt("closed", closedInWindow).WithInt("opened", openedInWindow)
}

// ---------------------------------------------------------------------------
// EDGES
// ---------------------------------------------------------------------------

// Edges is the dogfood gate: the edges nobody has filed an issue for, now and
// at --since, with the not-ok rate of the rounds inside the window beside it.
// `by` narrows the rounds to the named friends; empty reads them all.
func Edges(receipts []dogfood.Receipt, since, now time.Time, by []string) Stream {
	s := Stream{Name: "EDGES", Measure: "open-edges", Lower: true}
	wanted := map[string]bool{}
	for _, name := range by {
		n := strings.TrimSpace(name)
		if n != "" {
			wanted[strings.ToLower(n)] = true
		}
	}
	keep := func(r dogfood.Receipt) bool {
		if len(wanted) == 0 {
			return true
		}
		return wanted[strings.ToLower(strings.TrimSpace(r.By))]
	}

	openNow, openBefore, notOK, inWindow := 0, 0, 0, 0
	rounds := map[string]int{}
	for _, r := range receipts {
		if !keep(r) {
			continue
		}
		at := r.Time()
		if r.RecordsAnEdge() && !r.Filed() {
			openNow++
			if at.Before(since) {
				openBefore++
			}
		}
		if at.Before(since) || at.After(now) {
			continue
		}
		inWindow++
		rounds[strings.ToLower(strings.TrimSpace(r.By))]++
		if !r.OK {
			notOK++
		}
	}
	s.Now, s.HaveNow = float64(openNow), true
	s.Before, s.HaveBefore = float64(openBefore), true
	perRound := 0.0
	if len(rounds) > 0 {
		perRound = float64(notOK) / float64(len(rounds))
	}
	return s.WithInt("receipts", inWindow).
		WithInt("rounds", len(rounds)).
		WithInt("not-ok", notOK).
		WithNum("not-ok-per-round", perRound, len(rounds) > 0)
}

// ---------------------------------------------------------------------------
// FLEET
// ---------------------------------------------------------------------------

// Fleet is one build across the machines: the units NOT on the majority stamp,
// so a fleet on one build reads zero. Its `before` can only come from the state
// file — a snapshot is a photograph of one instant, and there is no honest way
// to ask it what yesterday looked like.
func Fleet(rows []VersionRow, certified, total int, haveCerts bool) Stream {
	s := Stream{Name: "FLEET", Measure: "units-off-the-one-build", Lower: true}
	counts := map[string]int{}
	for _, r := range rows {
		counts[r.Stamp]++
	}
	best, bestStamp := 0, ""
	for _, stamp := range sortedKeys(counts) {
		if counts[stamp] > best {
			best, bestStamp = counts[stamp], stamp
		}
	}
	s.Now, s.HaveNow = float64(len(rows)-best), true
	s = s.WithInt("units", len(rows)).
		WithInt("stamps", len(counts)).
		With("build", orDash(bestStamp))
	if haveCerts {
		s = s.With("certified", strconv.Itoa(certified)+"/"+strconv.Itoa(total))
	} else {
		s = s.With("certified", "-")
	}
	return s
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// ---------------------------------------------------------------------------
// LEDGER
// ---------------------------------------------------------------------------

// Ledger is what the pit stop still owes: the rows not yet closed. Like FLEET,
// its `before` comes from the state file, because the ledger's rows carry a
// status and not a date.
func Ledger(md string) Stream {
	s := Stream{Name: "LEDGER", Measure: "rows-not-yet-pass", Lower: true}
	rows, open := LedgerRows(md)
	s.Now, s.HaveNow = float64(open), true
	return s.WithInt("rows", rows).WithInt("open", open)
}
