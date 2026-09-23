package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/metrics/metricstest"
)

// TestFillVerbServesMetrics (nx-g61, #2720; Stella's hold on #3259): the
// production `nova-pulse fill --metrics-addr` serves /metrics itself. One
// --once tick over three ready cards on a bench with room for two exports the
// queue it left (1), the cards out on the bench (2) and one launcher latency
// per launch, and a GET on the verb's own listener, as it finishes, reads them.
func TestFillVerbServesMetrics(t *testing.T) {
	specs := fakePATH(t)
	fakeTool(t, specs, "nova-swarm", fakeSpec{Default: fakeRule{Exit: 0}})
	dir := t.TempDir()
	const bench = "bench-metrics"
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	fillReady(t, ready, 3)
	scraped := metricstest.AtClose(t)

	var out, errb bytes.Buffer
	code := run([]string{"fill",
		"--ready", ready, "--launched", launched,
		"--machines", fillMachines(t, dir, bench),
		"--bench", bench, "--capacity", "2", "--once",
		"--launcher", filepath.Join(fakeBins(t), "nova-swarm"+exeSuffix()),
		"--metrics-addr", "127.0.0.1:0",
	}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stdout=%q stderr=%q", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "FILL METRICS url=http://127.0.0.1:") {
		t.Fatalf("stdout %q, want the FILL METRICS url line", out.String())
	}
	body := scraped()
	if body == "" {
		t.Fatalf("fill never served /metrics; stdout=%q", out.String())
	}
	metricstest.Want(t, body,
		`nova_queue_depth{component="fill"} 1`,
		`nova_leases_held{component="fill"} 2`,
		`nova_provider_latency_seconds_count{component="fill",provider="bench-metrics"} 2`,
	)
}

// TestFillVerbRefusesABusyMetricsAddr: an address that cannot be bound is a
// refusal before any card moves, never a fill that silently exports nothing.
func TestFillVerbRefusesABusyMetricsAddr(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	fillReady(t, ready, 1)
	var out, errb bytes.Buffer
	code := run([]string{"fill", "--ready", ready, "--launched", launched,
		"--machines", fillMachines(t, dir, "bench-a"), "--bench", "bench-a",
		"--capacity", "1", "--once", "--metrics-addr", "not-a-host-port",
	}, &out, &errb, time.Now().UTC())
	if code != 2 || !strings.Contains(errb.String(), "FILL REFUSED --metrics-addr") {
		t.Fatalf("exit %d stderr %q, want 2 and FILL REFUSED --metrics-addr", code, errb.String())
	}
	if fillCount(t, ready) != 1 {
		t.Fatalf("ready holds %d cards, want 1: a refused fill moves nothing", fillCount(t, ready))
	}
}
