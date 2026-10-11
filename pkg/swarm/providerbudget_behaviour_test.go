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

// The behaviour of the global rate budget as the harness sees it (providerbudget.go, THE
// GLOBAL RATE BUDGET, docs; nova-sprint tla/RateBudget.tla: a limited request stalls out the
// delay, it never fails). The proxy is built in package, with an in-process RoundTripper in
// place of the client's transport: no socket is opened and no server is started. A fake
// RoundTripper never fires httptrace's WroteRequest, so the header-wait abort
// (providerproxy.go:277) is out of reach of this tier and is not tested here.

// budgetAnswer is one scripted reply to ProxyBudget.Take: granted, or the wait before the
// next ask, or an error meaning the budget could not be read.
type budgetAnswer struct {
	granted bool
	wait    time.Duration
	err     error
}

// budgetRig is a ProxyBudget whose Take is scripted and whose clock, slot and log are the
// test's own; it records every call the proxy made so a test can assert on what a caller
// sees. It mirrors budgetProbe (providerbudget_functional_test.go) under new names.
type budgetRig struct {
	holder   string
	answers  []budgetAnswer
	backoff  time.Duration
	cap      time.Duration
	renewEv  time.Duration

	mu       sync.Mutex
	takes    []string
	sleeps   []time.Duration
	released []string
	lines    []string
	renews   []string

	// clock and renew are the test's: nil is a sleep that returns at once and no renew.
	sleep func(ctx context.Context, d time.Duration) error
	renew func(ctx context.Context, holder string)
}

func (r *budgetRig) budget() *ProxyBudget {
	b := &ProxyBudget{
		Holder: r.holder,
		Take: func(_ context.Context, h string) (bool, time.Duration, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.takes = append(r.takes, h)
			i := len(r.takes) - 1
			if len(r.answers) == 0 {
				return true, 0, nil
			}
			if i >= len(r.answers) {
				i = len(r.answers) - 1
			}
			a := r.answers[i]
			return a.granted, a.wait, a.err
		},
		Release: func(h string) {
			r.mu.Lock()
			r.released = append(r.released, h)
			r.mu.Unlock()
		},
		Log: func(l string) {
			r.mu.Lock()
			r.lines = append(r.lines, l)
			r.mu.Unlock()
		},
		Sleep: func(ctx context.Context, d time.Duration) error {
			r.mu.Lock()
			r.sleeps = append(r.sleeps, d)
			r.mu.Unlock()
			if r.sleep != nil {
				return r.sleep(ctx, d)
			}
			return nil
		},
		Backoff:    r.backoff,
		BackoffCap: r.cap,
		RenewEvery: r.renewEv,
	}
	if r.renew != nil {
		b.Renew = func(ctx context.Context, h string) {
			r.mu.Lock()
			r.renews = append(r.renews, h)
			r.mu.Unlock()
			r.renew(ctx, h)
		}
	}
	return b
}

func (r *budgetRig) snapshot() (takes, released, lines []string, sleeps []time.Duration, renews []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.takes...), append([]string(nil), r.released...),
		append([]string(nil), r.lines...), append([]time.Duration(nil), r.sleeps...),
		append([]string(nil), r.renews...)
}

// budgetSend is what one request looked like when it reached the fake transport.
type budgetSend struct {
	method string
	path   string
	query  string
	body   string
	clen   int64
	header http.Header
}

// budgetWire is an in-process RoundTripper: it records each request and answers with the
// next scripted reply (the last one again once the script runs out).
type budgetWire struct {
	mu      sync.Mutex
	seen    []budgetSend
	answers []func(*http.Request) (*http.Response, error)
	n       int
}

