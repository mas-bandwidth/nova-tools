package swarm

import (
	"encoding/json"
	"strings"
)

// DeclareRouteModel names a launch's model in the job's harness config as a model of its
// provider (`provider.<provider>.models.<model>: {}`), so the harness knows the model
// whatever its catalog holds at start. An entry already there is kept as it is.
//
// Every job runs the harness in a fresh data home, so the model catalog fetches at each
// start; when that fetch is slow or loses the race it falls back to the snapshot built
// into the binary, which predates the newer models, and the launch fails with
// `ProviderModelNotFoundError`. Declaring the model in the harness config prevents that
// failure whatever the catalog fetch yields.
//
// ok is false when the config is not a JSON object or the model is not provider/model;
// the body is then returned unchanged.
func DeclareRouteModel(body []byte, provider, model string) ([]byte, bool) {
	provider, model = strings.TrimSpace(provider), strings.TrimSpace(model)
	if provider == "" || model == "" {
		return body, false
	}
	cfg := map[string]any{}
	if err := json.Unmarshal(body, &cfg); err != nil {
		return body, false
	}
	providers, _ := cfg["provider"].(map[string]any)
	if providers == nil {
		if _, other := cfg["provider"]; other {
			return body, false
		}
		providers = map[string]any{}
		cfg["provider"] = providers
	}
	entry, _ := providers[provider].(map[string]any)
	if entry == nil {
		if _, other := providers[provider]; other {
			return body, false
		}
		entry = map[string]any{}
		providers[provider] = entry
	}
	models, _ := entry["models"].(map[string]any)
	if models == nil {
		if _, other := entry["models"]; other {
			return body, false
		}
		models = map[string]any{}
		entry["models"] = models
	}
	if _, declared := models[model]; !declared {
		models[model] = map[string]any{}
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return body, false
	}
	return out, true
}

// LocalProvider is the provider word of a local route (docs/SPEC-LOCAL.md, "Fleet").
const LocalProvider = "local"

// DeclareLocalProvider declares the provider local in the job's harness config at base,
// the OpenAI-compatible endpoint of the fleet machine that serves the route's model
// (`provider.local: {npm, name, options.baseURL}`), keeping any models already declared
// under it. It carries no key: a local engine wants none, and the harness reaches a
// keyless provider through its baseURL alone. ok is false when the config is not a JSON
// object; the body is then returned unchanged.
func DeclareLocalProvider(body []byte, base string) ([]byte, bool) {
	cfg := map[string]any{}
	if err := json.Unmarshal(body, &cfg); err != nil {
		return body, false
	}
	providers, _ := cfg["provider"].(map[string]any)
	if providers == nil {
		if _, other := cfg["provider"]; other {
			return body, false
		}
		providers = map[string]any{}
		cfg["provider"] = providers
	}
	entry := map[string]any{"npm": "@ai-sdk/openai-compatible", "name": LocalProvider, "options": map[string]any{"baseURL": base}}
	if old, ok := providers[LocalProvider].(map[string]any); ok && old["models"] != nil {
		entry["models"] = old["models"]
	}
	providers[LocalProvider] = entry
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return body, false
	}
	return out, true
}
