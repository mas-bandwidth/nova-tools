package sprint

import (
	"fmt"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
)

// ReaderTiers is the readers table's text column: the tiers that reader reads.
// An empty cell is the default: a fleet reader reads flash alone while the store holds
// routes (fleetReadsFlashOnly; the owner, 2026-10-06, "I'm ok with flash readers on fleet
// but not pro"), and a friend's or a bud's reader, which brings its own model, every tier.
// The ask counts a reader only for a primary whose read tier (readTierOf) it serves.
const ReaderTiers = "tiers"

// ReaderTiersAll is the --tiers word for every tier, stored as the list of them.
const ReaderTiersAll = "all"

// ReaderTiersDefault is the word for an empty cell, as --tiers takes it and a view prints it.
const ReaderTiersDefault = "default"

// readerTierOrder is the order a tiers cell is stored in: the brief's
// flash[,pro,...], which is the ladder and then frontier.
var readerTierOrder = []string{cardhdr.RouteFlash, cardhdr.RoutePro, cardhdr.RouteHeavy, cardhdr.RouteFrontier}

// ReaderTiersShown is the word a view prints for a stored tiers cell: default for an
// empty one (flash on a fleet reader, every tier on a friend's), the list else.
func ReaderTiersShown(stored string) string {
	if strings.TrimSpace(stored) == "" {
		return ReaderTiersDefault
	}
	return stored
}

// ParseReaderTiers reads --tiers. Empty and default store "" (the default: flash on a
// fleet reader, every tier on a friend's); all stores every tier, named. Anything else is a
// comma list of routes, each once, stored in readerTierOrder with no spaces. A tier that is
// not a route, or a repeat, refuses the whole value.
func ParseReaderTiers(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == ReaderTiersDefault {
		return "", nil
	}
	if raw == ReaderTiersAll {
		return strings.Join(readerTierOrder, ","), nil
	}
	seen := map[string]bool{}
	var bad []string
	for _, p := range Split(raw) {
		if !cardhdr.IsRoute(p) || seen[p] {
			bad = append(bad, p)
			continue
		}
		seen[p] = true
	}
	if len(bad) > 0 {
		return "", fmt.Errorf("--tiers wants %s, comma separated, each once (or all for every tier, or default: flash on a fleet reader, every tier on a friend's); refused %s", cardhdr.RouteList, strings.Join(bad, ","))
	}
	var kept []string
	for _, t := range readerTierOrder {
		if seen[t] {
			kept = append(kept, t)
		}
	}
	return strings.Join(kept, ","), nil
}

// readerTiersStored is the reader's tiers cell, "" when the row names none
// (every tier) or the table carries no texts.
func (s *Snapshot) readerTiersStored(reader string) string {
	if s == nil || s.Readers == nil || s.Readers.Texts == nil {
		return ""
	}
	row := s.Readers.Texts[reader]
	if row == nil {
		return ""
	}
	return row[ReaderTiers]
}

// readerReadsTier says the reader reads tier. An empty cell reads every tier.
func (s *Snapshot) readerReadsTier(reader, tier string) bool {
	stored := strings.TrimSpace(s.readerTiersStored(reader))
	if stored == "" {
		return true
	}
	return slices.Contains(Split(stored), tier)
}

// readersCarryTiers says some reader row names a tiers cell. With none set,
// and no reader states, enoughReadersUp stays the old "every reader is up".
func (s *Snapshot) readersCarryTiers() bool {
	if s == nil || s.Readers == nil {
		return false
	}
	for _, rd := range s.Readers.Rows() {
		if strings.TrimSpace(s.readerTiersStored(rd)) != "" {
			return true
		}
	}
	return false
}

// returnedInTier is the primary's returned reads whose reader reads its tier.
// A returned read on a reader outside the tier is not a slot the ask can fill
// in place (Ask takes that card back, or leaves it when the ask is refused).
func returnedInTier(s *Snapshot, pr *Card, attempt int) []*Card {
	tier := s.readTierOf(pr)
	var out []*Card
	for _, rc := range returnedReadsAt(s, pr, attempt) {
		if s.readerReadsTier(rc.F("reader"), tier) {
			out = append(out, rc)
		}
	}
	return out
}
