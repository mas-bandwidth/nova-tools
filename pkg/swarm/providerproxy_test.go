// The provider proxy logic that owns no socket; the tests over a real upstream are in providerproxy_socket_functional_test.go.

package swarm

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPointProviderAtProxyKeepsTheKey(t *testing.T) {
	t.Parallel()

	in := []byte(`{"provider":{"fake":{"options":{"apiKey":"k","baseURL":"http://127.0.0.1:9/v1"}}}}`)
	out, ok := PointProviderAtProxy(in, "fake", "http://127.0.0.1:8/v1")
	require.True(t, ok, "the base URL was not retargeted")
	require.Equal(t, "http://127.0.0.1:8/v1", ProviderBaseURL(out, "fake"), "baseURL = %q", ProviderBaseURL(out, "fake"))
	require.Contains(t, string(out), `"apiKey": "k"`, "the key was dropped:\n%s", out)
	_, ok = PointProviderAtProxy([]byte(`{"provider":{"fake":{"options":{}}}}`), "fake", "http://127.0.0.1:8/v1")
	require.False(t, ok, "a provider with no base URL was pointed at the proxy")
}

func TestProviderProxyEligibleIsHTTPOnly(t *testing.T) {
	t.Parallel()

	require.True(t, ProviderProxyEligible("http://127.0.0.1:9/v1"), "an http base URL was not eligible")
	require.False(t, ProviderProxyEligible(""), "a non-http base URL was eligible")
	require.False(t, ProviderProxyEligible("not a url"), "a non-http base URL was eligible")
	require.False(t, ProviderProxyEligible("file:///tmp/x"), "a non-http base URL was eligible")
}
