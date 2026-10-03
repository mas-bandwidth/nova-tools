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
// that deals or asks reads them with its tables (Step.Routes; internal/sprint/route.go). A store
// that is not one has no route, and the deal deals as before.
type RouteReader interface {
	// Routes is every route and each tier's route array, and the round trips
	// the read made.
	Routes(ctx context.Context) (RouteSet, int64, error)
}

// PriceReader is a store that holds the routes a worker's step prices with: the
// routes set and each route's record, and nothing else (Step.Prices). A store that
// is not one has no route, and nothing is priced.
type PriceReader interface {
	// PriceRoutes is every route, read from the set and the routes' records alone,
	// and the round trips the read made.
	PriceRoutes(ctx context.Context) ([]sprint.Route, int64, error)
}

// priceRoutes is the routes a worker's step prices with, by name; an empty list when
// the store holds none.
func (st *Store) priceRoutes(ctx context.Context) ([]sprint.Route, error) {
	pr, ok := st.B.(PriceReader)
	if !ok {
		return []sprint.Route{}, nil
	}
	rs, _, err := pr.PriceRoutes(ctx)
	if err != nil {
		return nil, fmt.Errorf("the routes to price with (%s, %s): %w", config.RoutesKey, config.RouteKey("<name>"), err)
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].Name < rs[j].Name })
	return append([]sprint.Route{}, rs...), nil
}

// PriceRoutes reads the set, then every route's record in one pipeline: two round
// trips, the second only when the set names a route. It touches the keys
// config.RoutesKey and config.RouteKey alone, which every role reads
// (internal/redisacl, the routes family): never a tier's array or the sprint row.
func (r *Redis) PriceRoutes(ctx context.Context) ([]sprint.Route, int64, error) {
	names, err := r.C.SMembers(ctx, config.RoutesKey).Result()
	if err != nil || len(names) == 0 {
		return nil, 1, err
	}
	pipe := r.C.Pipeline()
	hs := make(map[string]interface{ Val() map[string]string }, len(names))
	for _, n := range names {
		hs[n] = pipe.HGetAll(ctx, config.RouteKey(n))
	}
	if err := redisconn.Exec(ctx, pipe); err != nil {
		return nil, 2, err
	}
	out := make([]sprint.Route, 0, len(names))
	for _, n := range names {
		out = append(out, RouteOf(n, hs[n].Val()))
	}
	return out, 2, nil
}

// PriceRoutes is the routes SetRoutes gave the store.
func (m *Mem) PriceRoutes(context.Context) ([]sprint.Route, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("routes"); err != nil {
		return nil, 0, err
	}
	return append([]sprint.Route(nil), m.routes...), 0, nil
}

// RouteSet is what a step that deals or asks plans with: the routes, the
// tiers' arrays, and the decide read's bounce and review bars (the sprint row's,
// read with them).
type RouteSet struct {
	Routes []sprint.Route
	Tiers  map[string][]string
	Bars   [2]string
	Gate   [2]string // the gate decision's flaky and pre-existing bars (the sprint row's)
}

// into is the set as the snapshot carries it.
func (rs RouteSet) into(s *sprint.Snapshot) {
	s.Routes, s.Tiers = rs.Routes, rs.Tiers
	s.DecideBounce, s.DecideReview = rs.Bars[0], rs.Bars[1]
	s.DecideGateFlaky, s.DecideGatePreexisting = rs.Gate[0], rs.Gate[1]
}

// routes is the routes a dealing step plans with, by name, and the tiers'
// arrays; an empty, non-nil list when the store holds none (read: the
// no-stall rule reads none again).
func (st *Store) routes(ctx context.Context) (RouteSet, error) {
	rs, _, err := st.routesTrips(ctx)
	return rs, err
}

// routesTrips is routes with the round trips its read made.
func (st *Store) routesTrips(ctx context.Context) (RouteSet, int64, error) {
	rr, ok := st.B.(RouteReader)
	if !ok {
		return RouteSet{Routes: []sprint.Route{}}, 0, nil
	}
	set, trips, err := rr.Routes(ctx)
	if err != nil {
		return RouteSet{}, trips, fmt.Errorf("the routes (%s): %w", config.RoutesKey, err)
	}
	if set.Routes == nil {
		set.Routes = []sprint.Route{}
	}
	sort.Slice(set.Routes, func(i, j int) bool { return set.Routes[i].Name < set.Routes[j].Name })
	return set, trips, nil
}

