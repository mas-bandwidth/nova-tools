package sprintdash

import "fmt"

// MergeRow is where --json's merge_row (docs/SPEC-SPRINT-DASHBOARD.md, "Merge row"):
// cards in merging and in review, landings in the last 30 minutes, the oldest
// merging card's age in minutes, the base gate and the failing test, the drift
// between the base and the development branch, and the minutes since the last
// sync and the last promotion. A nil minute is not known. An empty gate or
// failing test is one the record does not carry.
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

// Line is the row's one line, the words the page draws.
func (m MergeRow) Line() string {
	return fmt.Sprintf("merging %d · review %d · landed %d/30m · oldest %s · base gate %s · failing test %s · base lacks %d, dev lacks %d · sync %s · promotion %s",
		m.Merging, m.Review, m.LandedPer30m, minutesOf(m.OldestMergingMin), orDash(m.BaseGate), orDash(m.FailingTest),
		m.BaseLacks, m.DevLacks, minutesOf(m.SyncMinutes), minutesOf(m.PromotionMinutes))
}

func minutesOf(n *int) string {
	if n == nil {
		return "-"
	}
	return fmt.Sprintf("%dm", *n)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
