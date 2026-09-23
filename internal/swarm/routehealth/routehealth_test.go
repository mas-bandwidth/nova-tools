package routehealth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestThreeGatewayErrorsBenchesRoute(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	tr := NewTracker(WithNow(func() time.Time { return now }))

	key := "model@profile"

	// Two gateway errors: not yet benched
	tr.RecordProbe(key, ProbeResult{At: now, GatewayError: true})
	tr.RecordProbe(key, ProbeResult{At: now.Add(10 * time.Second), GatewayError: true})
	if tr.IsBenched(key) {
		t.Fatal("route should not be benched after 2 gateway errors")
	}

	// Third gateway error within 60s: benched
	tr.RecordProbe(key, ProbeResult{At: now.Add(20 * time.Second), GatewayError: true})
	if !tr.IsBenched(key) {
		t.Fatal("route should be benched after 3 gateway errors within the window")
	}
}

func TestPassingProbeClearsBench(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	tr := NewTracker(WithNow(func() time.Time { return now }))

	key := "model@profile"

	// Three gateway errors: benched
	for i := 0; i < 3; i++ {
		tr.RecordProbe(key, ProbeResult{At: now.Add(time.Duration(i*10) * time.Second), GatewayError: true})
	}
	if !tr.IsBenched(key) {
		t.Fatal("route should be benched")
	}

	// A passing card attempt is not a probe: the bench stays
	tr.RecordProbe(key, ProbeResult{At: now.Add(35 * time.Second), Passed: true})
	if !tr.IsBenched(key) {
		t.Fatal("a card pass must not clear the bench; only the known-answer probe does")
	}

	// A passing known-answer probe clears the bench
	tr.RecordProbe(key, ProbeResult{At: now.Add(40 * time.Second), Passed: true, KnownAnswer: true})
	if tr.IsBenched(key) {
		t.Fatal("a passing probe should clear the bench")
	}
}

func TestGatewayErrorsOutsideWindowDoNotBench(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	tr := NewTracker(WithNow(func() time.Time { return now }))

	key := "model@profile"

	// Two gateway errors early
	tr.RecordProbe(key, ProbeResult{At: now.Add(-90 * time.Second), GatewayError: true})
	tr.RecordProbe(key, ProbeResult{At: now.Add(-80 * time.Second), GatewayError: true})

	// One gateway error now: the old ones are outside the window
	tr.RecordProbe(key, ProbeResult{At: now, GatewayError: true})
	if tr.IsBenched(key) {
		t.Fatal("route should not be benched when errors are outside the window")
	}
}

func TestClearBenchExplicitly(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	tr := NewTracker(WithNow(func() time.Time { return now }))

	key := "model@profile"

	for i := 0; i < 3; i++ {
		tr.RecordProbe(key, ProbeResult{At: now.Add(time.Duration(i*10) * time.Second), GatewayError: true})
	}
	if !tr.IsBenched(key) {
		t.Fatal("route should be benched")
	}

	tr.ClearBench(key)
	if tr.IsBenched(key) {
		t.Fatal("ClearBench should unbench the route")
	}
}

func TestRecordProbeReturnsGatewayDeath(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	tr := NewTracker(WithNow(func() time.Time { return now }))

	key := "model@profile"

	isDeath := tr.RecordProbe(key, ProbeResult{At: now, GatewayError: true})
	if !isDeath {
		t.Fatal("a gateway error probe should return true (gateway death)")
	}

	isDeath = tr.RecordProbe(key, ProbeResult{At: now.Add(10 * time.Second), Passed: true})
	if isDeath {
		t.Fatal("a passing probe should not return true (not a gateway death)")
	}
}

func TestDeathCountTracksAllGatewayDeaths(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	tr := NewTracker(WithNow(func() time.Time { return now }))

	key := "model@profile"

	for i := 0; i < 5; i++ {
		tr.RecordProbe(key, ProbeResult{At: now.Add(time.Duration(i*10) * time.Second), GatewayError: true})
	}
	if c := tr.DeathCount(key); c != 5 {
		t.Fatalf("death count = %d, want 5", c)
	}
}

func TestGatewayDeathLine(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	line := GatewayDeathLine("model@profile", now)
	if !strings.Contains(line, "outcome=machinery-fail") {
		t.Fatalf("line missing outcome=machinery-fail: %s", line)
	}
	if !strings.Contains(line, "class=gateway-5xx") {
		t.Fatalf("line missing class=gateway-5xx: %s", line)
	}
	if !strings.Contains(line, "route=model@profile") {
		t.Fatalf("line missing route: %s", line)
	}
}

func TestWriteHealthLog(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	tr := NewTracker(WithNow(func() time.Time { return now }))

	key := "model@profile"
	for i := 0; i < 3; i++ {
		tr.RecordProbe(key, ProbeResult{At: now.Add(time.Duration(i*10) * time.Second), GatewayError: true})
	}

	var buf strings.Builder
	tr.WriteHealthLog(&buf)
	if !strings.Contains(buf.String(), "benched=true") {
		t.Fatalf("health log should show benched route: %s", buf.String())
	}
}

