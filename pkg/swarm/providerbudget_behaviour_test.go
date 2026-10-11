package swarm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wireBudget is a ProxyBudget whose Take, Release, Renew, Log and Sleep are
// the test's fakes: the behaviour tests drive the wire and read back exactly
// the calls the proxy made.
type wireBudget struct {
	mu         sync.Mutex
	takes      []string
	sleeps     []time.Duration
	released   []string
	renewed    []string
	lines      []string
	answer     func(i int) (bool, time.Duration, error)
	sleepErr   func(i int) error
	renewFn    func(h string)
	backoff    time.Duration
	backoffCap time.Duration
	renewEvery time.Duration
}

func (b *wireBudget) budget() *ProxyBudget {
	pb := &ProxyBudget{
		Holder:     "card.g1",
		Backoff:    b.backoff,
		BackoffCap: b.backoffCap,
		RenewEvery: b.renewEvery,
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
			i := len(b.sleeps)
			b.sleeps = append(b.sleeps, d)
			se := b.sleepErr
			b.mu.Unlock()
			if se != nil {
				return se(i)
			}
			return nil
		},
	}
	if b.renewFn != nil {
		pb.Renew = func(_ context.Context, h string) { b.renewFn(h) }
	}
	return pb
}

func (b *wireBudget) grants() { b.answer = func(int) (bool, time.Duration, error) { return true, 0, nil } }

func (b *wireBudget) linesJoined() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.Join(b.lines, "\n")
}

// wireRT is an in-process RoundTripper: it answers each send from the test's
// handler and records what the proxy put on the wire.
type wireRT struct {
	handle func(r *http.Request, i int) (*http.Response, error)

	mu    sync.Mutex
	sends []wireSend
}

type wireSend struct {
	method     string
	path       string
	rawQuery   string
	body       string
	contentLen int64
	header     http.Header
}

func (rt *wireRT) RoundTrip(r *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(r.Body)
	rt.mu.Lock()
	rt.sends = append(rt.sends, wireSend{
		method:     r.Method,
		path:       r.URL.Path,
		rawQuery:   r.URL.RawQuery,
		body:       string(body),
		contentLen: r.ContentLength,
		header:     r.Header.Clone(),
	})
	i := len(rt.sends) - 1
	h := rt.handle
	rt.mu.Unlock()
	if h == nil {
		return wireResponse(http.StatusOK, "ok"), nil
	}
	return h(r, i)
}

func wireResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func wireHdr(pairs ...string) http.Header {
	h := make(http.Header)
	for i := 0; i+1 < len(pairs); i += 2 {
		h.Add(pairs[i], pairs[i+1])
	}
	return h
}

// wireProxy builds the proxy in package the way coverProxy does, no listener:
// upstream, ctx and cancel, stall, a client whose Transport is the test's
// RoundTripper, and the budget. No socket is opened and no clock is real.
func wireProxy(t *testing.T, rt http.RoundTripper, b *wireBudget) *ProviderProxy {
	t.Helper()
	u, err := url.Parse("http://provider.test/v1")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	p := &ProviderProxy{
		upstream: u,
		silence:  time.Hour,
		stall:    make(chan struct{}),
		client:   &http.Client{Transport: rt},
		ctx:      ctx,
		cancel:   cancel,
		budget:   b.budget(),
	}
	t.Cleanup(cancel)
	return p
}

func wireSendReq(p *ProviderProxy, target, body string, hdr http.Header) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	for k, vv := range hdr {
		for _, v := range vv {
			req.Header.Add(k, v)
		}
	}
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	return rec
}

type wireErrReader struct{}

func (wireErrReader) Read([]byte) (int, error) { return 0, errors.New("the body is not readable") }

