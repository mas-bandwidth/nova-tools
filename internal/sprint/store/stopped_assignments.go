package store

import (
	"context"
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// stoppedAssignments journals observational STOP episodes through the same
// operation fence as assignments (SPEC-SPRINT, STOP assignment alerts).
// Snapshot.Running is the fenced state, not the tick's earlier machine read.
func (st *Store) stoppedAssignments(ctx context.Context, res *TickResult) error {
	load := []string{sprint.Fleet, sprint.Readers}
	// Pass an empty observation over, as running tick parts do. This fenced
	// probe grants no write authority: Run re-reads before journaling a change
	// (tla/DirtyTickRead.tla PassOver and ViewIsSnapshot).
	snap, _, err := st.fenced(ctx, load, nil, nil, nil)
	if err != nil {
		return fmt.Errorf("stopped assignments: %w", err)
	}
	probe := sprint.StoppedAssignments(snap)
	if len(probe.Refused) != 0 {
		return fmt.Errorf("stopped assignments: %v", probe.Refused)
	}
	if probe.Empty() {
		return nil
	}
	r, err := st.Run(ctx, Step{Verb: "tick stopped assignments", Actor: sprint.MachineActor,
		Load: load, Plan: sprint.StoppedAssignments})
	if err != nil {
		return fmt.Errorf("stopped assignments: %w", err)
	}
	if len(r.Refused) != 0 {
		return fmt.Errorf("stopped assignments: %v", r.Refused)
	}
	if r.Notes > 0 {
		res.Parts = append(res.Parts, PartResult{Name: "stopped assignments", Result: r})
	}
	return nil
}
