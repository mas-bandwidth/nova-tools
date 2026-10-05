package sprint

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// OpenRouterKeyURL is OpenRouter's key endpoint, reporting usage and rate limits.
const OpenRouterKeyURL = "https://openrouter.ai/api/v1/key"

// NCostGap is the judgment raised when provider usage reconciliation differs from internal cost records by more than 5%.
const NCostGap = "provider cost reconciliation gap over 5%"

// DefaultCostGapThreshold is the 5% gap threshold that triggers a judgment.
const DefaultCostGapThreshold = 0.05

// PropCostReconcilePrefix is the property prefix on the fleet table for reconciliation records.
const PropCostReconcilePrefix = "cost_reconcile_"

func init() {
	if Decisions != nil {
		Decisions[NCostGap] = []string{"look", "wait"}
	}
}

// PropCostReconcile is the fleet table property holding reconciliation data for a provider.
func PropCostReconcile(provider string) string {
	return PropCostReconcilePrefix + provider
}

// CostReconcileRecord is the reconciliation state recorded on the fleet table.
type CostReconcileRecord struct {
	Provider      string  `json:"provider"`
	ProviderUsage float64 `json:"provider_usage"`
	Internal      float64 `json:"internal"`
	Gap           float64 `json:"gap"`
	PctGap        float64 `json:"pct_gap"`
	At            string  `json:"at"`
}