// TestAStallThenGrantSendsOnceAndReleasesTheHolder pins the file header's
// contract (providerbudget.go:18-34): a take told to wait stalls the request,
// polling no faster than BudgetPollMax, and the granted request goes upstream
// once and frees its holder.
func TestAStallThenGrantSendsOnceAndReleasesTheHolder(t *testing.T) {
	t.Parallel()
	b := &wireBudget{answer: func(i int) (bool, time.Duration, error) {
		if i < 2 {
			if i == 0 {
				return false, 0, nil
			}
			return false, 6 * time.Second, nil
		}
		return true, 0, nil
	}}
	rt := &wireRT{handle: func(*http.Request, int) (*http.Response, error) { return wireResponse(http.StatusOK, "ok"), nil }}
	p := wireProxy(t, rt, b)
	rec := wireSendReq(p, "/chat/completions", `{"model":"mercury-3"}`, nil)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "ok", rec.Body.String())
	assert.Len(t, rt.sends, 1, "one upstream request")
	assert.Len(t, b.takes, 3, "two waits then the grant")
	assert.Equal(t, []time.Duration{10 * time.Millisecond, BudgetPollMax}, b.sleeps, "a zero wait sleeps the floor, a long wait the poll ceiling")
	assert.Equal(t, []string{"card.g1.r1"}, b.released, "the request releases its own holder once")
}

// TestABudgetNotReadGoesOpen pins ProxyBudget.Take's doc: an error is a budget
// not read, one line logged and the request proceeds open, no sleep.
func TestABudgetNotReadGoesOpen(t *testing.T) {
	t.Parallel()
	b := &wireBudget{answer: func(int) (bool, time.Duration, error) { return false, 0, errors.New("connection refused") }}
	rt := &wireRT{handle: func(*http.Request, int) (*http.Response, error) { return wireResponse(http.StatusOK, "ok"), nil }}
	p := wireProxy(t, rt, b)
	rec := wireSendReq(p, "/chat/completions", `{"model":"mercury-3"}`, nil)
	assert.Equal(t, http.StatusOK, rec.Code, "an unreadable budget does not stop the request")
	assert.Len(t, b.takes, 1)
	assert.Empty(t, b.sleeps, "an open grant does not sleep")
	require.Len(t, b.lines, 1)
	assert.Contains(t, b.lines[0], "RATE BUDGET OPEN")
	assert.Contains(t, b.lines[0], "card.g1.r1")
}

// TestAStallLogsTheWaitOnceAndTheGrant pins ProxyBudget.Log's doc: one line per
// stall, not one per poll, then the grant naming the holder and the wait count.
func TestAStallLogsTheWaitOnceAndTheGrant(t *testing.T) {
	t.Parallel()
	b := &wireBudget{answer: func(i int) (bool, time.Duration, error) {
		if i < 3 {
			return false, time.Second, nil
		}
		return true, 0, nil
	}}
	rt := &wireRT{handle: func(*http.Request, int) (*http.Response, error) { return wireResponse(http.StatusOK, "ok"), nil }}
	p := wireProxy(t, rt, b)
	rec := wireSendReq(p, "/chat/completions", `{"model":"mercury-3"}`, nil)
	assert.Equal(t, http.StatusOK, rec.Code)
	joined := b.linesJoined()
	assert.Equal(t, 1, strings.Count(joined, "RATE BUDGET WAIT"), "one wait line for the whole stall")
	assert.Contains(t, joined, "RATE BUDGET GRANTED card.g1.r1 after")
	assert.Contains(t, joined, "(3 waits)")
}

