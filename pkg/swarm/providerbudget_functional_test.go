//go:build functional

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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// upstreamSays is a provider that answers each request in turn with the next status and
// body of says (the last one again once they run out), counting the requests and
// checking every one carries the harness's whole body.
func upstreamSays(t *testing.T, says ...[2]string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.Equal(t, `{"model":"mercury-3"}`, string(got), "upstream got the harness's whole request")
		i := int(n.Add(1)) - 1
		say := says[min(i, len(says)-1)]
		code := map[string]int{"200": 200, "429": 429, "400": 400, "402": 402}[say[0]]
		w.WriteHeader(code)
		_, err = io.WriteString(w, say[1])
		assert.NoError(t, err)
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
	require.NoError(t, err)
	b.mu.Lock()
	b.proxy = p
	b.mu.Unlock()
	t.Cleanup(func() { assert.NoError(t, p.Close()) })
	return p
}

func harnessPost(t *testing.T, p *ProviderProxy) (int, string) {
	t.Helper()
	resp, err := http.Post(p.HarnessURL()+"/chat/completions", "application/json", strings.NewReader(`{"model":"mercury-3"}`))
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)
	assert.NoError(t, resp.Body.Close())
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
	assert.Equal(t, 200, code)
	assert.Equal(t, "ok", body)
	assert.Equal(t, int64(1), n.Load(), "one upstream request")
	assert.Len(t, b.takes, 3, "three takes")
	assert.Equal(t, []time.Duration{BudgetPollMax, BudgetPollMax}, b.sleeps, "two waits, each capped at the poll")
	assert.NotContains(t, b.stalled, false, "the proxy said it was stalling while the request waited")
	assert.False(t, p.Stalling(), "not stalling after the grant")
	assert.Equal(t, b.takes[:1], b.released, "the request's holder released once")
	assert.Contains(t, strings.Join(b.lines, "\n"), "RATE BUDGET WAIT")
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
	assert.Equal(t, 200, code, "no refusal reached the harness")
	assert.Equal(t, "done", body)
	assert.Equal(t, int64(3), n.Load(), "three upstream requests")
	assert.Equal(t, []time.Duration{BudgetBackoff, 2 * BudgetBackoff}, b.sleeps, "60 s then 120 s")
	assert.Len(t, b.takes, 3, "each send takes its own slot")
	assert.Len(t, b.released, 3, "each send releases its own slot")
}

// A 429 that says the quota is spent is out of credit, not a rate limit: handed to the
// harness once, so the take ends and the sprint rests the provider for its credit.
func TestAQuota429IsNotRetried(t *testing.T) {
	t.Parallel()
	srv, n := upstreamSays(t, [2]string{"429", `{"error":{"type":"insufficient_quota","message":"You exceeded your current quota"}}`})
	b := &budgetProbe{answer: func(int) (bool, time.Duration, error) { return true, 0, nil }}
	p := proxyWith(t, srv.URL, b)
	code, body := harnessPost(t, p)
	assert.Equal(t, 429, code)
	assert.Contains(t, body, "insufficient_quota")
	assert.Equal(t, int64(1), n.Load(), "one upstream request")
	assert.Empty(t, b.sleeps, "no backoff")
}

// The budget cannot be read: the request goes, open, with one line; never an error.
func TestABudgetNotReadGoesOpen(t *testing.T) {
	t.Parallel()
	srv, n := upstreamSays(t, [2]string{"200", "ok"})
	b := &budgetProbe{answer: func(int) (bool, time.Duration, error) { return false, 0, errors.New("connection refused") }}
	p := proxyWith(t, srv.URL, b)
	code, _ := harnessPost(t, p)
	assert.Equal(t, 200, code, "an unreadable budget does not stop the request")
	assert.Equal(t, int64(1), n.Load())
	require.Len(t, b.lines, 1)
	assert.Contains(t, b.lines[0], "RATE BUDGET OPEN")
}

// Out of funds is never a rate limit: it is handed to the harness as it came, once.
func TestOutOfFundsIsNotRetried(t *testing.T) {
	t.Parallel()
	srv, n := upstreamSays(t, [2]string{"402", `{"error":"Insufficient Balance"}`})
	b := &budgetProbe{answer: func(int) (bool, time.Duration, error) { return true, 0, nil }}
	p := proxyWith(t, srv.URL, b)
	code, body := harnessPost(t, p)
	assert.Equal(t, 402, code)
	assert.Contains(t, body, "Insufficient Balance")
	assert.Equal(t, int64(1), n.Load())
}

// With no budget the proxy is as it was: a 429 reaches the harness.
func TestAnUnlimitedRouteIsUnchanged(t *testing.T) {
	t.Parallel()
	srv, n := upstreamSays(t, [2]string{"429", "slow down"})
	p, err := ListenProviderProxy(ProviderProxyConfig{Upstream: srv.URL + "/v1"})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, p.Close()) })
	assert.False(t, p.Stalling(), "an unlimited proxy never stalls")
	resp, err := http.Post(p.HarnessURL()+"/chat/completions", "application/json", strings.NewReader(`{"model":"mercury-3"}`))
	require.NoError(t, err)
	assert.NoError(t, resp.Body.Close())
	assert.Equal(t, 429, resp.StatusCode, "the 429 as before")
	assert.Equal(t, int64(1), n.Load())
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
			assert.NoError(t, resp.Body.Close())
		}
	}()
	// the probe's Sleep returns at once, so the request spins on takes until the run closes
	require.Eventually(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return len(b.takes) >= 3
	}, 30*time.Second, 10*time.Millisecond)
	assert.NoError(t, p.Close())
	<-done
	assert.Equal(t, int64(0), n.Load(), "no upstream request while the budget was spent")
}
