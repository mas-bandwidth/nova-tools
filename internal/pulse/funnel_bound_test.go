package pulse

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestFunnelCostPerLandedLowerBound is nova-tools #3159 on FUNNEL: an admitted card with no
// measured spend makes cost_per_landed a lower bound, printed with its coverage; only when
// every admitted card is priced is the figure exact.
func TestFunnelCostPerLandedLowerBound(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		fixture, want, bare string
		priced              int
	}{
		{"funnel-three-card", "cost_per_landed>=5.0000 coverage=66.67% (2/3)", "5.0000", 2},
		{"funnel-three-card-priced", "cost_per_landed=6.0000 coverage=100.00% (3/3)", "6.0000", 3},
	} {
		records, err := ReadFunnel(filepath.Join("testdata", tc.fixture))
		if err != nil {
			t.Fatalf("%s: ReadFunnel: %v", tc.fixture, err)
		}
		if len(records) != 7 {
			t.Fatalf("%s: read %d records, want 7", tc.fixture, len(records))
		}
		s := ComputeFunnelSummary(records)
		if s.PricedCards != tc.priced || s.AdmittedCards != 3 {
			t.Errorf("%s: priced_cards=%d admitted=%d, want %d and 3", tc.fixture, s.PricedCards, s.AdmittedCards, tc.priced)
		}
		line := s.OneLine()
		if !strings.Contains(line, tc.want) {
			t.Errorf("%s: OneLine\n got %q\nwant it to contain %q", tc.fixture, line, tc.want)
		}
		// CostPerLanded keeps the bare number: the JSON field and every existing reader.
		if s.CostPerLanded != tc.bare {
			t.Errorf("%s: CostPerLanded = %q, want the bare number %q", tc.fixture, s.CostPerLanded, tc.bare)
		}
	}

	// The dash stays the dash: nothing landed means nothing to bound.
	s := ComputeFunnelSummary([]FunnelRecord{{Event: EventHarvest, Card: "c1", Spend: "2.0000"}})
	if line := s.OneLine(); !strings.Contains(line, "cost_per_landed=-") || strings.Contains(line, "coverage=") {
		t.Errorf("nothing landed: %q, want cost_per_landed=- with no coverage", line)
	}
}
