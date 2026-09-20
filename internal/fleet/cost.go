package fleet

import (
	"sort"
	"strings"
	"sync"
)

// ModelPricing defines the pricing rates for an LLM model, expressed in
// dollars per million (1,000,000) tokens for input, output, and cached tokens.
//
// In this house, token pricing is tracked explicitly: every route's cost is
// estimated before card dispatch so cheapest routes run first (docs/MODELS.md rule 1),
// and a missing rate is unpriced rather than free.
type ModelPricing struct {
	Model      string  // model identifier (e.g. "deepseek-flash", "claude-3.7-sonnet")
	InputCost  float64 // dollars per 1,000,000 input tokens
	OutputCost float64 // dollars per 1,000,000 output tokens
	CacheCost  float64 // dollars per 1,000,000 cache read / hit tokens
}

// InputRate returns the price per single input token.
func (p ModelPricing) InputRate() float64 { return p.InputCost / 1e6 }

// OutputRate returns the price per single output token.
func (p ModelPricing) OutputRate() float64 { return p.OutputCost / 1e6 }

// CacheRate returns the price per single cached token.
func (p ModelPricing) CacheRate() float64 { return p.CacheCost / 1e6 }

// InputCostPerM returns the input cost per million tokens.
func (p ModelPricing) InputCostPerM() float64 { return p.InputCost }

// OutputCostPerM returns the output cost per million tokens.
func (p ModelPricing) OutputCostPerM() float64 { return p.OutputCost }

// CacheCostPerM returns the cache cost per million tokens.
func (p ModelPricing) CacheCostPerM() float64 { return p.CacheCost }

// InputOutputTokens represents a workload's token counts broken down by category:
// un-cached input tokens, generated output tokens, and cache-hit input tokens.
type InputOutputTokens struct {
	Input  int64 // non-cached input tokens
	Output int64 // generated output tokens
	Cache  int64 // cached prompt / context tokens
}

// Total returns the total tokens across all categories.
func (t InputOutputTokens) Total() int64 {
	return t.Input + t.Output + t.Cache
}

// EffectivePricing resolves the pricing structure for this route:
// 1. Explicit non-zero pricing on the route itself takes precedence.
// 2. Flat, free, or local routes evaluate to zero marginal cost.
// 3. Otherwise, lookup is attempted by Model, then by Name / Route in the pricing table.
func (r *Route) EffectivePricing() ModelPricing {
	if r == nil {
		return ModelPricing{}
	}
	if r.Pricing.InputCost > 0 || r.Pricing.OutputCost > 0 || r.Pricing.CacheCost > 0 {
		return r.Pricing
	}
	if r.CostClass == "flat" || r.CostClass == "free" || r.CostClass == "local" {
		return ModelPricing{Model: r.Model, InputCost: 0, OutputCost: 0, CacheCost: 0}
	}
	if r.Model != "" {
		if p, ok := LookupPricing(r.Model); ok {
			return p
		}
	}
	if r.Name != "" {
		if p, ok := LookupPricing(r.Name); ok {
			return p
		}
	}
	if r.Route != "" {
		if p, ok := LookupPricing(r.Route); ok {
			return p
		}
	}
	return ModelPricing{Model: r.Model}
}

// EstimateCost computes the estimated dollar cost of running the given token
// workload on the specified route.
//
// Pricing is defined per million tokens, so the cost is calculated as:
// (Input * InputCost + Output * OutputCost + Cache * CacheCost) / 1,000,000.
func EstimateCost(tokens InputOutputTokens, route *Route) float64 {
	if route == nil {
		return 0.0
	}
	pricing := route.EffectivePricing()

	in := float64(tokens.Input)
	out := float64(tokens.Output)
	cache := float64(tokens.Cache)
	if in < 0 {
		in = 0
	}
	if out < 0 {
		out = 0
	}
	if cache < 0 {
		cache = 0
	}

	return (in*pricing.InputCost + out*pricing.OutputCost + cache*pricing.CacheCost) / 1_000_000.0
}

// RouteCost is an alias for EstimateCost.
func RouteCost(tokens InputOutputTokens, route *Route) float64 {
	return EstimateCost(tokens, route)
}

