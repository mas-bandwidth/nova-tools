package sprint

import (
	"fmt"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// A reader's tiers (docs/SPEC-SPRINT.md section 6, the readers table; the
// model is tla/ReaderTiers.tla): a reader row carries the tiers it reads,
// declared by the coordinator (reader add --tiers, reader set --tiers), every
// tier when it names none (the behaviour before rows carried tiers). The read
// floor says a read runs on a route of its primary's read tier (readTierOf),
// never below; a reader whose lane runs a flash model reads flash only (the
// owner, 2026-10-05, of a friend's flash lane: "very fast and cheap"). So
// the ask, the level and the sweep count a reader for a primary only when the
// reader reads the primary's read tier (readerReadsCard): no read is ever
// asked of a reader outside its tiers, a returned read is never asked again in
// place of one (Ask), and a card with too few readers of its tier up is the
// few-readers judgment (enoughReadersUp), never a read asked below its tier.

// ReaderTierNames is the tiers a reader may read, weakest first: the read
// tiers (readTiers; a frontier card is read on heavy, readTierOf).
var ReaderTierNames = readTiers

// ParseReaderTiers is the tiers --tiers names: a comma list of
// ReaderTierNames, each once, in ladder order; refused naming the tier it
// cannot read. An empty list is refused: a reader that reads nothing is a
// reader retired (reader retire).
func ParseReaderTiers(list string) ([]string, error) {
	var out []string
	for _, t := range strings.Split(list, ",") {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if !slices.Contains(ReaderTierNames, t) {
			return nil, fmt.Errorf("--tiers names %q, not a tier a reader reads (%s)", t, strings.Join(ReaderTierNames, ", "))
		}
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--tiers names no tier; it wants a comma list of %s (a reader that reads nothing: nova-sprint reader retire)", strings.Join(ReaderTierNames, ", "))
	}
	slices.SortFunc(out, func(a, b string) int {
		return slices.Index(ReaderTierNames, a) - slices.Index(ReaderTierNames, b)
	})
	return out, nil
}

// ReaderTiersOf is the tiers the reader reads: its row's, or every tier
// (ReaderTierNames) when it carries none.
func (s *Snapshot) ReaderTiersOf(reader string) []string {
	if t := s.ReaderTiers[reader]; len(t) > 0 {
		return t
	}
	return ReaderTierNames
}

// ReaderReads says the reader reads tier: its row carries it, or names none.
// A frontier tier is read as heavy (readTierOf's collapse).
func (s *Snapshot) ReaderReads(reader, tier string) bool {
	if tier == cardhdr.RouteFrontier {
		tier = cardhdr.RouteHeavy
	}
	return slices.Contains(s.ReaderTiersOf(reader), tier)
}

// readerReadsCard says the reader reads the primary's read tier (readTierOf):
// it may be asked the primary's read.
func (s *Snapshot) readerReadsCard(reader string, pr *Card) bool {
	return len(s.ReaderTiers[reader]) == 0 || s.ReaderReads(reader, s.readTierOf(pr))
}

// upReadersFor is the readers up that read the primary's read tier, in row order.
func (s *Snapshot) upReadersFor(pr *Card) []string {
	var out []string
	for _, rd := range s.UpReaders() {
		if s.readerReadsCard(rd, pr) {
			out = append(out, rd)
		}
	}
	return out
}

// ReaderTiersText is the tiers a reader reads as the views print them: "every
// tier" for a row that names none, else the comma list.
func (s *Snapshot) ReaderTiersText(reader string) string {
	if len(s.ReaderTiers[reader]) == 0 {
		return "every tier"
	}
	return strings.Join(s.ReaderTiers[reader], ",")
}
