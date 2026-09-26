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