// StandardCardWorkload represents typical token usage for a standard card workload:
// 25,000 input tokens, 2,000 output tokens, and 50,000 cache read tokens.
var StandardCardWorkload = InputOutputTokens{
	Input:  25_000,
	Output: 2_000,
	Cache:  50_000,
}

func routeName(r *Route) string {
	if r == nil {
		return ""
	}
	if r.Name != "" {
		return r.Name
	}
	return r.Route
}

// CompareRoutes compares two routes by their estimated cost for the given token workload.
// Returns -1 if a is cheaper than b, 1 if a is more expensive than b, and 0 if costs are equal.
func CompareRoutes(a, b *Route, tokens InputOutputTokens) int {
	costA := EstimateCost(tokens, a)
	costB := EstimateCost(tokens, b)
	if costA < costB {
		return -1
	}
	if costA > costB {
		return 1
	}
	return 0
}

// CompareRoutesByCost is an alias for CompareRoutes.
func CompareRoutesByCost(a, b *Route, tokens InputOutputTokens) int {
	return CompareRoutes(a, b, tokens)
}

// CompareRoutesForStandardWorkload compares two routes using StandardCardWorkload.
func CompareRoutesForStandardWorkload(a, b *Route) int {
	return CompareRoutes(a, b, StandardCardWorkload)
}

// SortRoutesByCost sorts routes in ascending order of estimated cost (cheapest first)
// for the given token workload, populating EstimatedCost on each route.
// Ties are broken deterministically by route Name / Route.
func SortRoutesByCost(routes []*Route, tokens InputOutputTokens) []*Route {
	for _, r := range routes {
		if r != nil {
			if r.Name == "" && r.Route != "" {
				r.Name = r.Route
			} else if r.Route == "" && r.Name != "" {
				r.Route = r.Name
			}
			r.EstimatedCost = EstimateCost(tokens, r)
		}
	}
	sort.SliceStable(routes, func(i, j int) bool {
		costI := EstimateCost(tokens, routes[i])
		costJ := EstimateCost(tokens, routes[j])
		if costI != costJ {
			return costI < costJ
		}
		if routes[i] == nil {
			return false
		}
		if routes[j] == nil {
			return true
		}
		return routeName(routes[i]) < routeName(routes[j])
	})
	return routes
}

// SortRoutesByEstimatedCost is an alias for SortRoutesByCost.
func SortRoutesByEstimatedCost(routes []*Route, tokens InputOutputTokens) []*Route {
	return SortRoutesByCost(routes, tokens)
}

// SortRoutesForStandardWorkload sorts routes for the StandardCardWorkload.
func SortRoutesForStandardWorkload(routes []*Route) []*Route {
	return SortRoutesByCost(routes, StandardCardWorkload)
}

// PricingTable is a table mapping model names to ModelPricing records.
type PricingTable map[string]ModelPricing

// ModelPricingTable is an alias for PricingTable.
type ModelPricingTable = PricingTable

var (
	pricingMu           sync.RWMutex
	DefaultPricingTable = initDefaultPricingTable()
)

// LookupPricing looks up model pricing by model or route name.
func LookupPricing(model string) (ModelPricing, bool) {
	model = strings.TrimSpace(model)
	pricingMu.RLock()
	defer pricingMu.RUnlock()

	if p, ok := DefaultPricingTable[model]; ok {
		return p, true
	}
	if p, ok := DefaultPricingTable[strings.ToLower(model)]; ok {
		return p, true
	}
	return ModelPricing{}, false
}

// RegisterPricing adds or updates a model's pricing in DefaultPricingTable.
func RegisterPricing(p ModelPricing) {
	pricingMu.Lock()
	defer pricingMu.Unlock()
	DefaultPricingTable[strings.TrimSpace(p.Model)] = p
}

