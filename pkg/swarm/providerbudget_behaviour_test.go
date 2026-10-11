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

// These are the unit-tier tests of the provider proxy's global rate budget
// (providerbudget.go: takeBudget, serveBudgeted, forwardBudgeted). They build
// the proxy in-process the way providerproxy_cover_test.go's coverProxy does:
// no listener, no socket, no real clock. The upstream is an in-process
// http.RoundTripper and the ProxyBudget's Take, Release, Renew, Log and Sleep
// are the test's fakes. The header-wait abort (which needs httptrace's
// WroteRequest, never fired by a fake RoundTripper) is out of reach here.

// pbudgetSend is one request the fake RoundTripper saw.
type pbudgetSend struct {
	method string
	path   string
	query  string
	header http.Header
	body   []byte
	length int64
}

// pbudgetWire is an in-process http.RoundTripper: it records every request it
// is asked to send and answers from the test's script.
type pbudgetWire struct {
	mu     sync.Mutex
	sent   []pbudgetSend
	answer func(i int, req *http.Request) (*http.Response, error)
}

func (w *pbudgetWire) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		_ = req.Body.Close()
	}
	w.mu.Lock()
	i := len(w.sent)
	w.sent = append(w.sent, pbudgetSend{
		method: req.Method,
		path:   req.URL.Path,
		query:  req.URL.RawQuery,
		header: req.Header.Clone(),
		body:   body,
		length: req.ContentLength,
	})
	w.mu.Unlock()
	if w.answer != nil {
		return w.answer(i, req)
	}
	return pbudgetAnswer(req, 200, nil, "ok"), nil
}

func (w *pbudgetWire) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.sent)
}

func (w *pbudgetWire) at(i int) pbudgetSend {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.sent[i]
}

// pbudgetAnswer builds the upstream response the fake RoundTripper returns.
func pbudgetAnswer(req *http.Request, status int, header http.Header, body string) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{
		StatusCode:    status,
		Header:        header,
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}

// pbudgetFake is a scripted ProxyBudget: fakes for Take, Release, Log and Sleep
// that record what they saw, so the tests assert on the calls the budget got.
type pbudgetFake struct {
	mu       sync.Mutex
	takes    []string
	sleeps   []time.Duration
	released []string
	lines    []string
	take     func(i int, holder string) (bool, time.Duration, error)
	sleep    func(ctx context.Context, d time.Duration) error
}

func (f *pbudgetFake) budget(holder string) *ProxyBudget {
	return &ProxyBudget{
		Holder: holder,
		Take: func(_ context.Context, h string) (bool, time.Duration, error) {
			f.mu.Lock()
			f.takes = append(f.takes, h)
			i := len(f.takes) - 1
			f.mu.Unlock()
			return f.take(i, h)
		},
		Release: func(h string) { f.mu.Lock(); f.released = append(f.released, h); f.mu.Unlock() },
		Log:     func(l string) { f.mu.Lock(); f.lines = append(f.lines, l); f.mu.Unlock() },
		Sleep: func(ctx context.Context, d time.Duration) error {
			f.mu.Lock()
			f.sleeps = append(f.sleeps, d)
			f.mu.Unlock()
			if f.sleep != nil {
				return f.sleep(ctx, d)
			}
			return nil
		},
	}
}

func (f *pbudgetFake) takesCopy() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.takes...)
}

func (f *pbudgetFake) sleepsCopy() []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Duration(nil), f.sleeps...)
}

func (f *pbudgetFake) releasedCopy() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.released...)
}

func (f *pbudgetFake) linesCopy() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.lines...)
}

// pbudgetProxy builds a ProviderProxy the way coverProxy does: no listener, the
// upstream an in-process RoundTripper, a live ctx (a nil ctx panics in
// context.AfterFunc) and the test's budget.
func pbudgetProxy(t *testing.T, rt http.RoundTripper, b *ProxyBudget) *ProviderProxy {
	t.Helper()
	u, err := url.Parse("http://provider.example/v1")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	p := &ProviderProxy{
		upstream: u,
		silence:  5 * time.Second,
		client:   &http.Client{Transport: rt},
		stall:    make(chan struct{}),
		ctx:      ctx,
		cancel:   cancel,
		budget:   b,
	}
	t.Cleanup(cancel)
	return p
}

func pbudgetDo(t *testing.T, p *ProviderProxy, method, target string, body io.Reader, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	for k, vv := range header {
		for _, v := range vv {
			req.Header.Add(k, v)
		}
	}
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	return rec
}

