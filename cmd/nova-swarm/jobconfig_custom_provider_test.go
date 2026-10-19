package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// A provider with no opencode catalog entry is written as a custom provider block from its
// own fields -- base URL, key env name, npm adapter -- and the key is only the {env:NAME}
// reference, never the value. The proxy opens in front of the block's own base (its
// transient refusals are sent again there, pkg/swarm/providerretry.go), so the harness is
// pointed at the proxy with its read clocks off; the test's proxy is built without a
// listener, so no socket is bound.
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
	const harnessHost = "127.0.0.1:1"
	var upstream string
	cfg := nativeRunConfig{
		model:          provider + "/" + model,
		label:          "c1",
		providerDirect: &swarm.DirectProvider{BaseURL: base, KeyEnv: keyEnv, NPMAdapter: "@ai-sdk/openai-compatible"},
		listenProxy: func(pc swarm.ProviderProxyConfig) (*swarm.ProviderProxy, error) {
			upstream = pc.Upstream
			return swarm.NewProviderProxy(pc, harnessHost)
		},
	}

	var notes bytes.Buffer
	sha, reason, proxy := writeJobConfig(cfg, provider, dataHome, jobDir, nil, &notes)
	require.Empty(t, reason, "the config is written")
	require.NotEmpty(t, sha, "the config's sha8 is recorded")
	require.NotNil(t, proxy, "an explicit provider block is proxied")
	t.Cleanup(func() { _ = proxy.Close() })
	assert.Equal(t, base, upstream, "the proxy forwards to the block's own base")
	assert.Equal(t, base, proxy.Upstream(), "the proxy's upstream is the provider's base")

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
	assert.Equal(t, "http://"+harnessHost+"/v1", pp.Options["baseURL"], "the harness is pointed at the proxy, the base's path kept")
	assert.Equal(t, false, pp.Options["headerTimeout"], "the harness's header clock is off, so a request waiting out a refusal is never aborted by it")
	assert.Equal(t, false, pp.Options["timeout"], "the harness's request clock is off")
	assert.Equal(t, "{env:"+keyEnv+"}", pp.Options["apiKey"], "the apiKey is the env reference, the variable name only")
	assert.Contains(t, pp.Models, model, "the route's model is declared under the provider")
	var declared struct {
		Limit struct {
			Output  int `json:"output"`
			Context int `json:"context"`
		} `json:"limit"`
	}
	require.NoError(t, json.Unmarshal(pp.Models[model], &declared))
	assert.Equal(t, swarm.DefaultDirectOutputLimit, declared.Limit.Output, "a row with no output limit gets the default, never the harness's own maximum")
	assert.NotContains(t, string(body), keyValue, "the key's value appears nowhere in the file")
}

// The route's provider row reaches the job writer in production: nova-sprint reads the
// row from nova-config and the member hands its three fields to native, which resolves
// them into the custom block before writeJobConfig. A worker description naming the same
// provider is the other real source of the same block. Both are pure: no socket, no key
// value.
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

// A limited direct route (nova-sprint routes limit on Ultramarine's provider or model) takes
// its budget on every request: the proxy now opens over the direct block, so the budget the
// sprint's server holds is wired into it, and a proxy with no budget server has none.
func TestALimitedDirectRouteTakesItsBudgetInTheProxy(t *testing.T) {
	t.Parallel()
	const provider, base = "acme", "https://api.acme.invalid/v1"
	for _, limited := range []bool{true, false} {
		var got swarm.ProviderProxyConfig
		cfg := nativeRunConfig{
			model:          provider + "/nova-1-flash",
			label:          "c1",
			providerDirect: &swarm.DirectProvider{BaseURL: base, KeyEnv: "ACME_TEST_API_KEY"},
			listenProxy: func(pc swarm.ProviderProxyConfig) (*swarm.ProviderProxy, error) {
				got = pc
				return swarm.NewProviderProxy(pc, "127.0.0.1:1")
			},
		}
		if limited {
			cfg.budgetServer, cfg.budgetAs, cfg.budgetSlots = "127.0.0.1:1", "reader-1", true
			cfg.budgetTake = func(context.Context, string) (bool, time.Duration, error) { return true, 0, nil }
		}
		var notes bytes.Buffer
		_, reason, proxy := writeJobConfig(cfg, provider, t.TempDir(), t.TempDir(), nil, &notes)
		require.Empty(t, reason)
		require.NotNil(t, proxy, "limited=%v: the direct block is proxied", limited)
		t.Cleanup(func() { _ = proxy.Close() })
		assert.Equal(t, base, got.Upstream)
		assert.NotNil(t, got.Log, "the proxy's refusal lines go to the run's notes")
		if limited {
			require.NotNil(t, got.Budget, "a limited direct route's every request takes the budget")
			assert.NotNil(t, got.Budget.Release, "and frees its concurrency slot")
		} else {
			assert.Nil(t, got.Budget, "an unlimited route takes no budget")
		}
	}
}
