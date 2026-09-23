package routehealth

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
// Three of these within the window bench the route.
//
// Two further rules bench a route (the #2634 DONE-WHEN):
//   - a known-answer probe that fails benches the route at once;
//   - more than 10% of the last 50 attempts dying as a gateway 5xx or with no token and no
//     error (more than 5 of 50) benches the route.
//
// A bench is sticky: only a passing known-answer probe at the width the route was benched
// under clears it (a card pass, a pass at another width, or the window ageing out does not). SyncMarkers persists
// the state as the ROUTE-BENCHED-<route> files the fill loop already skips.

const (
	// OutcomeMachineryFail is the outcome token for a gateway death.
	OutcomeMachineryFail = "machinery-fail"
	// ClassGateway5xx is the class token for a gateway 5xx death.
	ClassGateway5xx = "gateway-5xx"
	// DefaultGatewayWindow is the window in which gateway errors are counted.
	DefaultGatewayWindow = 60 * time.Second
	// DefaultGatewayThreshold is the number of gateway errors that bench a route.
	DefaultGatewayThreshold = 3
	// DefaultRateWindow is how many recent attempts the death rate is taken over.
	DefaultRateWindow = 50
	// DefaultRateLimit is the death rate over the rate window above which a route is benched.
	DefaultRateLimit = 0.10
	// MarkerPrefix names the file the fill loop reads to skip a benched route.
	MarkerPrefix = "ROUTE-BENCHED-"
)

// ProbeResult is the outcome of one probe attempt on a route.
type ProbeResult struct {
	At           time.Time
	GatewayError bool
	Passed       bool
	// NoTokenNoError is an attempt that ended with no model token and no error.
	NoTokenNoError bool
	// KnownAnswer marks the route's known-answer probe; one that does not pass benches.
	KnownAnswer bool
	// Width is the width the attempt ran under; a bench is cleared only by a pass at the
	// width it was benched under.
	Width int
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
	// routeKey -> whether each of the last rateWindow attempts was a death, oldest first
	attempts map[string][]bool
	// routeKey -> the width the route was benched under
	benchWidth map[string]int

	rateWindow int
	rateLimit  float64

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

// WithRate sets the attempt window and the death rate above which a route is benched.
func WithRate(window int, limit float64) Option {
	return func(t *Tracker) { t.rateWindow, t.rateLimit = window, limit }
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
		attempts:      make(map[string][]bool),
		benchWidth:    make(map[string]int),
		rateWindow:    DefaultRateWindow,
		rateLimit:     DefaultRateLimit,
		now:           func() time.Time { return time.Now().UTC() },
	}
	for _, o := range opts {
		o(t)
	}
	return t
}

// RecordProbe records one attempt on a route and applies the bench rules: a failed
// known-answer probe, threshold gateway errors within the window, or a death rate above
// the limit over the last rateWindow attempts bench the route. Only a passing known-answer
// probe at the width the route was benched under clears the bench.
// It returns true if this attempt was a death (gateway 5xx or no token and no error).
func (t *Tracker) RecordProbe(routeKey string, result ProbeResult) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	if result.At.IsZero() {
		result.At = now
	}

	death := !result.Passed && (result.GatewayError || result.NoTokenNoError)
	if t.rateWindow > 0 {
		a := append(t.attempts[routeKey], death)
		if len(a) > t.rateWindow {
			a = a[len(a)-t.rateWindow:]
		}
		t.attempts[routeKey] = a
	}

	if result.Passed {
		delete(t.gatewayErrors, routeKey)
		if result.KnownAnswer && t.benched[routeKey] && t.benchWidth[routeKey] == result.Width {
			t.benched[routeKey] = false
			delete(t.benchWidth, routeKey)
			delete(t.attempts, routeKey)
		}
		return false
	}

	if result.KnownAnswer {
		t.bench(routeKey, result.Width)
	}

	if death {
		t.deaths[routeKey]++
	}
	if result.GatewayError {
		t.gatewayErrors[routeKey] = append(t.gatewayErrors[routeKey], result.At)
		t.prune(routeKey, now)
		if len(t.gatewayErrors[routeKey]) >= t.threshold {
			t.bench(routeKey, result.Width)
		}
	}
	if death && t.rateWindow > 0 {
		n := 0
		for _, d := range t.attempts[routeKey] {
			if d {
				n++
			}
		}
		if float64(n) > t.rateLimit*float64(t.rateWindow) {
			t.bench(routeKey, result.Width)
		}
	}
	return death
}

// bench marks a route benched under width; an already benched route keeps its width.
func (t *Tracker) bench(routeKey string, width int) {
	if !t.benched[routeKey] {
		t.benchWidth[routeKey] = width
	}
	t.benched[routeKey] = true
}

// DeathRate returns the deaths among the last rateWindow attempts and the window size.
func (t *Tracker) DeathRate(routeKey string) (deaths, window int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, d := range t.attempts[routeKey] {
		if d {
			deaths++
		}
	}
	return deaths, t.rateWindow
}

// MarkerName is the file name the fill loop checks to skip a benched route.
func MarkerName(routeKey string) string {
	return MarkerPrefix + strings.ReplaceAll(routeKey, "/", "_")
}

// SyncMarkers persists the bench state into dir: a ROUTE-BENCHED-<route> file for every
// benched route, and no such file for every known route that is not benched.
func (t *Tracker) SyncMarkers(dir string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	routes := make([]string, 0, len(t.benched))
	for r := range t.benched {
		routes = append(routes, r)
	}
	sort.Strings(routes)
	for _, r := range routes {
		p := filepath.Join(dir, MarkerName(r))
		if t.benched[r] {
			body := fmt.Sprintf("route=%s width=%d at=%s\n", r, t.benchWidth[r], t.now().UTC().Format(time.RFC3339))
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				return err
			}
			continue
		}
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
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
	delete(t.benchWidth, routeKey)
	delete(t.gatewayErrors, routeKey)
	delete(t.attempts, routeKey)
}

// DeathCount returns the total deaths (gateway 5xx or no token and no error) recorded for a route.
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

// prune removes gateway errors older than the window from the given route. It never
// clears a bench: only a passing known-answer probe at the benched width does.
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
