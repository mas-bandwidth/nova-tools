package swarm

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProviderReadDeadlinesStayUnderNinetySeconds(t *testing.T) {
	t.Parallel()

	require.Positive(t, ProviderHeaderTimeout, "header deadline is %s, want a black-holed attempt under 90s", ProviderHeaderTimeout)
	require.Less(t, ProviderHeaderTimeout, 90*time.Second, "header deadline is %s, want a black-holed attempt under 90s", ProviderHeaderTimeout)
	require.Positive(t, ProviderChunkTimeout, "chunk deadline is %s, want a stalled stream under 90s", ProviderChunkTimeout)
	require.Less(t, ProviderChunkTimeout, 90*time.Second, "chunk deadline is %s, want a stalled stream under 90s", ProviderChunkTimeout)
	require.Equal(t, 45*time.Second, ProviderBodySilence, "body silence is %s, want 45s and under 90s", ProviderBodySilence)
	require.Less(t, ProviderBodySilence, 90*time.Second, "body silence is %s, want 45s and under 90s", ProviderBodySilence)
}

func TestApplyProviderReadDeadlineWritesBothAndKeepsTheKey(t *testing.T) {
	t.Parallel()

	in := []byte(`{"provider":{"deepseek":{"options":{"apiKey":"k","baseURL":"https://example.invalid"}}}}`)
	out := ApplyProviderReadDeadline(in, "deepseek")
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(out, &cfg))
	opts := cfg["provider"].(map[string]any)["deepseek"].(map[string]any)["options"].(map[string]any)
	require.Equal(t, "k", opts["apiKey"], "the existing options moved: %#v", opts)
	require.Equal(t, "https://example.invalid", opts["baseURL"], "the existing options moved: %#v", opts)
	require.Equal(t, float64(ProviderHeaderTimeout/time.Millisecond), opts["headerTimeout"], "headerTimeout = %v", opts["headerTimeout"])
	require.Equal(t, float64(ProviderChunkTimeout/time.Millisecond), opts["chunkTimeout"], "chunkTimeout = %v", opts["chunkTimeout"])
}

func TestApplyProviderReadDeadlineLeavesAnUnreadableConfig(t *testing.T) {
	t.Parallel()

	in := []byte(`not json`)
	out := ApplyProviderReadDeadline(in, "deepseek")
	require.Equal(t, string(in), string(out), "an unreadable config was rewritten:\n%s", out)
}

func TestApplyProviderReadDeadlineCreatesAMissingProvider(t *testing.T) {
	t.Parallel()

	out := ApplyProviderReadDeadline([]byte(`{}`), "deepseek")
	require.Contains(t, string(out), `"headerTimeout"`, "a built-in provider with no entry got no deadline:\n%s", out)
	require.Contains(t, string(out), `"chunkTimeout"`, "a built-in provider with no entry got no deadline:\n%s", out)
}

func TestALostResponseIsNotALaunchFailure(t *testing.T) {
	t.Parallel()

	// The server accepted the request, then the response never came. That is
	// UNKNOWN. A second launch would be a second request.
	for _, tail := range []string{
		"SSE read timed out",
		"Provider response headers timed out after 45000ms",
		"Headers Timeout Error",
		"HeadersTimeoutError: Headers Timeout Error",
	} {
		_, ok := ProviderLaunchFailure([]byte(tail))
		assert.False(t, ok, "%q is classified as a launch failure; it must not retry", tail)
		assert.True(t, LostResponse([]byte(tail)), "%q was not recognized as a lost response", tail)
	}
	_, ok := ProviderLaunchFailure([]byte("Unexpected server error ref=err_fake"))
	require.True(t, ok, "the inherited classifier still matches a server-error tail")
	// The match is the tail text. It is not evidence the provider never accepted the request.
	require.False(t, LostResponse([]byte("Unexpected server error ref=err_fake")), "a server error is not a lost response")
	require.False(t, LostResponse([]byte("go test ran with -timeout 30s and passed")), "a card's own timeout word is not a lost provider response")
}