// A take told to wait stalls the request (bounded by BudgetPollMax, a zero
// wait sleeps the floor), then a grant sends it once and frees its slot
// (providerbudget.go, THE GLOBAL RATE BUDGET: "a take told to wait STALLS the
// request inside the proxy").
func TestABudgetedSendTakesWaitWaitThenGrant(t *testing.T) {
	w := &pbudgetWire{}
	f := &pbudgetFake{take: func(i int, _ string) (bool, time.Duration, error) {
		switch i {
		case 0:
			return false, 0, nil
		case 1:
			return false, time.Hour, nil
		default:
			return true, 0, nil
		}
	}}
	p := pbudgetProxy(t, w, f.budget("card.g1"))
	rec := pbudgetDo(t, p, "POST", "http://provider.example/chat/completions", strings.NewReader(`{"model":"m"}`), nil)

	assert.Equal(t, 200, rec.Code)
	assert.Equal(t, "ok", rec.Body.String())
	assert.Equal(t, 1, w.count(), "one upstream request")
	assert.Equal(t, []time.Duration{10 * time.Millisecond, BudgetPollMax}, f.sleepsCopy(),
		"a zero wait sleeps the floor, a huge wait is capped at the poll")
	assert.Equal(t, []string{"card.g1.r1"}, f.releasedCopy(), "the request's slot released once")
}

// A take the server cannot read (an error, a timeout, an old server that does
// not know the verb) is granted OPEN: one line and the request goes, never an
// error, and no stall was slept (providerbudget.go, THE GLOBAL RATE BUDGET).
func TestABudgetThatCannotBeReadGoesOpen(t *testing.T) {
	w := &pbudgetWire{}
	f := &pbudgetFake{take: func(int, string) (bool, time.Duration, error) {
		return false, 0, errors.New("connection refused")
	}}
	p := pbudgetProxy(t, w, f.budget("card.g1"))
	rec := pbudgetDo(t, p, "POST", "http://provider.example/chat/completions", strings.NewReader(`{"model":"m"}`), nil)

	assert.Equal(t, 200, rec.Code)
	assert.Equal(t, 1, w.count())
	lines := f.linesCopy()
	assert.Len(t, lines, 1, "one line, the open grant")
	assert.Contains(t, lines[0], "RATE BUDGET OPEN")
	assert.Contains(t, lines[0], "card.g1")
	assert.Empty(t, f.sleepsCopy(), "an unreadable budget never sleeps")
}

// A stall logs its first wait once, not one line per poll, then one grant line
// naming the holder and the number of waits (providerbudget.go, the Log doc
// comment on ProxyBudget).
func TestABudgetStallLogsOneWaitThenTheGrant(t *testing.T) {
	w := &pbudgetWire{}
	f := &pbudgetFake{take: func(i int, _ string) (bool, time.Duration, error) {
		if i < 3 {
			return false, time.Second, nil
		}
		return true, 0, nil
	}}
	p := pbudgetProxy(t, w, f.budget("card.g1"))
	rec := pbudgetDo(t, p, "POST", "http://provider.example/chat/completions", strings.NewReader(`{"model":"m"}`), nil)

	assert.Equal(t, 200, rec.Code)
	lines := f.linesCopy()
	waits := 0
	granted := ""
	for _, l := range lines {
		if strings.Contains(l, "RATE BUDGET WAIT") {
			waits++
		}
		if strings.Contains(l, "RATE BUDGET GRANTED") {
			granted = l
		}
	}
	assert.Equal(t, 1, waits, "one wait line, not one per poll")
	assert.Contains(t, granted, "card.g1.r1")
	assert.Contains(t, granted, "(3 waits)")
}

