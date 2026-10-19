package swarm

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// directModelLimitOf declares provider direct over a config whose provider entry already
// names model (as DeclareRouteModel leaves it) and returns that model's written limit.
func directModelLimitOf(t *testing.T, p DirectProvider) map[string]any {
	t.Helper()
	raw := []byte(`{"provider":{"acme":{"models":{"nova-1-flash":{}}}}}`)
	out, ok := DeclareDirectProvider(raw, "acme", p)
	require.True(t, ok, "the block is declared")
	var cfg struct {
		Provider map[string]struct {
			Models map[string]struct {
				Limit map[string]any `json:"limit"`
			} `json:"models"`
		} `json:"provider"`
	}
	require.NoError(t, json.Unmarshal(out, &cfg))
	model, has := cfg.Provider["acme"].Models["nova-1-flash"]
	require.True(t, has, "the declared model is kept")
	require.NotNil(t, model.Limit, "the model carries a limit")
	return model.Limit
}

// A provider row with limits carries them into the model's opencode limit, as
// provider.<id>.models.<model>.limit {output, context}.
func TestDirectProviderWithALimitCarriesIt(t *testing.T) {
	t.Parallel()
	limit := directModelLimitOf(t, DirectProvider{BaseURL: "https://api.acme.invalid/v1", KeyEnv: "ACME_API_KEY", OutputLimit: 32768, ContextLimit: 262144})
	assert.EqualValues(t, 32768, limit["output"], "the declared output limit is written")
	assert.EqualValues(t, 262144, limit["context"], "the declared context limit is written")
}

// A provider row with no limit gets the default output limit, so the harness never asks
// for more output than the provider allows (Ultramarine 1 Flash's 400 at 128000), and a
// context of 0, opencode's unknown, because the pinned harness refuses a limit without one.
func TestDirectProviderWithoutALimitGetsTheDefault(t *testing.T) {
	t.Parallel()
	limit := directModelLimitOf(t, DirectProvider{BaseURL: "https://api.acme.invalid/v1", KeyEnv: "ACME_API_KEY"})
	assert.EqualValues(t, DefaultDirectOutputLimit, limit["output"], "an undeclared output limit takes the default")
	assert.EqualValues(t, 65536, limit["output"], "the default is 65536")
	assert.EqualValues(t, 0, limit["context"], "an undeclared context is written as opencode's unknown, 0")
}

// A limit the config already carries is kept key by key; only a missing key is filled.
func TestDirectProviderKeepsALimitAlreadyThere(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"provider":{"acme":{"models":{"m":{"limit":{"output":1000}}}}}}`)
	out, ok := DeclareDirectProvider(raw, "acme", DirectProvider{BaseURL: "https://api.acme.invalid/v1", KeyEnv: "ACME_API_KEY", OutputLimit: 9, ContextLimit: 5000})
	require.True(t, ok)
	var cfg struct {
		Provider map[string]struct {
			Models map[string]struct {
				Limit map[string]any `json:"limit"`
			} `json:"models"`
		} `json:"provider"`
	}
	require.NoError(t, json.Unmarshal(out, &cfg))
	limit := cfg.Provider["acme"].Models["m"].Limit
	assert.EqualValues(t, 1000, limit["output"], "the output already there is kept")
	assert.EqualValues(t, 5000, limit["context"], "the missing context is filled")
}
