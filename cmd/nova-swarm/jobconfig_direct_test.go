package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// A paperinstruments route (a provider the pinned harness's catalog does not know, whose
// table row carries a base URL and an envVar) is declared direct in the written opencode.json:
// the npm package, the env-ref apiKey and a baseURL (the read-deadline proxy's local URL,
// since the proxy stands in front of the route's table base) -- and no literal key.
func TestWriteJobConfigDirectProviderFromTable(t *testing.T) {
	t.Parallel()
	dataHome := t.TempDir()
	cfg := nativeRunConfig{model: "paperinstruments/ultramarine-1-flash", label: "c1"}

	var notes bytes.Buffer
	sha, reason, proxy := writeJobConfig(cfg, "paperinstruments", dataHome, t.TempDir(), nil, &notes)
	require.Empty(t, reason, "the config is written")
	require.NotEmpty(t, sha, "the config's sha8 is recorded")
	require.NotNil(t, proxy, "the read-deadline proxy stands in front of the table base")
	t.Cleanup(func() { assert.NoError(t, proxy.Close()) })

	body := writtenJobConfig(t, dataHome)
	var parsed struct {
		Provider map[string]struct {
			NPM     string         `json:"npm"`
			Options map[string]any `json:"options"`
		} `json:"provider"`
	}
	require.NoError(t, json.Unmarshal(body, &parsed), "the written config is JSON")
	pp := parsed.Provider["paperinstruments"]
	assert.Equal(t, "@ai-sdk/openai-compatible", pp.NPM, "the npm package is written")
	assert.Equal(t, "{env:PAPERINSTRUMENTS_API_KEY}", pp.Options["apiKey"], "the apiKey is the env reference")
	assert.Equal(t, proxy.HarnessURL(), pp.Options["baseURL"], "the baseURL is the read-deadline proxy's local URL")
	assert.NotContains(t, string(body), "sk-", "no literal key is written")
}

// A deepseek route (no table base URL) writes the same bytes and sha8 as a config built
// without the direct seed: the seed is a no-op for it.
func TestWriteJobConfigDeepseekUnchanged(t *testing.T) {
	t.Parallel()
	dataHome, jobDir := t.TempDir(), t.TempDir()
	cfg := nativeRunConfig{model: "deepseek/deepseek-chat", label: "c1"}

	var notes bytes.Buffer
	sha, reason, proxy := writeJobConfig(cfg, "deepseek", dataHome, jobDir, nil, &notes)
	require.Empty(t, reason, "the config is written")
	require.Nil(t, proxy, "deepseek has no table base: no proxy opens")

	before, merged := swarm.MergeFencePermission(nil, jobDir, nil)
	require.True(t, merged, "the fence merges into an empty config")
	before, ok := swarm.DeclareRouteModel(before, "deepseek", "deepseek-chat")
	require.True(t, ok, "the route's model is declared")
	before = swarm.ApplyProviderReadDeadline(before, "deepseek")

	assert.Equal(t, string(before), string(writtenJobConfig(t, dataHome)), "deepseek's config is byte-identical to one built without the direct seed")
	sum := sha256.Sum256(before)
	assert.Equal(t, sha, hex.EncodeToString(sum[:])[:8], "deepseek's sha8 is unchanged")
}