// A 429, or the provider's words for a concurrency limit, never reaches the
// harness: the proxy waits a backoff inside the same attempt, the backoff
// doubling to the cap, and sends the same request (method, path, query, body,
// ContentLength) again with the hop-by-hop headers dropped (providerbudget.go,
// THE GLOBAL RATE BUDGET; the RFC 7230 hop-by-hop set dropped by dropHop).
func TestALimitRefusalIsResentUntilTheBudgetIsServed(t *testing.T) {
	says := []struct {
		status int
		body   string
	}{
		{429, `{"error":"Too many requests"}`},
		{400, `{"error":{"message":"Your current concurrency is 9/12, which exceeds your concurrency limit"}}`},
		{429, `{"error":"Too many requests"}`},
	}
	w := &pbudgetWire{answer: func(i int, req *http.Request) (*http.Response, error) {
		if i < len(says) {
			return pbudgetAnswer(req, says[i].status, nil, says[i].body), nil
		}
		return pbudgetAnswer(req, 200, nil, "done"), nil
	}}
	f := &pbudgetFake{take: func(int, string) (bool, time.Duration, error) { return true, 0, nil }}
	b := f.budget("card.g1")
	b.Backoff = time.Second
	b.BackoffCap = 3 * time.Second
	p := pbudgetProxy(t, w, b)

	header := http.Header{}
	header.Set("Connection", "keep-alive")
	header.Set("Upgrade", "websocket")
	header.Set("X-Keep", "yes")
	const payload = `{"model":"m"}`
	rec := pbudgetDo(t, p, "POST", "http://provider.example/chat/completions?stream=true", strings.NewReader(payload), header)

	assert.Equal(t, 200, rec.Code, "no refusal reached the harness")
	assert.Equal(t, "done", rec.Body.String())
	assert.Equal(t, 4, w.count(), "the refusals were retried in place")
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second, 3 * time.Second}, f.sleepsCopy(),
		"the backoff doubles to the cap")
	assert.Equal(t, []string{"card.g1.r1", "card.g1.r2", "card.g1.r3", "card.g1.r4"}, f.releasedCopy(),
		"each send freed its own slot")
	for i := 0; i < 4; i++ {
		s := w.at(i)
		assert.Equal(t, "POST", s.method)
		assert.Equal(t, "/chat/completions", s.path)
		assert.Equal(t, "stream=true", s.query)
		assert.Equal(t, payload, string(s.body), "the same body every send")
		assert.Equal(t, int64(len(payload)), s.length, "the same ContentLength every send")
		assert.Equal(t, "yes", s.header.Get("X-Keep"))
		assert.Empty(t, s.header.Get("Connection"), "hop-by-hop header dropped")
		assert.Empty(t, s.header.Get("Upgrade"), "hop-by-hop header dropped")
	}
}

// A refusal whose body says the quota or credit is spent is out of funds, not
// a rate limit: handed to the harness once, whole, whatever its status, with
// no backoff (providerbudget.go, limitRefusal; the comment: out of funds is
// never a rate limit).
func TestAQuotaRefusalPassesThroughWholeOnce(t *testing.T) {
	body := "Insufficient Balance " + strings.Repeat("x", 70<<10)
	w := &pbudgetWire{answer: func(_ int, req *http.Request) (*http.Response, error) {
		h := http.Header{}
		h.Set("X-Quota-Reset", "later")
		return pbudgetAnswer(req, 402, h, body), nil
	}}
	f := &pbudgetFake{take: func(int, string) (bool, time.Duration, error) { return true, 0, nil }}
	p := pbudgetProxy(t, w, f.budget("card.g1"))
	rec := pbudgetDo(t, p, "POST", "http://provider.example/chat/completions", strings.NewReader(`{"model":"m"}`), nil)

	assert.Equal(t, 402, rec.Code)
	assert.Equal(t, "later", rec.Header().Get("X-Quota-Reset"))
	assert.Equal(t, body, rec.Body.String(), "the whole body, over the 64 KiB read, byte for byte")
	assert.Equal(t, 1, w.count())
	assert.Empty(t, f.sleepsCopy(), "no backoff for a credit refusal")
}

// The run's ctx ends while the request stalls on its budget: the request ends
// 502 naming the budget wait, and nothing reached upstream (providerbudget.go,
// serveBudgeted: "the run ended while its request waited for its budget").
func TestAStallEndsWithTheRunAndAnswersBadGateway(t *testing.T) {
	w := &pbudgetWire{}
	f := &pbudgetFake{take: func(int, string) (bool, time.Duration, error) {
		return false, time.Second, nil
	}}
	b := f.budget("card.g1")
	p := pbudgetProxy(t, w, b)
	f.sleep = func(context.Context, time.Duration) error {
		p.cancel()
		return context.Canceled
	}
	rec := pbudgetDo(t, p, "POST", "http://provider.example/chat/completions", strings.NewReader(`{"model":"m"}`), nil)

	assert.Equal(t, 502, rec.Code)
	assert.Contains(t, rec.Body.String(), "the run ended while its request waited for its budget")
	assert.Equal(t, 0, w.count(), "no upstream request while the budget was spent")
}

// The run ends during a refusal's backoff: the request ends 502 naming the
// wait out, after the slot was already freed (providerbudget.go, serveBudgeted:
// "the run ended while its request waited out a refusal").
func TestARefusalBackoffEndsWithTheRunAndAnswersBadGateway(t *testing.T) {
	w := &pbudgetWire{answer: func(_ int, req *http.Request) (*http.Response, error) {
		return pbudgetAnswer(req, 429, nil, `{"error":"Too many requests"}`), nil
	}}
	f := &pbudgetFake{take: func(int, string) (bool, time.Duration, error) { return true, 0, nil }}
	b := f.budget("card.g1")
	b.Backoff = time.Second
	p := pbudgetProxy(t, w, b)
	f.sleep = func(context.Context, time.Duration) error {
		p.cancel()
		return context.Canceled
	}
	rec := pbudgetDo(t, p, "POST", "http://provider.example/chat/completions", strings.NewReader(`{"model":"m"}`), nil)

	assert.Equal(t, 502, rec.Code)
	assert.Contains(t, rec.Body.String(), "waited out a refusal")
	assert.Equal(t, 1, w.count(), "the refusal was sent once")
	assert.Equal(t, []string{"card.g1.r1"}, f.releasedCopy(), "the slot was freed before the backoff")
}

