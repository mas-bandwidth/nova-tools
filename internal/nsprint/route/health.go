package route

// Route health (#2729): which routes the router may deal a card to right now.
//
// The allowed_routes table (allowed.go) says which routes a card may ever run
// on; route health takes away the ones that are failing tonight. Its rules are
// the ones PR #2718 proved in internal/swarm/routehealth, applied here to
// every attempt the router sees:
//
//  1. opencode's UnknownError, an err_xxxxxxxx id or a gateway 5xx is a
//     provider error at any wall. The launcher counted it only at 15 s or
//     less, so 264 of the 398 provider-incomplete cards of 2026-09-22 were
//     never charged to their route.
//  2. More than 5 of a route's last 50 attempts dying (a provider error, or
//     no model token and no error at all) benches the route.
//  3. A failed known-answer probe benches the route at once.
//  4. A bench is sticky: only a passing known-answer probe clears it, and the
//     route then starts on a clean window. Card passes and the window ageing
//     never do.
//
// Every bench and unbench returns a one-line receipt for the bus. A card that
// failed is redealt by hashing its label with the attempt number over the
// allowed list less the benched routes and the routes it must avoid, so a
// requeue never lands on the route that left it incomplete.

import (
	"fmt"
	"hash/fnv"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Class is what one attempt says about its route.
type Class string

const (
	ClassPass           Class = "pass"
	ClassProvider       Class = "provider-error"    // UnknownError, err_xxxxxxxx or a gateway 5xx, at any wall
	ClassNoTokenNoError Class = "no-token-no-error" // the route returned nothing and said nothing
	ClassFail           Class = "fail"              // the model answered and the card failed: the card's, not the route's
)

// Death reports whether the class counts against the route.
func (c Class) Death() bool { return c == ClassProvider || c == ClassNoTokenNoError }

const (
	// HealthWindow is how many of a route's recent attempts the death rate is taken over.
	HealthWindow = 50
	// HealthMaxDeaths is the most deaths in the window a route may have and stay dealt to (10%).
	HealthMaxDeaths = 5
)

// Attempt is one card attempt on a route, as the router sees it at card end.
type Attempt struct {
	Route       string
	At          time.Time
	Wall        time.Duration
	Tokens      int    // model tokens the attempt spent
	Tail        string // the attempt's last lines: the INCOMPLETE line and any error body
	Passed      bool
	KnownAnswer bool // the route's known-answer probe, not a card
}

var (
	providerRE = regexp.MustCompile(`\bUnknownError\b|\berr_[0-9a-f]{8}\b|(?i:\b(?:http|status)[ =:/]*5[0-9][0-9]\b|\bbad gateway\b|\bgateway time-?out\b|\bservice unavailable\b)`)
	errorRE    = regexp.MustCompile(`(?i)\b(?:error|refused|blocked|failed|fatal|panic|denied)\b`)
)

// Classify says what an attempt means for its route. The wall plays no part:
// a provider error at 3 s and at 1,502 s is the same provider error.
func Classify(a Attempt) Class {
	switch {
	case a.Passed:
		return ClassPass
	case providerRE.MatchString(a.Tail):
		return ClassProvider
	case a.Tokens == 0 && !errorRE.MatchString(a.Tail):
		return ClassNoTokenNoError
	default:
		return ClassFail
	}
}

// Health tracks every route's last HealthWindow attempts and its bench.
type Health struct {
	mu     sync.Mutex
	routes map[string]*routeHealth
	now    func() time.Time
}

type routeHealth struct {
	window  []bool // deaths among the last HealthWindow attempts, oldest first
	benched bool
	rule    string // why it is benched: probe or rate
}

// NewHealth returns an empty tracker: every route healthy.
func NewHealth() *Health {
	return &Health{routes: map[string]*routeHealth{}, now: func() time.Time { return time.Now().UTC() }}
}

func (h *Health) route(name string) *routeHealth {
	r := h.routes[name]
	if r == nil {
		r = &routeHealth{}
		h.routes[name] = r
	}
	return r
}

// Record applies one attempt to its route and returns the attempt's class and,
// when the attempt benched or unbenched the route, the receipt line for the bus.
func (h *Health) Record(a Attempt) (Class, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	at := a.At
	if at.IsZero() {
		at = h.now()
	}
	c := Classify(a)
	r := h.route(a.Route)

	if a.KnownAnswer && c == ClassPass {
		if !r.benched {
			r.push(false)
			return c, ""
		}
		was := r.rule
		*r = routeHealth{}
		return c, fmt.Sprintf("ROUTE-UNBENCHED route=%s rule=probe-passed was=%s at=%s", a.Route, was, at.UTC().Format(time.RFC3339))
	}

	r.push(c.Death())
	if r.benched {
		return c, ""
	}
	rule := ""
	switch {
	case a.KnownAnswer:
		rule = "probe"
	case c.Death() && r.deaths() > HealthMaxDeaths:
		rule = "rate"
	default:
		return c, ""
	}
	r.benched, r.rule = true, rule
	return c, fmt.Sprintf("ROUTE-BENCHED route=%s rule=%s deaths=%d/%d class=%s wall=%s at=%s",
		a.Route, rule, r.deaths(), HealthWindow, c, a.Wall, at.UTC().Format(time.RFC3339))
}

func (r *routeHealth) push(death bool) {
	r.window = append(r.window, death)
	if len(r.window) > HealthWindow {
		r.window = r.window[len(r.window)-HealthWindow:]
	}
}

func (r *routeHealth) deaths() int {
	n := 0
	for _, d := range r.window {
		if d {
			n++
		}
	}
	return n
}

// Benched reports whether the route is benched.
func (h *Health) Benched(route string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := h.routes[route]
	return r != nil && r.benched
}

// DeathRate returns the deaths among the route's last attempts and the window size.
func (h *Health) DeathRate(route string) (deaths, window int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r := h.routes[route]; r != nil {
		deaths = r.deaths()
	}
	return deaths, HealthWindow
}

// Report prints one HEALTH line per route seen, sorted by route.
func (h *Health) Report() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	names := make([]string, 0, len(h.routes))
	for n := range h.routes {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		r := h.routes[n]
		rule := r.rule
		if !r.benched {
			rule = "-"
		}
		fmt.Fprintf(&b, "HEALTH route=%s attempts=%d deaths=%d/%d benched=%t rule=%s\n", n, len(r.window), r.deaths(), HealthWindow, r.benched, rule)
	}
	return b.String()
}

