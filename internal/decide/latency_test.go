package decide

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Latency and wall clock belong in every decision row and on every line
// (SPEC-DECIDE H3, #1624): ms is the
// provider round trip on a monotonic clock around the call alone, wall_ms is
// the verb's start to its line, and each is ABSENT, never zero, where there
// was no call or no measurement.
func TestLatencyAndWallClockInEveryRowAndOnEveryLine(t *testing.T) {
	t.Parallel()

	reg := testRegistry(t)

	// No call, no measurement: the rules answer carries no ms, the row omits
	// both fields rather than writing zero, and the line says ms=-.
	rules := mustRoute(t, reg, Unit{ID: "h3-rules", Kind: KindRebase, Files: 2, Packages: 1, Lanes: 1}, DefaultFloor)
	if rules.Usage.HasMs {
		t.Errorf("a rules answer made no provider call, yet it reports ms=%d", rules.Usage.Ms)
	}
	raw, err := json.Marshal(EntryFor(rules, Unit{ID: "h3-rules", Kind: KindRebase}, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err := json.Unmarshal(raw, &row); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"ms", "wall_ms"} {
		if _, ok := row[field]; ok {
			t.Errorf("a row with no call must omit %q, never write zero: %s", field, raw)
		}
	}
	if line := rules.Line(); !strings.Contains(line, "ms=-") {
		t.Errorf("a ROUTE line with no call must carry ms=-: %s", line)
	}

	// One call, one measurement: the fake is asked, ms is present on the
	// result, the row carries it, and the line names it.
	fake := &fakeDecider{choice: "rung-1", conf: 0.97}
	jev, err := RouteJev(context.Background(), fake, reg, Unit{ID: "h3-jev", Kind: KindNewVerb, Files: 3, Packages: 1, Lanes: 1}, DefaultFloor)
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls != 1 {
		t.Fatalf("the fixture made %d provider calls, want 1", fake.calls)
	}
	if !jev.Usage.HasMs {
		t.Error("a decision that called the provider reports no ms for the round trip")
	}
	jev.WallMs, jev.HasWallMs = 12, true
	raw, err = json.Marshal(EntryFor(jev, Unit{ID: "h3-jev", Kind: KindNewVerb}, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	row = nil
	if err := json.Unmarshal(raw, &row); err != nil {
		t.Fatal(err)
	}
	if _, ok := row["ms"]; !ok {
		t.Errorf("a row for a measured call must carry ms: %s", raw)
	}
	if got, ok := row["wall_ms"]; !ok || got != float64(12) {
		t.Errorf("a row for a verb that stamped its wall clock must carry wall_ms=12: %s", raw)
	}
	if line := jev.Line(); !strings.Contains(line, "ms=") || strings.Contains(line, "ms=-") {
		t.Errorf("a ROUTE line for a measured call must carry ms=<n>: %s", line)
	}
}
