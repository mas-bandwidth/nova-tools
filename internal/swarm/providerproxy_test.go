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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
// than the gap, then the body. That is success. Nothing is marked lost.
func TestBodyThatResumesInsideTheDeadlineIsNotUnknown(t *testing.T) {
	t.Parallel()
	// SLEEPS: this test waits on the wall clock (calls time.Sleep). Skipped 2026-09-25
	// by Glenn's rule ("unit tests must not have real sleeps or waits"): it becomes a
	// mocked-clock unit test or a functional program (nova-tools #4221).
	t.Skip("SLEEPS: needs a mocked clock or a functional test (nova-tools #4221)")

	var upstream atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream.Add(1)
		discardReq(r)
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte("ok\n"))
	}))
	defer up.Close()

	p, err := ListenProviderProxy(ProviderProxyConfig{Upstream: up.URL, Silence: 2 * time.Second})
	require.NoError(t, err)
	defer p.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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

// TestDelayedHeadersInsideTheWaitPassThrough: headers late but inside the
// wait, then a streamed body. The status, a header and every body byte pass
// through unchanged. Nothing is marked lost.
func TestDelayedHeadersInsideTheWaitPassThrough(t *testing.T) {
	t.Parallel()
	// SLEEPS: this test waits on the wall clock (calls time.Sleep). Skipped 2026-09-25
	// by Glenn's rule ("unit tests must not have real sleeps or waits"): it becomes a
	// mocked-clock unit test or a functional program (nova-tools #4221).
	t.Skip("SLEEPS: needs a mocked clock or a functional test (nova-tools #4221)")

	var upstream atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream.Add(1)
		discardReq(r)
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Upstream", "kept")
		w.WriteHeader(http.StatusOK)
		f, _ := w.(http.Flusher)
		for _, chunk := range []string{"data: one\n\n", "data: two\n\n", "data: [DONE]\n\n"} {
			_, _ = w.Write([]byte(chunk))
			if f != nil {
				f.Flush()
			}
			time.Sleep(30 * time.Millisecond)
		}
	}))
	defer up.Close()

	p, err := ListenProviderProxy(ProviderProxyConfig{Upstream: up.URL, Silence: 2 * time.Second, HeaderWait: time.Second})
	require.NoError(t, err)
	defer p.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.HarnessURL(), strings.NewReader("card"))
	require.NoError(t, err)
	resp, err := proxyClient().Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.NoError(t, err, "a body after delayed headers failed: %v", err)
	require.Equal(t, http.StatusOK, resp.StatusCode, "status %d headers %v", resp.StatusCode, resp.Header)
	require.Equal(t, "kept", resp.Header.Get("X-Upstream"), "status %d headers %v", resp.StatusCode, resp.Header)
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"), "status %d headers %v", resp.StatusCode, resp.Header)
	want := "data: one\n\ndata: two\n\ndata: [DONE]\n\n"
	require.Equal(t, want, string(body), "body %q, want %q", body, want)
	require.False(t, p.Lost(), "delayed headers inside the wait were marked unknown (header wall %s)", p.HeaderWall())
	require.Zero(t, p.HeaderWall(), "delayed headers inside the wait were marked unknown (header wall %s)", p.HeaderWall())
	got, upn := p.Requests(), upstream.Load()
	require.Equal(t, int64(1), got, "requests=%d upstream=%d, want 1 and 1", got, upn)
	require.Equal(t, int32(1), upn, "requests=%d upstream=%d, want 1 and 1", got, upn)
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
