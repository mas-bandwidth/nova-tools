package store

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

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
// tiers' arrays, and the sprint row's nova-decide bars, read with them (Bars).
type RouteSet struct {
	Routes []sprint.Route
	Tiers  map[string][]string
	Bars   Bars
	// FriendCaps is each friend's dollar cap per clock hour by name (a canonical decimal;
	// empty for the default, sprint.DefaultFriendCapUSDHour), read with the routes by a
	// step that deals, so the tick's cap judgment reaches the friends (FriendCaps).
	FriendCaps map[string]string
}

// Bars is the sprint row's nova-decide bars, each by the name of its field, so no bar is
// ever read as another: the decide read's bounce and review bars, the attempt decision's
// no-result and nothing-to-do bars, the grade's, the landed score's, the gate decision's
// flaky and pre-existing bars, and the judgment decision's (nova-sprint answer reads it
// from routes --json). "" is no bar.
type Bars struct {
	Bounce, Review                      string // decide_bounce, decide_review
	AttemptNoResult, AttemptNothingToDo string // decide_attempt_no_result, decide_attempt_nothing_to_do
	Grade                               string // decide_grade
	Score                               string // decide_score_bar
	GateFlaky, GatePreexisting          string // decide_gate_flaky, decide_gate_preexisting
	Judgment                            string // decide_judgment_bar
	// RulesOff is the sprint row's rules the machine does not answer by (answer_rules_off: a
	// comma list of config.AnswerRules); "" turns none off.
	RulesOff string
}

// fields is each bar by its sprint row field (config.SprintKey(field) holds it).
func (b *Bars) fields() map[string]*string {
	return map[string]*string{
		config.FieldDecideBounce:             &b.Bounce,
		config.FieldDecideReview:             &b.Review,
		config.FieldDecideAttemptNoResult:    &b.AttemptNoResult,
		config.FieldDecideAttemptNothingToDo: &b.AttemptNothingToDo,
		config.FieldDecideGrade:              &b.Grade,
		config.FieldDecideScoreBar:           &b.Score,
		config.FieldDecideGateFlaky:          &b.GateFlaky,
		config.FieldDecideGatePreexisting:    &b.GatePreexisting,
		config.FieldDecideJudgment:           &b.Judgment,
		config.FieldAnswerRulesOff:           &b.RulesOff,
	}
}

// into is the set as the snapshot carries it.
func (rs RouteSet) into(s *sprint.Snapshot) {
	s.Routes, s.Tiers = rs.Routes, rs.Tiers
	s.FriendCaps = rs.FriendCaps
	s.DecideBounce, s.DecideReview, s.DecideGrade = rs.Bars.Bounce, rs.Bars.Review, rs.Bars.Grade
	s.DecideAttemptNoResult, s.DecideAttemptNothingToDo = rs.Bars.AttemptNoResult, rs.Bars.AttemptNothingToDo
	s.DecideScoreBar = rs.Bars.Score
	s.DecideGateFlaky, s.DecideGatePreexisting = rs.Bars.GateFlaky, rs.Bars.GatePreexisting
	s.RulesOff = sprint.Split(rs.Bars.RulesOff)
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
		// the friends' hourly caps ride with the routes so the tick's deal reads them from
		// the one route read it makes (Snapshot.FriendCaps, sprint.TickDeal); a roster that
		// cannot be read fails the read, so a friend past her cap is never let through
		// uncapped (fail closed: no cap known is an error, not no cap)
		if caps, err := st.FriendCaps(ctx); err != nil {
			return RouteSet{}, err
		} else {
			set.FriendCaps = caps
		}
		c.read, c.set, c.Trips = true, set, trips
	}
	return c.set, nil
}

// Routes reads the set, then every route's hash, each tier's array and the
// sprint row's nova-decide bars (Bars) in one pipeline: two round trips, the second only when
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
	var set RouteSet
	bars := map[string]interface{ Val() string }{}
	for f := range set.Bars.fields() {
		bars[f] = pipe.Get(ctx, config.SprintKey(f))
	}
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
	for f, v := range set.Bars.fields() {
		*v = bars[f].Val()
	}
	set.Routes, set.Tiers = out, tiers
	return set, 2, nil
}

