package sprint

import (
	"fmt"
	"sort"
)

// fleet sync makes the fleet table match the inventory (nova-config): every
// machine the inventory names a member is a row at its width, and every row
// the inventory no longer names is held. The inventory is the one place a
// machine's existence and capacity are written, so the fleet table is never
// typed by hand (docs/SPEC-SPRINT.md, section 5, "The fleet from the
// inventory").
//
// There is no new state. A sync only makes the moves fleet up and fleet down
// already make, for many members in one plan: a missing member is added down
// until it beats (presence brings it up, as for fleet up), a member whose
// width differs has its width set, a member the inventory dropped is held and
// its unfinished work cards are dealt to the members that stay up
// (tla/SprintEvents.tla, PlanDown: R2). The status of a member that stays is
// never written, and the deal's rolling index moves only with the cards a held
// member's redeal places (round.go): presence and the index are the tick's.
// A member the coordinator holds stays held: sync never releases, and sets
// its width as it does for any member.

// SyncMember is a machine the inventory names a member, at its width.
type SyncMember struct {
	Name  string
	Width int
}

// The kinds of drift between the fleet table and the inventory.
const (
	DriftAdd   = "add"   // the inventory names a member the table has no control card for
	DriftWidth = "width" // a member's width differs
	DriftHold  = "hold"  // the table has a member the inventory no longer names
)

// Drift is one difference between the fleet table and the inventory: what
// a sync would write.
type Drift struct {
	Member string
	Kind   string
	// From and To are a width change's widths; To is the width of an add.
	From, To int
}

// Line is the drift as a sentence.
func (d Drift) Line() string {
	switch d.Kind {
	case DriftAdd:
		return fmt.Sprintf("%s is a member of the inventory and not of the fleet: add it at width %d", d.Member, d.To)
	case DriftWidth:
		return fmt.Sprintf("%s has width %d and the inventory says %d", d.Member, d.From, d.To)
	}
	return fmt.Sprintf("%s is a member of the fleet and not of the inventory: hold it down", d.Member)
}

// ValidSync is why a sync of these members cannot be written: a name or a
// width the fleet refuses, or a name twice. "" is valid.
func ValidSync(want []SyncMember) string {
	seen := map[string]bool{}
	for _, m := range want {
		switch {
		case !ValidID(m.Name):
			return fmt.Sprintf("a member name wants letters, digits, _ and -: %q", m.Name)
		case m.Width < 1 || m.Width > MaxWidth:
			return fmt.Sprintf("%s: a width wants a whole number from 1 to %d, got %d", m.Name, MaxWidth, m.Width)
		case seen[m.Name]:
			return fmt.Sprintf("%s is named twice", m.Name)
		}
		seen[m.Name] = true
	}
	return ""
}

// FleetDrift is what a sync of want would write on the snapshot: the members
// to add, the widths to set and the members to hold, by name. It is empty
// exactly when the plan of a sync is empty, so a check and a sync agree, and
// a sync after a sync has nothing to write.
func FleetDrift(s *Snapshot, want []SyncMember) []Drift {
	wanted := map[string]int{}
	for _, m := range want {
		wanted[m.Name] = m.Width
	}
	var out []Drift
	for name, w := range wanted {
		ctl := s.MemberCtl(name)
		switch {
		case ctl == nil:
			out = append(out, Drift{Member: name, Kind: DriftAdd, To: w})
		case MemberWidth(ctl) != w:
			out = append(out, Drift{Member: name, Kind: DriftWidth, From: MemberWidth(ctl), To: w})
		}
	}
	for _, name := range s.Fleet.Rows() {
		if _, ok := wanted[name]; ok {
			continue
		}
		if ctl := s.MemberCtl(name); ctl != nil && (ctl.F("held") == "" || ctl.F("status") != Down) {
			out = append(out, Drift{Member: name, Kind: DriftHold})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Member < out[j].Member })
	return out
}

// HeldInInventory is the members of want the coordinator holds down: a sync
// leaves their hold, and says so.
func HeldInInventory(s *Snapshot, want []SyncMember) []string {
	var out []string
	for _, m := range want {
		if ctl := s.MemberCtl(m.Name); ctl != nil && ctl.F("held") != "" {
			out = append(out, m.Name)
		}
	}
	sort.Strings(out)
	return out
}

// fleetSyncPlan is the sync as one plan: every add and width in a unit of the
// member's control card, then each member to hold by downPlan, dealing its
// cards to the members that stay up, at their widths as the sync sets them.
func fleetSyncPlan(s *Snapshot, r FleetReq, rr *round, moves roundMoves) Plan {
	var p Plan
	if why := ValidSync(r.Sync); why != "" {
		p.refuse("sync", why)
		return p
	}
	drift := FleetDrift(s, r.Sync)
	newWidth := map[string]int{}
	for _, m := range r.Sync {
		newWidth[m.Name] = m.Width
	}
	holding := map[string]bool{}
	for _, d := range drift {
		if d.Kind == DriftHold {
			holding[d.Member] = true
		}
	}
	for _, d := range drift {
		switch d.Kind {
		case DriftAdd:
			if !s.Fleet.HasRow(d.Member) {
				p.Rows = append(p.Rows, RowAdd{Fleet, d.Member})
			}
			fields := map[string]string{"kind": "member", "status": Down, "since": stamp(s.Now), FieldWidth: itoa(d.To)}
			p.Units = append(p.Units, Unit{Key: CtlID(d.Member),
				Changes: []Change{change(Fleet, createEntry(CtlID(d.Member), d.Member, Ctl, 0, fields))},
				Moved:   fmt.Sprintf("%s added, down until it beats width=%d", d.Member, d.To)})
		case DriftWidth:
			p.Units = append(p.Units, Unit{Key: CtlID(d.Member),
				Changes: []Change{change(Fleet, setEntry(s.MemberCtl(d.Member), map[string]string{FieldWidth: itoa(d.To)}))},
				Moved:   fmt.Sprintf("%s width=%d (was %d)", d.Member, d.To, d.From)})
		}
	}
	var stay []string
	for _, m := range s.UpMembers() {
		if !holding[m] {
			stay = append(stay, m)
		}
	}
	q, widths := memberLoads(s, stay), memberWidths(s, stay)
	for m := range widths {
		if w, ok := newWidth[m]; ok {
			widths[m] = w
		}
	}
	for _, d := range drift {
		if d.Kind != DriftHold {
			continue
		}
		hp := downPlan(s, FleetReq{Op: "hold", Member: d.Member, Who: r.Who, Why: "gone from the inventory"}, stay, rr, moves, q, widths)
		p.Units = append(p.Units, hp.Units...)
		p.Refused = append(p.Refused, hp.Refused...)
		p.Notes = append(p.Notes, hp.Notes...)
	}
	return p
}
