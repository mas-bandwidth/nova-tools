package fleet

import (
	"sync"
)

// RollingErrorWatch tracks a rolling window of execution outcomes per provider route
// to detect upstream provider failures (HTTP 5xx, rate limits, timeouts, etc.) and
// support circuit-breaking (#2010).
//
// Outcomes are recorded per route in a fixed-size ring buffer. Non-provider failures
// (e.g. card syntax errors or test assertion failures) do not count as provider faults,
// but count towards the denominator of requests handled by the route.
type RollingErrorWatch struct {
	mu         sync.RWMutex
	windowSize int
	routes     map[string]*routeWindow
}

// routeWindow is a ring buffer tracking outcomes for a single route.
type routeWindow struct {
	samples  []bool // true = provider failure, false = success or non-provider error
	head     int    // index of oldest sample when window is full
	count    int    // number of samples currently recorded (up to cap(samples))
	failures int    // number of provider failures currently in the window
}

// NewRollingErrorWatch constructs a RollingErrorWatch with the specified rolling window size.
// If windowSize <= 0, a default of 50 is used.
func NewRollingErrorWatch(windowSize int) *RollingErrorWatch {
	if windowSize <= 0 {
		windowSize = 50
	}
	return &RollingErrorWatch{
		windowSize: windowSize,
		routes:     make(map[string]*routeWindow),
	}
}

// RecordSuccess records a successful execution for the given route.
func (w *RollingErrorWatch) RecordSuccess(route string) {
	w.record(route, false)
}

// RecordFailure records an execution failure for the given route. If isProviderError is true,
// the failure is classified as a provider-side fault (e.g., 5xx, 429 rate limit, gateway error)
// and counts toward the route's error rate. If false, the run is recorded as an interaction
// without provider fault.
func (w *RollingErrorWatch) RecordFailure(route string, isProviderError bool) {
	w.record(route, isProviderError)
}

func (w *RollingErrorWatch) record(route string, isProviderError bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	rw, ok := w.routes[route]
	if !ok {
		rw = &routeWindow{
			samples: make([]bool, w.windowSize),
		}
		w.routes[route] = rw
	}
	rw.record(isProviderError)
}

func (rw *routeWindow) record(isFailure bool) {
	if rw.count < len(rw.samples) {
		rw.samples[rw.count] = isFailure
		rw.count++
		if isFailure {
			rw.failures++
		}
		return
	}

	// Full window: overwrite oldest entry at head
	if rw.samples[rw.head] {
		rw.failures--
	}
	rw.samples[rw.head] = isFailure
	if isFailure {
		rw.failures++
	}
	rw.head = (rw.head + 1) % len(rw.samples)
}

// ErrorRate returns the current provider error rate for the route as a float in [0.0, 1.0].
// Returns 0.0 if no samples have been recorded for the route.
func (w *RollingErrorWatch) ErrorRate(route string) float64 {
	w.mu.RLock()
	defer w.mu.RUnlock()

	rw, ok := w.routes[route]
	if !ok || rw.count == 0 {
		return 0.0
	}
	return rw.errorRate()
}

func (rw *routeWindow) errorRate() float64 {
	if rw.count == 0 {
		return 0.0
	}
	return float64(rw.failures) / float64(rw.count)
}

// IsTripped returns true if the route's error rate meets or exceeds the given threshold.
// If no samples have been recorded, or no failures have occurred, IsTripped returns false.
func (w *RollingErrorWatch) IsTripped(route string, threshold float64) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()

	rw, ok := w.routes[route]
	if !ok || rw.count == 0 || rw.failures == 0 {
		return false
	}
	return rw.errorRate() >= threshold
}

// Samples returns the number of recorded samples in the rolling window for the route.
func (w *RollingErrorWatch) Samples(route string) int {
	w.mu.RLock()
	defer w.mu.RUnlock()

	rw, ok := w.routes[route]
	if !ok {
		return 0
	}
	return rw.count
}

// Failures returns the number of provider failures in the rolling window for the route.
func (w *RollingErrorWatch) Failures(route string) int {
	w.mu.RLock()
	defer w.mu.RUnlock()

	rw, ok := w.routes[route]
	if !ok {
		return 0
	}
	return rw.failures
}

// Reset clears the rolling window for a specific route.
func (w *RollingErrorWatch) Reset(route string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	delete(w.routes, route)
}
