package swarm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bounded retry of a transient refusal (providerretry.go, tla/ProviderRetry.tla): unit
// tests on an in-process proxy (NewProviderProxy, no listener), an in-process RoundTripper
// for the provider, and a recorded Sleep, so no socket opens and no real clock runs.

// retryRecorder is a RefusalRetry whose Sleep records each wait and whose Jitter is zero.
type retryRecorder struct {
	mu     sync.Mutex
	sleeps []time.Duration
	r      *RefusalRetry
}

func newRetryRecorder(attempts int, base, capped time.Duration) *retryRecorder {
	rr := &retryRecorder{}
	rr.r = &RefusalRetry{
		Attempts: attempts, Base: base, Cap: capped,
		Sleep: func(ctx context.Context, d time.Duration) error {
			rr.mu.Lock()
			rr.sleeps = append(rr.sleeps, d)
			rr.mu.Unlock()
			return ctx.Err()
		},
		Jitter: func(time.Duration) time.Duration { return 0 },
		Now:    func() time.Time { return time.Date(2026, 10, 19, 20, 0, 0, 0, time.UTC) },
	}
	return rr
}

func (rr *retryRecorder) got() []time.Duration {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	return append([]time.Duration(nil), rr.sleeps...)
}

// scriptedProvider answers each send with the next response the script makes, the last
// one again once the script runs out, and counts the sends and the bodies they carried.
type scriptedProvider struct {
	mu     sync.Mutex
	script []func() *http.Response
	bodies []string
}

func (s *scriptedProvider) RoundTrip(r *http.Request) (*http.Response, error) {
	b := new(strings.Builder)
	if r.Body != nil {
		buf := make([]byte, 512)
		for {
			n, err := r.Body.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				break
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bodies = append(s.bodies, b.String())
	i := min(len(s.bodies)-1, len(s.script)-1)
	return s.script[i](), nil
}

func (s *scriptedProvider) sends() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.bodies...)
}

const ultramarine429 = `{"error":{"message":"The request exceeds your organization's requests per minute. Retry in 1 s.","type":"rate_limit_error"}}`

func answer(status int, header http.Header, body string) func() *http.Response {
	return func() *http.Response { return unitResponse(status, header, []byte(body)) }
}

// retryProxy is an unlimited route's proxy with the given retry, built with no listener.
func retryProxy(t *testing.T, rt http.RoundTripper, retry *RefusalRetry, budget *ProxyBudget) (*ProviderProxy, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var lines []string
	p, err := NewProviderProxy(ProviderProxyConfig{
		Upstream: "https://api.provider.invalid/v1", Silence: time.Minute, After: func(time.Duration) <-chan time.Time { return nil },
		Transport: rt, Retry: retry, Budget: budget,
		Log: func(l string) { mu.Lock(); lines = append(lines, l); mu.Unlock() },
	}, "127.0.0.1:1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	return p, &lines
}

func serveOne(p *ProviderProxy, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1/v1/chat/completions", strings.NewReader(body))
	p.ServeHTTP(rec, req)
	return rec
}

// A 429 then a 200: the harness sees only the 200, after one retry that waited the
// provider's "Retry in 1 s" (the base backoff is shorter), and the same body went twice.
func TestA429Then200SucceedsAfterOneRetry(t *testing.T) {
	t.Parallel()
	up := &scriptedProvider{script: []func() *http.Response{
		answer(http.StatusTooManyRequests, nil, ultramarine429),
		answer(http.StatusOK, nil, "done"),
	}}
	rr := newRetryRecorder(0, 100*time.Millisecond, time.Minute)
	p, lines := retryProxy(t, up, rr.r, nil)

	rec := serveOne(p, `{"model":"ultramarine-1-flash"}`)

	assert.Equal(t, http.StatusOK, rec.Code, "the 429 never reached the harness")
	assert.Equal(t, "done", rec.Body.String())
	assert.Equal(t, []string{`{"model":"ultramarine-1-flash"}`, `{"model":"ultramarine-1-flash"}`}, up.sends(), "the same request went twice")
	assert.Equal(t, []time.Duration{time.Second}, rr.got(), "one wait, the provider's hint as its floor")
	require.Len(t, *lines, 1, "one line for the refusal sent again")
	assert.Contains(t, (*lines)[0], "PROVIDER REFUSED status=429 send=1/8")
	assert.False(t, p.Stalling(), "no stall is left once the request ends")
}

// A provider that refuses every send: after the attempt cap the last 429 is handed to the
// harness whole, status, header and body, and no further send is made.
func TestAPersistent429FailsAfterTheCap(t *testing.T) {
	t.Parallel()
	h := http.Header{"X-Request-Id": []string{"req_1"}}
	up := &scriptedProvider{script: []func() *http.Response{answer(http.StatusTooManyRequests, h, ultramarine429)}}
	rr := newRetryRecorder(4, time.Second, time.Minute)
	p, _ := retryProxy(t, up, rr.r, nil)

	rec := serveOne(p, "q")

	assert.Equal(t, http.StatusTooManyRequests, rec.Code, "the last refusal is the harness's")
	assert.Equal(t, ultramarine429, rec.Body.String(), "its body whole")
	assert.Equal(t, "req_1", rec.Header().Get("X-Request-Id"), "its headers")
	assert.Len(t, up.sends(), 4, "exactly the cap's sends")
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}, rr.got(), "a doubling backoff between them")
}

