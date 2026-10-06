package sprintdash

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// The merge row (docs/SPEC-SPRINT-DASHBOARD.md, "Merge"; the owner, 2026-10-04: "Is this
// progress visible in the sprint dashboard yet?"): where --json's merge_row, drawn under the
// progress bar. The row is a pure function of the facts where reads (MergeRowOf); where
// gathers them, the page draws them.

// The base gate's states: red names its failing test; "" is not known, drawn "-".
const (
	GateRed     = "red"
	GateGreen   = "green"
	GateUnknown = ""
)

// MergeRow is where --json's merge_row: the cards in merging and in review, the landings of
// the last 30 minutes, the oldest merging card's age, the base's gate and its failing test,
// the commits the base lacks of the development branch and dev lacks of the base, and the
// minutes since the last dev sync and the last promotion. A nil minute is not known.
type MergeRow struct {
	Merging          int64  `json:"merging"`
	Review           int64  `json:"review"`
	LandedPer30m     int64  `json:"landed_per_30m"`
	OldestMergingMin *int   `json:"oldest_merging_min"`
	BaseGate         string `json:"base_gate"`
	FailingTest      string `json:"failing_test"`
	BaseLacks        int    `json:"base_lacks"`
	DevLacks         int    `json:"dev_lacks"`
	SyncMinutes      *int   `json:"sync_minutes"`
	PromotionMinutes *int   `json:"promotion_minutes"`
}

// MergeFacts is what where reads for the merge row.
type MergeFacts struct {
	Now             time.Time
	Merging, Review int64       // the work table's merging and review counts
	Landed          []time.Time // landing stamps (the where record's)
	// MergingRead says the merging cards were read (where --json --cards or --rows), and
	// MergingSince is their accepted stamps, a zero one unreadable. Unread, the oldest is
	// not known.
	MergingRead  bool
	MergingSince []time.Time
	// BaseRed is the open base-red judgments' texts (the base-gate rule's stop and the
	// drift alarm's whole-tree gate); LanderGreen says the lander's tree gate passed at a
	// stream's last landing (the merge table's ci).
	BaseRed     []string
	LanderGreen bool
	// BaseLacks and DevLacks are the drift the last dev sync measured; LastSync and
	// LastPromotion are zero when none is recorded.
	BaseLacks, DevLacks     int
	LastSync, LastPromotion time.Time
}

// MergeWindow is the span landed_per_30m counts.
const MergeWindow = 30 * time.Minute

// MergeRowOf is the merge row of the facts. The gate is red on any open base-red judgment,
// green when there is none and the lander's gate passed at its last landing, and not known
// otherwise: never green by default.
func MergeRowOf(f MergeFacts) MergeRow {
	m := MergeRow{Merging: f.Merging, Review: f.Review, BaseLacks: f.BaseLacks, DevLacks: f.DevLacks}
	from := f.Now.Add(-MergeWindow)
	for _, at := range f.Landed {
		if !at.Before(from) && !at.After(f.Now) {
			m.LandedPer30m++
		}
	}
	if f.MergingRead {
		var oldest time.Time
		for _, at := range f.MergingSince {
			if !at.IsZero() && (oldest.IsZero() || at.Before(oldest)) {
				oldest = at
			}
		}
		m.OldestMergingMin = minutesSince(f.Now, oldest)
	}
	switch {
	case len(f.BaseRed) > 0:
		m.BaseGate, m.FailingTest = GateRed, failingTest(f.BaseRed[0])
	case f.LanderGreen:
		m.BaseGate = GateGreen
	}
	m.SyncMinutes = minutesSince(f.Now, f.LastSync)
	m.PromotionMinutes = minutesSince(f.Now, f.LastPromotion)
	return m
}

// minutesSince is the whole minutes from at to now, 0 for a stamp ahead of the clock, nil
// for a zero stamp.
func minutesSince(now, at time.Time) *int {
	if at.IsZero() {
		return nil
	}
	n := 0
	if now.After(at) {
		n = int(now.Sub(at).Minutes())
	}
	return &n
}

var testName = regexp.MustCompile(`\bTest[A-Z0-9_]\w*`)

// failingTestMax is the most bytes of a finding the row carries when it names no test.
const failingTestMax = 120

// failingTest is the first test a base-red finding names; naming none, the finding after
// its preamble (the last ": "), cut to one short line.
func failingTest(what string) string {
	if t := testName.FindString(what); t != "" {
		return t
	}
	if i := strings.LastIndex(what, ": "); i >= 0 {
		what = what[i+2:]
	}
	what = strings.Join(strings.Fields(what), " ")
	if len(what) > failingTestMax {
		what = strings.ToValidUTF8(what[:failingTestMax], "") + "…"
	}
	return what
}

// Line is the row in one line, as the page reads it left to right.
func (m MergeRow) Line() string {
	gate := dash(m.BaseGate)
	if m.FailingTest != "" {
		gate += " " + m.FailingTest
	}
	return fmt.Sprintf("merging %d · review %d · landed %d/30m · oldest %s · base %s · base lacks %d, dev lacks %d · sync %s · promoted %s",
		m.Merging, m.Review, m.LandedPer30m, minutesText(m.OldestMergingMin, ""), gate, m.BaseLacks, m.DevLacks,
		minutesText(m.SyncMinutes, " ago"), minutesText(m.PromotionMinutes, " ago"))
}

func minutesText(n *int, suffix string) string {
	if n == nil {
		return "-"
	}
	return fmt.Sprintf("%dm%s", *n, suffix)
}
