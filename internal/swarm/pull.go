package swarm

// The pull is docs/SPEC-JOBS.md section 5: nova-swarm pull drains the priority
// lanes red (fixes to a red bench or a red PR), then green (small,
// already-approved PRs), then small (the shortest step budget), then next. The
// ordering is internal/lanes'; this verb names the floor and prints the one line.
//
// A tie the rule cannot break is asked of Jev as one typed decision in 400 ms
// behind the 0.9 floor, and a refusal keeps source order.

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/lanes"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// DefaultPullFloor is the confidence floor behind which the scorer's order is a
// suggestion, never an authorization.
const DefaultPullFloor = 0.9

// pullDecisionBudget is the 400 ms the section allows the one typed decision.
const pullDecisionBudget = 400 * time.Millisecond

// PullLanesInput is everything nova-swarm pull-lanes needs. The name avoids the
// dev-landed Pull type in warm.go, which is the affinity pull of section 4.
type PullLanesInput struct {
	Queue  string
	Score  lanes.Scorer
	Floor  float64
	Stdout io.Writer
	Stderr io.Writer
}

// PullLanes drains the lanes in order and prints one line. Every path comes from
// a flag; a queue that cannot be read is a refusal with one remedy line.
func PullLanes(in PullLanesInput) int {
	entries, err := lanes.Drain(context.Background(), in.Queue, in.Score, in.Floor)
	if err != nil {
		fmt.Fprintf(in.Stderr, "PULL REFUSED: %s; pass --queue a readable directory holding %s/%s/{red,green,small,next}\n",
			oneline.Err(err), oneline.Field(in.Queue), lanes.LanesDir)
		return 2
	}
	counts := map[string]int{}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		counts[e.Lane]++
		ids = append(ids, e.ID)
	}
	fmt.Fprintf(in.Stdout, "PULL OK cards=%d red=%d green=%d small=%d next=%d order=%s\n",
		len(entries), counts[lanes.Red], counts[lanes.Green], counts[lanes.Small], counts[lanes.Next],
		oneline.Field(oneline.Cap(strings.Join(ids, ","), oneline.TailBytes)))
	return 0
}
