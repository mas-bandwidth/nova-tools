package swarm

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func directProviderEntry(raw []byte, provider string) map[string]any {
	var cfg map[string]any
	if json.Unmarshal(raw, &cfg) != nil {
		return nil
	}
	providers, _ := cfg["provider"].(map[string]any)
	entry, _ := providers[provider].(map[string]any)
	return entry
}

// A provider whose table row carries a base URL and an envVar is declared direct in the
// job config as an OpenAI-compatible provider: npm, the table's baseURL, and the apiKey as
// the {env:<envVar>} reference. The key is never a value read from the environment.
func TestSeedDirectProvider(t *testing.T) {
	t.Parallel()
	declared := []byte(`{"provider":{"paperinstruments":{"models":{"ultramarine-1-flash":{}}}}}`)
	seeded, ok := SeedDirectProvider(declared, "paperinstruments")
	require.True(t, ok, "a paperinstruments entry is seeded direct")
	opts := providerOptions(seeded, "paperinstruments")
	assert.Equal(t, "@ai-sdk/openai-compatible", directProviderEntry(seeded, "paperinstruments")["npm"], "the npm package is written")
	assert.Equal(t, ProviderTableBaseURL("paperinstruments"), opts["baseURL"], "the table's baseURL is written")
	assert.Equal(t, "{env:PAPERINSTRUMENTS_API_KEY}", opts["apiKey"], "the apiKey is the env reference, never a value")

	own := []byte(`{"provider":{"paperinstruments":{"options":{"baseURL":"http://127.0.0.1:9/v1","apiKey":"{env:SOME_OTHER_KEY}"}}}}`)
	kept, ok := SeedDirectProvider(own, "paperinstruments")
	require.True(t, ok, "an entry with its own options is accepted")
	keptOpts := providerOptions(kept, "paperinstruments")
	assert.Equal(t, "http://127.0.0.1:9/v1", keptOpts["baseURL"], "a baseURL already there is kept")
	assert.Equal(t, "{env:SOME_OTHER_KEY}", keptOpts["apiKey"], "an apiKey already there is kept")

	in := []byte(`{"provider":{"deepseek":{"models":{"deepseek-chat":{}}}}}`)
	out, ok := SeedDirectProvider(in, "deepseek")
	assert.False(t, ok, "deepseek has no table base URL: nothing is seeded")
	assert.Equal(t, string(in), string(out), "deepseek's config is returned byte for byte")

	unknown := []byte(`{"provider":{"nope":{"options":{}}}}`)
	out, ok = SeedDirectProvider(unknown, "nope")
	assert.False(t, ok, "a provider with no row is not seeded")
	assert.Equal(t, string(unknown), string(out), "an unknown provider's config is returned byte for byte")
}
