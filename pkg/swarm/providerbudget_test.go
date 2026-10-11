package swarm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// upstreamSays is a provider that answers each request in turn with the next status and
// body of says (the last one again once they run out), counting the requests and
// checking every one carries the harness's whole body.
func upstreamSays(t *testing.T, says ...[2]string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ := io.ReadAll(r.Body)
		if string(got) != `{"model":"mercury-3"}` {
			t.Errorf("upstream got body %q, want the harness's whole request", got)
		}
		i := int(n.Add(1)) - 1
		say := says[min(i, len(says)-1)]
		code := map[string]int{"200": 200, "429": 429, "400": 400, "402": 402}[say[0]]
		w.WriteHeader(code)
		_, _ = io.WriteString(w, say[1]) // ignored: a test upstream's write
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

// budgetProbe is a ProxyBudget with a scripted take, a recording clock and counts.
type budgetProbe struct {
	mu       sync.Mutex
	takes    []string
	sleeps   []time.Duration
	released []string
	lines    []string
	answer   func(i int) (bool, time.Duration, error)
	stalled  []bool
	proxy    *ProviderProxy
}

func (b *budgetProbe) budget() *ProxyBudget {
	return &ProxyBudget{
		Holder: "card.g1",
		Take: func(_ context.Context, h string) (bool, time.Duration, error) {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.takes = append(b.takes, h)
			return b.answer(len(b.takes) - 1)
		},
		Release: func(h string) { b.mu.Lock(); b.released = append(b.released, h); b.mu.Unlock() },
		Log:     func(l string) { b.mu.Lock(); b.lines = append(b.lines, l); b.mu.Unlock() },
		Sleep: func(ctx context.Context, d time.Duration) error {
			b.mu.Lock()
			b.sleeps = append(b.sleeps, d)
			p := b.proxy
			b.mu.Unlock()
			if p != nil {
				b.mu.Lock()
				b.stalled = append(b.stalled, p.Stalling())
				b.mu.Unlock()
			}
			return ctx.Err()
		},
	}
}

func proxyWith(t *testing.T, upstream string, b *budgetProbe) *ProviderProxy {
	t.Helper()
	p, err := ListenProviderProxy(ProviderProxyConfig{Upstream: upstream + "/v1", Budget: b.budget(), HeaderWait: 5 * time.Second, Silence: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.proxy = p
	b.mu.Unlock()
	t.Cleanup(func() {
		// ignored: a test proxy's close
		_ = p.Close()
	})
	return p
}

func harnessPost(t *testing.T, p *ProviderProxy) (int, string) {
	t.Helper()
	resp, err := http.Post(p.HarnessURL()+"/chat/completions", "application/json", strings.NewReader(`{"model":"mercury-3"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// Over its budget a request stalls inside the proxy, asking again after each wait, and the
// card is Stalling while it does; once granted it goes upstream once, and its slot is
// released after.
func TestABudgetedRequestStallsThenGoes(t *testing.T) {
	t.Parallel()
	srv, n := upstreamSays(t, [2]string{"200", "ok"})
	b := &budgetProbe{answer: func(i int) (bool, time.Duration, error) {
		if i < 2 {
			return false, 6 * time.Second, nil
		}
		return true, 0, nil
	}}
	p := proxyWith(t, srv.URL, b)
	code, body := harnessPost(t, p)
	if code != 200 || body != "ok" {
		t.Fatalf("harness got %d %q", code, body)
	}
	if n.Load() != 1 {
		t.Fatalf("upstream got %d requests, want 1", n.Load())
	}
	if len(b.takes) != 3 || len(b.sleeps) != 2 || b.sleeps[0] != BudgetPollMax {
		t.Fatalf("takes %v sleeps %v: want three takes, two waits capped at %s", b.takes, b.sleeps, BudgetPollMax)
	}
	for _, s := range b.stalled {
		if !s {
			t.Fatal("the proxy did not say it was stalling while the request waited")
		}
	}
	if p.Stalling() {
		t.Fatal("still stalling after the grant")
	}
	if len(b.released) != 1 || b.released[0] != b.takes[0] {
		t.Fatalf("released %v, want the request's holder once", b.released)
	}
	if !strings.Contains(strings.Join(b.lines, "\n"), "RATE BUDGET WAIT") {
		t.Fatalf("no wait line: %q", b.lines)
	}
}

// A 429, or the provider's words for a concurrency limit, never reaches the harness: the
// proxy waits 60 s, then 120 s, and sends the same request again in the same attempt.
func TestALimitRefusalIsRetriedInPlace(t *testing.T) {
	t.Parallel()
	srv, n := upstreamSays(t,
		[2]string{"429", `{"error":"Too many requests"}`},
		[2]string{"400", `{"error":{"message":"Your current concurrency is 9/12, which exceeds your concurrency limit"}}`},
		[2]string{"200", "done"})
	b := &budgetProbe{answer: func(int) (bool, time.Duration, error) { return true, 0, nil }}
	p := proxyWith(t, srv.URL, b)
	code, body := harnessPost(t, p)
	if code != 200 || body != "done" {
		t.Fatalf("harness got %d %q: a refusal reached it", code, body)
	}
	if n.Load() != 3 {
		t.Fatalf("upstream got %d requests, want 3", n.Load())
	}
	if len(b.sleeps) != 2 || b.sleeps[0] != BudgetBackoff || b.sleeps[1] != 2*BudgetBackoff {
		t.Fatalf("backoffs %v, want 60s then 120s", b.sleeps)
	}
	if len(b.takes) != 3 || len(b.released) != 3 {
		t.Fatalf("takes %v released %v: each send takes and releases its own slot", b.takes, b.released)
	}
}

// The budget cannot be read: the request goes, open, with one line; never an error.
func TestABudgetNotReadGoesOpen(t *testing.T) {
	t.Parallel()
	srv, n := upstreamSays(t, [2]string{"200", "ok"})
	b := &budgetProbe{answer: func(int) (bool, time.Duration, error) { return false, 0, errors.New("connection refused") }}
	p := proxyWith(t, srv.URL, b)
	if code, _ := harnessPost(t, p); code != 200 || n.Load() != 1 {
		t.Fatalf("got %d with %d upstream requests: an unreadable budget must not stop the request", code, n.Load())
	}
	if len(b.lines) != 1 || !strings.Contains(b.lines[0], "RATE BUDGET OPEN") {
		t.Fatalf("lines %q, want one OPEN line", b.lines)
	}
}

// Out of funds is never a rate limit: it is handed to the harness as it came, once.
func TestOutOfFundsIsNotRetried(t *testing.T) {
	t.Parallel()
	srv, n := upstreamSays(t, [2]string{"402", `{"error":"Insufficient Balance"}`})
	b := &budgetProbe{answer: func(int) (bool, time.Duration, error) { return true, 0, nil }}
	p := proxyWith(t, srv.URL, b)
	code, body := harnessPost(t, p)
	if code != 402 || !strings.Contains(body, "Insufficient Balance") || n.Load() != 1 {
		t.Fatalf("got %d %q after %d requests: out of funds must reach the harness once", code, body, n.Load())
	}
}

// With no budget the proxy is as it was: a 429 reaches the harness.
func TestAnUnlimitedRouteIsUnchanged(t *testing.T) {
	t.Parallel()
	srv, n := upstreamSays(t, [2]string{"429", "slow down"})
	p, err := ListenProviderProxy(ProviderProxyConfig{Upstream: srv.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Stalling() {
		t.Fatal("an unlimited proxy says it stalls")
	}
	resp, err := http.Post(p.HarnessURL()+"/chat/completions", "application/json", strings.NewReader(`{"model":"mercury-3"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 429 || n.Load() != 1 {
		t.Fatalf("got %d after %d requests, want the 429 as before", resp.StatusCode, n.Load())
	}
}

// The run ends while its request stalls: the request ends, no upstream request was made.
func TestAStallEndsWithTheRun(t *testing.T) {
	t.Parallel()
	srv, n := upstreamSays(t, [2]string{"200", "ok"})
	b := &budgetProbe{answer: func(int) (bool, time.Duration, error) { return false, time.Minute, nil }}
	p := proxyWith(t, srv.URL, b)
	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err := http.Post(p.HarnessURL()+"/chat/completions", "application/json", strings.NewReader(`{"model":"mercury-3"}`))
		if err == nil {
			resp.Body.Close()
		}
	}()
	// the probe's Sleep returns at once, so the request spins on takes until the run closes
	deadline := time.Now().Add(5 * time.Second)
	for {
		b.mu.Lock()
		k := len(b.takes)
		b.mu.Unlock()
		if k >= 3 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = p.Close() // ignored: the run's end
	<-done
	if n.Load() != 0 {
		t.Fatalf("upstream got %d requests while the budget was spent", n.Load())
	}
}

func TestLimitRefusal(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		status int
		body   string
		want   bool
	}{
		{429, "", true},
		{400, "Your current concurrency is 9/12, which exceeds your concurrency limit", true},
		{503, "Too Many Requests", true},
		{402, "Insufficient Balance", false},
		{429, "", true},
		{400, "rate limit: insufficient quota", false},
		{401, "bad key", false},
		{500, "internal", false},
		{200, "too many requests", false},
	} {
		if got := limitRefusal(c.status, []byte(c.body)); got != c.want {
			t.Errorf("limitRefusal(%d, %q) = %v, want %v", c.status, c.body, got, c.want)
		}
	}
}
