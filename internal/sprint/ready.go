package sprint

import (
	"fmt"
	"strings"
)

// ready.go is the READY predicate #2636 named (Glenn: "Dependencies are critical"): a task
// whose dependency is still open, or whose paths are still being worked by another task, is
// not ready, whatever its priority -- the dealer at 1e5e0227 put it in the ready set anyway,
// which is the bug. `sprint route` and `sprint refill` both call Blocked before handing a
// task out; neither ever routed a task straight from DependsOn or Paths before this file.

// Blocked answers why t is not ready to be routed or refilled, or "" when it is.
//
// A dependency is satisfied only by a task that is state=closed AND whose evidence says it
// LANDED or MERGED -- not merely that the card came back ok. An ok card is a friend's or a
// swarm attempt that finished; it is not yet work that reached the tree, and a task that
// depends on the RESULT of another task must wait for the tree, not for the attempt. (This
// is why sprint.RedisCards' own evidence -- "cards:done <id> ok at <at>" -- never satisfies
// a dependency: it names no landing.)
//
// A task's paths must also be disjoint from every task this sprint currently has in
// state=working: two consumers are never handed the same file at once. A task already
// working is never itself blocked by this rule -- only what comes AFTER it waits, and only
// until it closes -- so a serial head never blocks the queue behind a different lane.
func Blocked(t Task, tasks []Task) string {
	byID := map[string]Task{}
	for _, o := range tasks {
		byID[o.ID] = o
	}
	for _, raw := range t.DependsOn {
		d := strings.TrimSpace(raw)
		if d == "" {
			continue
		}
		dep, ok := byID[d]
		if !ok {
			return fmt.Sprintf("depends on %s, which is not in this sprint", d)
		}
		if dep.State != StateClosed {
			return fmt.Sprintf("depends on %s (%s)", d, dep.State)
		}
		if !landedOrMerged(dep.Evidence) {
			return fmt.Sprintf("depends on %s, closed but not landed or merged (evidence: %s)", d, dashed(dep.Evidence))
		}
	}
	if t.State == StateWorking {
		// The serial head is already somebody's; it is not blocked by its own paths.
		return ""
	}
	for _, other := range tasks {
		if other.ID == t.ID || other.State != StateWorking {
			continue
		}
		if SharesPaths(t, other) {
			return fmt.Sprintf("shares a path with %s, which is working", other.ID)
		}
	}
	return ""
}

// landedOrMerged is the dependency bar: the evidence that closed the task must say it landed
// or merged, not merely that the card came back ok.
func landedOrMerged(evidence string) bool {
	e := strings.ToLower(evidence)
	return strings.Contains(e, "landed") || strings.Contains(e, "merged")
}
