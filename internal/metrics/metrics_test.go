package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// scrape GETs /metrics from a mux the set registered itself on, the way
// Prometheus reaches a verb, and returns the exposition body.
func scrape(t *testing.T, s *Set) string {
	t.Helper()
	mux := http.NewServeMux()
	s.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := http.Get(srv.URL + Path)
	if err != nil {
		t.Fatalf("scrape %s: %v", Path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("scrape %s: status %d", Path, resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read scrape: %v", err)
	}
	return string(b)
}

// TestScrapeExportsTheThreeFamilies is the DONE-WHEN control at the package
// level: the registered promhttp handler serves queue depth, leases held and
// provider latency, labelled by component, in the Prometheus text format.
func TestScrapeExportsTheThreeFamilies(t *testing.T) {
	t.Parallel()

	s := New()
	s.QueueDepth(Fill, 7)
	s.LeasesHeld(Dealer, 3)
	s.ProviderLatency(Lander, "forge", 250*time.Millisecond)
	body := scrape(t, s)
	for _, want := range []string{
		`nova_queue_depth{component="fill"} 7`,
		`nova_leases_held{component="dealer"} 3`,
		`nova_provider_latency_seconds_count{component="lander",provider="forge"} 1`,
		`nova_provider_latency_seconds_sum{component="lander",provider="forge"} 0.25`,
		"# TYPE nova_provider_latency_seconds histogram",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape lacks %q:\n%s", want, body)
		}
	}
}

// TestNilSetIsANoOp: a verb run without metrics (every existing test) records
// nothing and does not panic.
func TestNilSetIsANoOp(t *testing.T) {
	t.Parallel()

	var s *Set
	s.QueueDepth(Fill, 1)
	s.LeasesHeld(Fill, 1)
	s.ProviderLatency(Fill, "bench", time.Second)
}

// TestSetsAreIndependent: two sets never share a registry, so a test's scrape
// sees only what its own run exported.
func TestSetsAreIndependent(t *testing.T) {
	t.Parallel()

	a, b := New(), New()
	a.QueueDepth(Fill, 9)
	if body := scrape(t, b); strings.Contains(body, `nova_queue_depth{component="fill"} 9`) {
		t.Fatalf("set b exported set a's gauge:\n%s", body)
	}
}
