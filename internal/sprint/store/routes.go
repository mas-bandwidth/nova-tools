package store

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// RouteReader is a store that holds the model tiers' routes and route arrays
// nova-config applies (config.RoutesKey, config.RouteKey, config.TierKey): a step
// that deals reads them with its tables (Step.Routes; internal/sprint/route.go). A
// store that is not one has no route, and the deal deals as before.
type RouteReader interface {
	// Routes is every route, each tier's route array, and the round trips the
	// read made.
	Routes(ctx context.Context) ([]sprint.Route, map[string][]string, int64, error)
}

// routes is the routes a dealing step plans with, by name, and the tiers' arrays;
// an empty, non-nil list when the store holds none (read: the no-stall rule reads
// none again).
func (st *Store) routes(ctx context.Context) ([]sprint.Route, map[string][]string, error) {
	rs, tiers, _, err := st.routesTrips(ctx)
	return rs, tiers, err
}

// routesTrips is routes with the round trips its read made.
func (st *Store) routesTrips(ctx context.Context) ([]sprint.Route, map[string][]string, int64, error) {
	rr, ok := st.B.(RouteReader)
	if !ok {
		return []sprint.Route{}, nil, 0, nil
	}
	rs, tiers, trips, err := rr.Routes(ctx)
	if err != nil {
		return nil, nil, trips, fmt.Errorf("the routes (%s): %w", config.RoutesKey, err)
	}
	if rs == nil {
		rs = []sprint.Route{}
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].Name < rs[j].Name })
	return rs, tiers, trips, nil
}

// RouteCache is the routes read once and shared: a tick's, read by its first part
// that deals or checks and handed to every later one (the routes are config, read
// once a tick; tla/DirtyTick.tla holds them constant). Trips is the round trips
// the one read made, as the backend counts them (a shared client's trip counter
// also counts its other goroutines').
type RouteCache struct {
	read  bool
	rs    []sprint.Route
	tiers map[string][]string
	Trips int64
}

// cached is the routes and the tiers' arrays from the cache, read through it the
// first time; a nil cache reads them for this step alone.
func (st *Store) cached(ctx context.Context, c *RouteCache) ([]sprint.Route, map[string][]string, error) {
	if c == nil {
		return st.routes(ctx)
	}
	if !c.read {
		rs, tiers, trips, err := st.routesTrips(ctx)
		if err != nil {
			return nil, nil, err
		}
		c.read, c.rs, c.tiers, c.Trips = true, rs, tiers, trips
	}
	return c.rs, c.tiers, nil
}

// Routes reads the set, then every route's hash and each tier's array in one
// pipeline: two round trips, the second only when the set names a route (the
// arrays ride in it, so the tick's trips do not rise).
func (r *Redis) Routes(ctx context.Context) ([]sprint.Route, map[string][]string, int64, error) {
	names, err := r.C.SMembers(ctx, config.RoutesKey).Result()
	if err != nil || len(names) == 0 {
		return nil, nil, 1, err
	}
	pipe := r.C.Pipeline()
	hs := make(map[string]interface{ Val() map[string]string }, len(names))
	for _, n := range names {
		hs[n] = pipe.HGetAll(ctx, config.RouteKey(n))
	}
	arrays := make(map[string]interface{ Val() string }, len(config.RouteTiers))
	for _, t := range config.RouteTiers {
		arrays[t] = pipe.HGet(ctx, config.TierKey(t), "routes")
	}
	if err := redisconn.Exec(ctx, pipe); err != nil {
		return nil, nil, 2, err
	}
	out := make([]sprint.Route, 0, len(names))
	for _, n := range names {
		out = append(out, RouteOf(n, hs[n].Val()))
	}
	tiers := map[string][]string{}
	for _, t := range config.RouteTiers {
		if a := sprint.Split(arrays[t].Val()); len(a) > 0 {
			tiers[t] = a
		}
	}
	return out, tiers, 2, nil
}

// RouteOf is a route from its hash as nova-config's apply writes it: a field
// missing or unreadable is its zero, and the price sheet its price fields.
func RouteOf(name string, h map[string]string) sprint.Route {
	n := func(k string) int { v, _ := strconv.Atoi(h[k]); return v }
	enabled, _ := strconv.ParseBool(h["enabled"])
	return sprint.Route{Name: name, Tier: h["tier"], Provider: h["provider"], Model: h["model"], Tokens: n("tokens"),
		Deadline: n("deadline"), Enabled: enabled, Prices: cardcost.PricesOf(h)}
}

// Routes is the routes SetRoutes gave the store and the arrays SetTiers gave it.
func (m *Mem) Routes(context.Context) ([]sprint.Route, map[string][]string, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("routes"); err != nil {
		return nil, nil, 0, err
	}
	tiers := map[string][]string{}
	for t, a := range m.tiers {
		tiers[t] = append([]string(nil), a...)
	}
	return append([]sprint.Route(nil), m.routes...), tiers, 0, nil
}

// SetRoutes gives the store its routes, as nova-config's apply does a live one.
func (m *Mem) SetRoutes(rs []sprint.Route) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.routes = append([]sprint.Route(nil), rs...)
}

// SetTiers gives the store its tiers' route arrays, as nova-config's apply does a
// live one (tier:<name>).
func (m *Mem) SetTiers(tiers map[string][]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tiers = tiers
}

// Routes is the store's routes, by name, and the tiers' arrays, as a dealing step
// reads them: for the read verbs (where, routes).
func (st *Store) Routes(ctx context.Context) ([]sprint.Route, map[string][]string, error) {
	return st.routes(ctx)
}
