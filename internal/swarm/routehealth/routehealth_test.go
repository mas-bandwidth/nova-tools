package routehealth

import (
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

	// A passing probe clears the bench
	tr.RecordProbe(key, ProbeResult{At: now.Add(40 * time.Second), Passed: true})
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

func TestWindowPruneUnbenches(t *testing.T) {
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

	// Advance clock past the window
	clock = now.Add(70 * time.Second)
	if tr.IsBenched(key) {
		t.Fatal("route should unbench when errors expire from the window")
	}
}
