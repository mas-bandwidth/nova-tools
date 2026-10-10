package sprint

import (
	"fmt"
	"maps"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// FieldHarnesses projects the inventory's machine capability onto its control card
// (docs/SPEC-SPRINT.md, the fleet from the inventory).
const FieldHarnesses = "harnesses"

// memberCanLaunch is the route guard of RouteIndex.tla: the machine lists the
// route's harness, opencode only for an absent list.
func (s *Snapshot) memberCanLaunch(member string, r Route) bool {
	if s.Fleet == nil {
		return swarm.CanLaunch(nil, r.Harness)
	}
	return swarm.CanLaunch(config.HarnessesOf(s.MemberCtl(member).F(FieldHarnesses)), r.Harness)
}

// membersForRoute keeps only members that can draw the primary's tier at this
// deal, including the next tier at escalation (RouteIndex.tla, Deal and Redeal).
func (s *Snapshot) membersForRoute(c *Card, members []string) []string {
	var out []string
	for _, m := range members {
		_, _, why, _ := s.routeOf(c, nil, nil, m)
		if why == "" {
			out = append(out, m)
		}
	}
	return out
}

// readerCanLaunch gates the actual ask; a friend brings her own model. A fleet
// reader is reader-<machine> (SPEC-SWARM).
func (s *Snapshot) readerCanLaunch(reader, tier string) bool {
	if s.ownModelReader(reader) || len(s.Routes) == 0 {
		return true
	}
	for _, name := range s.tierArray(tier) {
		for _, r := range s.Routes {
			if r.Name == name && r.Enabled && r.Tier == tier && s.readerCanLaunchRoute(reader, r) {
				return true
			}
		}
	}
	return false
}

// HarnessDrift names capability projections that differ, without creating a
// second inventory (docs/SPEC-CONFIG.md, machine).
func HarnessDrift(s *Snapshot, want map[string]string) []string {
	var out []string
	for _, m := range s.Fleet.Rows() {
		if h, ok := want[m]; ok && s.MemberCtl(m) != nil && strings.Join(config.HarnessesOf(s.MemberCtl(m).F(FieldHarnesses)), ",") != h {
			out = append(out, fmt.Sprintf("%s harnesses=%s (was %s)", m, h, strings.Join(config.HarnessesOf(s.MemberCtl(m).F(FieldHarnesses)), ",")))
		}
	}
	return out
}

// syncHarnesses adds the projection to the same sync unit that adds or updates
// the machine, preserving the one atomic write (SPEC-SPRINT, fleet sync).
func syncHarnesses(s *Snapshot, p Plan, want map[string]string) Plan {
	if len(p.Refused) > 0 {
		return p
	}
	for _, m := range s.Fleet.Rows() {
		h, ok := want[m]
		if !ok {
			continue
		}
		ctl := s.MemberCtl(m)
		if ctl == nil || strings.Join(config.HarnessesOf(ctl.F(FieldHarnesses)), ",") == h {
			continue
		}
		found := false
		for i := range p.Units {
			for j := range p.Units[i].Changes {
				c := &p.Units[i].Changes[j]
				if c.Table == Fleet && c.Entry.ID == CtlID(m) {
					if c.Entry.Set == nil {
						c.Entry.Set = map[string]string{}
					}
					c.Entry.Set[FieldHarnesses] = h
					found = true
				}
			}
		}
		if !found {
			p.Units = append(p.Units, Unit{Key: CtlID(m), Changes: []Change{change(Fleet, setEntry(ctl, map[string]string{FieldHarnesses: h}))}, Moved: m + " harnesses=" + h})
		}
	}
	for i := range p.Units {
		for j := range p.Units[i].Changes {
			c := &p.Units[i].Changes[j]
			if c.Table == Fleet && c.Entry.Create != nil && c.Entry.ID == CtlID(c.Entry.Create.Row) {
				if h, ok := want[c.Entry.Create.Row]; ok {
					c.Entry.Set[FieldHarnesses] = h
				}
			}
		}
	}
	return p
}

// readerCanLaunchRoute uses the same machine projection for the route draw.
func (s *Snapshot) readerCanLaunchRoute(reader string, r Route) bool {
	member, _ := ReaderMachine(reader)
	return s.memberCanLaunch(member, r)
}

// upHarnesses is every member up and the harness list its machine row carries
// (config.HarnessesOf; opencode alone for an absent list): what the route judgment reads.
func (s *Snapshot) upHarnesses() map[string][]string {
	out := map[string][]string{}
	for _, m := range s.UpMembers() {
		out[m] = config.HarnessesOf(s.MemberCtl(m).F(FieldHarnesses))
	}
	return out
}

// routeUnserved is the enabled routes no member up can launch, in store order
// (swarm.Unserved): each is one persistent judgment naming the route and its machines,
// never one failure per card (docs/SPEC-SWARM, "A member draws only routes whose harness
// it can launch"). A store with no route has every member run its own model, so none is
// unserved.
func (s *Snapshot) routeUnserved() []swarm.UnservedRoute {
	if len(s.Routes) == 0 {
		return nil
	}
	routes := make([]swarm.LaunchRoute, 0, len(s.Routes))
	for _, r := range s.Routes {
		if r.Enabled {
			routes = append(routes, swarm.LaunchRoute{Name: r.Name, Harness: r.Harness})
		}
	}
	return swarm.Unserved(routes, s.upHarnesses())
}

// membersForExisting keeps a queued card on machines that can launch its route
// during down and level moves (SPEC-SPRINT, machine harnesses).
func (s *Snapshot) membersForExisting(c *Card, members []string) []string {
	var out []string
	for _, m := range members {
		if s.memberCanLaunch(m, Route{Harness: c.F(FieldHarness)}) {
			out = append(out, m)
		}
	}
	return out
}

// fleetSyncRoutes plans relocations with the inventory's incoming capabilities,
// while the write guards and capability diff still use the observed pre-state.
func fleetSyncRoutes(s *Snapshot, r FleetReq, rr *round, moves roundMoves) Plan {
	cp := *s
	fleet := *s.Fleet
	fleet.cards = maps.Clone(s.Fleet.cards)
	cp.Fleet = &fleet
	for m, h := range r.Harnesses {
		if c := s.MemberCtl(m); c != nil {
			fleet.cards[c.ID] = withField(c, FieldHarnesses, h)
		}
	}
	return syncHarnesses(s, fleetSyncPlan(&cp, r, rr, moves), r.Harnesses)
}