func (w *budgetWire) RoundTrip(r *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	w.mu.Lock()
	w.seen = append(w.seen, budgetSend{
		method: r.Method,
		path:   r.URL.Path,
		query:  r.URL.RawQuery,
		body:   string(body),
		clen:   r.ContentLength,
		header: r.Header.Clone(),
	})
	i := w.n
	w.n++
	var ans func(*http.Request) (*http.Response, error)
	if i < len(w.answers) {
		ans = w.answers[i]
	} else if len(w.answers) > 0 {
		ans = w.answers[len(w.answers)-1]
	}
	w.mu.Unlock()
	if ans != nil {
		return ans(r)
	}
	return budgetResponse(http.StatusOK, nil, "ok"), nil
}

func (w *budgetWire) requests() []budgetSend {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]budgetSend(nil), w.seen...)
}

// budgetResponse is one upstream answer: a status, a header copy and a whole body.
func budgetResponse(status int, header http.Header, body string) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{
		StatusCode:    status,
		Header:        header,
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}

func budgetReply(status int, body string) func(*http.Request) (*http.Response, error) {
	return func(*http.Request) (*http.Response, error) {
		return budgetResponse(status, nil, body), nil
	}
}

// budgetError is the transport's own failure, not the provider's answer.
func budgetError(err error) func(*http.Request) (*http.Response, error) {
	return func(*http.Request) (*http.Response, error) { return nil, err }
}

// budgetProxy builds a limited ProviderProxy the way coverProxy builds the unbudgeted one:
// no listener, no real clock. The body timer never fires (after returns a channel that
// never delivers), so a test is not held by a real duration.
func budgetProxy(t *testing.T, wire http.RoundTripper, b *ProxyBudget) *ProviderProxy {
	t.Helper()
	u, err := url.Parse("https://provider.example/v1")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &ProviderProxy{
		upstream: u,
		silence:  ProviderBodySilence,
		headerWt: ProviderHeaderTimeout,
		after:    func(time.Duration) <-chan time.Time { return make(chan time.Time) },
		client:   &http.Client{Transport: wire},
		stall:    make(chan struct{}),
		ctx:      ctx,
		cancel:   cancel,
		budget:   b,
	}
}

// budgetPost drives the highest entry this card names, (*ProviderProxy).ServeHTTP, with a
// recorder and a request in process, and returns what the harness would read.
func budgetPost(t *testing.T, p *ProviderProxy, body string, hdr http.Header) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://proxy.local/chat/completions?stream=1", strings.NewReader(body))
	for k, vv := range hdr {
		for _, v := range vv {
			req.Header.Add(k, v)
		}
	}
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	return rec
}

// TestATakeThatWaitsThenGrantsReleasesTheHolder pins providerbudget.go takeBudget: a take
// told to wait stalls, asking again, clamped to BudgetPollMax (a zero wait sleeps the
// floor), and the granted request goes upstream once and frees its slot, "<Holder>.r1".
func TestATakeThatWaitsThenGrantsReleasesTheHolder(t *testing.T) {
	t.Parallel()
	wire := &budgetWire{}
	rig := &budgetRig{holder: "card.g1", answers: []budgetAnswer{
		{granted: false, wait: 6 * time.Second},
		{granted: false, wait: 0},
		{granted: true},
	}}
	p := budgetProxy(t, wire, rig.budget())

	rec := budgetPost(t, p, `{"model":"mercury-3"}`, nil)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "ok", rec.Body.String())
	takes, released, _, sleeps, _ := rig.snapshot()
	assert.Len(t, takes, 3, "wait, wait, then grant")
	assert.Equal(t, []time.Duration{BudgetPollMax, 10 * time.Millisecond}, sleeps,
		"a long wait is clamped to the poll; a zero wait sleeps the floor")
	assert.Equal(t, []string{"card.g1.r1"}, released, "the request's holder is released once")
	assert.Len(t, wire.requests(), 1, "one upstream request")
}