// RouteCache is the routes read once and shared: a tick's, read by its first part
// that deals or checks and handed to every later one (the routes are config, read
// once a tick; tla/DirtyTick.tla holds them constant). Trips is the round trips
// the one read made, as the backend counts them (a shared client's trip counter
// also counts its other goroutines').
type RouteCache struct {
	read  bool
	set   RouteSet
	Trips int64
}

// cached is the route set from the cache, read through it the first time; a nil
// cache reads it for this step alone.
func (st *Store) cached(ctx context.Context, c *RouteCache) (RouteSet, error) {
	if c == nil {
		return st.routes(ctx)
	}
	if !c.read {
		set, trips, err := st.routesTrips(ctx)
		if err != nil {
			return RouteSet{}, err
		}
		c.read, c.set, c.Trips = true, set, trips
	}
	return c.set, nil
}

// Routes reads the set, then every route's hash, each tier's array and the
// decide read's two bars in one pipeline: two round trips, the second only when
// the set names a route (the arrays and the bars ride in it, so the tick's trips
// do not rise; with no route a read card has none to draw, and no decide read).
func (r *Redis) Routes(ctx context.Context) (RouteSet, int64, error) {
	names, err := r.C.SMembers(ctx, config.RoutesKey).Result()
	if err != nil || len(names) == 0 {
		return RouteSet{}, 1, err
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
	bounce, review := pipe.Get(ctx, config.SprintKey(config.FieldDecideBounce)), pipe.Get(ctx, config.SprintKey(config.FieldDecideReview))
	flaky, pre := pipe.Get(ctx, config.SprintKey(config.FieldDecideGateFlaky)), pipe.Get(ctx, config.SprintKey(config.FieldDecideGatePreexisting))
	if err := redisconn.Exec(ctx, pipe); err != nil {
		return RouteSet{}, 2, err
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
	return RouteSet{Routes: out, Tiers: tiers, Bars: [2]string{bounce.Val(), review.Val()}, Gate: [2]string{flaky.Val(), pre.Val()}}, 2, nil
}

// RouteOf is a route from its hash as nova-config's apply writes it: a field
// missing or unreadable is its zero, and the price sheet its price fields.
func RouteOf(name string, h map[string]string) sprint.Route {
	n := func(k string) int { v, _ := strconv.Atoi(h[k]); return v }
	enabled, _ := strconv.ParseBool(h["enabled"])
	return sprint.Route{Name: name, Tier: h["tier"], Provider: h["provider"], Model: h["model"], Tokens: n("tokens"), USD: h["usd"],
		Deadline: n("deadline"), Enabled: enabled, Prices: cardcost.PricesOf(h)}
}

// Routes is the routes SetRoutes gave the store and the arrays SetTiers gave it.
func (m *Mem) Routes(context.Context) (RouteSet, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.fail("routes"); err != nil {
		return RouteSet{}, 0, err
	}
	tiers := map[string][]string{}
	for t, a := range m.tiers {
		tiers[t] = append([]string(nil), a...)
	}
	return RouteSet{Routes: append([]sprint.Route(nil), m.routes...), Tiers: tiers, Bars: m.bars, Gate: m.gate}, 0, nil
}

// SetGateBars gives the store the gate decision's flaky and pre-existing bars, as
// nova-config's apply does a live one (sprint:decide_gate_flaky, sprint:decide_gate_preexisting).
func (m *Mem) SetGateBars(flaky, preExisting string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gate = [2]string{flaky, preExisting}
}

// GateBars is the gate decision's flaky and pre-existing bars as the routes read takes
// them: the lander's, for its red batch gate (docs/SPEC-SPRINT.md section 7).
func (st *Store) GateBars(ctx context.Context) ([2]string, error) {
	set, err := st.routes(ctx)
	return set.Gate, err
}

// SetDecideBars gives the store the decide read's bounce and review bars, as
// nova-config's apply does a live one (sprint:decide_bounce, sprint:decide_review).
func (m *Mem) SetDecideBars(bounce, review string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bars = [2]string{bounce, review}
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
	set, err := st.routes(ctx)
	return set.Routes, set.Tiers, err
}