func initDefaultPricingTable() ModelPricingTable {
	table := make(ModelPricingTable)

	models := []ModelPricing{
		// DeepSeek
		{Model: "deepseek-flash", InputCost: 0.14, OutputCost: 0.28, CacheCost: 0.014},
		{Model: "deepseek/deepseek-flash", InputCost: 0.14, OutputCost: 0.28, CacheCost: 0.014},
		{Model: "deepseek-v4-flash", InputCost: 0.14, OutputCost: 0.28, CacheCost: 0.014},
		{Model: "deepseek-direct", InputCost: 0.14, OutputCost: 0.28, CacheCost: 0.014},
		{Model: "deepseek-v4-pro", InputCost: 0.55, OutputCost: 2.19, CacheCost: 0.07},
		{Model: "deepseek/deepseek-v4-pro", InputCost: 0.55, OutputCost: 2.19, CacheCost: 0.07},
		{Model: "deepseek-pro", InputCost: 0.55, OutputCost: 2.19, CacheCost: 0.07},
		{Model: "deepseek-chat", InputCost: 0.27, OutputCost: 1.10, CacheCost: 0.07},
		{Model: "deepseek/deepseek-chat", InputCost: 0.27, OutputCost: 1.10, CacheCost: 0.07},

		// Claude (Anthropic)
		{Model: "claude-3.7-sonnet", InputCost: 3.00, OutputCost: 15.00, CacheCost: 0.30},
		{Model: "anthropic/claude-3.7-sonnet", InputCost: 3.00, OutputCost: 15.00, CacheCost: 0.30},
		{Model: "claude-sonnet", InputCost: 3.00, OutputCost: 15.00, CacheCost: 0.30},
		{Model: "claude-opus-5", InputCost: 15.00, OutputCost: 75.00, CacheCost: 1.50},
		{Model: "claude-3-opus", InputCost: 15.00, OutputCost: 75.00, CacheCost: 1.50},
		{Model: "claude-opus", InputCost: 15.00, OutputCost: 75.00, CacheCost: 1.50},
		{Model: "claude-3.5-haiku", InputCost: 0.80, OutputCost: 4.00, CacheCost: 0.08},
		{Model: "claude-haiku", InputCost: 0.80, OutputCost: 4.00, CacheCost: 0.08},
		{Model: "claude-x", InputCost: 3.00, OutputCost: 15.00, CacheCost: 0.30},

		// OpenAI
		{Model: "gpt-4o", InputCost: 2.50, OutputCost: 10.00, CacheCost: 1.25},
		{Model: "openai/gpt-4o", InputCost: 2.50, OutputCost: 10.00, CacheCost: 1.25},
		{Model: "gpt-4o-mini", InputCost: 0.15, OutputCost: 0.60, CacheCost: 0.075},
		{Model: "openai/gpt-4o-mini", InputCost: 0.15, OutputCost: 0.60, CacheCost: 0.075},

		// Gemini (Google)
		{Model: "gemini-2.5-pro", InputCost: 1.25, OutputCost: 5.00, CacheCost: 0.3125},
		{Model: "google/gemini-2.5-pro", InputCost: 1.25, OutputCost: 5.00, CacheCost: 0.3125},
		{Model: "gemini-2.5-flash", InputCost: 0.075, OutputCost: 0.30, CacheCost: 0.01875},
		{Model: "google/gemini-2.5-flash", InputCost: 0.075, OutputCost: 0.30, CacheCost: 0.01875},

		// Inception
		{Model: "mercury-2.5", InputCost: 2.00, OutputCost: 10.00, CacheCost: 0.20},
		{Model: "mercury-pro", InputCost: 2.00, OutputCost: 10.00, CacheCost: 0.20},
		{Model: "inception/mercury-2.5", InputCost: 2.00, OutputCost: 10.00, CacheCost: 0.20},

		// Flat / Local / Zero
		{Model: "opencode/deepseek-v4-flash", InputCost: 0.0, OutputCost: 0.0, CacheCost: 0.0},
		{Model: "opencode/deepseek-v4-pro", InputCost: 0.0, OutputCost: 0.0, CacheCost: 0.0},
		{Model: "opencode-deepseek", InputCost: 0.0, OutputCost: 0.0, CacheCost: 0.0},
		{Model: "opencode/mimo-v2.5-free", InputCost: 0.0, OutputCost: 0.0, CacheCost: 0.0},
		{Model: "ollama/north-mini-code-32k", InputCost: 0.0, OutputCost: 0.0, CacheCost: 0.0},
		{Model: "local", InputCost: 0.0, OutputCost: 0.0, CacheCost: 0.0},
	}

	for _, m := range models {
		table[m.Model] = m
	}
	return table
}
