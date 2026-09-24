package pulse

import (
	"bytes"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/metrics"
	"github.com/mas-bandwidth/nova-tools/internal/metrics/metricstest"
)

// TestFillExportsMetrics (nx-g61, recut of #2720): one fill tick over three
// ready cards on a bench with room for two exports the queue left behind
// (1), the cards out on the bench (2) and one launcher latency per launch,
// and the registered /metrics handler serves them.
func TestFillExportsMetrics(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	for i := 1; i <= 3; i++ {
		writeCard(t, ready, fmt.Sprintf("card-00%d.md", i), fmt.Sprintf("RESULT: CARD-%d\n", i))
	}
	set := metrics.New()
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched,
		Machines: machinesFile(t, dir, []string{"bench-a"}, nil),
		Benches:  []string{"bench-a"},
		Once:     true,
		Stdout:   &out,
		Stderr:   &errb,
		Capacity: laneCap{"bench-a": 2},
		Launcher: &laneLauncher{},
		Metrics:  set,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	metricstest.Want(t, metricstest.Scrape(t, set),
		`nova_queue_depth{component="fill"} 1`,
		`nova_leases_held{component="fill"} 2`,
		`nova_provider_latency_seconds_count{component="fill",provider="bench-a"} 2`,
	)
}