// TestALimitRefusalIsRetriedInPlaceWithTheSameRequest pins the file header:
// a 429 never reaches the harness; the same request is sent again after a
// doubling backoff, each send takes and frees its own holder, and the body,
// method, path, query and ContentLength are the same on every send while
// hop-by-hop headers are dropped.
func TestALimitRefusalIsRetriedInPlaceWithTheSameRequest(t *testing.T) {
	t.Parallel()
	b := &wireBudget{answer: func(int) (bool, time.Duration, error) { return true, 0, nil }}
	b.backoff = time.Second
	b.backoffCap = 3 * time.Second
	rt := &wireRT{handle: func(_ *http.Request, i int) (*http.Response, error) {
		if i < 2 {
			return wireResponse(http.StatusTooManyRequests, `{"error":"Too many requests"}`), nil
		}
		return wireResponse(http.StatusOK, "done"), nil
	}}
	p := wireProxy(t, rt, b)
	rec := wireSendReq(p, "/chat/completions?stream=true", `{"model":"mercury-3"}`,
		wireHdr("Connection", "keep-alive", "X-Test", "1"))
	assert.Equal(t, http.StatusOK, rec.Code, "no refusal reached the harness")
	assert.Equal(t, "done", rec.Body.String())
	require.Len(t, rt.sends, 3, "two refusals then the answer")
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second}, b.sleeps, "the backoff doubles")
	assert.Equal(t, []string{"card.g1.r1", "card.g1.r2", "card.g1.r3"}, b.takes)
	assert.Equal(t, b.takes, b.released, "each send frees its own holder")
	for i, s := range rt.sends {
		assert.Equal(t, http.MethodPost, s.method, "send %d method", i)
		assert.Equal(t, "/chat/completions", s.path, "send %d path", i)
		assert.Equal(t, "stream=true", s.rawQuery, "send %d query", i)
		assert.Equal(t, `{"model":"mercury-3"}`, s.body, "send %d body", i)
		assert.Equal(t, int64(len(`{"model":"mercury-3"}`)), s.contentLen, "send %d content length", i)
		assert.Empty(t, s.header.Get("Connection"), "send %d dropped the hop-by-hop header", i)
		assert.Equal(t, "1", s.header.Get("X-Test"), "send %d kept the end-to-end header", i)
	}
	assert.Contains(t, b.linesJoined(), "RATE BUDGET REFUSED")
}

// TestAQuotaRefusalPassesThroughWholeOnce pins limitRefusal's doc: a refusal
// whose body says the quota or balance is spent is never a rate limit, so it is
// handed to the harness once with its status, headers and whole body, no backoff.
func TestAQuotaRefusalPassesThroughWholeOnce(t *testing.T) {
	t.Parallel()

	long := "Insufficient Balance " + strings.Repeat("a", 70<<10)
	rows := []struct {
		name   string
		status int
		body   string
	}{
		{name: "a 429 insufficient_quota", status: http.StatusTooManyRequests, body: `{"error":{"type":"insufficient_quota"}}`},
		{name: "a 402 insufficient balance over the read window", status: http.StatusPaymentRequired, body: long},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			b := &wireBudget{}
			b.grants()
			rt := &wireRT{handle: func(*http.Request, int) (*http.Response, error) {
				resp := wireResponse(row.status, row.body)
				resp.Header.Set("X-Upstream", "yes")
				return resp, nil
			}}
			p := wireProxy(t, rt, b)
			rec := wireSendReq(p, "/chat/completions", `{"model":"mercury-3"}`, nil)
			assert.Equal(t, row.status, rec.Code)
			assert.Equal(t, "yes", rec.Header().Get("X-Upstream"), "the upstream header is copied")
			assert.Equal(t, row.body, rec.Body.String(), "the whole body arrives byte for byte")
			assert.Len(t, rt.sends, 1, "one upstream request")
			assert.Empty(t, b.sleeps, "no backoff")
			assert.Equal(t, []string{"card.g1.r1"}, b.released)
		})
	}
}

// TestAStallEndedByTheRunAnswers502 pins serveBudgeted: when the run's context
// ends while the request waits for its budget, the harness gets a 502 naming
// the wait and nothing reached upstream.
func TestAStallEndedByTheRunAnswers502(t *testing.T) {
	t.Parallel()
	b := &wireBudget{answer: func(int) (bool, time.Duration, error) { return false, time.Minute, nil }}
	rt := &wireRT{handle: func(*http.Request, int) (*http.Response, error) { return wireResponse(http.StatusOK, "ok"), nil }}
	p := wireProxy(t, rt, b)
	b.sleepErr = func(int) error { p.cancel(); return context.Canceled }
	rec := wireSendReq(p, "/chat/completions", `{"model":"mercury-3"}`, nil)
	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Contains(t, rec.Body.String(), "the run ended while its request waited for its budget")
	assert.Empty(t, rt.sends, "nothing reached upstream")
}

