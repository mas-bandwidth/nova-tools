package land_test

import (
	"context"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/metrics"
	"github.com/mas-bandwidth/nova-tools/internal/metrics/metricstest"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
)

// TestLandExportsMetrics (nx-g61, recut of #2720): a lane over a batch of
// three where one member stays UNKNOWN exports the batch depth (3), the
// members admitted to the gate (2), and one forge latency per mergeable read
// plus one gate latency, and the registered /metrics handler serves them.
func TestLandExportsMetrics(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{at: time.Date(2026, 9, 23, 17, 0, 0, 0, time.UTC)}
	set := metrics.New()
	ln := land.Lane{
		Gate:   &scriptGate{verdicts: []land.Verdict{{OK: true}}},
		Bisect: &scriptBisect{},
		Forge: &scriptForge{seq: map[int][]string{
			1: {"MERGEABLE"}, 2: {"MERGEABLE"}, 3: {"UNKNOWN", "UNKNOWN", "UNKNOWN"},
		}},
		Land: &recordLand{}, Store: land.NewMemory(), Filer: &scriptFiler{}, Clock: clock,
		Metrics: set,
	}
	res, err := ln.Run(context.Background(), land.Batch{Repo: "nova-tools", Name: "b1",
		Members: []land.Member{{Number: 1, Head: "a"}, {Number: 2, Head: "b"}, {Number: 3, Head: "c"}}})
	if err != nil {
		t.Fatalf("lane: %v", err)
	}
	if !res.Landed {
		t.Fatalf("lane did not land: %+v", res)
	}
	metricstest.Want(t, metricstest.Scrape(t, set),
		`nova_queue_depth{component="lander"} 3`,
		`nova_leases_held{component="lander"} 2`,
		`nova_provider_latency_seconds_count{component="lander",provider="forge"} 5`,
		`nova_provider_latency_seconds_count{component="lander",provider="gate"} 1`,
	)
}