func TestWindowAgeingDoesNotUnbench(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	clock := now
	tr := NewTracker(WithNow(func() time.Time { return clock }))

	key := "model@profile"

	// Three gateway errors within window: benched
	for i := 0; i < 3; i++ {
		tr.RecordProbe(key, ProbeResult{At: now.Add(time.Duration(i*10) * time.Second), GatewayError: true})
	}
	if !tr.IsBenched(key) {
		t.Fatal("route should be benched")
	}

	// Advance clock past the window: only a passing probe unbenches (#2634)
	clock = now.Add(70 * time.Second)
	if !tr.IsBenched(key) {
		t.Fatal("route must stay benched when errors age out of the window")
	}
}

// Rule 1 (#2634): a failed known-answer probe benches the route on its own.
func TestFailedKnownAnswerProbeBenches(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	tr := NewTracker(WithNow(func() time.Time { return now }))

	tr.RecordProbe("plain", ProbeResult{At: now})
	if tr.IsBenched("plain") {
		t.Fatal("a failed ordinary attempt must not bench")
	}

	tr.RecordProbe("ka", ProbeResult{At: now, KnownAnswer: true, Width: 8})
	if !tr.IsBenched("ka") {
		t.Fatal("a failed known-answer probe must bench the route")
	}
}

// deathsAmong records n attempts spaced two minutes apart (so the 3-in-60 s rule never
// fires), of which the ones at the given indexes die with no token and no error.
func deathsAmong(tr *Tracker, key string, start time.Time, n int, dead map[int]bool) {
	for i := 0; i < n; i++ {
		at := start.Add(time.Duration(i) * 2 * time.Minute)
		if dead[i] {
			tr.RecordProbe(key, ProbeResult{At: at, NoTokenNoError: true, Width: 8})
		} else {
			tr.RecordProbe(key, ProbeResult{At: at, Passed: true, Width: 8})
		}
	}
}

// Rule 2 (#2634): more than 10% deaths over the last 50 attempts benches the route.
func TestDeathRateOverLast50Benches(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)

	five := map[int]bool{3: true, 13: true, 23: true, 33: true, 43: true}
	tr := NewTracker(WithNow(func() time.Time { return now }))
	deathsAmong(tr, "r", now, 50, five)
	if tr.IsBenched("r") {
		t.Fatal("5 deaths in 50 attempts (10%) must not bench")
	}

	six := map[int]bool{3: true, 11: true, 19: true, 27: true, 35: true, 43: true}
	tr = NewTracker(WithNow(func() time.Time { return now }))
	deathsAmong(tr, "r", now, 50, six)
	if !tr.IsBenched("r") {
		d, w := tr.DeathRate("r")
		t.Fatalf("6 deaths in 50 attempts (12%%) must bench; rate %d/%d", d, w)
	}

	// Gateway 5xx counts toward the same rate.
	tr = NewTracker(WithNow(func() time.Time { return now }))
	for i := 0; i < 50; i++ {
		at := now.Add(time.Duration(i) * 2 * time.Minute)
		tr.RecordProbe("g", ProbeResult{At: at, GatewayError: i%8 == 0, Passed: i%8 != 0, Width: 8})
	}
	if !tr.IsBenched("g") {
		t.Fatal("7 gateway deaths in 50 attempts must bench")
	}
}

// Rule 2 is a sliding window: deaths older than the last 50 attempts do not count.
func TestDeathRateSlidesOverLast50(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	tr := NewTracker(WithNow(func() time.Time { return now }))
	// 3 early deaths, then 50 attempts with 3 more: never more than 5 in any 50.
	dead := map[int]bool{0: true, 1: true, 2: true, 60: true, 70: true, 80: true}
	deathsAmong(tr, "r", now, 100, dead)
	if tr.IsBenched("r") {
		t.Fatal("deaths outside the last 50 attempts must not count")
	}
	if d, _ := tr.DeathRate("r"); d != 3 {
		t.Fatalf("deaths in window = %d, want 3", d)
	}
}

// Unbench only by a passing probe under the same width (#2634).
func TestPassAtOtherWidthDoesNotUnbench(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	tr := NewTracker(WithNow(func() time.Time { return now }))

	tr.RecordProbe("r", ProbeResult{At: now, KnownAnswer: true, Width: 16})
	tr.RecordProbe("r", ProbeResult{At: now, KnownAnswer: true, Passed: true, Width: 1})
	if !tr.IsBenched("r") {
		t.Fatal("a pass at width 1 must not clear a bench taken at width 16")
	}
	tr.RecordProbe("r", ProbeResult{At: now, KnownAnswer: true, Passed: true, Width: 16})
	if tr.IsBenched("r") {
		t.Fatal("a pass at the benched width must clear the bench")
	}
}

// The bench persists as the ROUTE-BENCHED-<route> file the fill loop skips.
func TestSyncMarkersPersistsBench(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	tr := NewTracker(WithNow(func() time.Time { return now }))
	dir := t.TempDir()
	key := "inception/mercury-2.5"
	marker := filepath.Join(dir, "ROUTE-BENCHED-inception_mercury-2.5")
	if MarkerName(key) != filepath.Base(marker) {
		t.Fatalf("MarkerName = %s", MarkerName(key))
	}

	tr.RecordProbe(key, ProbeResult{At: now, KnownAnswer: true, Width: 8})
	if err := tr.SyncMarkers(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("benched route has no marker: %v", err)
	}

	tr.RecordProbe(key, ProbeResult{At: now, KnownAnswer: true, Passed: true, Width: 8})
	if err := tr.SyncMarkers(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("unbenched route still has a marker: %v", err)
	}
}
