// Package dealer is the one place, coordinator-side, that decides a card is ready for a
// bench (nova-tools #3251).
//
// Glenn, 2026-09-23: "If they have ready tasks in their queue, they should work on them,
// period. Pull those decisions and intelligence back out to the coordinator, where the
// decisions should be made." The trigger: three benches sat at ready=5 for 50 minutes while
// their fill loops held every card on a mis-parsed `DEPENDS-ON: -` (321 HELD lines), each
// bench deciding readiness again from its own partial view. So a bench's fill launches what
// is in its ready queue, up to its free slots, and nothing else (internal/pulse/fill.go);
// and a card enters that queue only through here, after every decision below was taken
// once, from the whole picture:
//
//  1. DEPENDENCIES. Every DEPENDS-ON parent has landed on the base. `DEPENDS-ON: -` is none.
//  2. BACKPRESSURE. A bulk card waits while the reading debt is over its cap; a priority
//     card flows (the fixes that shrink the debt).
//  3. LANE. At most one live card per lane: a card whose lane is live anywhere, or dealt
//     earlier in this pass, waits.
//  4. ROUTE. The route and model are picked HERE and written on the card as `ROUTE:` and
//     `MODEL:` lines; the launcher reads them and never picks.
//  5. BENCH. The bench is up, carries the card's LEG, has room (its free slots minus what
//     already waits in its ready queue minus what this pass dealt it), and its load per core
//     is under the ceiling. Among the benches that qualify, the one with the most room.
//
// A card that fails any of them is HELD, with its reason, and never moved: the rule this
// package keeps is that no card the dealer did not find ready ever reaches a ready queue.
// Plan is pure (every input is a value or a seam), so the rule is tested without a disk,
// a network or a clock; Apply is the only writer.
package dealer

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

// Card is one undealt card, read from its file.
type Card struct {
	Path      string
	Name      string   // the file name, card-<n>.md
	Kind      string   // KIND:, which the router keys on
	Leg       string   // LEG:, lower case; "" runs anywhere
	Lane      string   // LANE:; "" holds no lane
	DependsOn []string // DEPENDS-ON:, with "-" read as none
	Priority  bool     // a front-tier card: flows under backpressure
	Route     string   // ROUTE: already on the card (a pinned pick), else ""
	Model     string   // MODEL: already on the card, else ""
}

// ReadCard reads one card file. priority says the card came from a front tier.
func ReadCard(path string, priority bool) (Card, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Card{}, err
	}
	text := string(raw)
	return Card{
		Path:      path,
		Name:      filepath.Base(path),
		Kind:      pulse.CardField(text, "KIND"),
		Leg:       strings.ToLower(pulse.CardField(text, "LEG")),
		Lane:      pulse.CardField(text, "LANE"),
		DependsOn: pulse.ParseCardDependencies(text),
		Priority:  priority,
		Route:     pulse.CardField(text, "ROUTE"),
		Model:     pulse.CardField(text, "MODEL"),
	}, nil
}

// Bench is one consumer, as its Redis rows describe it (see BenchFromRows).
type Bench struct {
	Name     string
	Up       bool
	Legs     map[string]bool // the legs the bench carries; a card naming another is not dealt here
	Slots    int             // the bench's desired width
	Working  int             // cards live on it now
	Queued   int             // cards already waiting in its ready queue
	Load1    float64
	Cores    int
	LoadRead bool // the row carried a load and a core count the dealer could read
}

// room is how many more cards this bench may be dealt before this pass dealt it any.
func (b Bench) room() int {
	n := b.Slots - b.Working - b.Queued
	if n < 0 {
		return 0
	}
	return n
}

// carries says whether the bench has the leg. A card naming no leg runs anywhere; a bench
// with no declared legs carries only those.
func (b Bench) carries(leg string) bool {
	return leg == "" || b.Legs[leg]
}

// Policy is the fleet-wide numbers the dealer applies.
type Policy struct {
	// MaxLoadPerCore is the load ceiling: a bench above it is dealt nothing this pass, and
	// a bench whose load was not read is dealt nothing either (a ceiling on a number nobody
	// measured is a guess). 0 is no ceiling.
	MaxLoadPerCore float64
	// ReadingDebt and DebtCap are backpressure: while the debt is over the cap, bulk cards
	// wait and priority cards flow. A cap of 0 is no backpressure.
	ReadingDebt int
	DebtCap     int
}

