package sprint

import (
	"fmt"
	"strconv"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// ArchivedWorkRow is whether a work row is an archived stream's: hidden and holding only
// landed cards. A hidden row that holds a card not landed again (an add to it) is back on
// the table, counted in the headline, before the next tick draws it again.
func ArchivedWorkRow(t ntable.Table, r ntable.Row) bool {
	if !r.Hidden {
		return false
	}
	j := t.Column(Landed)
	for k, c := range t.Columns {
		if c.Projection == ntable.Count && k != j && k < len(r.Cells) && r.Cells[k].Count > 0 {
			return false
		}
	}
	return true
}

// WorkTableCounts is the landed and all primaries of the work table's streams on the
// table: an archived stream's row (ArchivedWorkRow) is not counted (ArchivedTableCounts
// counts those).
func WorkTableCounts(t ntable.Table) (landed, all int64) {
	return countWorkRows(t, false)
}

// ArchivedTableCounts is the landed and all primaries of the archived streams.
func ArchivedTableCounts(t ntable.Table) (landed, all int64) {
	return countWorkRows(t, true)
}

func countWorkRows(t ntable.Table, archived bool) (landed, all int64) {
	j := t.Column(Landed)
	for _, r := range t.Rows {
		if ArchivedWorkRow(t, r) != archived {
			continue
		}
		for k, c := range t.Columns {
			if c.Projection != ntable.Count || k >= len(r.Cells) {
				continue
			}
			all += r.Cells[k].Count
			if k == j {
				landed += r.Cells[k].Count
			}
		}
	}
	return landed, all
}

// HeadlineCounts is the landed and all primaries of the work table: over cards admitted or
// finished since the last tidy when one is recorded (bases, statsSince), or the whole
// epoch's when none is recorded (statsSince zero). An archived stream's cards are not in the
// total.
func HeadlineCounts(t ntable.Table, bases map[string]StreamBase, statsSince time.Time) (landed, all int64) {
	if statsSince.IsZero() {
		return WorkTableCounts(t)
	}
	j := t.Column(Landed)
	for _, r := range t.Rows {
		if ArchivedWorkRow(t, r) {
			continue
		}
		var open, landedNow int64
		for k, c := range t.Columns {
			if c.Projection != ntable.Count || k >= len(r.Cells) {
				continue
			}
			if k == j {
				landedNow += r.Cells[k].Count
			} else {
				open += r.Cells[k].Count
			}
		}
		baseLanded := int64(0)
		if b, ok := bases[r.Key]; ok {
			baseLanded = int64(b.Landed)
		}
		landedSince := max(0, landedNow-baseLanded)
		landed += landedSince
		all += landedSince + open
	}
	return landed, all
}

// ProgressLine is landed / all primaries and the percent: "3/10 30.0%", with "since <time>"
// beside the numbers when a tidy is recorded: "0/10 0.0% since 2026-10-09T20:00:00Z".
func ProgressLine(landed, all int64, statsSince time.Time) string {
	pct := "0.0%"
	if all > 0 {
		pct = strconv.FormatFloat(100*float64(landed)/float64(all), 'f', 1, 64) + "%"
	}
	res := fmt.Sprintf("%d/%d %s", landed, all, pct)
	if !statsSince.IsZero() {
		res += " since " + statsSince.UTC().Format(time.RFC3339)
	}
	return res
}
