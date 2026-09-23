package decide

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Latency and wall clock belong in every decision row and on every line, and
// the summary reports their distribution (SPEC-DECIDE H3, #1624): ms is the
// provider round trip on a monotonic clock around the call alone, wall_ms is
// the verb's start to its line, and each is ABSENT, never zero, where there
// was no call or no measurement.
func TestLatencyAndWallClockInEveryRowAndOnEveryLine(t *testing.T) {
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

	// The summary prints the median and the 95th percentile over a fixture
	// log, per kind and per decider: ms 10,20,30,40 has median 20 and p95 40.
	// The tokens ride on the lines the summary already prints -- the kind's
	// own line, the deciders' on the finish -- so no line moves and no field
	// before them changes.
	lat := func(ms int) *int { v := ms; return &v }
	entries := []Entry{
		{Unit: "m1", Kind: KindRebase, Source: SourceJev, Ms: lat(10), RowanPick: "pro", Evidence: Unit{ID: "m1", Kind: KindRebase}},
		{Unit: "m2", Kind: KindRebase, Source: SourceJev, Ms: lat(20), RowanPick: "pro", Evidence: Unit{ID: "m2", Kind: KindRebase}},
		{Unit: "m3", Kind: KindRebase, Source: SourceJev, Ms: lat(30), RowanPick: "pro", Evidence: Unit{ID: "m3", Kind: KindRebase}},
		{Unit: "m4", Kind: KindRebase, Source: SourceJev, Ms: lat(40), RowanPick: "pro", Evidence: Unit{ID: "m4", Kind: KindRebase}},
		{Unit: "m5", Kind: KindRebase, Source: SourceRules, RowanPick: "flash", Evidence: Unit{ID: "m5", Kind: KindRebase}},
	}
	sum, err := Summarize(reg, entries)
	if err != nil {
		t.Fatal(err)
	}
	render := sum.Render()
	for _, want := range []string{
		"LOG kind=rebase decisions=5",
		"lat_n=4 median_ms=20 p95_ms=40",
		"lat_jev=4,20,40",
		"lat_rules=0,-,-",
	} {
		if !strings.Contains(render, want) {
			t.Errorf("the summary is missing %q:\n%s", want, render)
		}
	}
}