// RouteOf is a route from its hash as nova-config's apply writes it: a field
// missing or unreadable is its zero, and the price sheet its price fields.
// First is the hash's first field (docs/SPEC-CONFIG.md, route; docs/SPEC-SPRINT.md,
// the deal): true draws this route before the others of its tier.
func RouteOf(name string, h map[string]string) sprint.Route {
	n := func(k string) int { v, _ := strconv.Atoi(h[k]); return v }
	enabled, _ := strconv.ParseBool(h["enabled"])
	first, _ := strconv.ParseBool(h["first"])
	// a route whose row names no hourly cap takes its tier's (flash 5, pro 20, heavy 0):
	// resolved here, where the row and its tier are read together, so the core's Route
	// carries the effective cap and a route built by hand is uncapped when it names none
	capUSDHour := h["cap_usd_hour"]
	if capUSDHour == "" {
		capUSDHour = sprint.TierCapUSDHour[h["tier"]]
	}
	return sprint.Route{Name: name, Tier: h["tier"], Provider: h["provider"], Model: h["model"], Harness: h["harness"], Tokens: n("tokens"), USD: h["usd"], CapUSDHour: capUSDHour,
		Deadline: n("deadline"), Enabled: enabled, First: first, Prices: cardcost.PricesOf(h)}
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
	return RouteSet{Routes: append([]sprint.Route(nil), m.routes...), Tiers: tiers, Bars: m.bars}, 0, nil
}

// SetGateBars gives the store the gate decision's flaky and pre-existing bars, as
// nova-config's apply does a live one (sprint:decide_gate_flaky, sprint:decide_gate_preexisting).
func (m *Mem) SetGateBars(flaky, preExisting string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bars.GateFlaky, m.bars.GatePreexisting = flaky, preExisting
}

// GateBars is the gate decision's flaky and pre-existing bars as the routes read takes
// them: the lander's, for its red batch gate (docs/SPEC-SPRINT.md section 7).
func (st *Store) GateBars(ctx context.Context) ([2]string, error) {
	set, err := st.routes(ctx)
	return [2]string{set.Bars.GateFlaky, set.Bars.GatePreexisting}, err
}

// SetDecideBars gives the store the sprint row's nova-decide bars, as nova-config's apply
// does a live one (sprint:decide_bounce, sprint:decide_review,
// sprint:decide_attempt_no_result, sprint:decide_attempt_nothing_to_do, sprint:decide_grade).
func (m *Mem) SetDecideBars(b Bars) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bars = b
}

// SetScoreBar gives the store the landed score's bar, as nova-config's apply does a live
// one (sprint:decide_score_bar).
func (m *Mem) SetScoreBar(bar string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bars.Score = bar
}

// SetJudgmentBar gives the store the judgment decision's bar, as nova-config's apply
// does a live one (sprint:decide_judgment_bar).
func (m *Mem) SetJudgmentBar(bar string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bars.Judgment = bar
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

// OutOfCredit is the words of sprint.FundsCause when every enabled route of the store rests
// for its provider's funds (sprint.AllOutOfCredit), "" otherwise: what a start is refused
// with. It reads the routes and the fleet table's properties, no card.
func (st *Store) OutOfCredit(ctx context.Context) (string, error) {
	routes, _, err := st.Routes(ctx)
	if err != nil || len(routes) == 0 {
		return "", err
	}
	shapes, err := st.B.Shapes(ctx, []string{st.Names.Table(sprint.Fleet)})
	if err != nil {
		return "", err
	}
	fleet := sprint.NewTable(sprint.Fleet)
	fleet.SetProps(shapes[0].Props)
	why := sprint.AllOutOfCredit(routes, sprint.RouteRests(routes, fleet), st.now())
	if why == "" {
		return "", nil
	}
	// a friend up keeps the machine running: the paid routes rest, friends are dealt
	// (sprint.AnyFriendUp; the owner, 2026-10-04)
	seats, err := st.friendSeats(ctx, nil, st.now())
	if err != nil {
		return "", err
	}
	if sprint.AnyFriendUp(seats) {
		return "", nil
	}
	return why, nil
}

// JudgmentBar is the judgment decision's bar as nova-config applied it, read with the
// routes: for routes --json, which answer reads. "" when none is applied.
func (st *Store) JudgmentBar(ctx context.Context) (string, error) {
	set, err := st.routes(ctx)
	return set.Bars.Judgment, err
}

// SetRulesOff gives the store the rules the machine does not answer by, as nova-config's
// apply does a live one (sprint:answer_rules_off).
func (m *Mem) SetRulesOff(rules ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bars.RulesOff = strings.Join(rules, ",")
}

// RulesOff is the rules the machine does not answer by as nova-config applied them, read
// with the routes: the lander reads base-gate there (cmd/nova-sprint, landgo.go).
func (st *Store) RulesOff(ctx context.Context) ([]string, error) {
	set, err := st.routes(ctx)
	return sprint.Split(set.Bars.RulesOff), err
}
