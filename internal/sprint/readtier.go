package sprint

import (
	"fmt"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
)

// Read tiers (docs/SPEC-SPRINT.md section 6; the owner, 2026-10-04). The floor: a card's
// reads run at the tier its attempt ran on at least (readTierOf: the attempt's route
// tier, raised to the stream's or the sprint's read tier when that is stronger, never
// lowered), and a stream's read tier is never set below its work tier (StreamWorkTier,
// the strongest tier its cards' briefs name): `stream set --read-tier` raises it and
// refuses to lower it with one line. The escalation: when a stream's landed card is
// returned by dev or an audit (`promoted --returned`), when two readers at the stream's
// read tier disagree on one attempt, or when a card alternates broken and ok across
// attempts, the tick raises one judgment per stream, "raise the read tier of <stream> to
// <next>?", with the decisions raise (default) and keep; the raise is `stream set
// --read-tier <next> --reason <why>`, recorded on the stream row, and it never lowers.

// NRaiseReadTier is the tick's judgment that a stream's read tier should rise, one per
// stream.
const NRaiseReadTier = "raise the read tier of the stream?"

// RaiseReadTierDecisions are the judgment's decisions: raise, the default, and keep.
var RaiseReadTierDecisions = []string{"raise", "keep"}

// FieldReadTierReason is a stream's control card's field: why its read tier was last set
// (`stream set --read-tier <t> --reason <why>`), beside FieldReadTier.
const FieldReadTierReason = "read_tier_reason"

// A landed primary's record of being returned by dev or an audit (Promoted, `promoted
// --returned <ids>`): when, the tier its reads ran on then, and the promotion's sha.
const (
	FieldReturnedByDev     = "returned_by_dev"
	FieldReturnedByDevTier = "returned_by_dev_tier"
	FieldReturnedByDevSha  = "returned_by_dev_sha"
)

// NextReadTier is the read tier above t (readTiers), "" at the top.
func NextReadTier(t string) string {
	i := slices.Index(readTiers, t)
	if i < 0 || i+1 >= len(readTiers) {
		return ""
	}
	return readTiers[i+1]
}

// StreamWorkTier is the stream's work tier: the strongest tier its open cards' briefs
// name (TierWord; a frontier card works on pro's routes for reads), flash with none.
func StreamWorkTier(s *Snapshot, stream string) string {
	work := cardhdr.RouteFlash
	for _, col := range []State{Waiting, Ready, Working, Review, Merging} {
		for _, c := range s.Work.Cell(stream, col) {
			if IsSentinel(c) {
				continue
			}
			t := TierWord(c)
			if t == cardhdr.RouteFrontier {
				t = cardhdr.RoutePro
			}
			work = stronger(work, t)
		}
	}
	return work
}

// ReadTierRaise is why a stream's read tier should rise: the stream, the tier to raise it
// to and the cause.
type ReadTierRaise struct {
	Stream string
	Next   string
	Why    string
}

// What is the judgment's line.
func (r ReadTierRaise) What() string {
	return fmt.Sprintf("raise the read tier of %s to %s? %s", r.Stream, r.Next, r.Why)
}

// RaiseReadTier is whether the stream's read tier should rise, and why: the first cause
// found among its cards, in work order. A cause holds while the stream's read tier for
// the card is the tier it was found at: a raise ends it, and a keep (ack) holds it quiet.
func RaiseReadTier(s *Snapshot, stream string) (ReadTierRaise, bool) {
	raise := func(c *Card, at, why string) (ReadTierRaise, bool) {
		next := NextReadTier(at)
		if next == "" || s.readTierOf(c) != at {
			return ReadTierRaise{}, false
		}
		// a setting at or above next already asks it: under the interim rule a heavy read is
		// drawn on pro (readTierOf), so a stream raised to heavy reads at pro and pro is its top
		if set := s.readTierSetting(c.Row); set != "" && stronger(set, next) == set {
			return ReadTierRaise{}, false
		}
		return ReadTierRaise{Stream: stream, Next: next, Why: why}, true
	}
	for _, c := range s.Work.Cell(stream, Landed) {
		if c.F(FieldReturnedByDev) == "" {
			continue
		}
		if r, ok := raise(c, c.F(FieldReturnedByDevTier), fmt.Sprintf("%s landed and was returned by dev or an audit at %s (its reads ran on %s)", c.ID, c.F(FieldReturnedByDev), orDash(c.F(FieldReturnedByDevTier)))); ok {
			return r, true
		}
	}
	for _, c := range s.Work.Cell(stream, Review) {
		attempt := c.Int("attempt")
		var oks, brokens []string
		for _, rc := range liveReadsAt(s, c, attempt) {
			switch rc.Col {
			case OK:
				oks = append(oks, rc.Row)
			case Broken:
				brokens = append(brokens, rc.Row)
			}
		}
		at := s.readTierOf(c)
		switch {
		case len(oks) > 0 && len(brokens) > 0:
			if r, ok := raise(c, at, fmt.Sprintf("two readers at %s disagree on %s attempt %d (%s ok, %s broken)", at, c.ID, attempt, strings.Join(oks, ","), strings.Join(brokens, ","))); ok {
				return r, true
			}
		case len(brokens) > 0 && c.F(FieldPassedHead) != "":
			if r, ok := raise(c, at, fmt.Sprintf("%s alternates broken and ok across attempts: a reader passed it at %s and attempt %d was found broken by %s", c.ID, c.F(FieldPassedHead), attempt, strings.Join(brokens, ","))); ok {
				return r, true
			}
		}
	}
	return ReadTierRaise{}, false
}

// raiseReadTierConds is the tick's condition for each stream whose read tier should rise
// (NRaiseReadTier): one judgment per stream.
func raiseReadTierConds(s *Snapshot) []cond {
	var out []cond
	for _, st := range s.Work.Rows() {
		if r, ok := RaiseReadTier(s, st); ok {
			out = append(out, cond{typ: NRaiseReadTier, stream: st, streamLevel: true, what: r.What(), decisions: RaiseReadTierDecisions, tier: r.Next})
		}
	}
	return out
}
