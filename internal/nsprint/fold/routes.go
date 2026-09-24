package fold

// routes.go ranks the routes per work type (nova-tools #3104, #2756 v6 4.10)
// and is the one writer of routes:<type>. PR-producing types rank on $ per
// landed card, read types on $ per useful (8+) read; every card of the route
// on the type is in the numerator, failures included. A PR type's route with
// fewer than route_min_landed landed is probation (the dealer caps it at
// probation_share of the type's cards); one with 0 landed over at least
// route_bench_after PRs is benched for the type with `landed 0 of <n>`, and
// the router refuses it (route.Table.CheckFold).

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/route"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
)

// The interim policy numbers of 4.10 (3); s:<S>:policy overrides each.
const (
	DefaultRouteMinLanded  = 3
	DefaultRouteBenchAfter = 15
	DefaultProbationShare  = "0.1"
)

// ReadTypes are the work types ranked on $ per useful read (4.10 (2)). Every
// other type produces PRs and ranks on $ per landed: an unknown type is held
// to landing, the stricter bar, never waved through on reads.
var ReadTypes = []string{"read", "rule", "cold", "probe", "schema-read"}

// KindOf is the kind of a work type: route.KindRead or route.KindPR.
func KindOf(workType string) string {
	for _, t := range ReadTypes {
		if t == workType {
			return route.KindRead
		}
	}
	return route.KindPR
}

// Policy is the sprint's routes policy.
type Policy struct {
	RouteMinLanded  int
	RouteBenchAfter int
	ProbationShare  string
}

func policyFrom(minLanded, benchAfter, share string) (Policy, error) {
	p := Policy{DefaultRouteMinLanded, DefaultRouteBenchAfter, DefaultProbationShare}
	count := func(name, v string, to *int) error {
		if v == "" {
			return nil
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return fmt.Errorf("policy %s=%q is not a count of at least 1", name, v)
		}
		*to = n
		return nil
	}
	if err := count("route_min_landed", minLanded, &p.RouteMinLanded); err != nil {
		return p, err
	}
	if err := count("route_bench_after", benchAfter, &p.RouteBenchAfter); err != nil {
		return p, err
	}
	if share != "" {
		f, err := strconv.ParseFloat(share, 64)
		if err != nil || f < 0 || f > 1 || math.IsNaN(f) {
			return p, fmt.Errorf("policy probation_share=%q is not a share 0-1", share)
		}
		p.ProbationShare = strconv.FormatFloat(f, 'f', -1, 64)
	}
	return p, nil
}

// typeTally is one route's cards on one work type.
type typeTally struct {
	cards, prs, landed, useful, priced int
	micro                              int64
}

func (t *typeTally) add(pr, landed, useful, priced bool, micro int64) {
	t.cards++
	if pr {
		t.prs++
	}
	if landed {
		t.landed++
	}
	if useful {
		t.useful++
	}
	if priced {
		t.priced++
		t.micro += micro
	}
}

