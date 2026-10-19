package swarm

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A provider row's max_output declares the model's output limit, so the harness asks for
// no more than it (opencode sends its default output budget, which Paper Instruments
// refuses above 65536). The limit is written into the model's entry, keeping any limit
// the entry already carries.
func TestDirectProviderDeclaresTheModelOutputLimit(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"provider":{"paperinstruments":{"models":{"ultramarine-1-flash":{}}}}}`)
	out, ok := DeclareDirectProvider(raw, "paperinstruments", DirectProvider{
		BaseURL:   "https://api.paper.invalid/v1",
		KeyEnv:    "PAPERINSTRUMENTS_API_KEY",
		Model:     "ultramarine-1-flash",
		MaxOutput: 65536,
	})
	require.True(t, ok, "the provider is declared direct")

	var cfg map[string]any
	require.NoError(t, json.Unmarshal(out, &cfg), "the declared config is JSON:\n%s", out)
	pp := cfg["provider"].(map[string]any)["paperinstruments"].(map[string]any)
	entry := pp["models"].(map[string]any)["ultramarine-1-flash"].(map[string]any)
	limit := entry["limit"].(map[string]any)
	assert.Equal(t, float64(65536), limit["output"], "the model's output limit is written")
}

// A limit the model's entry already carries is kept: the declared output limit is added
// beside it, never over it.
func TestDirectProviderKeepsALimitTheEntryAlreadyHas(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"provider":{"acme":{"models":{"nova-1-flash":{"limit":{"context":128000}}}}}}`)
	out, ok := DeclareDirectProvider(raw, "acme", DirectProvider{
		BaseURL:   "https://api.acme.invalid/v1",
		KeyEnv:    "ACME_API_KEY",
		Model:     "nova-1-flash",
		MaxOutput: 65536,
	})
	require.True(t, ok, "the provider is declared direct")

	var cfg map[string]any
	require.NoError(t, json.Unmarshal(out, &cfg), "the declared config is JSON:\n%s", out)
	pp := cfg["provider"].(map[string]any)["acme"].(map[string]any)
	entry := pp["models"].(map[string]any)["nova-1-flash"].(map[string]any)
	limit := entry["limit"].(map[string]any)
	assert.Equal(t, float64(128000), limit["context"], "an existing context limit is kept")
	assert.Equal(t, float64(65536), limit["output"], "the model's output limit is written beside it")
}
