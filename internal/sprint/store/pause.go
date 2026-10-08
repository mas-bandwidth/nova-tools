package store

import (
	"context"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// SetPaused implements SprintPause.tla Pause/Unpause under the same operation
// fence as admission. It preserves the run generation, queues and owner leases.
func (st *Store) SetPaused(ctx context.Context, paused bool) (before, after Machine, res Result, err error) {
	before, after, changed, err := st.machineTransitionWithPause(ctx, true, st.Actor, "", time.Time{}, &paused)
	if err != nil || !changed {
		return before, after, res, err
	}
	res.Moved = []string{"machine " + before.StateWord() + " -> " + after.StateWord()}
	return before, after, res, nil
}

// pauseHoldsPart names only parts that preserve assignments while admissions
// are held (SPEC-SPRINT section 14; SprintPause.tla). Completion acceptance,
// queue settlement and checks continue; no timeout returns or rework
// replace claims because a job waits for unpause.
func pauseHoldsPart(name string) bool {
	switch name {
	case sprint.PartDrain, "accept", "check", "stopped assignments":
		return false
	default:
		return true
	}
}