// ReadProviderUsage reads the provider's reported usage via its API endpoint.
// For OpenRouter, it calls GET /api/v1/key and reads data.usage_daily (or data.usage).
func ReadProviderUsage(ctx context.Context, rt http.RoundTripper, provider string, getenv func(string) string) (float64, bool, error) {
	if provider != "openrouter" {
		return 0, false, nil
	}
	key := getenv("OPENROUTER_API_KEY")
	if key == "" {
		return 0, false, fmt.Errorf("OPENROUTER_API_KEY is not set")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, OpenRouterKeyURL, nil)
	if err != nil {
		return 0, false, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	if rt == nil {
		rt = http.DefaultTransport
	}
	resp, err := (&http.Client{Transport: rt}).Do(req)
	if err != nil {
		return 0, false, fmt.Errorf("GET %s failed: %w", OpenRouterKeyURL, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return 0, false, fmt.Errorf("reading response from %s failed: %w", OpenRouterKeyURL, err)
	}
	if resp.StatusCode != http.StatusOK {
		return 0, false, fmt.Errorf("GET %s answered %d", OpenRouterKeyURL, resp.StatusCode)
	}
	var wire struct {
		Data struct {
			UsageDaily *float64 `json:"usage_daily"`
			Usage      *float64 `json:"usage"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return 0, false, fmt.Errorf("parsing response from %s failed: %w", OpenRouterKeyURL, err)
	}
	if wire.Data.UsageDaily != nil {
		return *wire.Data.UsageDaily, true, nil
	}
	if wire.Data.Usage != nil {
		return *wire.Data.Usage, true, nil
	}
	return 0, false, fmt.Errorf("GET %s answered no data.usage_daily or data.usage", OpenRouterKeyURL)
}

// ProviderInternalSpend sums the charged cost (or predicted when actual is absent)
// across all cards on the work table for the given provider.
func ProviderInternalSpend(s *Snapshot, provider string) float64 {
	if s == nil || s.Work == nil {
		return 0
	}
	routeProviders := map[string]string{}
	for _, r := range s.Routes {
		if r.Name != "" && r.Provider != "" {
			routeProviders[r.Name] = r.Provider
		}
	}
	sum := new(big.Rat)
	provLower := strings.ToLower(provider)

	isProvider := func(route, model string) bool {
		if prov, ok := routeProviders[route]; ok && strings.EqualFold(prov, provider) {
			return true
		}
		if strings.EqualFold(route, provider) || strings.HasPrefix(strings.ToLower(route), provLower+"-") || strings.HasPrefix(strings.ToLower(route), provLower+"/") {
			return true
		}
		if strings.HasPrefix(strings.ToLower(model), provLower+"/") || strings.EqualFold(model, provider) {
			return true
		}
		return false
	}

	for _, c := range s.Work.Column(States...) {
		if IsSentinel(c) {
			continue
		}
		for _, con := range CardCostOf(c).Consumers {
			route := cmp.Or(con.Route, con.Usage.Route)
			model := cmp.Or(con.Model, con.Usage.Model)
			if !isProvider(route, model) {
				continue
			}
			usdStr := cmp.Or(con.Usage.Actual, con.Usage.Predicted)
			usd, err := amountOf(usdStr)
			if err != nil || usd == nil {
				continue
			}
			sum.Add(sum, usd)
		}
	}
	f, _ := sum.Float64()
	return f
}

// parseCostReconcileLine reads a reconciliation record from JSON or fields.
func parseCostReconcileLine(line string) CostReconcileRecord {
	var rec CostReconcileRecord
	if err := json.Unmarshal([]byte(line), &rec); err == nil {
		return rec
	}
	for _, f := range strings.Fields(line) {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			continue
		}
		switch k {
		case "provider":
			rec.Provider = v
		case "provider_usage":
			rec.ProviderUsage, _ = strconv.ParseFloat(v, 64)
		case "internal":
			rec.Internal, _ = strconv.ParseFloat(v, 64)
		case "gap":
			rec.Gap, _ = strconv.ParseFloat(v, 64)
		case "pct_gap":
			rec.PctGap, _ = strconv.ParseFloat(v, 64)
		case "at":
			rec.At = v
		}
	}
	return rec
}

// UnreconciledSpend returns the total unreconciled spend across providers
// based on reconciliation records on the fleet table.
func UnreconciledSpend(s *Snapshot) float64 {
	if s == nil || s.Fleet == nil {
		return 0
	}
	total := 0.0
	for k, v := range s.Fleet.Props() {
		if strings.HasPrefix(k, PropCostReconcilePrefix) {
			rec := parseCostReconcileLine(v)
			if rec.ProviderUsage > rec.Internal {
				total += rec.ProviderUsage - rec.Internal
			}
		}
	}
	return total
}

// CostReconcileReq is the request for provider usage reconciliation.
type CostReconcileReq struct {
	Provider      string
	ProviderUsage float64
	Threshold     float64
	Who           string
}

// CostReconcile compares internal spend against provider usage, writes reconciliation
// telemetry to the fleet table, and raises ONE judgment if the gap exceeds threshold.
func CostReconcile(s *Snapshot, r CostReconcileReq) Plan {
	var p Plan
	threshold := r.Threshold
	if threshold <= 0 {
		threshold = DefaultCostGapThreshold
	}
	internal := ProviderInternalSpend(s, r.Provider)
	gap := math.Abs(r.ProviderUsage - internal)
	pct := 0.0
	if r.ProviderUsage > 0 {
		pct = gap / r.ProviderUsage
	} else if internal > 0 {
		pct = gap / internal
	}

	propName := PropCostReconcile(r.Provider)
	rec := CostReconcileRecord{
		Provider:      r.Provider,
		ProviderUsage: r.ProviderUsage,
		Internal:      internal,
		Gap:           gap,
		PctGap:        pct,
		At:            stamp(s.Now),
	}
	val, _ := json.Marshal(rec)
	wasProp, had := "", false
	if s.Fleet != nil {
		wasProp, had = s.Fleet.Prop(propName)
	}
	p.Props = append(p.Props, PropWrite{Table: Fleet, Name: propName, Value: string(val), Was: wasProp, WasAbsent: !had})

	if pct > threshold {
		alreadyOpen := false
		for _, o := range s.Open {
			if o.Note.Type == NCostGap && (len(o.Note.Primaries) == 0 || contains(o.Note.Primaries, r.Provider)) {
				alreadyOpen = true
				break
			}
		}
		if !alreadyOpen {
			n := Note{
				Kind:        Judgment,
				Type:        NCostGap,
				SprintLevel: true,
				Primaries:   []string{r.Provider},
				At:          s.Now,
				What: fmt.Sprintf("provider %s cost reconciliation gap is %.1f%% (provider: $%.2f, internal: $%.2f, gap: $%.2f > %.0f%%)",
					r.Provider, pct*100, r.ProviderUsage, internal, gap, threshold*100),
				Decisions: append([]string(nil), Decisions[NCostGap]...),
			}
			p.Notes = append(p.Notes, n)
		}
	}
	return p
}
