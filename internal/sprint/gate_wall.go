package sprint

import (
	"fmt"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
)

// THE GATE'S WALL (docs/SPEC-SPRINT.md section 5, the gate's wall). A card whose gate cannot
// run within the flash bound on a flash member is not dealt flash: flash first spends an
// attempt that cannot finish (a 40-minute deadline missed on flash before pro did the card
// twice). At add, the gate's measured wall decides it: the median wall of the ok work takes
// the sprint record holds (each primary's cost records, cost.go) for cards whose TEST line
// names the same package. A card measured over the bound is admitted on pro (FieldTierNow),
// which its first deal draws from; one never measured, or at or under the bound, is flash
// first as before.

// FlashGateBound is the longest measured gate wall a card is dealt flash with.
const FlashGateBound = 15 * time.Minute

// FieldGateWall is the measurement a card admitted pro by its gate's wall carries:
// `<median> n=<takes> over <bound>`.
const FieldGateWall = "gate_wall"

// gateWalls is the sprint record's ok work-take walls by the TEST package of their card,
// read once per add.
func gateWalls(s *Snapshot) map[string][]float64 {
	out := map[string][]float64{}
	for _, c := range s.Work.Cards() {
		pkg := gatePackage(c.F("brief"))
		if pkg == "" {
			continue
		}
		for _, k := range CardCostOf(c).Consumers {
			if wall, ok := wallSeconds(k.Usage.Wall); ok && k.Kind == "work" && k.End == "ok" {
				out[pkg] = append(out[pkg], wall)
			}
		}
	}
	return out
}

// gatePackage is the package a brief's TEST line names, "" for none (TEST: none, no TEST
// line, or one cardhdr.ParseTest refuses).
func gatePackage(brief string) string {
	for line := range strings.SplitSeq(brief, "\n") {
		if k, v, ok := cardhdr.KeyValue(line); ok && k == "TEST" {
			tl, why := cardhdr.ParseTest(v)
			if why != "" || tl.None {
				return ""
			}
			return tl.Package
		}
	}
	return ""
}

// gateTier is what add writes on a card with brief by its gate's measured wall (walls,
// gateWalls): FieldTierNow pro and FieldGateWall when the median is over FlashGateBound, with
// the sentence its unit says; nothing for a card whose tier is pinned (pinnedTier: a
// frontier card, a model pin), one never measured, or one at or under the bound.
func gateTier(walls map[string][]float64, brief string) (set map[string]string, said string) {
	m, bad := cardhdr.ReadModel(brief)
	pkg := gatePackage(brief)
	if bad != "" || pkg == "" || pinnedTier(&Card{}, m) || len(walls[pkg]) == 0 {
		return nil, ""
	}
	got := measure(walls[pkg])
	median := (time.Duration(got.Median) * time.Second).Round(time.Second)
	if median <= FlashGateBound {
		return nil, ""
	}
	set = map[string]string{FieldTierNow: cardhdr.RoutePro, FieldGateWall: fmt.Sprintf("%s n=%d over %s", median, got.N, FlashGateBound)}
	return set, fmt.Sprintf("admitted pro: gate %s measured %s (median of %d ok takes) over the flash bound %s", pkg, median, got.N, FlashGateBound)
}
