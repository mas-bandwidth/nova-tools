package fleet

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Issue #2010: Provider Registry & Multi-Provider Route Arbiter.
//
// The provider registry defines available LLM inference routes, concurrency
// ceilings, cost ranking, and circuit-breaker error thresholds.
//
// TSV table format:
//   route<TAB>provider<TAB>model<TAB>tier<TAB>concurrency<TAB>cost_per_mtoken<TAB>error_threshold<TAB>notes

// Route represents a single configured provider route.
type Route struct {
	Route          string       // e.g. deepseek-direct, opencode-deepseek, openrouter-deepseek, mercury-pro
	Name           string       // alias for route identifier
	Provider       string       // e.g. deepseek, opencode, openrouter, mercury
	Model          string       // e.g. deepseek-v4-flash
	Tier           string       // "flash", "pro", "any"
	Concurrency    int          // max in-flight cards allowed across fleet
	CostPerMToken  float64      // cost ranking in USD per million tokens
	ErrorThreshold float64      // e.g. 0.20 for 20% failure rate
	Notes          string       // optional free-text notes
	CostClass      string       // optional: "flat", "metered", "free", "local"
	Pricing        ModelPricing // explicit pricing per million tokens
	EstimatedCost  float64      // estimated cost score populated during ranking
}

// ProviderRegistry holds parsed provider routes and indices.
type ProviderRegistry struct {
	path    string
	routes  []*Route
	byRoute map[string]*Route
}

// Path returns the file path this registry was read from, if any.
func (pr *ProviderRegistry) Path() string {
	if pr == nil {
		return ""
	}
	return pr.path
}

// Routes returns all configured routes in file order.
func (pr *ProviderRegistry) Routes() []*Route {
	if pr == nil {
		return nil
	}
	out := make([]*Route, len(pr.routes))
	copy(out, pr.routes)
	return out
}

// Lookup finds a single route by its route name.
func (pr *ProviderRegistry) Lookup(route string) (*Route, bool) {
	if pr == nil {
		return nil, false
	}
	r, ok := pr.byRoute[strings.TrimSpace(route)]
	return r, ok
}

// ReadProviderRegistry reads and validates a provider registry TSV file from disk.
func ReadProviderRegistry(path string) (*ProviderRegistry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read provider registry %s: %w", path, err)
	}
	defer f.Close()

	reg, err := ParseProviderRegistry(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	reg.path = path
	return reg, nil
}

