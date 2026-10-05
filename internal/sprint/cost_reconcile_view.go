package sprint

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// THE LATEST RECONCILE, SHOWN (docs/SPEC-SPRINT.md, "What a card cost", the reconciliation;
// release-check-spend-reconciledb.w1): `nova-sprint where` and `view coordinator` carry each
// provider's last reconciliation as the fleet table keeps it (CostReconcileRecord), so a gap
// the release's spend gate would refuse on is seen before a release is tried.

// ReconcileRow is one provider's latest reconciliation as the views carry it.
type ReconcileRow struct {
	Provider string  `json:"provider"`
	At       string  `json:"at"`
	Known    bool    `json:"known"`
	Note     string  `json:"note,omitempty"` // why the provider's count could not be read
	Day      string  `json:"day,omitempty"`
	Used     float64 `json:"provider_usd"` // the provider's count of Day
	Internal float64 `json:"records_usd"`  // the sprint's records of Day
	Gap      float64 `json:"gap_usd"`      // Used less Internal
	Share    float64 `json:"share"`        // |Gap| over Used
	// Over says the gap passes the bound the judgment and the release's gate refuse at
	// (CostGapOver, at least CostGapFloor dollars).
	Over bool `json:"over"`
}

// LatestReconciles is every provider's latest reconciliation off the fleet table's
// properties, by provider; none when nothing was reconciled.
func LatestReconciles(fleet *Table) []ReconcileRow {
	if fleet == nil {
		return nil
	}
	var out []ReconcileRow
	for name := range fleet.Props() {
		provider, ok := strings.CutPrefix(name, PropCostReconcilePrefix)
		if !ok {
			continue
		}
		r, ok := CostReconcileOf(fleet, provider)
		if !ok {
			continue
		}
		out = append(out, ReconcileRow{Provider: provider, At: r.At, Known: r.Known, Note: r.Note, Day: r.Day, Used: r.Used, Internal: r.Internal,
			Gap: r.Gap, Share: r.Share, Over: r.Day != "" && r.Share > CostGapOver && math.Abs(r.Gap) >= CostGapFloor})
	}
	slices.SortFunc(out, func(a, b ReconcileRow) int { return strings.Compare(a.Provider, b.Provider) })
	return out
}

// ReconcileLine is the latest reconciliations as one line of a text view; "" with none.
func ReconcileLine(rows []ReconcileRow) string {
	if len(rows) == 0 {
		return ""
	}
	var parts []string
	for _, r := range rows {
		p := oneline.Field(r.Provider)
		switch {
		case r.Day == "" && !r.Known:
			parts = append(parts, p+" unknown: "+oneline.Escape(r.Note))
			continue
		case r.Day == "":
			continue
		}
		s := fmt.Sprintf("%s day=%s provider=%s records=%s gap=%s (%.1f%%)", p, r.Day, Dollars(r.Used), Dollars(r.Internal), Dollars(math.Abs(r.Gap)), r.Share*100)
		if r.Over {
			s += " OVER"
		}
		if !r.Known {
			s += " (last read unknown: " + oneline.Escape(r.Note) + ")"
		}
		parts = append(parts, s)
	}
	return "COST RECONCILE " + strings.Join(parts, "; ")
}
