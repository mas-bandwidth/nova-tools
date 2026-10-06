package config

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// OpenRouterModelsURL is OpenRouter's published models catalog.
const OpenRouterModelsURL = "https://openrouter.ai/api/v1/models"

// RoutePrices holds the price sheet fields for a route.
type RoutePrices struct {
	Input     string // USD per million uncached input tokens
	Output    string // USD per million output tokens
	CacheRead string // USD per million cached input tokens read
	Source    string
	AsOf      string
}

// FetchOpenRouterPrices reads the published model prices from OpenRouter.
func FetchOpenRouterPrices(ctx context.Context, rt http.RoundTripper) (map[string]RoutePrices, error) {
	if rt == nil {
		rt = http.DefaultTransport
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, OpenRouterModelsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("openrouter request: %w", err)
	}

	resp, err := (&http.Client{Transport: rt}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", OpenRouterModelsURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s answered %d", OpenRouterModelsURL, resp.StatusCode)
	}

	return ParseOpenRouterPrices(resp.Body)
}

// ParseOpenRouterPrices parses the JSON models response from OpenRouter.
func ParseOpenRouterPrices(r io.Reader) (map[string]RoutePrices, error) {
	var body struct {
		Data []struct {
			ID      string `json:"id"`
			Pricing struct {
				Prompt         any `json:"prompt"`
				Completion     any `json:"completion"`
				InputCacheRead any `json:"input_cache_read"`
			} `json:"pricing"`
		} `json:"data"`
	}

	// 10MB limit for models catalog
	limited := io.LimitReader(r, 10<<20)
	if err := json.NewDecoder(limited).Decode(&body); err != nil {
		return nil, fmt.Errorf("parse openrouter models: %w", err)
	}

	out := make(map[string]RoutePrices, len(body.Data))
	for _, m := range body.Data {
		in, err := PerMillion(m.Pricing.Prompt)
		if err != nil {
			continue
		}
		comp, err := PerMillion(m.Pricing.Completion)
		if err != nil {
			continue
		}
		cr, _ := PerMillion(m.Pricing.InputCacheRead)
		out[m.ID] = RoutePrices{
			Input:     in,
			Output:    comp,
			CacheRead: cr,
			Source:    OpenRouterModelsURL,
		}
	}
	return out, nil
}

// PerMillion converts a price per token (any of string, float64, int, json.Number)
// to a canonical decimal string representing USD per million tokens.
func PerMillion(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	var s string
	switch val := v.(type) {
	case string:
		s = strings.TrimSpace(val)
	case float64:
		s = strconv.FormatFloat(val, 'f', -1, 64)
	case int:
		s = strconv.Itoa(val)
	case int64:
		s = strconv.FormatInt(val, 10)
	case json.Number:
		s = val.String()
	default:
		return "", fmt.Errorf("unexpected price value type %T", v)
	}
	if s == "" {
		return "", nil
	}
	r, err := cardcost.Decimal(s)
	if err != nil {
		return "", err
	}
	million := big.NewRat(1000000, 1)
	res := new(big.Rat).Mul(r, million)
	return cardcost.Text(res), nil
}

// MatchPrices finds the published prices for a route's model in the catalog.
func MatchPrices(catalog map[string]RoutePrices, provider, model string) (RoutePrices, bool) {
	if p, ok := catalog[model]; ok {
		return p, true
	}
	// Direct prefix match:
	if p, ok := catalog["deepseek/"+model]; ok {
		return p, true
	}
	// Suffix matching:
	for id, p := range catalog {
		if strings.HasSuffix(id, "/"+model) {
			return p, true
		}
	}
	// If model has a slash (e.g. "x-ai/grok-4"), check if catalog has exact id or suffix
	parts := strings.Split(model, "/")
	if len(parts) > 1 {
		base := parts[len(parts)-1]
		for id, p := range catalog {
			if strings.HasSuffix(id, "/"+base) {
				return p, true
			}
		}
	}
	return RoutePrices{}, false
}

// CheckOver2x checks if any price field in newPrices is strictly more than 2x the old price.
func CheckOver2x(curFields map[string]string, newPrices RoutePrices) (field, oldVal, newVal string, over bool) {
	checks := []struct {
		field string
		old   string
		new   string
	}{
		{cardcost.FieldInput, curFields[cardcost.FieldInput], newPrices.Input},
		{cardcost.FieldOutput, curFields[cardcost.FieldOutput], newPrices.Output},
		{cardcost.FieldCacheRead, curFields[cardcost.FieldCacheRead], newPrices.CacheRead},
	}
	two := big.NewRat(2, 1)
	for _, c := range checks {
		if c.old == "" || c.old == "-" || c.old == "0" || c.new == "" || c.new == "-" || c.new == "0" {
			continue
		}
		rOld, errOld := cardcost.Decimal(c.old)
		rNew, errNew := cardcost.Decimal(c.new)
		if errOld != nil || errNew != nil || rOld.Sign() <= 0 {
			continue
		}
		limit := new(big.Rat).Mul(rOld, two)
		if rNew.Cmp(limit) > 0 {
			return c.field, c.old, c.new, true
		}
	}
	return "", "", "", false
}

// CheckPriceDiffersOver10Pct checks if any price field differs from list price by > 10%.
func CheckPriceDiffersOver10Pct(curFields map[string]string, listPrices RoutePrices) bool {
	checks := []struct {
		old string
		new string
	}{
		{curFields[cardcost.FieldInput], listPrices.Input},
		{curFields[cardcost.FieldOutput], listPrices.Output},
		{curFields[cardcost.FieldCacheRead], listPrices.CacheRead},
	}
	tenth := big.NewRat(1, 10)
	for _, c := range checks {
		if c.old == "" && c.new != "" {
			return true
		}
		if c.old != "" && c.new == "" {
			return true
		}
		if c.old == "" && c.new == "" {
			continue
		}
		rOld, errOld := cardcost.Decimal(c.old)
		rNew, errNew := cardcost.Decimal(c.new)
		if errOld != nil || errNew != nil {
			return true
		}
		if rOld.Sign() == 0 {
			if rNew.Sign() != 0 {
				return true
			}
			continue
		}
		diff := new(big.Rat).Abs(new(big.Rat).Sub(rNew, rOld))
		bound := new(big.Rat).Mul(rOld, tenth)
		if diff.Cmp(bound) > 0 {
			return true
		}
	}
	return false
}