// Deps answers whether a DEPENDS-ON parent has landed. pulse.GitAndResultsChecker is the
// real one.
type Deps interface {
	IsDependencyMerged(dep string) (bool, string)
}

// Router picks a card's route and model. The pick is DATA on the card: Apply writes it.
type Router interface {
	Route(c Card) (route, model string, err error)
}

// Deal is one card placed on one bench, with the route it will run on.
type Deal struct {
	Card  Card
	Bench string
	Route string
	Model string
}

// Held is one card the dealer did not find ready, and why. It stays where it is.
type Held struct {
	Card   Card
	Reason string
}

// Plan is one pass's answer.
type Plan struct {
	Deals []Deal
	Held  []Held
}

// PlanDeal decides one pass: priority cards first, then the rest, each in the order given.
// live maps a lane to the card holding it (every bench's launched cards, and every card
// already waiting in a bench's ready queue). It never touches a file.
func PlanDeal(cards []Card, benches []Bench, live map[string]string, p Policy, deps Deps, r Router) Plan {
	order := append([]Card(nil), cards...)
	sort.SliceStable(order, func(i, j int) bool { return order[i].Priority && !order[j].Priority })
	lanes := map[string]string{}
	for k, v := range live {
		lanes[k] = v
	}
	dealt := make([]int, len(benches))
	var plan Plan
	hold := func(c Card, why string) { plan.Held = append(plan.Held, Held{Card: c, Reason: why}) }
	for _, c := range order {
		if why := unlanded(c, deps); why != "" {
			hold(c, why)
			continue
		}
		if !c.Priority && p.DebtCap > 0 && p.ReadingDebt > p.DebtCap {
			hold(c, fmt.Sprintf("backpressure: reading debt %d over cap %d; bulk waits, priority flows", p.ReadingDebt, p.DebtCap))
			continue
		}
		if holder, ok := lanes[c.Lane]; c.Lane != "" && ok {
			hold(c, fmt.Sprintf("lane %s is live on %s", c.Lane, holder))
			continue
		}
		route, model, err := pick(c, r)
		if err != nil {
			hold(c, "no route: "+err.Error())
			continue
		}
		best := -1
		for i, b := range benches {
			if !b.Up || !b.carries(c.Leg) || overLoad(b, p) {
				continue
			}
			free := b.room() - dealt[i]
			if free <= 0 {
				continue
			}
			if best < 0 || free > benches[best].room()-dealt[best] {
				best = i
			}
		}
		if best < 0 {
			hold(c, fmt.Sprintf("no bench up with room under the load ceiling carries leg %q", c.Leg))
			continue
		}
		dealt[best]++
		if c.Lane != "" {
			lanes[c.Lane] = c.Name
		}
		plan.Deals = append(plan.Deals, Deal{Card: c, Bench: benches[best].Name, Route: route, Model: model})
	}
	return plan
}

// unlanded names the first DEPENDS-ON parent that has not landed, or "".
func unlanded(c Card, deps Deps) string {
	for _, d := range c.DependsOn {
		if deps == nil {
			return fmt.Sprintf("dependency %s: no checker to ask; refusing to guess it landed", d)
		}
		if ok, why := deps.IsDependencyMerged(d); !ok {
			return fmt.Sprintf("dependency %s not landed: %s", d, why)
		}
	}
	return ""
}

// pick keeps a route already on the card (a pinned pick), else asks the router. A pick with
// either half empty is no pick.
func pick(c Card, r Router) (string, string, error) {
	if c.Route != "" && c.Model != "" {
		return c.Route, c.Model, nil
	}
	if r == nil {
		return "", "", fmt.Errorf("no router")
	}
	route, model, err := r.Route(c)
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(route) == "" || strings.TrimSpace(model) == "" {
		return "", "", fmt.Errorf("the router answered route=%q model=%q", route, model)
	}
	return route, model, nil
}

// overLoad says the bench is over the ceiling, or its load was never read while a ceiling
// is set.
func overLoad(b Bench, p Policy) bool {
	if p.MaxLoadPerCore <= 0 {
		return false
	}
	if !b.LoadRead || b.Cores <= 0 {
		return true
	}
	return b.Load1/float64(b.Cores) > p.MaxLoadPerCore
}