// ParseProviderRegistry parses a provider registry TSV table from an io.Reader.
// It skips comments ('#') and empty lines, recognizes optional header lines,
// and enforces schema types, limits, and unique route names.
func ParseProviderRegistry(r io.Reader) (*ProviderRegistry, error) {
	reg := &ProviderRegistry{
		byRoute: make(map[string]*Route),
	}

	scanner := bufio.NewScanner(r)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimRight(scanner.Text(), "\r\n")
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		fields := strings.Split(line, "\t")

		// Recognize and skip header row
		if strings.EqualFold(strings.TrimSpace(fields[0]), "route") &&
			len(fields) > 1 && strings.EqualFold(strings.TrimSpace(fields[1]), "provider") {
			continue
		}

		if len(fields) < 7 || len(fields) > 8 {
			return nil, fmt.Errorf("line %d: wants 8 tab-separated fields (route, provider, model, tier, concurrency, cost_per_mtoken, error_threshold, notes), got %d", lineNum, len(fields))
		}

		route := strings.TrimSpace(fields[0])
		if route == "" {
			return nil, fmt.Errorf("line %d: has no route name", lineNum)
		}

		provider := strings.TrimSpace(fields[1])
		if provider == "" {
			return nil, fmt.Errorf("line %d: route %s has no provider", lineNum, route)
		}

		model := strings.TrimSpace(fields[2])
		if model == "" {
			return nil, fmt.Errorf("line %d: route %s has no model", lineNum, route)
		}

		tier := strings.ToLower(strings.TrimSpace(fields[3]))
		if tier != "flash" && tier != "pro" && tier != "any" {
			return nil, fmt.Errorf("line %d: route %s has unknown tier %q; tier must be flash, pro, or any", lineNum, route, tier)
		}

		concurrencyStr := strings.TrimSpace(fields[4])
		concurrency, err := strconv.Atoi(concurrencyStr)
		if err != nil || concurrency < 0 {
			return nil, fmt.Errorf("line %d: route %s wants concurrency as a non-negative integer, got %q", lineNum, route, concurrencyStr)
		}

		costStr := strings.TrimSpace(fields[5])
		cost, err := strconv.ParseFloat(costStr, 64)
		if err != nil || cost < 0 {
			return nil, fmt.Errorf("line %d: route %s wants cost_per_mtoken as a non-negative number, got %q", lineNum, route, costStr)
		}

		threshStr := strings.TrimSpace(fields[6])
		thresh, err := strconv.ParseFloat(threshStr, 64)
		if err != nil || thresh < 0 {
			return nil, fmt.Errorf("line %d: route %s wants error_threshold as a non-negative number, got %q", lineNum, route, threshStr)
		}

		notes := ""
		if len(fields) >= 8 {
			notes = undash(fields[7])
		}

		if _, exists := reg.byRoute[route]; exists {
			return nil, fmt.Errorf("line %d: route %q is named twice; one line per route", lineNum, route)
		}

		rEntry := &Route{
			Route:          route,
			Name:           route,
			Provider:       provider,
			Model:          model,
			Tier:           tier,
			Concurrency:    concurrency,
			CostPerMToken:  cost,
			ErrorThreshold: thresh,
			Notes:          notes,
		}

		reg.routes = append(reg.routes, rEntry)
		reg.byRoute[route] = rEntry
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading provider registry: %w", err)
	}

	return reg, nil
}

// AvailableRoutes filters and orders routes available for card execution:
// - Filter by tier: matches card tier, or if either card or route tier is "any"
// - Filter out routes where inFlight[route] >= Concurrency (concurrency ceiling)
// - Filter out routes where errorRate[route] >= ErrorThreshold (circuit breaker)
// - Sort remaining routes in cost order (lowest CostPerMToken first, deterministic by route name on tie)
func (pr *ProviderRegistry) AvailableRoutes(tier string, inFlight map[string]int, errorRate map[string]float64) []*Route {
	if pr == nil {
		return nil
	}

	reqTier := strings.ToLower(strings.TrimSpace(tier))
	var available []*Route

	for _, r := range pr.routes {
		// 1. Tier filtering:
		// A route matches if its tier matches the requested tier, or if either is "any".
		rTier := strings.ToLower(r.Tier)
		if reqTier != "" && reqTier != "any" && rTier != "any" && rTier != reqTier {
			continue
		}

		// 2. Concurrency ceiling:
		// Filter out routes where inFlight[route] >= Concurrency
		currentFlight := 0
		if inFlight != nil {
			currentFlight = inFlight[r.Route]
		}
		if currentFlight >= r.Concurrency {
			continue
		}

		// 3. Circuit breaker:
		// Filter out routes where errorRate[route] >= ErrorThreshold
		var currentErr float64
		if errorRate != nil {
			currentErr = errorRate[r.Route]
		}
		if (r.ErrorThreshold > 0 && currentErr >= r.ErrorThreshold) || (r.ErrorThreshold == 0 && currentErr > 0) {
			continue
		}

		available = append(available, r)
	}

	// 4. Cost ordering: lowest cost first, deterministic by route name on tie
	sort.Slice(available, func(i, j int) bool {
		if available[i].CostPerMToken != available[j].CostPerMToken {
			return available[i].CostPerMToken < available[j].CostPerMToken
		}
		return available[i].Route < available[j].Route
	})

	return available
}
