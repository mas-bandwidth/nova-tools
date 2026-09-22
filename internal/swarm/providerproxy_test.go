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

	p, err := ListenProviderProxy(ProviderProxyConfig{Upstream: up.URL, Silence: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := proxyClient()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.HarnessURL(), strings.NewReader("card"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr == nil {
		t.Fatal("a silent body was delivered as a finished response")
	}
	if !p.Lost() {
		t.Fatal("a silent body was not unknown")
	}
	if gap := p.SilenceWall(); gap <= 0 {
		t.Fatalf("the body gap was not measured: %s", gap)
	}

	again, err := http.NewRequestWithContext(ctx, http.MethodPost, p.HarnessURL(), strings.NewReader("again"))
	if err != nil {
		t.Fatal(err)
	}
	resp2, err := client.Do(again)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp2.Body)
	resp2.Body.Close()

	if got, upn := p.Requests(), upstream.Load(); got != 1 || upn != 1 {
		t.Fatalf("requests=%d upstream=%d, want 1 and 1", got, upn)
	}
	t.Logf("CANARY requests=%d silence_ms=%d verdict=unknown", p.Requests(), p.SilenceWall().Milliseconds())
}

// TestBodyThatResumesInsideTheDeadlineIsNotUnknown: headers, a pause shorter
// than the gap, then the body. That is success. Nothing is marked lost.
func TestBodyThatResumesInsideTheDeadlineIsNotUnknown(t *testing.T) {
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
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.HarnessURL(), strings.NewReader("card"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := proxyClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("a body inside the deadline failed: %v", err)
	}
	if string(body) != "ok\n" {
		t.Fatalf("body %q, want ok", body)
	}
	if p.Lost() {
		t.Fatal("a body inside the deadline was marked unknown")
	}
	if got, upn := p.Requests(), upstream.Load(); got != 1 || upn != 1 {
		t.Fatalf("requests=%d upstream=%d, want 1 and 1", got, upn)
	}
}

// TestUnsetSilenceArmsFortyFiveSeconds: a proxy with no silence of its own
// arms ProviderBodySilence. The clock fires at once so the test does not wait.
func TestUnsetSilenceArmsFortyFiveSeconds(t *testing.T) {
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
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.HarnessURL(), strings.NewReader("card"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := proxyClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	mu.Lock()
	got := append([]time.Duration(nil), armed...)
	mu.Unlock()
	if len(got) == 0 || got[0] != ProviderBodySilence {
		t.Fatalf("armed %v, want %s", got, ProviderBodySilence)
	}
	if !p.Lost() || p.Requests() != 1 {
		t.Fatalf("lost=%v requests=%d", p.Lost(), p.Requests())
	}
}

func TestPointProviderAtProxyKeepsTheKey(t *testing.T) {
	in := []byte(`{"provider":{"fake":{"options":{"apiKey":"k","baseURL":"http://127.0.0.1:9/v1"}}}}`)
	out, ok := PointProviderAtProxy(in, "fake", "http://127.0.0.1:8/v1")
	if !ok {
		t.Fatal("the base URL was not retargeted")
	}
	if ProviderBaseURL(out, "fake") != "http://127.0.0.1:8/v1" {
		t.Fatalf("baseURL = %q", ProviderBaseURL(out, "fake"))
	}
	if !strings.Contains(string(out), `"apiKey": "k"`) {
		t.Fatalf("the key was dropped:\n%s", out)
	}
	if _, ok := PointProviderAtProxy([]byte(`{"provider":{"fake":{"options":{}}}}`), "fake", "http://127.0.0.1:8/v1"); ok {
		t.Fatal("a provider with no base URL was pointed at the proxy")
	}
}

func TestProviderProxyEligibleIsHTTPOnly(t *testing.T) {
	if !ProviderProxyEligible("http://127.0.0.1:9/v1") {
		t.Fatal("an http base URL was not eligible")
	}
	if ProviderProxyEligible("") || ProviderProxyEligible("not a url") || ProviderProxyEligible("file:///tmp/x") {
		t.Fatal("a non-http base URL was eligible")
	}
}
