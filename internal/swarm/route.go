package swarm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// THE ROUTE (issue #917): a provider/model a task spends against, and the ceiling on how
// many of its tasks run at once. Above ~30-40 concurrent requests a key queues forever, and
// the queue is invisible in the pool's own counts. The ceiling is enforced at the launch
// gate, so a route at its cap simply starts nothing, and `status` reports where each route
// stands.

// routeBelowCap answers whether another task for sc's route may launch. The count is the
// tasks this dispatcher is watching (which are the route's running sidecars in its own
// pool) plus, when a bench slot store is named, the leases under it whose label carries the
// route. A route with no cap is always below it.
func (in RunInput) routeBelowCap(sc Sidecar) bool {
	route := sc.Route
	if route == "" {
		route = in.Worker.RouteName()
	}
	cap := sc.MaxInflight
	if cap <= 0 {
		cap = in.Worker.MaxInflight
	}
	if cap <= 0 {
		return true
	}
	n := 0
	if in.Pool != nil {
		if tasks, err := in.Pool.List(Running); err == nil {
			for _, t := range tasks {
				if t.ID != sc.ID && t.Route == route {
					n++
				}
			}
		}
	}
	n += SlotStoreInflight(in.SlotsStore, route)
	return n < cap
}

// SlotStoreInflight counts the leases in a bench slot store whose label carries the route.
// The store is a directory of lease files, each a JSON object with a `label` and/or a
// `route`; a file that does not read as one is not a lease and is not counted.
func SlotStoreInflight(store, route string) int {
	if strings.TrimSpace(store) == "" || strings.TrimSpace(route) == "" {
		return 0
	}
	entries, err := os.ReadDir(store)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(store, e.Name()))
		if err != nil {
			continue
		}
		var lease struct {
			Label string `json:"label"`
			Route string `json:"route"`
		}
		if err := json.Unmarshal(raw, &lease); err != nil {
			continue
		}
		if lease.Route == route || (lease.Route == "" && strings.Contains(lease.Label, route)) {
			n++
		}
	}
	return n
}

// RouteState is one route's live count and the ceiling it runs under.
type RouteState struct {
	Route    string
	Inflight int
	Cap      int
}

// RouteStatus is the live count per route for every route with a running task, in
// first-seen order. The caller renders them, so the route is escaped where it is printed.
func RouteStatus(p *Pool) []RouteState {
	if p == nil {
		return nil
	}
	tasks, err := p.List(Running)
	if err != nil {
		return nil
	}
	type agg struct{ inflight, cap int }
	seen := map[string]*agg{}
	var order []string
	for _, sc := range tasks {
		if sc.Route == "" {
			continue
		}
		a := seen[sc.Route]
		if a == nil {
			a = &agg{}
			seen[sc.Route] = a
			order = append(order, sc.Route)
		}
		a.inflight++
		if sc.MaxInflight > a.cap {
			a.cap = sc.MaxInflight
		}
	}
	var out []RouteState
	for _, route := range order {
		a := seen[route]
		out = append(out, RouteState{Route: route, Inflight: a.inflight, Cap: a.cap})
	}
	return out
}

// requeueStall is rule 7's shape for a stalled task: ONE new attempt, and a second stall is
// final. It is the existing requeue path with its own mark, because a stall is not a reap
// and must not spend the deadline retry's one allowance.
func (in RunInput) requeueStall(sc Sidecar, now time.Time) bool {
	if sc.Stalled >= 1 {
		return false
	}
	text, err := in.Pool.Text(Running, sc.ID)
	if err != nil {
		return false
	}
	next := freshAttempt(sc, now)
	next.From, next.Requeued, next.Stalled = sc.ID, 1, sc.Stalled+1
	if err := in.Pool.Add(text, next); err != nil {
		return false
	}
	return true
}
