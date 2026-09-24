// Package deal is the reconciler's deal pass (nova-sprint #2756 section 5.3).
//
// Land-rate backpressure (#3095, control 48) is LandPass and Cut. LandPass
// reads land_debt and land_cap and splits a queue the caller already ordered.
// While debt is above the cap, a bulk card waits and ci, fix, rebase, and
// front cards still take free slots. One call does not look again: a landing
// is visible on the next call, which is the next pass. Neither function
// reserves, launches, or writes the pool or the PR sets.
package deal

import (
	"context"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/backpressure"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// LandResult is one pass of the land-rate gate over a queue.
type LandResult struct {
	Debt, Cap      int
	Above          bool
	Dealt, Waiting []backpressure.Card
}

// LandPass deals what the land-rate gate allows into the free slots, and
// returns the rest still waiting. free is the bench's free slots this pass.
// A card the gate holds does not consume a slot.
func LandPass(ctx context.Context, st *store.Store, sprint string, free int, queued []backpressure.Card) (LandResult, error) {
	p, err := backpressure.Measure(ctx, st, sprint)
	if err != nil {
		return LandResult{}, err
	}
	dealt, waiting := selectQueued(p.Debt, p.Cap, free, queued)
	return LandResult{Debt: p.Debt, Cap: p.Cap, Above: p.Above(), Dealt: dealt, Waiting: waiting}, nil
}

// Cut reports whether a new card of workType may be cut. A refusal is
// backpressure.ErrCutRefused. Nothing is written.
func Cut(ctx context.Context, st *store.Store, sprint, workType string) error {
	return backpressure.CheckCut(ctx, st, sprint, workType)
}

func selectQueued(debt, cap, free int, queued []backpressure.Card) (dealt, waiting []backpressure.Card) {
	if free < 0 {
		free = 0
	}
	for _, c := range queued {
		if free > 0 && backpressure.Flows(debt, cap, c) {
			dealt = append(dealt, c)
			free--
			continue
		}
		waiting = append(waiting, c)
	}
	return dealt, waiting
}
