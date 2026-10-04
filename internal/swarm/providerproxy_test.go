package swarm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func proxyClient() *http.Client {
	return &http.Client{
		Transport:     &http.Transport{Proxy: nil, DisableCompression: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func discardReq(r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	_ = r.Body.Close()
}

// TestSilentBodyAfterHeadersIsOneUpstreamRequest is the body-read deadline on
// a real timer. Headers have arrived. No body byte follows. The proxy aborts
// that one request and refuses the next one, so the upstream count stays 1.
func TestSilentBodyAfterHeadersIsOneUpstreamRequest(t *testing.T) {
	t.Parallel()

	var upstream atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream.Add(1)
		discardReq(r)
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer up.Close()

	// The body's silence is an event the test fires, never a gap it waits out
	// (nova-tools#4328): the clock answers at once, as if the whole gap passed.
	p, err := ListenProviderProxy(ProviderProxyConfig{Upstream: up.URL, Silence: 200 * time.Millisecond,
		After: func(time.Duration) <-chan time.Time {
			passed := make(chan time.Time, 1)
			passed <- time.Unix(0, 1)
			return passed
		}})
	require.NoError(t, err)
	defer p.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	client := proxyClient()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.HarnessURL(), strings.NewReader("card"))
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	_, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Error(t, readErr, "a silent body was delivered as a finished response")
	require.True(t, p.Lost(), "a silent body was not unknown")
	gap := p.SilenceWall()
	require.Positive(t, gap, "the body gap was not measured: %s", gap)

	again, err := http.NewRequestWithContext(ctx, http.MethodPost, p.HarnessURL(), strings.NewReader("again"))
	require.NoError(t, err)
	resp2, err := client.Do(again)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp2.Body)
	resp2.Body.Close()

	got, upn := p.Requests(), upstream.Load()
	require.Equal(t, int64(1), got, "requests=%d upstream=%d, want 1 and 1", got, upn)
	require.Equal(t, int32(1), upn, "requests=%d upstream=%d, want 1 and 1", got, upn)
	t.Logf("CANARY requests=%d silence_ms=%d verdict=unknown", p.Requests(), p.SilenceWall().Milliseconds())
}

// TestBodyThatResumesInsideTheDeadlineIsNotUnknown: headers, a pause shorter
// than the gap, then the body. That is success. Nothing is marked lost. The
// gap is the After seam: the upstream holds its body until the proxy has armed
// the silence timer (the proxy is waiting on the body), and that timer never
// fires, so the pause is inside the deadline by construction, with no
// duration waited.
func TestBodyThatResumesInsideTheDeadlineIsNotUnknown(t *testing.T) {
	t.Parallel()

	armed := make(chan struct{}, 1)
	var upstream atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream.Add(1)
		discardReq(r)
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-armed: // the proxy is inside its body wait
		case <-r.Context().Done():
			return
		}
		_, _ = w.Write([]byte("ok\n"))
	}))
	defer up.Close()

	p, err := ListenProviderProxy(ProviderProxyConfig{
		Upstream: up.URL,
		After: func(time.Duration) <-chan time.Time {
			select {
			case armed <- struct{}{}:
			default:
			}
			return nil // a gap that never ends: the body always comes inside it
		},
	})
	require.NoError(t, err)
	defer p.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.HarnessURL(), strings.NewReader("card"))
	require.NoError(t, err)
	resp, err := proxyClient().Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.NoError(t, err, "a body inside the deadline failed: %v", err)
	require.Equal(t, "ok\n", string(body), "body %q, want ok", body)
	require.False(t, p.Lost(), "a body inside the deadline was marked unknown")
	got, upn := p.Requests(), upstream.Load()
	require.Equal(t, int64(1), got, "requests=%d upstream=%d, want 1 and 1", got, upn)
	require.Equal(t, int32(1), upn, "requests=%d upstream=%d, want 1 and 1", got, upn)
}

// TestUnsetSilenceArmsFortyFiveSeconds: a proxy with no silence of its own
// arms ProviderBodySilence. The clock fires at once so the test does not wait.
func TestUnsetSilenceArmsFortyFiveSeconds(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var armed []time.Duration
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
		After: func(d time.Duration) <-chan time.Time {
			mu.Lock()
			armed = append(armed, d)
			mu.Unlock()
			ch := make(chan time.Time, 1)
			ch <- time.Unix(0, 1)
			return ch
		},
	})
	require.NoError(t, err)
	defer p.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.HarnessURL(), strings.NewReader("card"))
	require.NoError(t, err)
	resp, err := proxyClient().Do(req)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	mu.Lock()
	got := append([]time.Duration(nil), armed...)
	mu.Unlock()
	require.NotEmpty(t, got, "armed %v, want %s", got, ProviderBodySilence)
	require.Equal(t, ProviderBodySilence, got[0], "armed %v, want %s", got, ProviderBodySilence)
	require.True(t, p.Lost(), "lost=%v requests=%d", p.Lost(), p.Requests())
	require.Equal(t, int64(1), p.Requests(), "lost=%v requests=%d", p.Lost(), p.Requests())
}

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

// TestUnsetHeaderWaitIsFortyFiveSeconds: a proxy with no header wait of its
// own waits ProviderHeaderTimeout.
func TestUnsetHeaderWaitIsFortyFiveSeconds(t *testing.T) {
	t.Parallel()

	up := httptest.NewServer(http.NotFoundHandler())
	defer up.Close()
	p, err := ListenProviderProxy(ProviderProxyConfig{Upstream: up.URL})
	require.NoError(t, err)
	defer p.Close()
	require.Equal(t, ProviderHeaderTimeout, p.HeaderWait(), "header wait %s, want %s (45s)", p.HeaderWait(), ProviderHeaderTimeout)
	require.Equal(t, 45*time.Second, ProviderHeaderTimeout, "header wait %s, want %s (45s)", p.HeaderWait(), ProviderHeaderTimeout)
}
