package main

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// A provider with no opencode catalog entry is written as a custom provider block from its
// own fields -- base URL, key env name, npm adapter -- and the key is only the {env:NAME}
// reference, never the value. The block names a provider the providers table does not know,
// so no proxy opens and no socket is bound.
func TestJobConfigWritesCustomProviderBlockForUncataloguedProvider(t *testing.T) {
	t.Parallel()
	const (
		provider = "acme"
		keyEnv   = "ACME_TEST_API_KEY"
		keyValue = "sk-acme-not-a-real-key-value"
		model    = "nova-1-flash"
		base     = "https://api.acme.invalid/v1"
	)
	// The fake key exists in this process only so the test can prove it is not written.
	// t.Setenv refuses a parallel test, so set the unique name directly and put it back.
	prev, had := os.LookupEnv(keyEnv)
	require.NoError(t, os.Setenv(keyEnv, keyValue))
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(keyEnv, prev)
		} else {
			_ = os.Unsetenv(keyEnv)
		}
	})
	dataHome, jobDir := t.TempDir(), t.TempDir()
	cfg := nativeRunConfig{
		model:          provider + "/" + model,
		label:          "c1",
		providerDirect: &swarm.DirectProvider{BaseURL: base, KeyEnv: keyEnv, NPM: "@ai-sdk/openai-compatible"},
	}

	var notes bytes.Buffer
	sha, reason, proxy := writeJobConfig(cfg, provider, dataHome, jobDir, nil, &notes)
	require.Empty(t, reason, "the config is written")
	require.NotEmpty(t, sha, "the config's sha8 is recorded")
	assert.Nil(t, proxy, "an explicit provider block opens no proxy and no socket")

	body := writtenJobConfig(t, dataHome)
	var parsed struct {
		Provider map[string]struct {
			NPM     string                     `json:"npm"`
			Options map[string]any             `json:"options"`
			Models  map[string]json.RawMessage `json:"models"`
		} `json:"provider"`
	}
	require.NoError(t, json.Unmarshal(body, &parsed), "the written config is JSON")
	pp, ok := parsed.Provider[provider]
	require.True(t, ok, "the uncatalogued provider has a block")
	assert.Equal(t, "@ai-sdk/openai-compatible", pp.NPM, "the npm adapter is written")
	assert.Equal(t, base, pp.Options["baseURL"], "the provider's base URL is written")
	assert.Equal(t, "{env:"+keyEnv+"}", pp.Options["apiKey"], "the apiKey is the env reference, the variable name only")
	assert.Contains(t, pp.Models, model, "the route's model is declared under the provider")
	assert.NotContains(t, string(body), keyValue, "the key's value appears nowhere in the file")
}