// BenchFromRows builds a Bench from its two Redis hashes: `bench:<b>` (bench-row, every
// second: working, load1, ncpu, dealer_queue) and `bench:<b>:desired` (slots, legs, comma
// separated). up is the measured presence (the row's TTL has not lapsed).
func BenchFromRows(name string, up bool, row, desired map[string]string) Bench {
	b := Bench{Name: name, Up: up, Legs: map[string]bool{}}
	b.Slots, _ = strconv.Atoi(strings.TrimSpace(desired["slots"]))
	for _, l := range strings.Split(desired["legs"], ",") {
		if l = strings.ToLower(strings.TrimSpace(l)); l != "" {
			b.Legs[l] = true
		}
	}
	b.Working, _ = strconv.Atoi(strings.TrimSpace(row["working"]))
	b.Queued, _ = strconv.Atoi(strings.TrimSpace(row["dealer_queue"]))
	load, lerr := strconv.ParseFloat(strings.TrimSpace(row["load1"]), 64)
	cores, cerr := strconv.Atoi(strings.TrimSpace(row["ncpu"]))
	if lerr == nil && cerr == nil && load >= 0 && cores > 0 {
		b.Load1, b.Cores, b.LoadRead = load, cores, true
	}
	return b
}

// TableRouter is the route table: rows of kind -> (route, model), with "*" the kind any
// card falls back to. Among the rows for a kind the pick is a hash of the card name, so a
// spread of routes is a wider fleet and a re-deal of the same card lands on the same route.
type TableRouter struct {
	Rows []RouteRow
}

// RouteRow is one row of the route table.
type RouteRow struct{ Kind, Route, Model string }

// ReadRouteTable reads `<kind>\t<route>\t<model>` per line; `#` and blank lines are skipped.
func ReadRouteTable(path string) (TableRouter, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return TableRouter{}, err
	}
	var t TableRouter
	for n, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 3 || strings.TrimSpace(f[1]) == "" || strings.TrimSpace(f[2]) == "" {
			return TableRouter{}, fmt.Errorf("%s:%d: want <kind>\\t<route>\\t<model>, got %q", path, n+1, line)
		}
		t.Rows = append(t.Rows, RouteRow{Kind: strings.TrimSpace(f[0]), Route: strings.TrimSpace(f[1]), Model: strings.TrimSpace(f[2])})
	}
	return t, nil
}

// Route picks among the card kind's rows, else the "*" rows.
func (t TableRouter) Route(c Card) (string, string, error) {
	var rows []RouteRow
	for _, want := range []string{c.Kind, "*"} {
		for _, r := range t.Rows {
			if r.Kind == want && want != "" {
				rows = append(rows, r)
			}
		}
		if len(rows) > 0 {
			break
		}
	}
	if len(rows) == 0 {
		return "", "", fmt.Errorf("the route table has no row for kind %q and no * row", c.Kind)
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(c.Name))
	r := rows[int(h.Sum32()%uint32(len(rows)))]
	return r.Route, r.Model, nil
}

// Apply carries out a plan: each dealt card gets its ROUTE: and MODEL: lines written, then
// is renamed into the ready queue readyDir(bench) names. The rename is the move; a card
// another hand took first is skipped. It answers the deals it made.
func Apply(plan Plan, readyDir func(bench string) string) ([]Deal, error) {
	var done []Deal
	for _, d := range plan.Deals {
		dir := readyDir(d.Bench)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return done, err
		}
		if err := markRoute(d.Card.Path, d.Route, d.Model); err != nil {
			continue // the card left under us: nothing to deal
		}
		if err := os.Rename(d.Card.Path, filepath.Join(dir, d.Card.Name)); err != nil {
			continue
		}
		done = append(done, d)
	}
	return done, nil
}

// markRoute rewrites a card in place with exactly one ROUTE: and one MODEL: line, written
// through a temporary file and a rename so a reader never sees half a card.
func markRoute(path, route, model string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var keep []string
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "ROUTE:") || strings.HasPrefix(t, "MODEL:") {
			continue
		}
		keep = append(keep, line)
	}
	keep = append(keep, "ROUTE: "+route, "MODEL: "+model)
	tmp := path + ".dealing"
	if err := os.WriteFile(tmp, []byte(strings.Join(keep, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