// TestATakeThatErrorsGoesOpen pins providerbudget.go takeBudget: a budget that cannot be
// read is granted OPEN, one line and no sleep, so the request is never an error.
func TestATakeThatErrorsGoesOpen(t *testing.T) {
	t.Parallel()
	wire := &budgetWire{}
	rig := &budgetRig{holder: "card.g2", answers: []budgetAnswer{
		{err: errors.New("connection refused")},
	}}
	p := budgetProxy(t, wire, rig.budget())

	rec := budgetPost(t, p, `{}`, nil)

	assert.Equal(t, http.StatusOK, rec.Code)
	_, released, lines, sleeps, _ := rig.snapshot()
	assert.Empty(t, sleeps, "an unreadable budget does not sleep")
	assert.Equal(t, []string{"card.g2.r1"}, released)
	require.Len(t, lines, 1)
	assert.Contains(t, lines[0], "RATE BUDGET OPEN")
	assert.Contains(t, lines[0], "card.g2.r1")
}

// TestAStallLogsOnceAndThenItsGrant pins providerbudget.go takeBudget's logging: a stall
// logs one line, not one per poll, and the grant after it names the holder and the waits.
func TestAStallLogsOnceAndThenItsGrant(t *testing.T) {
	t.Parallel()
	wire := &budgetWire{}
	rig := &budgetRig{holder: "card.g3", answers: []budgetAnswer{
		{granted: false, wait: 0},
		{granted: false, wait: 0},
		{granted: false, wait: 0},
		{granted: true},
	}}
	p := budgetProxy(t, wire, rig.budget())

	rec := budgetPost(t, p, `{}`, nil)

	assert.Equal(t, http.StatusOK, rec.Code)
	_, _, lines, _, _ := rig.snapshot()
	joined := strings.Join(lines, "\n")
	assert.Equal(t, 1, strings.Count(joined, "RATE BUDGET WAIT"), "one wait line, not one per poll")
	assert.Equal(t, 1, strings.Count(joined, "RATE BUDGET GRANTED"), "one grant line after the stall")
	assert.Contains(t, joined, "RATE BUDGET GRANTED card.g3.r1 after")
	assert.Contains(t, joined, "(3 waits)")
}

// TestALimitRefusalIsRetriedInPlace pins providerbudget.go serveBudgeted/forwardBudgeted:
// a 429 and a concurrency 400 never reach the harness; the proxy backs off (1 s doubling)
// and sends the same request again, each send under its own holder and each slot freed.
func TestALimitRefusalIsRetriedInPlace(t *testing.T) {
	t.Parallel()
	wire := &budgetWire{answers: []func(*http.Request) (*http.Response, error){
		budgetReply(http.StatusTooManyRequests, `{"error":"Too many requests"}`),
		budgetReply(http.StatusBadRequest, `{"error":{"message":"Your current concurrency is 9/12, which exceeds your concurrency limit"}}`),
		budgetReply(http.StatusOK, "done"),
	}}
	rig := &budgetRig{holder: "card.g4", backoff: time.Second, cap: 3 * time.Second,
		answers: []budgetAnswer{{granted: true}}}
	p := budgetProxy(t, wire, rig.budget())
	hdr := http.Header{"Connection": {"keep-alive"}, "X-Trace": {"abc"}}

	rec := budgetPost(t, p, `{"model":"mercury-3"}`, hdr)

	assert.Equal(t, http.StatusOK, rec.Code, "the refusal never reaches the harness")
	assert.Equal(t, "done", rec.Body.String())
	_, released, _, sleeps, _ := rig.snapshot()
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second}, sleeps, "the backoff doubles")
	assert.Equal(t, []string{"card.g4.r1", "card.g4.r2", "card.g4.r3"}, released, "each send frees its holder")
	sends := wire.requests()
	require.Len(t, sends, 3)
	for i, s := range sends {
		assert.Equal(t, http.MethodPost, s.method, "send %d keeps the method", i)
		assert.Equal(t, "/chat/completions", s.path, "send %d keeps the path", i)
		assert.Equal(t, "stream=1", s.query, "send %d keeps the query", i)
		assert.Equal(t, `{"model":"mercury-3"}`, s.body, "send %d keeps the whole body", i)
		assert.EqualValues(t, len(`{"model":"mercury-3"}`), s.clen, "send %d keeps the length", i)
		assert.Equal(t, "abc", s.header.Get("X-Trace"), "send %d keeps an end-to-end header", i)
		assert.Empty(t, s.header.Get("Connection"), "send %d drops the hop-by-hop header", i)
	}
}