// pbudgetBadReader is a request body that cannot be read.
type pbudgetBadReader struct{}

func (pbudgetBadReader) Read([]byte) (int, error) { return 0, errors.New("pipe closed") }

// A request body that cannot be read is a 502 and never touches the budget
// (providerbudget.go, serveBudgeted reads the body before the first take).
func TestARequestBodyThatCannotBeReadNeverTakesTheBudget(t *testing.T) {
	w := &pbudgetWire{}
	f := &pbudgetFake{take: func(int, string) (bool, time.Duration, error) {
		t.Fatal("Take must not be called for a body that cannot be read")
		return false, 0, nil
	}}
	p := pbudgetProxy(t, w, f.budget("card.g1"))
	rec := pbudgetDo(t, p, "POST", "http://provider.example/chat/completions", pbudgetBadReader{}, nil)

	assert.Equal(t, 502, rec.Code)
	assert.Contains(t, rec.Body.String(), "could not be read")
	assert.Empty(t, f.takesCopy())
	assert.Equal(t, 0, w.count())
}

// The RoundTripper errors: a 502, no retry, and the request's slot is still
// freed (providerbudget.go, serveBudgeted releases after every send).
func TestAnUpstreamErrorAnswersBadGatewayAndStillReleases(t *testing.T) {
	w := &pbudgetWire{answer: func(int, *http.Request) (*http.Response, error) {
		return nil, errors.New("connection reset")
	}}
	f := &pbudgetFake{take: func(int, string) (bool, time.Duration, error) { return true, 0, nil }}
	p := pbudgetProxy(t, w, f.budget("card.g1"))
	rec := pbudgetDo(t, p, "POST", "http://provider.example/chat/completions", strings.NewReader(`{"model":"m"}`), nil)

	assert.Equal(t, 502, rec.Code)
	assert.Contains(t, rec.Body.String(), "upstream")
	assert.Equal(t, 1, w.count(), "no retry of a transport error")
	assert.Equal(t, []string{"card.g1.r1"}, f.releasedCopy(), "the slot was still freed")
	assert.Empty(t, f.sleepsCopy())
}

// With a short RenewEvery and an upstream that blocks until Renew is seen,
// Renew is called with the send's holder while the request runs
// (providerbudget.go, forwardBudgeted renews the holder's slot).
func TestTheSlotIsRenewedWhileTheSendRuns(t *testing.T) {
	var (
		mu    sync.Mutex
		got   string
		seen  = make(chan struct{}, 1)
		ready = make(chan struct{})
	)
	w := &pbudgetWire{answer: func(_ int, req *http.Request) (*http.Response, error) {
		select {
		case <-seen:
		case <-time.After(3 * time.Second):
		}
		return pbudgetAnswer(req, 200, nil, "ok"), nil
	}}
	f := &pbudgetFake{take: func(int, string) (bool, time.Duration, error) { return true, 0, nil }}
	b := f.budget("card.g1")
	b.RenewEvery = time.Millisecond
	b.Renew = func(_ context.Context, h string) {
		mu.Lock()
		if got == "" {
			got = h
			close(ready)
		}
		mu.Unlock()
		select {
		case seen <- struct{}{}:
		default:
		}
	}
	p := pbudgetProxy(t, w, b)
	rec := pbudgetDo(t, p, "POST", "http://provider.example/chat/completions", strings.NewReader(`{"model":"m"}`), nil)

	assert.Equal(t, 200, rec.Code)
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("Renew was never called while the send ran")
	}
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "card.g1.r1", got)
}

// With Renew nil the send runs as it always did and nothing panics
// (providerbudget.go, ProxyBudget: either Renew or Release may be nil).
func TestABudgetWithNoRenewSendsWithoutPanic(t *testing.T) {
	w := &pbudgetWire{}
	f := &pbudgetFake{take: func(int, string) (bool, time.Duration, error) { return true, 0, nil }}
	p := pbudgetProxy(t, w, f.budget("card.g1"))
	rec := pbudgetDo(t, p, "POST", "http://provider.example/chat/completions", strings.NewReader(`{"model":"m"}`), nil)

	assert.Equal(t, 200, rec.Code)
	assert.Equal(t, "ok", rec.Body.String())
	assert.Equal(t, 1, w.count())
}
