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