// TestARefusalThatIsNotALimitPassesThroughWhole pins providerbudget.go forwardBudgeted and
// limitRefusal: an out-of-funds 402 is not a rate limit, so its status, its headers and its
// whole body (over 64 KiB) reach the harness once, with no backoff.
func TestARefusalThatIsNotALimitPassesThroughWhole(t *testing.T) {
	t.Parallel()
	whole := strings.Repeat("x", 70<<10)
	wire := &budgetWire{answers: []func(*http.Request) (*http.Response, error){
		func(*http.Request) (*http.Response, error) {
			return budgetResponse(http.StatusPaymentRequired, http.Header{"X-Quota": {"empty"}}, `{"error":"Insufficient Balance"}`+whole), nil
		},
	}}
	rig := &budgetRig{holder: "card.g5", answers: []budgetAnswer{{granted: true}}}
	p := budgetProxy(t, wire, rig.budget())

	rec := budgetPost(t, p, `{}`, nil)

	assert.Equal(t, http.StatusPaymentRequired, rec.Code)
	assert.Equal(t, "empty", rec.Header().Get("X-Quota"))
	assert.Equal(t, `{"error":"Insufficient Balance"}`+whole, rec.Body.String(), "the whole body arrives byte for byte")
	_, _, _, sleeps, _ := rig.snapshot()
	assert.Empty(t, sleeps, "an out-of-funds answer is not retried")
	assert.Len(t, wire.requests(), 1)
}

// TestAStallEndedByTheRunIs502 pins providerbudget.go takeBudget/serveBudgeted: a run that
// ends while its request waits for its budget answers 502 and sends nothing upstream.
func TestAStallEndedByTheRunIs502(t *testing.T) {
	t.Parallel()
	wire := &budgetWire{}
	rig := &budgetRig{holder: "card.g6", answers: []budgetAnswer{{granted: false, wait: time.Minute}}}
	rig.sleep = func(ctx context.Context, _ time.Duration) error { <-ctx.Done(); return ctx.Err() }
	p := budgetProxy(t, wire, rig.budget())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy.local/chat/completions", strings.NewReader(`{}`))
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.ServeHTTP(rec, req)
	}()
	require.Eventually(t, func() bool {
		takes, _, _, _, _ := rig.snapshot()
		return len(takes) >= 1
	}, time.Second, time.Millisecond, "the request stalled on its budget")
	p.cancel()
	<-done

	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Contains(t, rec.Body.String(), "the run ended while its request waited for its budget")
	assert.Empty(t, wire.requests(), "nothing reached upstream")
}

// TestARefusalBackoffEndedByTheRunIs502 pins providerbudget.go serveBudgeted: a refusal
// backoff whose sleep errors (the run ended) answers 502 rather than looping forever.
func TestARefusalBackoffEndedByTheRunIs502(t *testing.T) {
	t.Parallel()
	wire := &budgetWire{answers: []func(*http.Request) (*http.Response, error){
		budgetReply(http.StatusTooManyRequests, `{"error":"Too many requests"}`),
	}}
	rig := &budgetRig{holder: "card.g7", backoff: time.Second, answers: []budgetAnswer{{granted: true}}}
	rig.sleep = func(context.Context, time.Duration) error { return errors.New("run over") }
	p := budgetProxy(t, wire, rig.budget())

	rec := budgetPost(t, p, `{}`, nil)

	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Contains(t, rec.Body.String(), "waited out a refusal")
}

