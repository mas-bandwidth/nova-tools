package swarm

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDeclareRouteModelNamesTheModelInTheConfig: the route's model is declared under its
// provider in a config with none, beside a provider entry that has options, and beside
// models already declared; an entry already there is kept byte for byte in meaning; a
// config that is not an object, or a provider that is not an object, is left unchanged.
func TestDeclareRouteModelNamesTheModelInTheConfig(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, body, provider, model string
		ok                          bool
		want                        string // the models object of the provider, as JSON
	}{
		{"no provider block", `{"$schema":"x"}`, "openrouter", "x-ai/grok-4.7", true, `{"x-ai/grok-4.7":{}}`},
		{"a provider with options", `{"provider":{"opencode":{"options":{"chunkTimeout":45000}}}}`, "opencode", "qwen3.8-max", true, `{"qwen3.8-max":{}}`},
		{"other models kept", `{"provider":{"openrouter":{"models":{"a/b":{"name":"B"}}}}}`, "openrouter", "x-ai/grok-4.7", true, `{"a/b":{"name":"B"},"x-ai/grok-4.7":{}}`},
		{"already declared, kept", `{"provider":{"openrouter":{"models":{"x-ai/grok-4.7":{"limit":{"output":8}}}}}}`, "openrouter", "x-ai/grok-4.7", true, `{"x-ai/grok-4.7":{"limit":{"output":8}}}`},
		{"not an object", `[1,2]`, "openrouter", "m", false, ""},
		{"a provider that is not an object", `{"provider":{"openrouter":"x"}}`, "openrouter", "m", false, ""},
		{"no model", `{}`, "openrouter", "", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, ok := DeclareRouteModel([]byte(c.body), c.provider, c.model)
			require.Equal(t, c.ok, ok)
			if !ok {
				assert.Equal(t, c.body, string(out), "an undeclarable config is returned unchanged")
				return
			}
			var cfg map[string]any
			require.NoError(t, json.Unmarshal(out, &cfg))
			models := cfg["provider"].(map[string]any)[c.provider].(map[string]any)["models"]
			got, err := json.Marshal(models)
			require.NoError(t, err)
			assert.JSONEq(t, c.want, string(got))
		})
	}
	// the options beside the models are kept
	out, ok := DeclareRouteModel([]byte(`{"provider":{"opencode":{"options":{"chunkTimeout":45000}}}}`), "opencode", "qwen3.8-max")
	require.True(t, ok)
	assert.Contains(t, string(out), `"chunkTimeout": 45000`)
}

// TestCauseFromTextNamesTheHarnessCatalog: the harness's printed refusal of a model
// (verbatim shape from opencode v1.18.20, 2026-10-01) is class unknown-model with the
// message `model not found in the harness catalog: <model>`, never `other`.
func TestCauseFromTextNamesTheHarnessCatalog(t *testing.T) {
	t.Parallel()
	line := `message=failed ref=err_1ad647ee error="ProviderModelNotFoundError: Model not found: openrouter/x-ai/grok-4.7. Did you mean: x-ai/grok-4.20, x-ai/grok-4.20-multi-agent, x-ai/grok-4.3?"`
	c := CauseFromText(line)
	assert.Equal(t, CauseUnknownModel, c.Class)
	assert.Equal(t, "model not found in the harness catalog: openrouter/x-ai/grok-4.7", c.Message)
	assert.Equal(t, "provider: class=unknown-model status=- msg=model not found in the harness catalog: openrouter/x-ai/grok-4.7", c.Reason())
}
