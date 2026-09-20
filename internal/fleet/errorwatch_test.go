package fleet

import (
	"math"
	"sync"
	"testing"
)

// TestRollingErrorWatch tests the rolling error rate tracker for provider routes (#2010):
// - Rolling window (50 samples): 10 failures out of 50 = 20% error rate
// - Threshold tripping: threshold=0.15 trips when rate=0.20
// - Recovery when subsequent successes arrive
func TestRollingErrorWatch(t *testing.T) {
	const windowSize = 50
	const route = "anthropic/claude-3-5-sonnet"

	watch := NewRollingErrorWatch(windowSize)

	// Initially, no samples recorded: rate is 0.0 and not tripped
	if rate := watch.ErrorRate(route); rate != 0.0 {
		t.Fatalf("initial ErrorRate = %f, want 0.0", rate)
	}
	if watch.IsTripped(route, 0.15) {
		t.Fatalf("initial IsTripped(0.15) = true, want false")
	}

	// 1. Rolling window: record 10 failures out of 50 samples = 20% error rate
	// Record 40 successes and 10 failures
	for i := 0; i < 40; i++ {
		watch.RecordSuccess(route)
	}
	for i := 0; i < 10; i++ {
		watch.RecordFailure(route, true)
	}

	if samples := watch.Samples(route); samples != 50 {
		t.Fatalf("samples = %d, want 50", samples)
	}
	if failures := watch.Failures(route); failures != 10 {
		t.Fatalf("failures = %d, want 10", failures)
	}

	rate := watch.ErrorRate(route)
	if math.Abs(rate-0.20) > 1e-9 {
		t.Fatalf("ErrorRate = %f, want 0.20 (20%%)", rate)
	}

	// 2. Threshold tripping: threshold=0.15 trips when rate=0.20
	if !watch.IsTripped(route, 0.15) {
		t.Fatalf("IsTripped(0.15) = false at rate=0.20, want true")
	}
	if !watch.IsTripped(route, 0.20) {
		t.Fatalf("IsTripped(0.20) = false at rate=0.20, want true")
	}
	if watch.IsTripped(route, 0.25) {
		t.Fatalf("IsTripped(0.25) = true at rate=0.20, want false")
	}

	// 3. Recovery when subsequent successes arrive
	// Currently the window has 40 successes followed by 10 failures.
	// As we record more successes, the window rolls:
	// Adding 40 successes pushes out the 40 oldest successes; 10 failures remain.
	for i := 0; i < 40; i++ {
		watch.RecordSuccess(route)
	}
	if rate := watch.ErrorRate(route); math.Abs(rate-0.20) > 1e-9 {
		t.Fatalf("after 40 successes, ErrorRate = %f, want 0.20", rate)
	}
	if !watch.IsTripped(route, 0.15) {
		t.Fatalf("IsTripped(0.15) = false before failures roll out, want true")
	}

	// Adding 5 more successes pushes out 5 of the 10 failures:
	// Window now has 5 failures out of 50 samples = 10% error rate.
	for i := 0; i < 5; i++ {
		watch.RecordSuccess(route)
	}
	rate = watch.ErrorRate(route)
	if math.Abs(rate-0.10) > 1e-9 {
		t.Fatalf("after 5 more successes, ErrorRate = %f, want 0.10", rate)
	}
	// Error rate (0.10) is now below threshold (0.15): route has recovered!
	if watch.IsTripped(route, 0.15) {
		t.Fatalf("IsTripped(0.15) = true at rate=0.10, want false (recovered)")
	}

	// Adding another 5 successes pushes out the remaining 5 failures:
	for i := 0; i < 5; i++ {
		watch.RecordSuccess(route)
	}
	rate = watch.ErrorRate(route)
	if math.Abs(rate-0.0) > 1e-9 {
		t.Fatalf("after rolling out all failures, ErrorRate = %f, want 0.0", rate)
	}
	if watch.IsTripped(route, 0.15) {
		t.Fatalf("IsTripped(0.15) = true at rate=0.0, want false")
	}
}

// TestRollingErrorWatch_NonProviderErrors verifies that failures with
// isProviderError=false are not counted as provider faults, but are still
// counted as valid interaction samples in the denominator.
func TestRollingErrorWatch_NonProviderErrors(t *testing.T) {
	const windowSize = 50
	const route = "deepseek/v3"

	watch := NewRollingErrorWatch(windowSize)

	// Record 30 successes, 10 non-provider failures (e.g. card syntax error),
	// and 10 provider failures (e.g. 503 service unavailable).
	for i := 0; i < 30; i++ {
		watch.RecordSuccess(route)
	}
	for i := 0; i < 10; i++ {
		watch.RecordFailure(route, false)
	}
	for i := 0; i < 10; i++ {
		watch.RecordFailure(route, true)
	}

	if samples := watch.Samples(route); samples != 50 {
		t.Fatalf("samples = %d, want 50", samples)
	}
	if failures := watch.Failures(route); failures != 10 {
		t.Fatalf("failures = %d, want 10", failures)
	}

	rate := watch.ErrorRate(route)
	if math.Abs(rate-0.20) > 1e-9 {
		t.Fatalf("ErrorRate = %f, want 0.20 (20%%)", rate)
	}
	if !watch.IsTripped(route, 0.15) {
		t.Fatalf("IsTripped(0.15) = false, want true")
	}
}

// TestRollingErrorWatch_RouteIsolation verifies that error rates on one route
// do not affect another route.
func TestRollingErrorWatch_RouteIsolation(t *testing.T) {
	watch := NewRollingErrorWatch(20)

	// route-a has 100% failure
	for i := 0; i < 20; i++ {
		watch.RecordFailure("route-a", true)
	}
	// route-b has 100% success
	for i := 0; i < 20; i++ {
		watch.RecordSuccess("route-b")
	}

	if rate := watch.ErrorRate("route-a"); math.Abs(rate-1.0) > 1e-9 {
		t.Fatalf("route-a ErrorRate = %f, want 1.0", rate)
	}
	if !watch.IsTripped("route-a", 0.5) {
		t.Fatalf("route-a IsTripped(0.5) = false, want true")
	}

	if rate := watch.ErrorRate("route-b"); math.Abs(rate-0.0) > 1e-9 {
		t.Fatalf("route-b ErrorRate = %f, want 0.0", rate)
	}
	if watch.IsTripped("route-b", 0.5) {
		t.Fatalf("route-b IsTripped(0.5) = true, want false")
	}

	// Unknown route returns 0.0 and false
	if rate := watch.ErrorRate("unknown"); rate != 0.0 {
		t.Fatalf("unknown route ErrorRate = %f, want 0.0", rate)
	}
	if watch.IsTripped("unknown", 0.1) {
		t.Fatalf("unknown route IsTripped(0.1) = true, want false")
	}
}

// TestRollingErrorWatch_Concurrent ensures thread safety under concurrent access.
func TestRollingErrorWatch_Concurrent(t *testing.T) {
	watch := NewRollingErrorWatch(50)
	const goroutines = 8
	const iterations = 500

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			route := "concurrent-route"
			for i := 0; i < iterations; i++ {
				if i%5 == 0 {
					watch.RecordFailure(route, true)
				} else if i%5 == 1 {
					watch.RecordFailure(route, false)
				} else {
					watch.RecordSuccess(route)
				}
				_ = watch.ErrorRate(route)
				_ = watch.IsTripped(route, 0.25)
			}
		}(g)
	}

	wg.Wait()

	if samples := watch.Samples("concurrent-route"); samples != 50 {
		t.Fatalf("concurrent-route samples = %d, want 50 (window cap)", samples)
	}
}
