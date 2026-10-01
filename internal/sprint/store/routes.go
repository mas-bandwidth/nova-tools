package store

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// RouteReader is a store that holds the model tiers' routes nova-config applies
// (config.RoutesKey, config.RouteKey): a step that deals reads them with its
// tables (Step.Routes; internal/sprint/route.go). A store that is not one has no
// route, and the deal deals as before.
type RouteReader interface {
	Routes(ctx context.Context) ([]sprint.Route, error)
}

// routes is the routes a dealing step plans with, by name; none when the store
// holds none.
func (st *Store) routes(ctx context.Context) ([]sprint.Route, error) {
	rr, ok := st.B.(RouteReader)
	if !ok {
		return nil, nil
	}
	rs, err := rr.Routes(ctx)
	if err != nil {
		return nil, fmt.Errorf("the routes (%s): %w", config.RoutesKey, err)
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].Name < rs[j].Name })
	return rs, nil
}

// Routes reads the set and every route's hash: two round trips, the second
// only when the set names a route.
func (r *Redis) Routes(ctx context.Context) ([]sprint.Route, error) {
	names, err := r.C.SMembers(ctx, config.RoutesKey).Result()
	if err != nil || len(names) == 0 {
		return nil, err
	}
	pipe := r.C.Pipeline()
	hs := make(map[string]interface{ Val() map[string]string }, len(names))
	for _, n := range names {
		hs[n] = pipe.HGetAll(ctx, config.RouteKey(n))
	}
	if err := redisconn.Exec(ctx, pipe); err != nil {
		return nil, err
	}
	out := make([]sprint.Route, 0, len(names))
	for _, n := range names {
		out = append(out, RouteOf(n, hs[n].Val()))
	}
	return out, nil
}

// RouteOf is a route from its hash as nova-config's apply writes it: a field
// missing or unreadable is its zero, a weight missing is 1.
func RouteOf(name string, h map[string]string) sprint.Route {
	n := func(k string) int { v, _ := strconv.Atoi(h[k]); return v }
	enabled, _ := strconv.ParseBool(h["enabled"])
	w := n("weight")
	if h["weight"] == "" {
		w = 1
	}
	return sprint.Route{Name: name, Tier: h["tier"], Provider: h["provider"], Model: h["model"], Tokens: n("tokens"),
		Deadline: n("deadline"), Weight: w, Enabled: enabled}
}

// Routes is the routes SetRoutes gave the store.
func (m *Mem) Routes(context.Context) ([]sprint.Route, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]sprint.Route(nil), m.routes...), nil
}

// SetRoutes gives the store its routes, as nova-config's apply does a live one.
func (m *Mem) SetRoutes(rs []sprint.Route) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.routes = append([]sprint.Route(nil), rs...)
}

// Routes is the store's routes, by name, as a dealing step reads them: for the
// read verbs (where, routes).
func (st *Store) Routes(ctx context.Context) ([]sprint.Route, error) { return st.routes(ctx) }