// The backoff doubles to its cap and stays there; a 503 and a 529 are transient too.
func TestTheBackoffIsCapped(t *testing.T) {
	t.Parallel()
	up := &scriptedProvider{script: []func() *http.Response{
		answer(http.StatusServiceUnavailable, nil, "unavailable"),
		answer(529, nil, `{"type":"overloaded_error"}`),
		answer(http.StatusBadGateway, nil, "upstream overloaded"),
		answer(http.StatusServiceUnavailable, nil, "unavailable"),
		answer(http.StatusServiceUnavailable, nil, "unavailable"),
		answer(http.StatusOK, nil, "ok"),
	}}
	rr := newRetryRecorder(8, time.Second, 4*time.Second)
	p, _ := retryProxy(t, up, rr.r, nil)

	rec := serveOne(p, "q")

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second, 4 * time.Second}, rr.got(), "never past the cap")
}

// The provider's own wait is a floor under the backoff, from Retry-After seconds, an HTTP
// date, retry-after-ms or the body's words; a hint is cut to RefusalHintMax.
func TestTheProvidersHintIsAFloor(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 19, 20, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		header http.Header
		body   string
		want   time.Duration
	}{
		{"retry-after seconds", http.Header{"Retry-After": []string{"7"}}, "slow down", 7 * time.Second},
		{"retry-after date", http.Header{"Retry-After": []string{now.Add(12 * time.Second).Format(http.TimeFormat)}}, "", 12 * time.Second},
		{"retry-after-ms", http.Header{"Retry-After-Ms": []string{"2500"}}, "", 2500 * time.Millisecond},
		{"body seconds", nil, ultramarine429, time.Second},
		{"body ms", nil, "Rate limit reached. Please try again in 300ms.", 300 * time.Millisecond},
		{"too long", http.Header{"Retry-After": []string{"3600"}}, "", RefusalHintMax},
		{"none", nil, "Too many requests", 0},
	}
	for _, c := range cases {
		h := c.header
		if h == nil {
			h = http.Header{}
		}
		assert.Equal(t, c.want, refusalHint(h, []byte(c.body), now), c.name)
	}

	up := &scriptedProvider{script: []func() *http.Response{
		answer(http.StatusTooManyRequests, http.Header{"Retry-After": []string{"9"}}, "Too many requests"),
		answer(http.StatusOK, nil, "ok"),
	}}
	rr := newRetryRecorder(0, time.Second, time.Minute)
	p, _ := retryProxy(t, up, rr.r, nil)
	assert.Equal(t, http.StatusOK, serveOne(p, "q").Code)
	assert.Equal(t, []time.Duration{9 * time.Second}, rr.got(), "the proxy waited the provider's 9 s, not its 1 s")
}

// What is not transient goes to the harness on the first answer: a 400, an auth refusal,
// and out of funds under any status, a 429 included.
func TestARefusalThatIsNotTransientIsNotSentAgain(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		status int
		body   string
	}{
		{http.StatusBadRequest, `{"error":"max_tokens must be between 1 and 65536"}`},
		{http.StatusUnauthorized, `{"error":"Missing API key"}`},
		{http.StatusTooManyRequests, `{"error":{"code":"insufficient_quota","message":"You exceeded your current quota"}}`},
		{http.StatusServiceUnavailable, `{"error":"insufficient balance"}`},
		{http.StatusInternalServerError, `{"error":"internal"}`},
	} {
		up := &scriptedProvider{script: []func() *http.Response{answer(c.status, nil, c.body), answer(http.StatusOK, nil, "ok")}}
		rr := newRetryRecorder(0, time.Second, time.Minute)
		p, _ := retryProxy(t, up, rr.r, nil)
		rec := serveOne(p, "q")
		assert.Equal(t, c.status, rec.Code, "%d %s", c.status, c.body)
		assert.Equal(t, c.body, rec.Body.String())
		assert.Len(t, up.sends(), 1, "%d %s: one send", c.status, c.body)
		assert.Empty(t, rr.got(), "%d %s: no wait", c.status, c.body)
	}
}

