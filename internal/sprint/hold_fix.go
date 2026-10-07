package sprint

import (
	"fmt"
	"slices"
	"strings"
)

// HoldFix is a hold note's fix line (the owner, 2026-10-07): when a hold note
// carries a fix line in a small fixed grammar, the machine applies it at finish.
// The four fix lines are:
//
//	PATHS-PROPOSED: a,b,...  widens PATHS in place (within the stream's land-protected set)
//	NEEDS: card-id           adds a dependency and parks the card waiting
//	TIER: flash|pro|heavy    recuts the tier in place
//	GATE-HOST: linux         marks the card's gates to run on a bench
//
// A fix line the machine cannot apply leaves the hold as today, with the reason
// on the judgment.
type HoldFix struct {
	// PATHSProposed is the PATHS-PROPOSED fix line.
	PATHSProposed []string
	// NEEDS is the NEEDS: card-id fix line.
	NEEDS string
	// TIER is the TIER: flash|pro|heavy fix line.
	TIER string
	// GATEHOST is the GATE-HOST: linux fix line.
	GATEHOST string
}

// HoldFixKind is the kind of fix line found in a note.
type HoldFixKind string

const (
	HoldFixPATHSProposed HoldFixKind = "PATHS-PROPOSED"
	HoldFixNEEDS         HoldFixKind = "NEEDS"
	HoldFixTIER          HoldFixKind = "TIER"
	HoldFixGATEHOST      HoldFixKind = "GATE-HOST"
)

// ParseHoldFix parses fix lines from a hold note. Each fix line is one
// `KEY: value` at the start of a line. Unknown keys are ignored.
func ParseHoldFix(note string) HoldFix {
	var fix HoldFix
	for _, line := range strings.Split(note, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		idx := strings.Index(line, ":")
		if idx == -1 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		switch key {
		case "PATHS-PROPOSED":
			fix.PATHSProposed = splitPaths(val)
		case "NEEDS":
			fix.NEEDS = val
		case "TIER":
			fix.TIER = strings.ToLower(val)
		case "GATE-HOST":
			fix.GATEHOST = strings.ToLower(val)
		}
	}
	return fix
}

// splitPaths splits a comma-separated list of paths.
func splitPaths(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ApplyHoldFix applies the fix lines to a card and its attempt. It returns
// a plan with the changes and any refusals for fix lines that cannot be applied.
func ApplyHoldFix(s *Snapshot, c *Card, a *Attempt, fix HoldFix) (Plan, []Refusal) {
	var p Plan
	var refused []Refusal

	// PATHS-PROPOSED: widen PATHS in place (within the stream's land-protected set)
	if len(fix.PATHSProposed) > 0 {
		ctl := s.StreamCtl(c.F("stream"))
		if ctl == nil {
			refused = append(refused, Refusal{"PATHS-PROPOSED", "stream " + c.F("stream") + " not found"})
		} else {
			protected := s.ProtectedPaths(c.F("stream"))
			var added []string
			for _, path := range fix.PATHSProposed {
				if slices.Contains(protected, path) {
					added = append(added, path)
				}
			}
			if len(added) > 0 {
				old := strings.Split(ctl.F(FieldPATHS), ",")
				seen := map[string]bool{}
				for _, p := range old {
					seen[strings.TrimSpace(p)] = true
				}
				var merged []string
				for _, p := range old {
					trimmed := strings.TrimSpace(p)
					if trimmed != "" {
						merged = append(merged, trimmed)
					}
				}
				for _, path := range added {
					if !seen[path] {
						merged = append(merged, path)
						seen[path] = true
					}
				}
				p.Units = append(p.Units, Unit{
					Key:    ctl.ID,
					Stream: c.F("stream"),
					Changes: []Change{change(Merge, setEntry(ctl, map[string]string{FieldPATHS: strings.Join(merged, ",")}, nil))},
					Moved:  fmt.Sprintf("stream %s PATHS widened to %s", c.F("stream"), strings.Join(merged, ", ")),
				})
			}
			if len(added) < len(fix.PATHSProposed) {
				var outside []string
				for _, path := range fix.PATHSProposed {
					if !slices.Contains(added, path) {
						outside = append(outside, path)
					}
				}
				refused = append(refused, Refusal{"PATHS-PROPOSED", fmt.Sprintf("paths outside land-protected set: %s", strings.Join(outside, ", "))})
			}
		}
	}

	// NEEDS: add a dependency and park the card waiting
	if fix.NEEDS != "" {
		depPrim := s.Work.Card(fix.NEEDS)
		if depPrim == nil {
			refused = append(refused, Refusal{"NEEDS", fmt.Sprintf("card %s not found", fix.NEEDS)})
		} else if a != nil && a.Dependencies != "" {
			deps := strings.Split(a.Dependencies, ",")
			seen := map[string]bool{}
			for _, d := range deps {
				seen[strings.TrimSpace(d)] = true
			}
			if !seen[fix.NEEDS] {
				newDeps := append([]string{a.Dependencies}, fix.NEEDS)
				p.Units = append(p.Units, Unit{
					Key:    a.ID,
					Stream: c.F("stream"),
					Changes: []Change{change(Merge, setEntry(a, map[string]string{FieldDependencies: strings.Join(newDeps, ",")}, nil))},
					Moved:  fmt.Sprintf("%s added dependency %s", a.ID, fix.NEEDS),
				})
			}
		} else if a == nil {
			refused = append(refused, Refusal{"NEEDS", "no attempt to add dependency to"})
		}
	}

	// TIER: recut the tier in place
	if fix.TIER != "" {
		if fix.TIER != "flash" && fix.TIER != "pro" && fix.TIER != "heavy" {
			refused = append(refused, Refusal{"TIER", fmt.Sprintf("unknown tier %s; must be flash, pro, or heavy", fix.TIER)})
		} else if a != nil {
			tiers := s.FleetRowTiers(a.Owner)
			if !slices.Contains(tiers, fix.TIER) {
				refused = append(refused, Refusal{"TIER", fmt.Sprintf("tier %s not served by %s", fix.TIER, a.Owner)})
			} else {
				primTier := a.Field("tier_now")
				if primTier != fix.TIER {
					p.Units = append(p.Units, Unit{
						Key:    c.ID,
						Stream: c.F("stream"),
						Changes: []Change{change(Merge, setEntry(c, map[string]string{"tier_now": fix.TIER}, nil))},
						Moved:  fmt.Sprintf("%s tier recut to %s", c.ID, fix.TIER),
					})
				}
			}
		} else {
			refused = append(refused, Refusal{"TIER", "no attempt to recut tier on"})
		}
	}

	// GATE-HOST: mark gates to run on a bench
	if fix.GATEHOST != "" {
		if fix.GATEHOST != "linux" {
			refused = append(refused, Refusal{"GATE-HOST", fmt.Sprintf("unknown host %s; must be linux", fix.GATEHOST)})
		} else if a != nil {
			gateHost := a.Field("gate_host")
			if gateHost != fix.GATEHOST {
				p.Units = append(p.Units, Unit{
					Key:    a.ID,
					Stream: c.F("stream"),
					Changes: []Change{change(Merge, setEntry(a, map[string]string{"gate_host": fix.GATEHOST}, nil))},
					Moved:  fmt.Sprintf("%s gates marked to run on %s", a.ID, fix.GATEHOST),
				})
			}
		} else {
			refused = append(refused, Refusal{"GATE-HOST", "no attempt to mark gate host on"})
		}
	}

	return p, refused
}