// rankTypes turns the per type, per route tallies into one route.Fold per
// type, in type order. FoldSHA and At are the record's to fill.
func rankTypes(sprint string, byType map[string]map[string]*typeTally, p Policy) []*route.Fold {
	types := make([]string, 0, len(byType))
	for t := range byType {
		types = append(types, t)
	}
	sort.Strings(types)
	var out []*route.Fold
	for _, typ := range types {
		f := &route.Fold{
			Type: typ, Kind: KindOf(typ), Sprint: sprint,
			ProbationShare: p.ProbationShare, RouteMinLanded: p.RouteMinLanded, RouteBenchAfter: p.RouteBenchAfter,
		}
		f.Metric = route.MetricLanded
		if f.Kind == route.KindRead {
			f.Metric = route.MetricUseful
		}
		type ranked struct {
			row   route.FoldRoute
			value float64
		}
		var rows []ranked
		for name, t := range byType[typ] {
			denom := t.landed
			if f.Kind == route.KindRead {
				denom = t.useful
			}
			r := route.FoldRoute{
				Route: name, Cards: t.cards, PRs: t.prs, Landed: t.landed, Useful: t.useful,
				Unpriced: t.cards - t.priced, Metric: per(Route{Cards: t.cards, Priced: t.priced, USDMicro: t.micro}, denom), USD: tokens.Dash,
			}
			if t.priced > 0 {
				r.USD = tokens.Usd(t.micro)
			}
			switch {
			case f.Kind == route.KindRead:
				r.Status, r.Reason = route.StatusIn, fmt.Sprintf("useful %d of %d", t.useful, t.cards)
			case t.landed == 0 && t.prs >= p.RouteBenchAfter:
				r.Status, r.Reason = route.StatusBenched, fmt.Sprintf("landed 0 of %d", t.prs)
			case t.landed < p.RouteMinLanded:
				r.Status, r.Reason = route.StatusProbation, fmt.Sprintf("landed %d of %d, under route_min_landed %d", t.landed, t.prs, p.RouteMinLanded)
			default:
				r.Status, r.Reason = route.StatusIn, fmt.Sprintf("landed %d of %d", t.landed, t.prs)
			}
			value := math.Inf(1) // nothing to divide by, or no cost measured: last
			if denom > 0 && t.priced > 0 {
				value = float64(t.micro) / float64(denom)
			}
			rows = append(rows, ranked{r, value})
		}
		order := map[string]int{route.StatusIn: 0, route.StatusProbation: 1, route.StatusBenched: 2}
		sort.Slice(rows, func(i, j int) bool {
			a, b := rows[i], rows[j]
			if order[a.row.Status] != order[b.row.Status] {
				return order[a.row.Status] < order[b.row.Status]
			}
			if a.value != b.value {
				return a.value < b.value
			}
			return a.row.Route < b.row.Route
		})
		for _, r := range rows {
			f.Routes = append(f.Routes, r.row)
		}
		out = append(out, f)
	}
	return out
}

func commaList(names []string) string {
	if len(names) == 0 {
		return tokens.Dash
	}
	for i, n := range names {
		names[i] = oneline.Field(n)
	}
	return strings.Join(names, ",")
}

// printTypes prints one FOLD TYPE line per work type and one FOLD TYPE-ROUTE
// line per route of it, in rank order.
func printTypes(out io.Writer, s Summary) {
	for _, f := range s.Types {
		fmt.Fprintf(out, "FOLD TYPE sprint=%s type=%s kind=%s metric=%s routes=%s benched=%s\n",
			s.Sprint, oneline.Field(f.Type), f.Kind, f.Metric, commaList(f.Order()), commaList(f.Benched()))
		for _, r := range f.Routes {
			fmt.Fprintf(out, "FOLD TYPE-ROUTE sprint=%s type=%s route=%s status=%s %s=%s cards=%d prs=%d landed=%d useful=%d usd=%s unpriced=%d reason=%q\n",
				s.Sprint, oneline.Field(f.Type), oneline.Field(r.Route), r.Status, f.Metric, r.Metric,
				r.Cards, r.PRs, r.Landed, r.Useful, r.USD, r.Unpriced, r.Reason)
		}
	}
}

func typesSexp(b *strings.Builder, s Summary) {
	b.WriteString("  :types (")
	for i, f := range s.Types {
		if i > 0 {
			b.WriteString("\n           ")
		}
		fmt.Fprintf(b, "(type %s :kind %s :metric %s :routes (", q(f.Type), q(f.Kind), q(f.Metric))
		for j, r := range f.Routes {
			if j > 0 {
				b.WriteString(" ")
			}
			fmt.Fprintf(b, "(route %s :status %s :metric %s :cards %d :prs %d :landed %d :useful %d :usd %s :unpriced %d :reason %s)",
				q(r.Route), q(r.Status), q(r.Metric), r.Cards, r.PRs, r.Landed, r.Useful, q(r.USD), r.Unpriced, q(r.Reason))
		}
		b.WriteString("))")
	}
	b.WriteString(")")
}

// recordArgs is the routes:<type> keys and the ARGV tail the record script
// writes them from: per key, the count of field/value strings, then those.
func recordArgs(s Summary, sha, at string) (keys []string, argv []any) {
	for _, f := range s.Types {
		f.FoldSHA, f.At = sha, at
		fields := f.Fields()
		keys = append(keys, route.FoldKey(f.Type))
		argv = append(argv, len(fields))
		for _, v := range fields {
			argv = append(argv, v)
		}
	}
	return keys, argv
}