// A run that ends while its request waits out a refusal ends the request; nothing more is
// sent, and the wait is a stall (the card waiting, not idle) while it lasts.
func TestARunEndingDuringTheBackoffEndsTheRequest(t *testing.T) {
	t.Parallel()
	up := &scriptedProvider{script: []func() *http.Response{answer(http.StatusTooManyRequests, nil, ultramarine429)}}
	var p *ProviderProxy
	var stalled bool
	retry := &RefusalRetry{
		Sleep: func(ctx context.Context, _ time.Duration) error {
			stalled = p.Stalling()
			p.cancel()
			<-ctx.Done()
			return ctx.Err()
		},
		Jitter: func(time.Duration) time.Duration { return 0 },
	}
	p, _ = retryProxy(t, up, retry, nil)

	rec := serveOne(p, "q")

	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Contains(t, rec.Body.String(), "waited out a refusal")
	assert.True(t, stalled, "the backoff counted as a stall")
	assert.Len(t, up.sends(), 1)
}

// A limited route keeps its own rule for a limit refusal (never handed to the harness, a
// doubling backoff), with the provider's hint a floor under it; its 503 takes the bounded
// retry, and each send takes and frees its own slot.
func TestALimitedRouteHonoursTheHintAndBoundsA503(t *testing.T) {
	t.Parallel()
	u := newUnitBudget()
	u.b.Backoff = time.Second
	u.b.BackoffCap = time.Minute
	up := &scriptedProvider{script: []func() *http.Response{
		answer(http.StatusTooManyRequests, http.Header{"Retry-After": []string{"5"}}, "Too many requests"),
		answer(http.StatusTooManyRequests, nil, ultramarine429),
		answer(http.StatusServiceUnavailable, nil, "unavailable"),
	}}
	rr := newRetryRecorder(2, 3*time.Second, time.Minute)
	p, _ := retryProxy(t, up, rr.r, u.b)

	rec := serveOne(p, "q")

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "the 503 is handed on after the bounded retry's two sends")
	_, released, _, _, budgetSleeps := u.got()
	assert.Equal(t, []time.Duration{5 * time.Second, 2 * time.Second}, budgetSleeps, "the limit backoff: the 5 s hint over 1 s, then 2 s over the 1 s hint")
	assert.Equal(t, []time.Duration{3 * time.Second}, rr.got(), "one bounded wait for the 503")
	assert.Len(t, up.sends(), 4, "two limit refusals do not count against the two sends")
	assert.Len(t, released, 4, "every send frees its slot")
}

// classifyRefusal on the words providers use.
func TestClassifyRefusal(t *testing.T) {
	t.Parallel()
	assert.Equal(t, refusalNone, classifyRefusal(http.StatusOK, nil))
	assert.Equal(t, refusalLimit, classifyRefusal(http.StatusTooManyRequests, []byte(ultramarine429)))
	assert.Equal(t, refusalLimit, classifyRefusal(http.StatusBadRequest, []byte("Your current concurrency is 9/12, which exceeds your concurrency limit")))
	assert.Equal(t, refusalBusy, classifyRefusal(http.StatusServiceUnavailable, nil))
	assert.Equal(t, refusalBusy, classifyRefusal(529, nil))
	assert.Equal(t, refusalBusy, classifyRefusal(http.StatusInternalServerError, []byte("model overloaded")))
	assert.Equal(t, refusalNone, classifyRefusal(http.StatusInternalServerError, []byte("internal")))
	assert.Equal(t, refusalNone, classifyRefusal(http.StatusTooManyRequests, []byte("insufficient credit")))
	assert.Equal(t, refusalNone, classifyRefusal(http.StatusServiceUnavailable, []byte("out of credit")))
}

// The production wait adds up to a quarter at random, never less than the backoff.
func TestTheDefaultJitterIsAtMostAQuarter(t *testing.T) {
	t.Parallel()
	var r *RefusalRetry
	for i := 0; i < 200; i++ {
		d := r.wait(3, 0)
		assert.GreaterOrEqual(t, d, 4*time.Second)
		assert.LessOrEqual(t, d, 5*time.Second)
	}
	assert.Equal(t, RefusalRetryAttempts, r.attempts())
}

// A refusal whose body goes silent after its headers is the same lost response as a silent
// success: the proxy marks the run lost and aborts, and a limited route's slot is freed then,
// not left to its lease.
func TestASilentRefusalBodyIsLostAndFreesItsSlot(t *testing.T) {
	t.Parallel()
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	rt := unitRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{}, Body: pr}, nil
	})
	u := newUnitBudget()
	fired := make(chan time.Time)
	close(fired)
	p, err := NewProviderProxy(ProviderProxyConfig{
		Upstream: "https://api.provider.invalid/v1", Silence: time.Minute,
		After:     func(time.Duration) <-chan time.Time { return fired },
		Transport: rt, Budget: u.b,
	}, "127.0.0.1:1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })

	assert.PanicsWithValue(t, http.ErrAbortHandler, func() { serveOne(p, "q") })
	assert.True(t, p.Lost(), "the silent refusal is a lost response")
	_, released, _, _, _ := u.got()
	assert.Equal(t, []string{"card.g1.r1"}, released, "the slot is freed on the abort")
}
