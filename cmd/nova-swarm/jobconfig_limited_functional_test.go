//go:build functional

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// A fleet member's limited inception job (--model only, --budget-server named) gets its
// provider's base from the providers table, the proxy opens in front of that base, and the
// harness is pointed at the proxy; the apiKey stays the harness's own (nothing is written
// for it). The proxy's listener is a socket, so this case is functional.
func TestALimitedInceptionJobIsPointedAtTheBudgetProxy(t *testing.T) {
	t.Parallel()
	dataHome := t.TempDir()
	cfg := nativeRunConfig{model: "inception/mercury-3-preview-1002", label: "c1", budgetServer: "127.0.0.1:1", budgetAs: "m1",
		budgetTake: func(context.Context, string) (bool, time.Duration, error) { return true, 0, nil }}

	var notes bytes.Buffer
	_, reason, proxy := writeJobConfig(cfg, "inception", dataHome, t.TempDir(), nil, &notes)
	require.Empty(t, reason, "the config is written")
	require.NotNil(t, proxy, "the limited route opens its proxy")
	t.Cleanup(func() { assert.NoError(t, proxy.Close()) })

	body := writtenJobConfig(t, dataHome)
	var parsed struct {
		Provider map[string]struct {
			Options map[string]any `json:"options"`
		} `json:"provider"`
	}
	require.NoError(t, json.Unmarshal(body, &parsed), "the written config is JSON")

	assert.True(t, swarm.ProviderProxyEligible(swarm.ProviderTableBaseURL("inception")), "the table names an http base for inception")
	assert.Equal(t, swarm.ProviderTableBaseURL("inception"), proxy.Upstream(), "the proxy's upstream is the table's base")
	assert.Equal(t, proxy.HarnessURL(), swarm.ProviderBaseURL(body, "inception"), "the harness is pointed at the proxy")
	assert.NotContains(t, parsed.Provider["inception"].Options, "apiKey", "no apiKey is written: the harness sends the route's own key")
	assert.Equal(t, false, parsed.Provider["inception"].Options["headerTimeout"], "the harness's header clock is off, so a request stalled on its budget is never aborted by the harness")
	assert.Equal(t, false, parsed.Provider["inception"].Options["timeout"], "the harness's request clock is off for the same reason")
	assert.NotContains(t, notes.String(), "has no http baseURL", "the NATIVE NOTE of an unmetered limited route is not written")
}
