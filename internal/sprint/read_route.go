package sprint

import (
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
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
// there for it to draw (readerCanLaunch: the member's machine lists the route's harness,
// docs/SPEC-SWARM "A member draws only routes whose harness it can launch"). A fleet
// reader whose row names no tier reads flash alone (fleetReadsFlashOnly): the owner,
// 2026-10-06, "I'm ok with flash readers on fleet but not pro"; its row names pro or
// above (reader set --tiers) to read them. A store with no route (the no-route twin)
// has every reader run its own model, so every tier it names is served.
func (s *Snapshot) readerServesTier(reader, tier string) bool {
	if tier != cardhdr.RouteFlash && s.fleetReadsFlashOnly(reader) {
		return false
	}
	return s.readerReadsTier(reader, tier) && s.readerCanLaunch(reader, tier)
}

// fleetReadsFlashOnly says the reader is the fleet's (it brings no model of its own and
// draws its read's route), its row names no tier, and the store holds routes: it reads
// flash and no other tier. A store with no route at all has every reader run its own model
// (tierRouted), and an empty row there reads every tier, as a friend's reader does.
func (s *Snapshot) fleetReadsFlashOnly(reader string) bool {
	return len(s.Routes) > 0 && !s.ownModelReader(reader) && strings.TrimSpace(s.readerTiersStored(reader)) == ""
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
