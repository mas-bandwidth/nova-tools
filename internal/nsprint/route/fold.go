package route

// fold.go is the router's half of `routes:<type>` (nova-tools #3104, #2756 v6
// 4.10): the hash `nova-sprint fold` writes per work type, and the check that
// reads it. The fold is the only writer; this file owns the hash's shape so
// the writer and the reader cannot disagree on it.
//
// Where a fold has written routes:<type>, it governs that type: a route runs
// a card of the type only when the fold ranked it in or probation. A benched
// route is refused with the fold's reason (`landed 0 of <n>`), and a route the
// fold never measured on the type is refused until a fold admits it. The
// static table still has the last word on rung and on a dead route. Where no
// fold has written the type yet, the static table (Check) answers alone.

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

// Status is where the fold put a route for one work type.
const (
	StatusIn        = "in"        // ranked; dealt without a cap
	StatusProbation = "probation" // fewer than route_min_landed landed; capped at probation_share of the type
	StatusBenched   = "benched"   // 0 landed over route_bench_after PRs; refused for the type
)

// The two kinds of work type and the metric each ranks on.
const (
	KindPR       = "pr"   // PR-producing: ranked on $ per landed card
	KindRead     = "read" // read: ranked on $ per useful (8+) read
	MetricLanded = "usd_per_landed"
	MetricUseful = "usd_per_useful"
)

// FoldKey is the store key of one work type's fold ranking.
func FoldKey(workType string) string { return "routes:" + workType }

// FoldRoute is one route's row in routes:<type>.
type FoldRoute struct {
	Route    string
	Status   string
	Metric   string // dollars per landed (pr) or per useful (read); "-" when nothing divides
	Cards    int    // every card of the route on this type, failures included
	PRs      int
	Landed   int
	Useful   int
	USD      string // dollars of the priced cards; "-" when none was priced
	Unpriced int
	Reason   string
}

// Fold is routes:<type> as the fold wrote it. Routes is ranked: in, then
// probation, each best metric first, then benched.
type Fold struct {
	Type, Kind, Metric string
	Sprint, FoldSHA    string
	At                 string
	ProbationShare     string
	RouteMinLanded     int
	RouteBenchAfter    int
	Routes             []FoldRoute
}

// Order is the routes the router may deal the type to, best first.
func (f *Fold) Order() []string {
	var out []string
	for _, r := range f.Routes {
		if r.Status != StatusBenched {
			out = append(out, r.Route)
		}
	}
	return out
}

// Benched is the routes the fold benched for the type.
func (f *Fold) Benched() []string {
	var out []string
	for _, r := range f.Routes {
		if r.Status == StatusBenched {
			out = append(out, r.Route)
		}
	}
	return out
}

// Route returns the named route's row.
func (f *Fold) Route(name string) (FoldRoute, bool) {
	for _, r := range f.Routes {
		if r.Route == name {
			return r, true
		}
	}
	return FoldRoute{}, false
}

var (
	typeName  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	routeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
)

// ValidType reports whether s can name a work type in a store key.
func ValidType(s string) bool { return typeName.MatchString(s) }

// ValidRoute reports whether s can name a route in routes:<type>.
func ValidRoute(s string) bool { return routeName.MatchString(s) }

// Fields is the hash as flat field, value pairs for one HSET, after a DEL:
// the type's fields, then `<route>.<field>` per route. `routes` is the order
// the router deals in; `benched` lists the refused routes.
func (f *Fold) Fields() []string {
	out := []string{
		"type", f.Type, "kind", f.Kind, "metric", f.Metric,
		"routes", strings.Join(f.Order(), " "), "benched", strings.Join(f.Benched(), " "),
		"sprint", f.Sprint, "fold_sha", f.FoldSHA, "at", f.At,
		"probation_share", f.ProbationShare,
		"route_min_landed", strconv.Itoa(f.RouteMinLanded),
		"route_bench_after", strconv.Itoa(f.RouteBenchAfter),
	}
	for _, r := range f.Routes {
		p := r.Route + "."
		out = append(out,
			p+"status", r.Status, p+"metric", r.Metric,
			p+"n", strconv.Itoa(r.Cards), p+"prs", strconv.Itoa(r.PRs),
			p+"landed", strconv.Itoa(r.Landed), p+"useful", strconv.Itoa(r.Useful),
			p+"usd", r.USD, p+"unpriced", strconv.Itoa(r.Unpriced), p+"reason", r.Reason)
	}
	return out
}

