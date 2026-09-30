package sprint

import (
	"fmt"
	"sort"
	"strings"
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
// A hold is marked by who made it (the control card's held_by): the sync
// marks the holds it makes, and releases them when the machine is back in the
// inventory with room, the member coming up when it beats. A hold the
// coordinator made (fleet down) carries no mark, is never released by the
// sync, and the member takes its width as any member does.

// SyncMember is a machine the inventory names a member, at its width.
type SyncMember struct {
	Name  string
	Width int
}

// FieldHeldBy is the control card's field naming who made the member's hold
// when it was not the coordinator; HeldBySync is the sync.
const (
	FieldHeldBy = "held_by"
	HeldBySync  = "sync"
)

// The kinds of drift between the fleet table and the inventory.
const (
	DriftAdd     = "add"     // the inventory names a member the table has no control card for
	DriftWidth   = "width"   // a member's width differs
	DriftHold    = "hold"    // the table has a member the inventory no longer names
	DriftRelease = "release" // a member the sync held is back in the inventory with room
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
	case DriftRelease:
		return fmt.Sprintf("%s was held by the sync and is back in the inventory: release it", d.Member)
	}
	return fmt.Sprintf("%s is a member of the fleet and not of the inventory: hold it down", d.Member)
}

// SyncProblems is each member of want a sync cannot write, with why: a name
// or a width the fleet refuses, or a name twice. nil is none.
func SyncProblems(want []SyncMember) []Refusal {
	var out []Refusal
	seen := map[string]bool{}
	for _, m := range want {
		switch {
		case !ValidID(m.Name):
			out = append(out, Refusal{m.Name, fmt.Sprintf("a member name wants letters, digits, _ and -: %q", m.Name)})
		case m.Width < 1 || m.Width > MaxWidth:
			out = append(out, Refusal{m.Name, fmt.Sprintf("%s: a width wants a whole number from 1 to %d, got %d", m.Name, MaxWidth, m.Width)})
		case seen[m.Name]:
			out = append(out, Refusal{m.Name, m.Name + " is named twice"})
		}
		seen[m.Name] = true
	}
	return out
}

// ValidSync is why a sync of these members cannot be written: the first of
// SyncProblems. "" is valid.
func ValidSync(want []SyncMember) string {
	if ps := SyncProblems(want); len(ps) > 0 {
		return ps[0].Why
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
		if ctl == nil {
			out = append(out, Drift{Member: name, Kind: DriftAdd, To: w})
			continue
		}
		if ctl.F("held") != "" && ctl.F(FieldHeldBy) == HeldBySync {
			out = append(out, Drift{Member: name, Kind: DriftRelease})
		}
		if MemberWidth(ctl) != w {
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
	sort.SliceStable(out, func(i, j int) bool { return out[i].Member < out[j].Member })
	return out
}

// HeldInInventory is the members of want the coordinator holds down (a hold
// the sync made is not one): a sync leaves their hold, and says so.
func HeldInInventory(s *Snapshot, want []SyncMember) []string {
	var out []string
	for _, m := range want {
		if ctl := s.MemberCtl(m.Name); ctl != nil && ctl.F("held") != "" && ctl.F(FieldHeldBy) != HeldBySync {
			out = append(out, m.Name)
		}
	}
	sort.Strings(out)
	return out
}

// fleetSyncPlan is the sync as one plan: every add, release and width in a
// unit of the member's control card, then each member to hold by downPlan,
// dealing its cards to the members that stay up, at their widths as the sync
// sets them. A member the sync cannot write (a width the fleet refuses) is
// refused by name and the others are planned; the step is all or none
// (store.FleetStep, Named), so one refusal writes nothing.
func fleetSyncPlan(s *Snapshot, r FleetReq, rr *round, moves roundMoves) Plan {
	var p Plan
	refused := map[string]bool{}
	for _, pr := range SyncProblems(r.Sync) {
		p.refuse(pr.Key, pr.Why)
		refused[pr.Key] = true
	}
	drift := FleetDrift(s, r.Sync)
	newWidth := map[string]int{}
	for _, m := range r.Sync {
		newWidth[m.Name] = m.Width
	}
	holding := map[string]bool{}
	byMember := map[string][]Drift{}
	var order []string
	for _, d := range drift {
		if refused[d.Member] {
			continue
		}
		if d.Kind == DriftHold {
			holding[d.Member] = true
			continue
		}
		if len(byMember[d.Member]) == 0 {
			order = append(order, d.Member)
		}
		byMember[d.Member] = append(byMember[d.Member], d)
	}
	for _, m := range order {
		ds := byMember[m]
		if ds[0].Kind == DriftAdd {
			d := ds[0]
			if !s.Fleet.HasRow(m) {
				p.Rows = append(p.Rows, RowAdd{Fleet, m})
			}
			// down until it beats: presence brings it up, as for fleet up
			fields := map[string]string{"kind": "member", "status": Down, "since": stamp(s.Now), FieldWidth: itoa(d.To)}
			p.Units = append(p.Units, Unit{Key: CtlID(m),
				Changes: []Change{change(Fleet, createEntry(CtlID(m), m, Ctl, 0, fields))},
				Moved:   fmt.Sprintf("%s added, down until it beats width=%d", m, d.To)})
			continue
		}
		set := map[string]string{}
		var unset []string
		var said []string
		for _, d := range ds {
			switch d.Kind {
			case DriftRelease:
				unset = append(unset, "held", FieldHeldBy)
				said = append(said, "released, down until it beats")
			case DriftWidth:
				set[FieldWidth] = itoa(d.To)
				said = append(said, fmt.Sprintf("width=%d (was %d)", d.To, d.From))
			}
		}
		p.Units = append(p.Units, Unit{Key: CtlID(m),
			Changes: []Change{change(Fleet, setEntry(s.MemberCtl(m), set, unset...))},
			Moved:   m + " " + strings.Join(said, ", ")})
	}
	var stay []string
	for _, m := range s.UpMembers() {
		if !holding[m] {
			stay = append(stay, m)
		}
	}
	q, widths := memberLoads(s, stay), memberWidths(s, stay)
	for m := range widths {
		if w, ok := newWidth[m]; ok && !refused[m] {
			widths[m] = w
		}
	}
	for _, d := range drift {
		if d.Kind != DriftHold || refused[d.Member] {
			continue
		}
		hp := downPlan(s, FleetReq{Op: "hold", Member: d.Member, Who: r.Who, Why: "gone from the inventory", HeldBy: HeldBySync}, stay, rr, moves, q, widths)
		p.Units = append(p.Units, hp.Units...)
		p.Refused = append(p.Refused, hp.Refused...)
		p.Notes = append(p.Notes, hp.Notes...)
	}
	return p
}
