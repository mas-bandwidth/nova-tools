package sprint

import (
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// A read needs a reader, not a route (docs/SPEC-SPRINT.md section 6; the card
// pr-wire-server-restart, 2026-10-05, could not be read: the fleet's table held flash
// and pro routes only, and the friends and buds that read heavy were up). The owner
// chose this over a heavy fleet route, which would let the fleet be dealt heavy cards
// it cannot run. A fleet reader runs the route its read draws, so it serves a tier
// only while an enabled route of the tier is in the tier's array (or the store holds
// no route, where every reader runs its own). A friend's or a bud's reader,
// reader-<name> beaten by her daemon, brings its own model: it serves every tier its
// tiers cell names, route or none, and its verdict's usage line says the model and
// the harness its read ran on (readUsageFields).

// ownModelReader says the reader brings its own model: it is a friend's reader,
// reader-<name> for a friend (or a bud, a friend on a Claude account) whose seat the
// snapshot holds or whose friend row the fleet table has (internal/friend ReaderOf).
// Any other reader is the fleet's and runs its read's route.
func (s *Snapshot) ownModelReader(reader string) bool {
	name, ok := strings.CutPrefix(reader, ReaderPrefix)
	if !ok || name == "" {
		return false
	}
	if slices.ContainsFunc(s.Friends, func(f FriendSeat) bool { return f.Name == name }) {
		return true
	}
	return s.Fleet != nil && s.Fleet.HasRow(FriendRow(name))
}

// tierRouted says a fleet reader can draw a read of tier: the store holds no route
// (its reader runs its own), or an enabled route of the tier is in its array.
func (s *Snapshot) tierRouted(tier string) bool {
	if len(s.Routes) == 0 {
		return true
	}
	for _, name := range s.tierArray(tier) {
		for _, r := range s.Routes {
			if r.Name == name && r.Tier == tier && r.Enabled {
				return true
			}
		}
	}
	return false
}

// readerServesTier says the reader may be asked a read of tier: its tiers cell names
// the tier (readerReadsTier), and it brings its own model or a route of the tier is
// there to draw (tierRouted).
func (s *Snapshot) readerServesTier(reader, tier string) bool {
	return s.readerReadsTier(reader, tier) && (s.ownModelReader(reader) || s.tierRouted(tier))
}

// ownModelReaderUp says a reader up that brings its own model serves tier: the reads of
// the tier need no route.
func (s *Snapshot) ownModelReaderUp(tier string) bool {
	if s.Readers == nil {
		return false
	}
	for _, rd := range s.Readers.Rows() {
		if s.ReaderIsUp(rd) && s.ownModelReader(rd) && s.readerReadsTier(rd, tier) {
			return true
		}
	}
	return false
}

// friendReaderSeat is the seat of the friend whose reader this is (reader-<name>), when
// the snapshot seats her and her friend row names a class (friendTiers). Her class is
// read from that row (nova-config friend), never from her readers-table cell and never
// from a route (readerReadsTier).
func (s *Snapshot) friendReaderSeat(reader string) (FriendSeat, bool) {
	name, ok := strings.CutPrefix(reader, ReaderPrefix)
	if s == nil || !ok || name == "" {
		return FriendSeat{}, false
	}
	i := slices.IndexFunc(s.Friends, func(f FriendSeat) bool { return f.Name == name })
	if i < 0 || len(friendTiers(s.Friends[i])) == 0 {
		return FriendSeat{}, false
	}
	return s.Friends[i], true
}

// The order a read is asked in (docs/SPEC-SPRINT.md section 6, the order of a read):
// the friend readers of the class first, those on another login than the attempt's
// worker before the worker's own (two readers, two minds), then the readers that draw
// a route of the tier. A friend reader's read draws no route (readFieldsOf).
const (
	rankFriendOther = iota // a friend reader of the class on another login than the worker
	rankFriendSame         // the worker's own friend reader
	rankRoute              // a fleet reader: it draws a route of the tier
)

// readerRank is where the reader stands in the order a read of pr is asked in.
func (s *Snapshot) readerRank(reader string, pr *Card) int {
	if !s.ownModelReader(reader) || !s.readerReadsTier(reader, s.readTierOf(pr)) {
		return rankRoute
	}
	if w := attemptWorker(s, pr.ID, pr.Int("attempt")); w != "" && reader == ReaderPrefix+w {
		return rankFriendSame
	}
	return rankFriendOther
}

// readFieldsOf is the route fields of a read of pr asked of the reader: the tier alone
// when the reader brings its own model, for no route is looked for, else the route
// drawn as a work card's is (readRouteOf).
func (s *Snapshot) readFieldsOf(ri routeIndexes, reader string, pr *Card, avoid []string) map[string]string {
	if s.ownModelReader(reader) {
		return map[string]string{FieldTier: s.readTierOf(pr)}
	}
	return s.readRouteOf(ri, pr, avoid)
}

// readUsageFields is what a verdict on the read card c records of the run beside its
// usage when the read drew no route: the model (provider/model) and the harness its
// reader's usage line names (model=, harness=), each only where the card names none.
// A routed read keeps its route's.
func readUsageFields(c *Card, usage string) map[string]string {
	out := map[string]string{}
	if c.F(FieldRoute) != "" || strings.TrimSpace(usage) == "" {
		return out
	}
	u := cardcost.ParseUsage(usage)
	if m := u.Model; m != "" && m != "-" && c.F(FieldModel) == "" {
		out[FieldModel] = m
	}
	for _, w := range strings.Fields(usage) {
		if h, ok := strings.CutPrefix(w, "harness="); ok && h != "" && h != "-" && c.F(FieldHarness) == "" {
			out[FieldHarness] = h
		}
	}
	return out
}
