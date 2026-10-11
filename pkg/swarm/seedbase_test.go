package swarm

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The providers table names inception's HTTP API base, the one the pinned harness's own
// catalog gives its inception provider, so a limited inception route has an endpoint for
// the budget proxy (providerbudget.go, THE GLOBAL RATE BUDGET).
func TestTheTableNamesInceptionsBase(t *testing.T) {
	t.Parallel()
	base := ProviderTableBaseURL("inception")
	u, err := url.Parse(base)
	require.NoError(t, err, "inception's table base parses")
	assert.Equal(t, "https", u.Scheme, "inception's base is an https endpoint")
	assert.Equal(t, "/v1", u.Path, "inception's base is its OpenAI-compatible /v1 API")
	assert.True(t, ProviderProxyEligible(base), "the budget proxy can stand in front of inception's base")
	assert.Empty(t, ProviderTableBaseURL("deepseek"), "deepseek is left unseeded: only its concurrency cap is enforced, at the take")
	assert.Empty(t, ProviderTableBaseURL("no-such-provider"), "a provider with no row has no base")
}

// SeedProviderBaseURL writes the base into an entry that has none, keeps a base already
// there, and keeps every other option.
func TestSeedProviderBaseURL(t *testing.T) {
	t.Parallel()
	declared := []byte(`{"provider":{"inception":{"models":{"m":{}},"options":{"headerTimeout":45000}}}}`)
	seeded, ok := SeedProviderBaseURL(declared, "inception", "http://127.0.0.1:8/v1")
	require.True(t, ok, "a declared entry is seeded")
	assert.Equal(t, "http://127.0.0.1:8/v1", ProviderBaseURL(seeded, "inception"), "the base is written")
	assert.EqualValues(t, 45000, providerOptions(seeded, "inception")["headerTimeout"], "the other options stay")

	own := []byte(`{"provider":{"inception":{"options":{"baseURL":"http://127.0.0.1:9/v1"}}}}`)
	kept, ok := SeedProviderBaseURL(own, "inception", "http://127.0.0.1:8/v1")
	require.True(t, ok, "an entry with its own base is accepted")
	assert.Equal(t, "http://127.0.0.1:9/v1", ProviderBaseURL(kept, "inception"), "a base already there is kept")

	_, ok = SeedProviderBaseURL(declared, "inception", "")
	assert.False(t, ok, "an empty base seeds nothing")
	_, ok = SeedProviderBaseURL([]byte(`{"provider":{}}`), "inception", "http://127.0.0.1:8/v1")
	assert.False(t, ok, "a config with no entry for the provider is not seeded")
}

// A limited route's harness clocks are turned off so a stall in the proxy is never an abort;
// chunkTimeout, which runs only after headers, stays.
func TestLetTheHarnessWaitOutAStall(t *testing.T) {
	t.Parallel()
	in := []byte(`{"provider":{"inception":{"options":{"baseURL":"http://127.0.0.1:1/v1","headerTimeout":45000,"chunkTimeout":45000}}}}`)
	out, ok := LetTheHarnessWaitOutAStall(in, "inception")
	require.True(t, ok, "an entry with options is written")
	opts := providerOptions(out, "inception")
	assert.Equal(t, false, opts["headerTimeout"], "the header clock is off")
	assert.Equal(t, false, opts["timeout"], "the request clock is off")
	assert.EqualValues(t, 45000, opts["chunkTimeout"], "the chunk clock stays")
	assert.Equal(t, "http://127.0.0.1:1/v1", opts["baseURL"], "the proxy base stays")

	_, ok = LetTheHarnessWaitOutAStall([]byte(`{"provider":{}}`), "inception")
	assert.False(t, ok, "no entry, nothing written")
}
