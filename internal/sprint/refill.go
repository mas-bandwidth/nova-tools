package sprint

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/deal"
)

// REFILL IS NOT A NEW MECHANISM. "When somebody completes work, you must make sure they have
// more work to do. a queue if you will. as above, so below" (Glenn, 2026-09-22) is the same
// rule a bench's ready queue has always had, so the rule itself lives in internal/deal and
// this file is the thin part: read the sprint's open tasks, ask deal.Plan where they go,
// write the placements. `nova-pulse fill` -- the card dealer -- is the second caller of
// deal.Plan; it reads directories today, and when it moves onto the store it calls the same
// function with benches as consumers and ready cards as items. There is no friend-specific
// queue code anywhere in this package, and that is the point.

// Placement is one refill decision as the verb reports it.
type Placement struct {
	deal.Placement
	Task Task
}

// RefillResult is what a refill did, and what it could not place.
type RefillResult struct {
	Placements []Placement
	// Unplaced are open tasks no present consumer matched, with the reason. They stay in
	// the sprint: a task nobody can take is a fact to report, never a task to drop.
	Unplaced map[string]string
	// Depths are the queue depths before the refill, by consumer.
	Depths map[string]int
}

// Lines is the result as the verb prints it: one REFILL line per consumer, then one PLACE
// line per task.
func (r RefillResult) Lines() []string {
	byConsumer := map[string]int{}
	var order []string
	for _, p := range r.Placements {
		if _, seen := byConsumer[p.Consumer]; !seen {
			order = append(order, p.Consumer)
		}
		byConsumer[p.Consumer]++
	}
	sort.Strings(order)
	var out []string
	for _, c := range order {
		out = append(out, fmt.Sprintf("REFILL %s +%d depth %d -> %d", c, byConsumer[c], r.Depths[c], r.Depths[c]+byConsumer[c]))
	}
	for _, p := range r.Placements {
		line := fmt.Sprintf("PLACE %s %s -> %s (%s)", p.Task.ID, dashed(p.Task.Ref), p.Queue, p.Reason)
		if p.Task.Reader != "" {
			line += " reader=" + p.Task.Reader
		}
		out = append(out, line)
	}
	var unplaced []string
	for id := range r.Unplaced {
		unplaced = append(unplaced, id)
	}
	sort.Strings(unplaced)
	for _, id := range unplaced {
		out = append(out, fmt.Sprintf("UNPLACED %s %s", id, r.Unplaced[id]))
	}
	return out
}

// Ready turns a sprint's open tasks into the dealer's ready set, with the two scoring
// inputs the dealer cannot compute for itself: how many other open tasks each one unblocks,
// and how long it has been open.
func Ready(tasks []Task, now time.Time) []deal.Item {
	unblocks := map[string]int{}
	open := map[string]bool{}
	for _, t := range tasks {
		if t.Open() {
			open[t.ID] = true
		}
	}
	for _, t := range tasks {
		if !t.Open() {
			continue
		}
		for _, d := range t.DependsOn {
			d = strings.TrimSpace(d)
			if open[d] {
				unblocks[d]++
			}
		}
	}
	var out []deal.Item
	for _, t := range tasks {
		if !t.Open() || t.State == StateWorking {
			continue // a leased task is already somebody's; refill is for what is not
		}
		req := Requirements(t, now)
		req.Unblocks = unblocks[t.ID]
		created := t.CreatedAt
		if created.IsZero() {
			created = now
		}
		out = append(out, deal.Item{ID: t.ID, Req: req, Time: created})
	}
	return out
}

// Refill tops every consumer's queue that has dropped below the low water mark, from the
// sprint's open list, ownership first then age. It never routes to a consumer whose presence
// is not up: an away friend is simply not in the table the dealer is handed.
//
// With dry true it computes and reports and writes nothing, which is how a coordinator sees
// what a refill would do before it does it.
func Refill(ctx context.Context, st Store, sprintName string, cs []deal.Capabilities, lowWater int, now time.Time, dry bool) (RefillResult, error) {
	tasks, err := st.Tasks(ctx, sprintName)
	if err != nil {
		return RefillResult{}, err
	}
	present, err := st.Presence(ctx)
	if err != nil {
		return RefillResult{}, err
	}
	table := deal.ApplyPresence(cs, present)
	var names []string
	for _, c := range table {
		names = append(names, c.Name)
	}
	depths, err := st.QueueDepths(ctx, names)
	if err != nil {
		return RefillResult{}, err
	}
	ready := Ready(tasks, now)
	placements := deal.Plan(table, depths, ready, lowWater)

	byID := map[string]Task{}
	for _, t := range tasks {
		byID[t.ID] = t
	}
	result := RefillResult{Unplaced: map[string]string{}, Depths: depths}
	placed := map[string]bool{}
	for _, p := range placements {
		t := byID[p.ItemID]
		t.Route = routeOf(table, p.Consumer)
		t.RouteReason = p.Reason
		if t.Owner == "" {
			t.Owner = p.Consumer
		}
		if !dry {
			if _, err := st.Place(ctx, p.Queue, t); err != nil {
				return result, fmt.Errorf("placing %s on %s: %w", t.ID, p.Queue, err)
			}
			if err := st.PutTask(ctx, t); err != nil {
				return result, err
			}
		}
		placed[p.ItemID] = true
		result.Placements = append(result.Placements, Placement{Placement: p, Task: t})
	}
	for _, it := range ready {
		if placed[it.ID] {
			continue
		}
		if _, _, err := deal.Match(it.Req, table, nil); err != nil {
			result.Unplaced[it.ID] = err.Error()
		}
	}
	return result, nil
}

func routeOf(table []deal.Capabilities, consumer string) string {
	for _, c := range table {
		if c.Name == consumer {
			if c.Kind == deal.KindFriend {
				return RouteFriend + c.Name
			}
			return RouteBench + c.Name
		}
	}
	return RouteBench + consumer
}
