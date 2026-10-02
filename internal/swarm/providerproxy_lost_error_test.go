package swarm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A request that arrives after a body stall has lost the run is answered 502 "lost",
// and does not touch the upstream or increment the request count.
func TestProviderProxyRefusesRequestAfterLoss(t *testing.T) {
	t.Parallel()

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		discardReq(r)
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer up.Close()

	p, err := ListenProviderProxy(ProviderProxyConfig{
		Upstream: up.URL,
		Silence:  200 * time.Millisecond,
		After: func(time.Duration) <-chan time.Time {
			passed := make(chan time.Time, 1)
			passed <- time.Unix(0, 1)
			return passed
		},
	})
	require.NoError(t, err)
	defer p.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := proxyClient()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.HarnessURL(), strings.NewReader("card"))
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	_, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	require.True(t, p.Lost(), "proxy should be marked lost after silent body")

	// The second request arrives after the proxy is dead/lost.
	req2, err := http.NewRequestWithContext(ctx, http.MethodPost, p.HarnessURL(), strings.NewReader("after-loss"))
	require.NoError(t, err)

	resp2, err := client.Do(req2)
	require.NoError(t, err)
	defer resp2.Body.Close()

	body, err := io.ReadAll(resp2.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusBadGateway, resp2.StatusCode, "must return 502 Bad Gateway")
	assert.Equal(t, "lost\n", string(body), "must return 'lost\n' error message")
	assert.Equal(t, int64(1), p.Requests(), "must not increment requests after loss")
}