// Deal is where a card's next attempt runs.
type Deal struct {
	Label, Rung, Type, Route string
	Attempt                  int
	Avoid                    []string
}

// Line is the ROUTE line the dealer writes for the attempt.
func (d Deal) Line() string {
	typ := d.Type
	if typ == "" {
		typ = "-"
	}
	avoid := strings.Join(d.Avoid, ",")
	if avoid == "" {
		avoid = "-"
	}
	return fmt.Sprintf("ROUTE card=%s rung=%s type=%s route=%s attempt=%d avoid=%s", d.Label, d.Rung, typ, d.Route, d.Attempt, avoid)
}

// Redeal picks the route for attempt number attempt of the card labelled
// label: the table's allowed list for the card's rung and work type, less the
// benched routes, the card's current route and every route in avoid, chosen
// by hashing the label with the attempt number. With nothing left it returns
// an error starting REFUSED; it never falls back to an avoided route.
func (h *Health) Redeal(t *Table, c Card, label string, attempt int, avoid ...string) (Deal, error) {
	d := Deal{Label: label, Rung: c.Rung, Type: c.Type, Attempt: attempt}
	skip := map[string]bool{}
	for _, r := range append([]string{c.Route}, avoid...) {
		if r != "" && !skip[r] {
			skip[r] = true
			d.Avoid = append(d.Avoid, r)
		}
	}
	var cands []string
	for _, r := range t.Allowed(c.Rung, c.Type) {
		if !skip[r] && !h.Benched(r) {
			cands = append(cands, r)
		}
	}
	if len(cands) == 0 {
		return d, fmt.Errorf("REFUSED card=%s rung=%s attempt=%d: every allowed route is benched or avoided (avoid=%s)",
			field(label), field(c.Rung), attempt, strings.Join(d.Avoid, ","))
	}
	f := fnv.New32a()
	f.Write([]byte(label + "\x00" + strconv.Itoa(attempt)))
	d.Route = cands[int(f.Sum32()%uint32(len(cands)))]
	return d, nil
}
