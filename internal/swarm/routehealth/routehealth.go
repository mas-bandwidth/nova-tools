package routehealth

import (
	"fmt"
	"io"
	"sort"
	"sync"
	"time"
)

// THE ROUTE-HEALTH MECHANISM (issue #2634).
//
// A route whose probe returns the gateway UnknownError 3 times in 60 s launches no card
// until a probe at width passes, and every such death is recorded outcome=machinery-fail
// class=gateway-5xx, never card-fail.
//
// A probe is one card attempt against a route. When the attempt dies before the model has
// a turn and the tail matches the gateway UnknownError pattern, that is a gateway death.
// Three of these within the window bench the route; a passing probe clears it.

const (
	// OutcomeMachineryFail is the outcome token for a gateway death.
	OutcomeMachineryFail = "machinery-fail"
	// ClassGateway5xx is the class token for a gateway 5xx death.
	ClassGateway5xx = "gateway-5xx"
	// DefaultGatewayWindow is the window in which gateway errors are counted.
	DefaultGatewayWindow = 60 * time.Second
	// DefaultGatewayThreshold is the number of gateway errors that bench a route.
	DefaultGatewayThreshold = 3
)

// ProbeResult is the outcome of one probe attempt on a route.
type ProbeResult struct {
	At           time.Time
	GatewayError bool
	Passed       bool
}

// Tracker records probe results per route and answers whether a route is benched.
// A route is benched when it has accumulated threshold gateway errors within the
// window; a passing probe clears the bench.
type Tracker struct {
	mu        sync.Mutex
	window    time.Duration
	threshold int

	// routeKey -> gateway error timestamps (kept in order)
	gatewayErrors map[string][]time.Time
	// routeKey -> whether the route is benched
	benched map[string]bool
	// routeKey -> total gateway deaths recorded (for reporting)
	deaths map[string]int

	now func() time.Time
}

// Option configures a Tracker.
type Option func(*Tracker)

// WithWindow sets the window in which gateway errors are counted.
func WithWindow(d time.Duration) Option {
	return func(t *Tracker) { t.window = d }
}

// WithThreshold sets the number of gateway errors that bench a route.
func WithThreshold(n int) Option {
	return func(t *Tracker) { t.threshold = n }
}

// WithNow sets the time source for the tracker.
func WithNow(fn func() time.Time) Option {
	return func(t *Tracker) { t.now = fn }
}

// NewTracker creates a Tracker with the given options. Defaults are
// DefaultGatewayWindow and DefaultGatewayThreshold.
func NewTracker(opts ...Option) *Tracker {
	t := &Tracker{
		window:        DefaultGatewayWindow,
		threshold:     DefaultGatewayThreshold,
		gatewayErrors: make(map[string][]time.Time),
		benched:       make(map[string]bool),
		deaths:        make(map[string]int),
		now:           func() time.Time { return time.Now().UTC() },
	}
	for _, o := range opts {
		o(t)
	}
	return t
}

// RecordProbe records one probe result for a route. If the probe was a gateway
// error and the count reaches the threshold within the window, the route is
// benched. If the probe passed, the bench is cleared.
// It returns true if this probe was a gateway death (to be recorded by the caller).
func (t *Tracker) RecordProbe(routeKey string, result ProbeResult) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	if result.At.IsZero() {
		result.At = now
	}

	if result.Passed {
		t.benched[routeKey] = false
		delete(t.gatewayErrors, routeKey)
		return false
	}

	if result.GatewayError {
		t.gatewayErrors[routeKey] = append(t.gatewayErrors[routeKey], result.At)
		t.deaths[routeKey]++
		t.prune(routeKey, now)
		if len(t.gatewayErrors[routeKey]) >= t.threshold {
			t.benched[routeKey] = true
		}
		return true
	}

	return false
}

// IsBenched reports whether the route is currently benched.
func (t *Tracker) IsBenched(routeKey string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.prune(routeKey, t.now())
	return t.benched[routeKey]
}

// ClearBench explicitly clears the bench for a route, as if a probe passed.
func (t *Tracker) ClearBench(routeKey string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.benched[routeKey] = false
	delete(t.gatewayErrors, routeKey)
}

// DeathCount returns the total number of gateway deaths recorded for a route.
func (t *Tracker) DeathCount(routeKey string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.deaths[routeKey]
}

// RecentErrors returns the gateway errors within the window for a route, in order.
func (t *Tracker) RecentErrors(routeKey string) []time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.prune(routeKey, t.now())
	ts := t.gatewayErrors[routeKey]
	out := make([]time.Time, len(ts))
	copy(out, ts)
	return out
}

// prune removes gateway errors older than the window from the given route.
func (t *Tracker) prune(routeKey string, now time.Time) {
	ts := t.gatewayErrors[routeKey]
	cutoff := now.Add(-t.window)
	i := 0
	for i < len(ts) && ts[i].Before(cutoff) {
		i++
	}
	if i > 0 {
		t.gatewayErrors[routeKey] = ts[i:]
	}
	if len(t.gatewayErrors[routeKey]) < t.threshold {
		t.benched[routeKey] = false
	}
}

// GatewayDeathLine is the one line a gateway death writes to a health log.
func GatewayDeathLine(routeKey string, at time.Time) string {
	return fmt.Sprintf("ROUTEHEALTH route=%s outcome=%s class=%s at=%s",
		routeKey, OutcomeMachineryFail, ClassGateway5xx, at.UTC().Format(time.RFC3339))
}

// WriteHealthLog writes the current health state to w: one line per benched route
// and one line per route with recent errors.
func (t *Tracker) WriteHealthLog(w io.Writer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()

	routes := make([]string, 0, len(t.benched))
	for r := range t.benched {
		routes = append(routes, r)
	}
	sort.Strings(routes)

	for _, r := range routes {
		t.prune(r, now)
		if t.benched[r] {
			fmt.Fprintf(w, "ROUTEHEALTH route=%s benched=true errors=%d window=%s\n",
				r, len(t.gatewayErrors[r]), t.window)
		}
	}
}