// ParseFold reads routes:<type> from its hash. An empty hash is (nil, nil):
// no fold has ranked the type. A hash the fold would not write is an error,
// never a guess, so the router fails closed on a damaged record.
func ParseFold(workType string, h map[string]string) (*Fold, error) {
	if len(h) == 0 {
		return nil, nil
	}
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%s: %s", FoldKey(workType), fmt.Sprintf(format, a...))
	}
	f := &Fold{
		Type: h["type"], Kind: h["kind"], Metric: h["metric"],
		Sprint: h["sprint"], FoldSHA: h["fold_sha"], At: h["at"],
		ProbationShare: h["probation_share"],
	}
	if f.Type != workType {
		return nil, bad("type=%q, want %q", f.Type, workType)
	}
	switch {
	case f.Kind == KindPR && f.Metric == MetricLanded, f.Kind == KindRead && f.Metric == MetricUseful:
	default:
		return nil, bad("kind=%q metric=%q; a pr type ranks on %s, a read type on %s", f.Kind, f.Metric, MetricLanded, MetricUseful)
	}
	if f.FoldSHA == "" {
		return nil, bad("no fold_sha; only a recorded fold writes this key")
	}
	var err error
	if f.RouteMinLanded, err = strconv.Atoi(h["route_min_landed"]); err != nil {
		return nil, bad("route_min_landed=%q is not a count", h["route_min_landed"])
	}
	if f.RouteBenchAfter, err = strconv.Atoi(h["route_bench_after"]); err != nil {
		return nil, bad("route_bench_after=%q is not a count", h["route_bench_after"])
	}
	seen := map[string]bool{}
	for _, list := range []struct{ field, want string }{{"routes", ""}, {"benched", StatusBenched}} {
		for _, name := range strings.Fields(h[list.field]) {
			if !ValidRoute(name) || seen[name] {
				return nil, bad("%s names %q twice or not as a route", list.field, name)
			}
			seen[name] = true
			p := name + "."
			r := FoldRoute{Route: name, Status: h[p+"status"], Metric: h[p+"metric"], USD: h[p+"usd"], Reason: h[p+"reason"]}
			switch {
			case list.want == StatusBenched && r.Status != StatusBenched:
				return nil, bad("%s is on the benched list with status %q", name, r.Status)
			case list.want == "" && r.Status != StatusIn && r.Status != StatusProbation:
				return nil, bad("%s is on the routes list with status %q", name, r.Status)
			}
			for _, c := range []struct {
				field string
				to    *int
			}{{"n", &r.Cards}, {"prs", &r.PRs}, {"landed", &r.Landed}, {"useful", &r.Useful}, {"unpriced", &r.Unpriced}} {
				v, err := strconv.Atoi(h[p+c.field])
				if err != nil || v < 0 {
					return nil, bad("%s%s=%q is not a count", p, c.field, h[p+c.field])
				}
				*c.to = v
			}
			f.Routes = append(f.Routes, r)
		}
	}
	return f, nil
}

// ReadFold reads routes:<type> from the store in one HGETALL. (nil, nil) when
// no fold has written the type.
func ReadFold(ctx context.Context, rdb redis.Cmdable, workType string) (*Fold, error) {
	if !ValidType(workType) {
		return nil, fmt.Errorf("work type %q is not a work type ([a-z0-9_-])", workType)
	}
	h, err := rdb.HGetAll(ctx, FoldKey(workType)).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("read %s: %w", FoldKey(workType), err)
	}
	return ParseFold(workType, h)
}

// CheckFold is the router's check for one card when the fold has ranked its
// work type: nil when the card may run on its route, else an error whose text
// starts with REFUSED. f nil (no fold for the type) is Check alone. f for
// another type is refused: the caller read the wrong key.
func (t *Table) CheckFold(c Card, f *Fold) error {
	if f == nil {
		return t.Check(c)
	}
	refuse := func(why string) error {
		return fmt.Errorf("REFUSED route=%s rung=%s type=%s: %s", field(c.Route), field(c.Rung), field(c.Type), why)
	}
	if f.Type != c.Type {
		return refuse(fmt.Sprintf("the fold record is for type %s", field(f.Type)))
	}
	i, ok := t.byRoute[c.Route]
	if !ok {
		return refuse(fmt.Sprintf("route %s is not in the table; a new route enters by a measured A/B row", field(c.Route)))
	}
	row := t.rows[i]
	if row.Rung != c.Rung {
		return refuse(fmt.Sprintf("%s is a rung %s route", row.Route, row.Rung))
	}
	if row.Flag == FlagDead {
		return refuse(fmt.Sprintf("%s is dead: %s", row.Route, row.Why))
	}
	r, ok := f.Route(c.Route)
	switch {
	case !ok:
		return refuse(fmt.Sprintf("%s has no row in %s (fold %s); a route enters a type only by a fold", row.Route, FoldKey(f.Type), short(f.FoldSHA)))
	case r.Status == StatusBenched:
		return refuse(fmt.Sprintf("%s is benched for %s by fold %s: %s", row.Route, f.Type, short(f.FoldSHA), r.Reason))
	}
	return nil
}

// AllowedFold is the fold's order for one rung: the routes of that rung the
// router may deal the type to, best first. f nil is Allowed.
func (t *Table) AllowedFold(rung, workType string, f *Fold) []string {
	if f == nil {
		return t.Allowed(rung, workType)
	}
	var out []string
	for _, name := range f.Order() {
		if t.CheckFold(Card{Rung: rung, Type: workType, Route: name}, f) == nil {
			out = append(out, name)
		}
	}
	return out
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return field(sha)
}