// TestARequestBodyThatCannotBeReadIs502 pins providerbudget.go serveBudgeted: a body the
// proxy cannot read answers 502 and the take is never made.
func TestARequestBodyThatCannotBeReadIs502(t *testing.T) {
	t.Parallel()
	wire := &budgetWire{}
	rig := &budgetRig{holder: "card.g8", answers: []budgetAnswer{{granted: true}}}
	p := budgetProxy(t, wire, rig.budget())

	req := httptest.NewRequest(http.MethodPost, "http://proxy.local/chat/completions", strings.NewReader(""))
	req.Body = io.NopCloser(budgetBadReader{})
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Contains(t, rec.Body.String(), "the request could not be read")
	takes, released, _, _, _ := rig.snapshot()
	assert.Empty(t, takes, "the budget is not asked for a body that cannot be read")
	assert.Empty(t, released)
	assert.Empty(t, wire.requests())
}

// budgetBadReader fails every read, the shape of a body that cannot be read.
type budgetBadReader struct{}

func (budgetBadReader) Read([]byte) (int, error) { return 0, errors.New("body read failed") }

// TestATransportFailureIs502AndFreesTheSlot pins providerbudget.go forwardBudgeted: the
// transport's own error answers 502, is not retried, and the slot is still freed.
func TestATransportFailureIs502AndFreesTheSlot(t *testing.T) {
	t.Parallel()
	wire := &budgetWire{answers: []func(*http.Request) (*http.Response, error){
		budgetError(errors.New("connection reset")),
	}}
	rig := &budgetRig{holder: "card.g9", answers: []budgetAnswer{{granted: true}}}
	p := budgetProxy(t, wire, rig.budget())

	rec := budgetPost(t, p, `{}`, nil)

	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Equal(t, "upstream\n", rec.Body.String())
	_, released, _, sleeps, _ := rig.snapshot()
	assert.Empty(t, sleeps, "a transport failure is not retried")
	assert.Equal(t, []string{"card.g9.r1"}, released, "the slot is freed")
	assert.Len(t, wire.requests(), 1)
}

// TestARunningSendRenewsItsHolder pins providerbudget.go forwardBudgeted: while a send
// runs, Renew is called for that send's holder, and a nil Renew is no panic.
func TestARunningSendRenewsItsHolder(t *testing.T) {
	t.Parallel()
	renewSeen := make(chan string, 4)
	release := make(chan struct{})
	var once sync.Once
	wire := &budgetWire{answers: []func(*http.Request) (*http.Response, error){
		func(*http.Request) (*http.Response, error) {
			<-release
			return budgetResponse(http.StatusOK, nil, "ok"), nil
		},
	}}
	rig := &budgetRig{holder: "card.g10", renewEv: time.Millisecond, answers: []budgetAnswer{{granted: true}}}
	rig.renew = func(_ context.Context, h string) {
		once.Do(func() { close(release) })
		renewSeen <- h
	}
	p := budgetProxy(t, wire, rig.budget())

	req := httptest.NewRequest(http.MethodPost, "http://proxy.local/chat/completions", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.ServeHTTP(rec, req)
	}()
	var got string
	require.Eventually(t, func() bool {
		select {
		case got = <-renewSeen:
			return true
		default:
			return false
		}
	}, 5*time.Second, time.Millisecond, "Renew is called while the send runs")
	<-done

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "card.g10.r1", got, "Renew is called with the send's holder")
	_, _, _, _, renews := rig.snapshot()
	assert.NotEmpty(t, renews)
}

// TestASendWithNoRenewRunsAnyway pins providerbudget.go forwardBudgeted: Renew nil is the
// real proxy's common case and must not panic; the request runs and the slot is freed.
func TestASendWithNoRenewRunsAnyway(t *testing.T) {
	t.Parallel()
	wire := &budgetWire{}
	rig := &budgetRig{holder: "card.g11", answers: []budgetAnswer{{granted: true}}}
	p := budgetProxy(t, wire, rig.budget())

	rec := budgetPost(t, p, `{}`, nil)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "ok", rec.Body.String())
	_, released, _, _, renews := rig.snapshot()
	assert.Equal(t, []string{"card.g11.r1"}, released)
	assert.Empty(t, renews)
}
