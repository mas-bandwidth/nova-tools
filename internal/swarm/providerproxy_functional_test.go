//go:build functional

package swarm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestNoHeadersIsOneUpstreamRequestAndUnknown waits out a real 200 ms header
// timeout: the wait is net/http's ResponseHeaderTimeout, which takes no clock, so
// until the proxy has a header-wait seam the test is the functional tier's, not a
// unit test (Glenn 2026-09-26, nova-tools#4328: unit tests never wait on the wall
// clock).

// TestNoHeadersIsOneUpstreamRequestAndUnknown is the header wait on a real
// timer (stella 5782441006). The upstream reads the POST and never answers
// with headers. The proxy ends it at its own header wait, marks it lost, and
// refuses the next request, so the upstream count stays 1.
func TestNoHeadersIsOneUpstreamRequestAndUnknown(t *testing.T) {
	t.Parallel()

	var upstream atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream.Add(1)
		discardReq(r)
		<-r.Context().Done()
	}))
	defer up.Close()

	p, err := ListenProviderProxy(ProviderProxyConfig{Upstream: up.URL, Silence: 20 * time.Second, HeaderWait: 200 * time.Millisecond})
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
	if err == nil {
		resp.Body.Close()
		t.Fatalf("a request with no upstream headers was answered %d", resp.StatusCode)
	}
	if !p.Lost() {
		t.Fatal("no headers after a written request was not unknown")
	}
	if p.HeaderWall() < 200*time.Millisecond || p.SilenceWall() != 0 {
		t.Fatalf("header wall %s silence wall %s", p.HeaderWall(), p.SilenceWall())
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
	if resp2.StatusCode != http.StatusBadGateway {
		t.Fatalf("the request after a lost one was answered %d", resp2.StatusCode)
	}
	if got, upn := p.Requests(), upstream.Load(); got != 1 || upn != 1 {
		t.Fatalf("requests=%d upstream=%d, want 1 and 1", got, upn)
	}
}

// TestBodyThatResumesInsideTheDeadlineIsNotUnknown: headers, a pause shorter
// than the gap, then the body. That is success. Nothing is marked lost.
func TestBodyThatResumesInsideTheDeadlineIsNotUnknown(t *testing.T) {
	t.Parallel()

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

// TestDelayedHeadersInsideTheWaitPassThrough: headers late but inside the
// wait, then a streamed body. The status, a header and every body byte pass
// through unchanged. Nothing is marked lost.
func TestDelayedHeadersInsideTheWaitPassThrough(t *testing.T) {
	t.Parallel()

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
		t.Fatalf("a body after delayed headers failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK || resp.Header.Get("X-Upstream") != "kept" || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status %d headers %v", resp.StatusCode, resp.Header)
	}
	if want := "data: one\n\ndata: two\n\ndata: [DONE]\n\n"; string(body) != want {
		t.Fatalf("body %q, want %q", body, want)
	}
	if p.Lost() || p.HeaderWall() != 0 {
		t.Fatalf("delayed headers inside the wait were marked unknown (header wall %s)", p.HeaderWall())
	}
	if got, upn := p.Requests(), upstream.Load(); got != 1 || upn != 1 {
		t.Fatalf("requests=%d upstream=%d, want 1 and 1", got, upn)
	}
}
