package fleet

import (
	"math"
	"testing"
)

// TestEstimateCost tests the core token pricing and cost estimation logic
// over explicit pricing, table lookups, cache discounts, and standard workloads.
func TestEstimateCost(t *testing.T) {
	// 1. Explicit pricing on a route
	route := &Route{
		Name:     "anthropic/claude-sonnet-custom",
		Provider: "anthropic",
		Model:    "custom-sonnet",
		Pricing: ModelPricing{
			Model:      "custom-sonnet",
			InputCost:  3.00,  // $3.00 per 1M input
			OutputCost: 15.00, // $15.00 per 1M output
			CacheCost:  0.30,  // $0.30 per 1M cache
		},
	}

	t.Run("Exact 1M tokens", func(t *testing.T) {
		costIn := EstimateCost(InputOutputTokens{Input: 1_000_000}, route)
		if math.Abs(costIn-3.00) > 1e-9 {
			t.Errorf("1M input tokens want $3.00, got %v", costIn)
		}

		costOut := EstimateCost(InputOutputTokens{Output: 1_000_000}, route)
		if math.Abs(costOut-15.00) > 1e-9 {
			t.Errorf("1M output tokens want $15.00, got %v", costOut)
		}

		costCache := EstimateCost(InputOutputTokens{Cache: 1_000_000}, route)
		if math.Abs(costCache-0.30) > 1e-9 {
			t.Errorf("1M cache tokens want $0.30, got %v", costCache)
		}
	})

	t.Run("Combined mixed tokens", func(t *testing.T) {
		// 10,000 input ($0.030) + 1,000 output ($0.015) + 20,000 cache ($0.006) = $0.051
		tokens := InputOutputTokens{
			Input:  10_000,
			Output: 1_000,
			Cache:  20_000,
		}
		got := EstimateCost(tokens, route)
		want := (10_000.0*3.00 + 1_000.0*15.00 + 20_000.0*0.30) / 1_000_000.0 // 0.051
		if math.Abs(got-want) > 1e-9 {
			t.Errorf("EstimateCost mixed tokens = %v, want %v", got, want)
		}
	})

	t.Run("Standard card workload on registered models", func(t *testing.T) {
		flashRoute := &Route{Name: "deepseek/deepseek-flash", Model: "deepseek-flash"}
		proRoute := &Route{Name: "deepseek/deepseek-v4-pro", Model: "deepseek-v4-pro"}
		sonnetRoute := &Route{Name: "anthropic/claude-3.7-sonnet", Model: "claude-3.7-sonnet"}

		costFlash := EstimateCost(StandardCardWorkload, flashRoute)
		costPro := EstimateCost(StandardCardWorkload, proRoute)
		costSonnet := EstimateCost(StandardCardWorkload, sonnetRoute)

		// Flash: (25k*0.14 + 2k*0.28 + 50k*0.014)/1M = (3.5 + 0.56 + 0.70)/1000 = $0.00476
		wantFlash := (25_000*0.14 + 2_000*0.28 + 50_000*0.014) / 1e6
		if math.Abs(costFlash-wantFlash) > 1e-9 {
			t.Errorf("Flash standard workload = %v, want %v", costFlash, wantFlash)
		}

		// Pro: (25k*0.55 + 2k*2.19 + 50k*0.07)/1M = (13.75 + 4.38 + 3.5)/1000 = $0.02163
		wantPro := (25_000*0.55 + 2_000*2.19 + 50_000*0.07) / 1e6
		if math.Abs(costPro-wantPro) > 1e-9 {
			t.Errorf("Pro standard workload = %v, want %v", costPro, wantPro)
		}

		// Sonnet: (25k*3.00 + 2k*15.00 + 50k*0.30)/1M = (75.0 + 30.0 + 15.0)/1000 = $0.120
		wantSonnet := (25_000*3.00 + 2_000*15.00 + 50_000*0.30) / 1e6
		if math.Abs(costSonnet-wantSonnet) > 1e-9 {
			t.Errorf("Sonnet standard workload = %v, want %v", costSonnet, wantSonnet)
		}

		// Ordering check: flash is cheaper than pro, which is cheaper than sonnet
		if costFlash >= costPro {
			t.Errorf("expected flash (%v) < pro (%v)", costFlash, costPro)
		}
		if costPro >= costSonnet {
			t.Errorf("expected pro (%v) < sonnet (%v)", costPro, costSonnet)
		}
	})

	t.Run("Free and flat routes cost zero", func(t *testing.T) {
		flatRoute := &Route{
			Name:      "opencode/deepseek-v4-flash",
			Model:     "deepseek-flash",
			CostClass: "flat",
		}
		freeRoute := &Route{
			Name:      "ollama/north-mini-code-32k",
			CostClass: "free",
		}

		if got := EstimateCost(StandardCardWorkload, flatRoute); got != 0.0 {
			t.Errorf("flat route cost = %v, want 0.0", got)
		}
		if got := EstimateCost(StandardCardWorkload, freeRoute); got != 0.0 {
			t.Errorf("free route cost = %v, want 0.0", got)
		}
	})

	t.Run("Nil and empty edge cases", func(t *testing.T) {
		if got := EstimateCost(StandardCardWorkload, nil); got != 0.0 {
			t.Errorf("nil route cost = %v, want 0.0", got)
		}
		if got := EstimateCost(InputOutputTokens{}, route); got != 0.0 {
			t.Errorf("zero tokens cost = %v, want 0.0", got)
		}
	})
}

