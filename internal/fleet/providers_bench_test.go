package fleet

import (
	"fmt"
	"math/rand/v2"
	"testing"
)

// BenchmarkProviderRegistryAvailableRoutes benchmarks AvailableRoutes with 50 routes and 500 lookups.
func BenchmarkProviderRegistryAvailableRoutes(b *testing.B) {
	reg := setupBenchmarkRegistry(50)

	// Realistic inFlight and errorRate states
	inFlight := make(map[string]int, 50)
	errorRate := make(map[string]float64, 50)
	for i, r := range reg.Routes() {
		// ~20% of routes near or at capacity
		if i%5 == 0 {
			inFlight[r.Route] = r.Concurrency
		} else {
			inFlight[r.Route] = r.Concurrency / 2
		}
		// ~10% of routes tripped
		if i%10 == 0 {
			errorRate[r.Route] = r.ErrorThreshold + 0.05
		} else {
			errorRate[r.Route] = 0.01
		}
	}

	tiers := []string{"flash", "pro", "any", "FLASH", "PRO"}

	b.Run("50routes_500lookups", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for j := 0; j < 500; j++ {
				tier := tiers[j%len(tiers)]
				routes := reg.AvailableRoutes(tier, inFlight, errorRate)
				if len(routes) == 0 {
					b.Fatal("expected non-empty available routes")
				}
			}
		}
	})

	b.Run("single_lookup", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			tier := tiers[i%len(tiers)]
			routes := reg.AvailableRoutes(tier, inFlight, errorRate)
			if len(routes) == 0 {
				b.Fatal("expected non-empty available routes")
			}
		}
	})
}

func setupBenchmarkRegistry(numRoutes int) *ProviderRegistry {
	reg := &ProviderRegistry{
		byRoute: make(map[string]*Route, numRoutes),
	}
	tiers := []string{"flash", "pro", "any"}
	providers := []string{"deepseek", "anthropic", "openai", "openrouter", "mercury"}

	rng := rand.New(rand.NewPCG(42, 99))

	for i := 0; i < numRoutes; i++ {
		routeName := fmt.Sprintf("route-%s-%02d", providers[i%len(providers)], i)
		r := &Route{
			Route:          routeName,
			Name:           routeName,
			Provider:       providers[i%len(providers)],
			Model:          fmt.Sprintf("model-v%d", i%5),
			Tier:           tiers[i%len(tiers)],
			Concurrency:    5 + (i % 20),
			CostPerMToken:  float64(10+rng.IntN(200)) / 10.0,
			ErrorThreshold: 0.10 + float64(rng.IntN(20))/100.0,
			Notes:          "benchmark route",
		}
		reg.routes = append(reg.routes, r)
		reg.byRoute[routeName] = r
	}
	return reg
}
