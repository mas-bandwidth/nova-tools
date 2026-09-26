package deal

import (
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/metrics"
	"github.com/mas-bandwidth/nova-tools/internal/metrics/metricstest"
)

// okLauncher accepts every batch without opening a session.
type okLauncher struct{}

func (okLauncher) Launch(context.Context, Opener, Bench, []Reservation) error { return nil }

// unusedDialer is never reached: okLauncher opens nothing.
type unusedDialer struct{}

func (unusedDialer) Dial(Bench) Session { return nil }

// TestDealExportsMetrics (nx-g61, recut of #2720): a pass that deals 3 of 5
// pooled cards onto a bench already leasing 1 of 4 slots exports the pool it
// left (2), the leases now held (4) and one session latency for the bench,
// and the registered /metrics handler serves them.
func TestDealExportsMetrics(t *testing.T) {
	t.Parallel()

	b := upBench("bench-a", 4)
	b.Leased = 1
	in := Input{Benches: []Bench{b}, Sprints: []Sprint{{Name: "s1", Pool: fiftyCards("s1")[:5]}}}
	set := metrics.New()
	p := &Pass{
		Source: staticSource{in}, Fence: fence("tok"), Reserver: newFakeStore("tok", in),
		Row: newFakeStore("tok", in), Launcher: okLauncher{}, Dialer: unusedDialer{},
		Metrics: set,
	}
	res, err := p.Run(context.Background())
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if got := res.Launched(); got != 3 {
		t.Fatalf("launched %d, want 3", got)
	}
	metricstest.Want(t, metricstest.Scrape(t, set),
		`nova_queue_depth{component="dealer"} 2`,
		`nova_leases_held{component="dealer"} 4`,
		`nova_provider_latency_seconds_count{component="dealer",provider="bench-a"} 1`,
	)
}
