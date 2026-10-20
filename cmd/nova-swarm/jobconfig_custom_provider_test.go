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
		providerDirect: &swarm.DirectProvider{BaseURL: base, KeyEnv: keyEnv, NPMAdapter: "@ai-sdk/openai-compatible"},
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

// The route's provider fields reach the job writer: a caller may resolve the row's three
// fields before writeJobConfig, and a worker description naming the same provider is the
// other real source of the same block. Both are pure: no socket, no key value.
func TestRouteProviderDirectFromTheProvidersRow(t *testing.T) {
	t.Parallel()
	const (
		provider = "acme"
		base     = "https://api.acme.invalid/v1"
		keyEnv   = "ACME_TEST_API_KEY"
		npm      = "@ai-sdk/openai-compatible"
	)
	got := routeProviderDirect(nativeRunConfig{
		providerBaseURL: base, providerKeyEnv: keyEnv, providerNPM: npm,
	}, provider)
	require.NotNil(t, got, "a route provider row resolves to a custom block")
	assert.Equal(t, base, got.BaseURL)
	assert.Equal(t, keyEnv, got.KeyEnv)
	assert.Equal(t, npm, got.NPMAdapter)

	fromWorker := routeProviderDirect(nativeRunConfig{worker: &swarm.Worker{
		Provider: provider, BaseURL: base, Secret: keyEnv,
	}}, provider)
	require.NotNil(t, fromWorker, "a worker description naming the provider resolves too")
	assert.Equal(t, base, fromWorker.BaseURL)
	assert.Equal(t, keyEnv, fromWorker.KeyEnv)

	assert.Nil(t, routeProviderDirect(nativeRunConfig{}, provider), "a catalogued provider keeps the providers table")
	assert.Nil(t, routeProviderDirect(nativeRunConfig{providerBaseURL: base}, provider), "a base with no key variable is not a block")
}

// The declared output limit reaches the job config from both sources of a direct provider:
// a worker description names no row, so its model gets DefaultDirectOutputLimit; a provider
// row's max_output is carried through nativeRunConfig. The limit carries both keys because
// opencode v1.18.20 refuses one missing either, and a context none declared is 0.
func TestJobConfigDirectProviderCarriesTheOutputLimit(t *testing.T) {
	t.Parallel()
	const (
		provider = "paperinstruments"
		model    = "ultramarine-1-flash"
		keyEnv   = "PAPERINSTRUMENTS_API_KEY"
		base     = "https://api.paper.invalid/v1"
	)
	limitOf := func(t *testing.T, dataHome string) map[string]any {
		t.Helper()
		var parsed struct {
			Provider map[string]struct {
				Models map[string]struct {
					Limit map[string]any `json:"limit"`
				} `json:"models"`
			} `json:"provider"`
		}
		require.NoError(t, json.Unmarshal(writtenJobConfig(t, dataHome), &parsed), "the written config is JSON")
		return parsed.Provider[provider].Models[model].Limit
	}

	t.Run("worker description", func(t *testing.T) {
		t.Parallel()
		dataHome := t.TempDir()
		cfg := nativeRunConfig{
			model:  provider + "/" + model,
			label:  "c1",
			worker: &swarm.Worker{Provider: provider, Model: model, BaseURL: base, EnvVar: keyEnv},
		}
		cfg.providerDirect = routeProviderDirect(cfg, provider)
		require.NotNil(t, cfg.providerDirect, "the worker description declares the provider direct")
		var notes bytes.Buffer
		_, reason, proxy := writeJobConfig(cfg, provider, dataHome, t.TempDir(), nil, &notes)
		require.Empty(t, reason, "the config is written")
		assert.Nil(t, proxy, "an explicit provider block opens no proxy and no socket")
		limit := limitOf(t, dataHome)
		assert.Equal(t, float64(65536), limit["output"], "a worker description with no row gets the default output limit")
		assert.Equal(t, float64(0), limit["context"], "a missing context limit is written as 0")
	})

	t.Run("provider row", func(t *testing.T) {
		t.Parallel()
		dataHome := t.TempDir()
		cfg := nativeRunConfig{
			model:             provider + "/" + model,
			label:             "c1",
			providerBaseURL:   base,
			providerKeyEnv:    keyEnv,
			providerMaxOutput: 32000,
		}
		cfg.providerDirect = routeProviderDirect(cfg, provider)
		require.NotNil(t, cfg.providerDirect, "the provider row declares the provider direct")
		var notes bytes.Buffer
		_, reason, proxy := writeJobConfig(cfg, provider, dataHome, t.TempDir(), nil, &notes)
		require.Empty(t, reason, "the config is written")
		assert.Nil(t, proxy, "an explicit provider block opens no proxy and no socket")
		limit := limitOf(t, dataHome)
		assert.Equal(t, float64(32000), limit["output"], "the row's max_output is the model's output limit")
		assert.Equal(t, float64(0), limit["context"], "a missing context limit is written as 0")
	})
}
