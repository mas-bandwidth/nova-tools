package route

// pin.go: OpenRouter provider preferences pinned per route (#3151).
//
// The same model costs 9x through another door (fold 2026-09-22): OpenRouter
// picks an upstream provider per request unless the request pins one, and
// the usage rows could not say which one served. So every OpenRouter route
// that can run (allowed or held) carries `pin:`, the one upstream provider
// slug measured cheapest for its model, and the card's harness config sends
// it as OpenRouter's provider block with fallbacks off. With one provider
// and no fallbacks the upstream is known by construction, so the usage row
// records it as a fact rather than asking anyone.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ViaOpenRouter is the via of the routes OpenRouter serves.
const ViaOpenRouter = "openrouter"

// OpenRouterKeyEnv is the key the harness config names for OpenRouter: the
// variable's NAME, never its value.
const OpenRouterKeyEnv = "OPENROUTER_API_KEY"

// ProviderPrefs is the provider block of an OpenRouter chat request: try
// exactly the providers in Order and no other.
type ProviderPrefs struct {
	Order          []string `json:"order"`
	AllowFallbacks bool     `json:"allow_fallbacks"`
}

// Prefs is the route's OpenRouter provider block, and false for a route
// OpenRouter does not serve or one with no pin (a dropped row never runs).
func (r Row) Prefs() (ProviderPrefs, bool) {
	if r.Via != ViaOpenRouter || r.Pin == "" {
		return ProviderPrefs{}, false
	}
	return ProviderPrefs{Order: []string{r.Pin}, AllowFallbacks: false}, true
}

// HarnessConfig is the opencode.json the card's harness reads for an
// OpenRouter route: the openrouter provider with its key named by variable
// and the route's model carrying the provider block, which the harness sends
// in every request body for that model. False when the route has no Prefs.
func (r Row) HarnessConfig() ([]byte, bool) {
	prefs, ok := r.Prefs()
	if !ok {
		return nil, false
	}
	cfg := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"provider": map[string]any{
			ViaOpenRouter: map[string]any{
				"options": map[string]any{"apiKey": "{env:" + OpenRouterKeyEnv + "}"},
				"models": map[string]any{
					r.Model: map[string]any{"options": map[string]any{"provider": prefs}},
				},
			},
		},
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, false
	}
	return append(raw, '\n'), true
}

// checkPin is the parse rule: a pin is one provider slug on an OpenRouter
// route, and every OpenRouter route that can run carries one.
func checkPin(r Row) error {
	switch {
	case r.Pin != "" && r.Via != ViaOpenRouter:
		return fmt.Errorf("route %s: pin %s on via %s; only an openrouter route pins an upstream provider", r.Route, r.Pin, r.Via)
	case strings.ContainsAny(r.Pin, " \t,[]"):
		return fmt.Errorf("route %s: pin %q is one provider slug", r.Route, r.Pin)
	case r.Via == ViaOpenRouter && r.State != Dropped && r.Pin == "":
		return fmt.Errorf("route %s: an openrouter route that can run needs pin: <provider slug> (#3151)", r.Route)
	}
	return nil
}