// TestCompareRoutes tests comparing routes by estimated cost.
func TestCompareRoutes(t *testing.T) {
	flat := &Route{Name: "flat", CostClass: "flat"}
	cheap := &Route{Name: "cheap", Model: "deepseek-flash"}
	expensive := &Route{Name: "expensive", Model: "claude-opus-5"}

	if cmp := CompareRoutes(flat, cheap, StandardCardWorkload); cmp != -1 {
		t.Errorf("CompareRoutes(flat, cheap) = %d, want -1", cmp)
	}
	if cmp := CompareRoutes(expensive, cheap, StandardCardWorkload); cmp != 1 {
		t.Errorf("CompareRoutes(expensive, cheap) = %d, want 1", cmp)
	}
	if cmp := CompareRoutes(cheap, cheap, StandardCardWorkload); cmp != 0 {
		t.Errorf("CompareRoutes(cheap, cheap) = %d, want 0", cmp)
	}
}

// TestSortRoutesByCost verifies that routes are correctly ordered from
// cheapest to most expensive for a standard card workload.
func TestSortRoutesByCost(t *testing.T) {
	rOpus := &Route{Name: "opus", Model: "claude-opus-5"}
	rSonnet := &Route{Name: "sonnet", Model: "claude-3.7-sonnet"}
	rPro := &Route{Name: "pro", Model: "deepseek-v4-pro"}
	rFlash := &Route{Name: "flash", Model: "deepseek-flash"}
	rFlat := &Route{Name: "flat", CostClass: "flat"}

	// Provide out of order
	routes := []*Route{rOpus, rFlash, rSonnet, rFlat, rPro}

	sorted := SortRoutesByCost(routes, StandardCardWorkload)

	// Expected order: flat ($0.0) -> flash (~$0.0048) -> pro (~$0.0216) -> sonnet (~$0.12) -> opus (~$0.60)
	expected := []string{"flat", "flash", "pro", "sonnet", "opus"}
	for i, wantName := range expected {
		if sorted[i].Name != wantName {
			t.Errorf("at index %d: want %s, got %s (cost: %v)", i, wantName, sorted[i].Name, sorted[i].EstimatedCost)
		}
	}

	// Verify EstimatedCost field was populated and is monotonic
	for i := 1; i < len(sorted); i++ {
		if sorted[i-1].EstimatedCost > sorted[i].EstimatedCost {
			t.Errorf("non-monotonic costs at %d: %v > %v", i, sorted[i-1].EstimatedCost, sorted[i].EstimatedCost)
		}
	}

	// Verify SortRoutesForStandardWorkload helper produces same order
	routes2 := []*Route{rOpus, rFlash, rSonnet, rFlat, rPro}
	sorted2 := SortRoutesForStandardWorkload(routes2)
	for i, wantName := range expected {
		if sorted2[i].Name != wantName {
			t.Errorf("SortRoutesForStandardWorkload index %d: want %s, got %s", i, wantName, sorted2[i].Name)
		}
	}
}

// TestPricingLookupAndRegister verifies pricing table lookups and runtime registration.
func TestPricingLookupAndRegister(t *testing.T) {
	// Lookup existing
	p, ok := LookupPricing("deepseek-flash")
	if !ok {
		t.Fatal("expected deepseek-flash in DefaultPricingTable")
	}
	if p.InputCost != 0.14 || p.OutputCost != 0.28 {
		t.Errorf("unexpected rates for deepseek-flash: %+v", p)
	}

	// Lookup unknown
	_, ok = LookupPricing("unknown-model-xyz")
	if ok {
		t.Error("unexpected ok for unknown model")
	}

	// Register new model
	custom := ModelPricing{
		Model:      "experimental-v1",
		InputCost:  1.00,
		OutputCost: 2.00,
		CacheCost:  0.10,
	}
	RegisterPricing(custom)

	got, ok := LookupPricing("experimental-v1")
	if !ok {
		t.Fatal("registered model experimental-v1 not found")
	}
	if got.InputCost != 1.00 || got.OutputCost != 2.00 || got.CacheCost != 0.10 {
		t.Errorf("unexpected rates after register: %+v", got)
	}
}
