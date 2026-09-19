package pulse

// The decide pass the run loop makes after each harvest (card 8371, #896).
//
// A harvest folds the cards that came back; this pass reads the pool's newly
// finished tasks and asks the triage decide library for one typed decision
// each. A task whose needs_human is at or above the floor is a person's: it is
// appended to <queue>/HUMAN as one line and is NOT auto-retried. A
// provider_error above the floor is retried once by the existing requeue path.
// Below the floor nothing changes: the pool's own class stands.
//
// It makes no model call of its own: every decision is the decide library's,
// handed in as swarm.DecideFunc so a test never reaches the network.

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// DecideInput is one decide pass: the queue that holds HUMAN, the swarm pool
// directories to read, the typed-decision seam and the floor.
type DecideInput struct {
	Queue string
	Pools []string
	Do    swarm.DecideFunc
	Floor float64
	Now   func() time.Time
}

// DecideHarvest walks each pool's finished tasks, makes one typed decision per
// task and disposes of it: needs_human at or above the floor goes to
// <queue>/HUMAN and is never retried; a provider_error above the floor is
// requeued once; below the floor nothing changes. It returns the counts.
func DecideHarvest(in DecideInput) (human, retried int) {
	if in.Do == nil {
		return 0, 0
	}
	now := in.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	floor := in.Floor
	if floor == 0 {
		floor = swarm.DefaultDecideFloor
	}
	for _, poolDir := range in.Pools {
		p, err := swarm.OpenPool(poolDir)
		if err != nil {
			continue
		}
		decisions, err := swarm.DecideFinished(p, in.Do, floor, now)
		if err != nil {
			continue
		}
		for _, d := range decisions {
			if d.NeedsHuman >= floor {
				appendHuman(in.Queue, d)
				human++
				continue
			}
			if d.Reason == "provider_error" && d.Confidence >= floor {
				if _, ok := p.RequeueOnce(d.Task, now()); ok {
					retried++
				}
			}
		}
	}
	return human, retried
}

// appendHuman appends one line to <queue>/HUMAN: task, reason, confidence and
// the card label, every value one token.
func appendHuman(queue string, d swarm.TaskDecision) {
	path := filepath.Join(queue, "HUMAN")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "HUMAN task=%s reason=%s conf=%.2f card=%s\n",
		oneline.Field(d.Task), oneline.Field(d.Reason), d.Confidence, field(d.Label))
}
