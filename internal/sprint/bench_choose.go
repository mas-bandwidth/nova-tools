package sprint

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

// GateBench is one declared bench and its observed beat (docs/SPEC-SPRINT.md,
// choosing the gate bench). It is an input snapshot, never a second inventory.
type GateBench struct {
	Name        string
	Bench       bool
	Cores       int
	Load1       float64
	LoadKnown   bool
	GoProcesses int
	At          time.Time
}

// GateBenchChoice is one start's bench and the explanation carried by its brief.
type GateBenchChoice struct {
	Host   string
	Reason string
}

// ChooseGateBench is the pure selection rule (docs/SPEC-SPRINT.md, choosing the
// gate bench): current measured benches below their cap, least load per core,
// with name as a stable tie breaker. A zero factor uses the default 1.5.
func ChooseGateBench(now time.Time, benches []GateBench, factor float64) (GateBenchChoice, error) {
	if factor == 0 {
		factor = 1.5
	}
	if math.IsNaN(factor) || math.IsInf(factor, 0) || factor <= 0 {
		return GateBenchChoice{}, fmt.Errorf("bench load cap factor must be a finite number above zero; run: nova-config machine list")
	}
	ordered := slices.Clone(benches)
	slices.SortFunc(ordered, func(a, b GateBench) int { return strings.Compare(a.Name, b.Name) })
	var chosen *GateBench
	var notes []string
	total := 0
	for _, b := range ordered {
		if !b.Bench {
			continue
		}
		total++
		why := ""
		switch {
		case !ValidID(b.Name):
			why = "invalid machine name"
		case !b.LoadKnown || b.Cores <= 0 || math.IsNaN(b.Load1) || math.IsInf(b.Load1, 0) || b.Load1 < 0:
			why = "load or cores unknown"
		case b.At.IsZero() || now.Before(b.At) || now.Sub(b.At) > BeatDeadline*MissedBeatsDown:
			why = "beat not current"
		case b.Load1 > float64(b.Cores)*factor:
			why = "over load cap"
		}
		if len(notes) < 8 {
			note := fmt.Sprintf("%s load %.1f of %d cores, go %d", b.Name, b.Load1, b.Cores, b.GoProcesses)
			if why != "" {
				note += " (skipped: " + why + ")"
			}
			notes = append(notes, note)
		}
		if why == "" && (chosen == nil || b.Load1/float64(b.Cores) < chosen.Load1/float64(chosen.Cores)) {
			c := b
			chosen = &c
		}
	}
	if total > len(notes) {
		notes = append(notes, fmt.Sprintf("%d more declared benches", total-len(notes)))
	}
	if chosen == nil {
		return GateBenchChoice{}, fmt.Errorf("no eligible current bench (%s); run: nova-config machine list", strings.Join(notes, "; "))
	}
	return GateBenchChoice{Host: chosen.Name, Reason: fmt.Sprintf("load %.1f of %d cores, go %d; least load per core; %s", chosen.Load1, chosen.Cores, chosen.GoProcesses, strings.Join(notes, "; "))}, nil
}