// TestARefusalBackoffEndedByTheRunAnswers502 pins serveBudgeted: when the
// context ends during the backoff after a refusal, the harness gets a 502
// naming the refusal wait.
func TestARefusalBackoffEndedByTheRunAnswers502(t *testing.T) {
	t.Parallel()
	b := &wireBudget{}
	b.grants()
	b.backoff = time.Second
	rt := &wireRT{handle: func(*http.Request, int) (*http.Response, error) {
		return wireResponse(http.StatusTooManyRequests, `{"error":"Too many requests"}`), nil
	}}
	p := wireProxy(t, rt, b)
	b.sleepErr = func(int) error { return context.Canceled }
	rec := wireSendReq(p, "/chat/completions", `{"model":"mercury-3"}`, nil)
	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Contains(t, rec.Body.String(), "waited out a refusal")
	assert.Len(t, rt.sends, 1, "the one refused send")
}

// TestAnUnreadableRequestBodyAnswers502WithoutATake pins serveBudgeted: a body
// that cannot be read is answered 502 before the budget is ever asked.
func TestAnUnreadableRequestBodyAnswers502WithoutATake(t *testing.T) {
	t.Parallel()
	b := &wireBudget{}
	b.grants()
	rt := &wireRT{}
	p := wireProxy(t, rt, b)
	req := httptest.NewRequest(http.MethodPost, "/chat/completions", io.NopCloser(wireErrReader{}))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Empty(t, b.takes, "the budget is never asked for an unreadable body")
	assert.Empty(t, rt.sends)
}

// TestAnUpstreamErrorAnswers502AndStillReleases pins forwardBudgeted and
// serveBudgeted: a transport error is a 502, no retry, and the holder's slot
// is still freed.
func TestAnUpstreamErrorAnswers502AndStillReleases(t *testing.T) {
	t.Parallel()
	b := &wireBudget{}
	b.grants()
	rt := &wireRT{handle: func(*http.Request, int) (*http.Response, error) {
		return nil, errors.New("connection reset by peer")
	}}
	p := wireProxy(t, rt, b)
	rec := wireSendReq(p, "/chat/completions", `{"model":"mercury-3"}`, nil)
	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Len(t, rt.sends, 1, "no retry")
	assert.Empty(t, b.sleeps)
	assert.Equal(t, []string{"card.g1.r1"}, b.released, "the slot is freed")
}

// TestRenewRunsWhileTheSendWaits pins ProxyBudget.Renew: with a short
// RenewEvery the send renews its holder while it runs upstream.
func TestRenewRunsWhileTheSendWaits(t *testing.T) {
	t.Parallel()
	renewed := make(chan string, 1)
	b := &wireBudget{}
	b.grants()
	b.renewEvery = 2 * time.Millisecond
	b.renewFn = func(h string) {
		b.mu.Lock()
		b.renewed = append(b.renewed, h)
		b.mu.Unlock()
		select {
		case renewed <- h:
		default:
		}
	}
	rt := &wireRT{handle: func(*http.Request, int) (*http.Response, error) {
		select {
		case <-renewed:
		case <-time.After(2 * time.Second):
			t.Error("renew was never seen while the send waited")
		}
		return wireResponse(http.StatusOK, "ok"), nil
	}}
	p := wireProxy(t, rt, b)
	rec := wireSendReq(p, "/chat/completions", `{"model":"mercury-3"}`, nil)
	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotEmpty(t, b.renewed, "the running send renewed at least once")
	for _, h := range b.renewed {
		assert.Equal(t, "card.g1.r1", h, "the send renews its own holder")
	}
}

// TestNilRenewDoesNotPanic pins ProxyBudget's doc that Renew and Release may be
// nil: a send with no Renew still answers.
func TestNilRenewDoesNotPanic(t *testing.T) {
	t.Parallel()
	b := &wireBudget{}
	b.grants()
	rt := &wireRT{handle: func(*http.Request, int) (*http.Response, error) { return wireResponse(http.StatusOK, "ok"), nil }}
	p := wireProxy(t, rt, b)
	assert.NotPanics(t, func() {
		rec := wireSendReq(p, "/chat/completions", `{"model":"mercury-3"}`, nil)
		assert.Equal(t, http.StatusOK, rec.Code)
	})
	assert.Equal(t, []string{"card.g1.r1"}, b.released)
}
