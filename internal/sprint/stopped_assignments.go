package sprint

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

const (
	NStoppedAssignments        = "STOPPED with unresolved assignments"
	NStoppedAssignmentsCleared = "STOP assignment condition cleared"
	propStoppedAssignments     = "stopped_assignments."
)

// StoppedAssignments observes canonical leases, never native execution
// (SPEC-SPRINT, STOP assignment alerts). Its durable row episode follows
// SprintRules.tla AlarmOncePerEpisode and ClearFollowsAlarm. Running includes
// a paused machine: existing work during pause is not a STOP anomaly.
func StoppedAssignments(s *Snapshot) Plan {
	var p Plan
	if s.Fleet == nil || s.Readers == nil {
		return p // an unread table is unknown, never evidence that an episode cleared
	}
	leases := map[string][]string{}
	if !s.Running {
		for _, t := range []*Table{s.Fleet, s.Readers} {
			col := Working
			if t == s.Readers {
				col = Reading
			}
			for _, c := range t.Column(col) {
				leases[c.Row] = append(leases[c.Row], fmt.Sprintf("%s:%s@%d", t.Name, c.ID, max(c.Int("gen"), 1)))
			}
		}
	}
	rows := map[string]bool{}
	for row := range leases {
		rows[row] = true
	}
	for key, value := range s.Fleet.Props() {
		if row, ok := strings.CutPrefix(key, propStoppedAssignments); ok && value != "" {
			rows[row] = true
		}
	}
	for _, row := range slices.Sorted(maps.Keys(rows)) {
		key := propStoppedAssignments + row
		said, had := s.Fleet.Prop(key)
		cards := leases[row]
		if len(cards) > 0 && said != "" || len(cards) == 0 && said == "" {
			continue
		}
		if s.Coordinator == "" {
			return Plan{Refused: []Refusal{{Key: row, Why: "STOP assignment notification needs the canonical coordinator; run: nova-sprint coordinator -h"}}}
		}
		typ, value := NStoppedAssignments, stamp(s.Now)
		what := fmt.Sprintf("machine STOPPED: %s retains %d canonical working/read assignments (%s); execution unknown / needs evidence; a row is not proof of a live worker", row, len(cards), Preview(slices.Sorted(slices.Values(cards)), ", "))
		if len(cards) == 0 {
			typ, value = NStoppedAssignmentsCleared, ""
			what = fmt.Sprintf("%s: the STOP assignment condition recorded at %s cleared; no conclusion about native execution", row, said)
		}
		// Inbox groups addressed notes by type: retain each owner's evidence.
		n := happened(typ+": "+row, "", s.Now)
		n.Who, n.To, n.What = MachineActor, s.Coordinator, what
		n.Hint = "run: nova-sprint queue --as " + row + " --json; verify the owner's native work and preserved head before any supported return; do not infer execution or resume from a row"
		p.Props = append(p.Props, PropWrite{Table: Fleet, Name: key, Value: value, Was: said, WasAbsent: !had})
		p.Units = append(p.Units, Unit{Key: key, Notes: []Note{n}, Moved: typ + ": " + row})
	}
	return p
}

// TickStoppedAssignments clears an ended STOP episode in the existing tick
// part machinery; an empty observation is passed over (SPEC-SPRINT, STOP
// assignment alerts; tla/DirtyTickRead.tla PassOver).
func TickStoppedAssignments(s *Snapshot, _ TickReq) (Plan, int) {
	p := StoppedAssignments(s)
	if len(p.Refused) > 0 {
		return p, 1 // a missing recipient is pending, not an empty observation
	}
	return p, 0
}

// TickEndWithStoppedAssignments gives real and shadow ticks the same
// observational recovery part (SPEC-SPRINT, STOP assignment alerts).
func TickEndWithStoppedAssignments(s *Snapshot, rules, idle bool) []TickPartDef {
	out := TickEndWith(rules, idle)
	p := StoppedAssignments(s)
	if p.Empty() && len(p.Refused) == 0 {
		return out
	}
	return append([]TickPartDef{{Name: "stopped assignments", Fn: TickStoppedAssignments}}, out...)
}
