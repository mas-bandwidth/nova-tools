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
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// budgetUnitTransport is the proxy's upstream in the unit tier: an in-process RoundTripper,
// no socket. round answers each send; got records the request as the upstream saw it, its
// body read whole, so the harness's method, path, query, body, length and headers can be
// checked per send.
type budgetUnitTransport struct {
	mu    sync.Mutex
	got   []budgetUnitSend
	round func(*http.Request) (*http.Response, error)
}

// budgetUnitSend is what one forwarded request looked like at the upstream.
type budgetUnitSend struct {
	method string
	path   string
	query  string
	body   string
	length int64
	header http.Header
}

func (t *budgetUnitTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(r.Body)
	t.mu.Lock()
	t.got = append(t.got, budgetUnitSend{
		method: r.Method,
		path:   r.URL.Path,
		query:  r.URL.RawQuery,
		body:   string(body),
		length: r.ContentLength,
		header: r.Header.Clone(),
	})
	t.mu.Unlock()
	return t.round(r)
}

// budgetUnitProbe is a ProxyBudget with a scripted take and recording fakes: the unit tier's
// budget, no socket and no clock, so the Sleep seam is the test's.
type budgetUnitProbe struct {
	mu       sync.Mutex
	takes    []string
	sleeps   []time.Duration
	released []string
	renewed  []string
	lines    []string
	answer   func(i int) (bool, time.Duration, error)
	sleepErr error
	renewCh  chan struct{}
}

func (b *budgetUnitProbe) budget() *ProxyBudget {
	return &ProxyBudget{
		Holder: "card.g1",
		Take: func(_ context.Context, holder string) (bool, time.Duration, error) {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.takes = append(b.takes, holder)
			return b.answer(len(b.takes) - 1)
		},
		Release: func(holder string) {
			b.mu.Lock()
			b.released = append(b.released, holder)
			b.mu.Unlock()
		},
		Renew: func(_ context.Context, holder string) {
			b.mu.Lock()
			b.renewed = append(b.renewed, holder)
			ch := b.renewCh
			b.mu.Unlock()
			if ch != nil {
				select {
				case ch <- struct{}{}:
				default:
				}
			}
		},
		Log: func(line string) {
			b.mu.Lock()
			b.lines = append(b.lines, line)
			b.mu.Unlock()
		},
		Sleep: func(_ context.Context, d time.Duration) error {
			b.mu.Lock()
			b.sleeps = append(b.sleeps, d)
			err := b.sleepErr
			b.mu.Unlock()
			return err
		},
	}
}

// renewedHolders returns the holders Renew saw, safe against the renew goroutine.
func (b *budgetUnitProbe) renewedHolders() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.renewed...)
}

// budgetUnitProxy is a ProviderProxy built in-package, the way coverProxy builds the struct,
// never with ListenProviderProxy: an upstream, a live ctx and cancel, an in-process transport
// and the probe's budget, so the unit tier opens no socket and waits on no real clock.
func budgetUnitProxy(b *ProxyBudget, tr http.RoundTripper) *ProviderProxy {
	ctx, cancel := context.WithCancel(context.Background())
	return &ProviderProxy{
		upstream: &url.URL{Scheme: "https", Host: "provider.example.com", Path: "/v1"},
		silence:  time.Second,
		headerWt: time.Second,
		client: &http.Client{
			Transport:     tr,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		stall:  make(chan struct{}),
		ctx:    ctx,
		cancel: cancel,
		budget: b,
	}
}

// budgetUnitCall drives ServeHTTP, the entry this card names, and returns the recorder.
func budgetUnitCall(t *testing.T, p *ProviderProxy, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	return rec
}

func budgetUnitResponse(status int, header http.Header, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// The global budget's take answers wait, then wait, then grants (providerbudget.go:18-34): one
// upstream request carries the harness's answer; each stall sleeps at most BudgetPollMax, and a
// zero wait sleeps the floor; the send's holder is released once.
func TestTakeAnswersWaitThenGrants(t *testing.T) {
	t.Parallel()

	b := &budgetUnitProbe{answer: func(i int) (bool, time.Duration, error) {
		switch i {
		case 0:
			return false, 30 * time.Second, nil
		case 1:
			return false, 0, nil
		default:
			return true, 0, nil
		}
	}}
	tr := &budgetUnitTransport{round: func(*http.Request) (*http.Response, error) {
		return budgetUnitResponse(http.StatusOK, nil, "ok"), nil
	}}
	p := budgetUnitProxy(b.budget(), tr)
	rec := budgetUnitCall(t, p, httptest.NewRequest(http.MethodPost, "http://harness.example.com/chat/completions", strings.NewReader(`{"model":"mercury-3"}`)))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "ok", rec.Body.String())
	assert.Len(t, tr.got, 1, "one upstream request")
	require.Len(t, b.sleeps, 2)
	assert.Equal(t, []time.Duration{BudgetPollMax, 10 * time.Millisecond}, b.sleeps, "the waits sleep, clamped to the poll, and a zero wait sleeps the floor")
	require.Len(t, b.released, 1)
	assert.Equal(t, "card.g1.r1", b.released[0], "the send's holder released once")
}

// The budget cannot be read (providerbudget.go:28-30): the request goes at once, open, with one
// RATE BUDGET OPEN line naming the holder, and no stall.
func TestTakeErrorGoesOpenImmediately(t *testing.T) {
	t.Parallel()

	b := &budgetUnitProbe{answer: func(int) (bool, time.Duration, error) {
		return false, 0, errors.New("connection refused")
	}}
	tr := &budgetUnitTransport{round: func(*http.Request) (*http.Response, error) {
		return budgetUnitResponse(http.StatusOK, nil, "ok"), nil
	}}
	p := budgetUnitProxy(b.budget(), tr)
	rec := budgetUnitCall(t, p, httptest.NewRequest(http.MethodPost, "http://harness.example.com/chat/completions", strings.NewReader(`{"model":"mercury-3"}`)))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Len(t, tr.got, 1)
	assert.Empty(t, b.sleeps, "an open grant does not stall")
	require.Len(t, b.lines, 1)
	assert.Contains(t, b.lines[0], "RATE BUDGET OPEN")
	assert.Contains(t, b.lines[0], "card.g1.r1")
}

// A stall logs one RATE BUDGET WAIT line for the whole wait, not one per poll, then RATE BUDGET
// GRANTED with the holder and the wait count (providerbudget.go:29-30, takeBudget's log).
func TestAStallLogsOneWaitThenGranted(t *testing.T) {
	t.Parallel()

	b := &budgetUnitProbe{answer: func(i int) (bool, time.Duration, error) {
		if i < 4 {
			return false, time.Second, nil
		}
		return true, 0, nil
	}}
	tr := &budgetUnitTransport{round: func(*http.Request) (*http.Response, error) {
		return budgetUnitResponse(http.StatusOK, nil, "ok"), nil
	}}
	p := budgetUnitProxy(b.budget(), tr)
	rec := budgetUnitCall(t, p, httptest.NewRequest(http.MethodPost, "http://harness.example.com/chat/completions", strings.NewReader(`{"model":"mercury-3"}`)))

	assert.Equal(t, http.StatusOK, rec.Code)
	waits, grants := 0, 0
	for _, l := range b.lines {
		if strings.HasPrefix(l, "RATE BUDGET WAIT ") {
			waits++
		}
		if strings.HasPrefix(l, "RATE BUDGET GRANTED ") {
			grants++
		}
	}
	assert.Equal(t, 1, waits, "one WAIT line for the whole stall, not one per poll")
	require.Equal(t, 1, grants)
	require.NotEmpty(t, b.lines)
	assert.Contains(t, b.lines[len(b.lines)-1], "RATE BUDGET GRANTED card.g1.r1")
	assert.Contains(t, b.lines[len(b.lines)-1], "(4 waits)")
}

// A limit refusal never reaches the harness (providerbudget.go:30-34): the proxy backs off,
// doubling to the cap, and sends the same request again in the same attempt; every send keeps
// the method, path, query, body and length, drops hop-by-hop headers, and releases its own slot.
func TestALimitRefusalIsRetriedUntilItPasses(t *testing.T) {
	t.Parallel()

	calls := 0
	tr := &budgetUnitTransport{round: func(*http.Request) (*http.Response, error) {
		calls++
		switch calls {
		case 1:
			return budgetUnitResponse(http.StatusTooManyRequests, nil, `{"error":"Too many requests"}`), nil
		case 2:
			return budgetUnitResponse(http.StatusBadRequest, nil, "Your current concurrency is 9/12, which exceeds your concurrency limit"), nil
		default:
			return budgetUnitResponse(http.StatusOK, nil, "done"), nil
		}
	}}
	b := &budgetUnitProbe{answer: func(int) (bool, time.Duration, error) { return true, 0, nil }}
	bg := b.budget()
	bg.Backoff = time.Second
	bg.BackoffCap = 3 * time.Second
	p := budgetUnitProxy(bg, tr)
	req := httptest.NewRequest(http.MethodPost, "http://harness.example.com/v1/chat/completions?x=1", strings.NewReader(`{"model":"mercury-3"}`))
	req.Header.Set("Authorization", "Bearer k")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Keep-Alive", "timeout=5")
	rec := budgetUnitCall(t, p, req)

	assert.Equal(t, http.StatusOK, rec.Code, "no refusal reached the harness")
	assert.Equal(t, "done", rec.Body.String())
	assert.Len(t, tr.got, 3, "three upstream sends")
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second}, b.sleeps, "the backoffs double")
	require.Len(t, b.released, 3)
	assert.Equal(t, []string{"card.g1.r1", "card.g1.r2", "card.g1.r3"}, b.released, "each send releases its own slot")
	for _, s := range tr.got {
		assert.Equal(t, http.MethodPost, s.method)
		assert.Equal(t, "/v1/chat/completions", s.path)
		assert.Equal(t, "x=1", s.query)
		assert.Equal(t, `{"model":"mercury-3"}`, s.body)
		assert.Equal(t, int64(len(`{"model":"mercury-3"}`)), s.length)
		assert.Equal(t, "Bearer k", s.header.Get("Authorization"))
		assert.Empty(t, s.header.Get("Connection"), "hop-by-hop headers dropped")
		assert.Empty(t, s.header.Get("Keep-Alive"), "hop-by-hop headers dropped")
	}
}

// A 429 that says the quota is spent is out of credit, never a rate limit (providerbudget.go:31-34,
// limitRefusal): it reaches the harness once, whole, with its headers, and no backoff.
func TestAQuota429PassesThroughWhole(t *testing.T) {
	t.Parallel()

	tr := &budgetUnitTransport{round: func(*http.Request) (*http.Response, error) {
		return budgetUnitResponse(http.StatusTooManyRequests, http.Header{"X-Rate": []string{"quota"}}, `{"error":{"type":"insufficient_quota","message":"You exceeded your current quota"}}`), nil
	}}
	b := &budgetUnitProbe{answer: func(int) (bool, time.Duration, error) { return true, 0, nil }}
	p := budgetUnitProxy(b.budget(), tr)
	rec := budgetUnitCall(t, p, httptest.NewRequest(http.MethodPost, "http://harness.example.com/chat/completions", strings.NewReader(`{"model":"mercury-3"}`)))

	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Equal(t, "quota", rec.Header().Get("X-Rate"))
	assert.Equal(t, `{"error":{"type":"insufficient_quota","message":"You exceeded your current quota"}}`, rec.Body.String())
	assert.Len(t, tr.got, 1, "one upstream request, no retry")
	assert.Empty(t, b.sleeps, "no backoff")
}

// Out of funds is never a rate limit (providerbudget.go:31-34): a 402 "Insufficient Balance" with
// a body over the refusal window reaches the harness once, byte for byte, and no backoff.
func TestOutOfFunds402PassesThroughWholeBody(t *testing.T) {
	t.Parallel()

	big := strings.Repeat("x", 64<<10+1)
	tr := &budgetUnitTransport{round: func(*http.Request) (*http.Response, error) {
		return budgetUnitResponse(http.StatusPaymentRequired, http.Header{"Content-Type": []string{"application/json"}}, `{"error":"Insufficient Balance"}`+big), nil
	}}
	b := &budgetUnitProbe{answer: func(int) (bool, time.Duration, error) { return true, 0, nil }}
	p := budgetUnitProxy(b.budget(), tr)
	rec := budgetUnitCall(t, p, httptest.NewRequest(http.MethodPost, "http://harness.example.com/chat/completions", strings.NewReader(`{"model":"mercury-3"}`)))

	assert.Equal(t, http.StatusPaymentRequired, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Equal(t, `{"error":"Insufficient Balance"}`+big, rec.Body.String(), "the body over the refusal window arrives byte for byte")
	assert.Len(t, tr.got, 1)
	assert.Empty(t, b.sleeps, "no backoff")
}

// The run ends while its request stalls on the budget (providerbudget.go:24-27): the harness gets
// 502 "the run ended while its request waited for its budget", and nothing reached upstream.
func TestTheRunEndingDuringAStallIs502(t *testing.T) {
	t.Parallel()

	b := &budgetUnitProbe{answer: func(int) (bool, time.Duration, error) { return false, time.Minute, nil }}
	b.sleepErr = context.Canceled
	tr := &budgetUnitTransport{round: func(*http.Request) (*http.Response, error) {
		return budgetUnitResponse(http.StatusOK, nil, "ok"), nil
	}}
	p := budgetUnitProxy(b.budget(), tr)
	rec := budgetUnitCall(t, p, httptest.NewRequest(http.MethodPost, "http://harness.example.com/chat/completions", strings.NewReader(`{"model":"mercury-3"}`)))

	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Contains(t, rec.Body.String(), "the run ended while its request waited for its budget")
	assert.Empty(t, tr.got, "nothing reached upstream while the budget was spent")
}

// The run ends while its request waits out a refusal's backoff (providerbudget.go:32-34): the
// harness gets 502 "waited out a refusal", and the refusal itself never reached the harness.
func TestTheRunEndingDuringABackoffIs502(t *testing.T) {
	t.Parallel()

	tr := &budgetUnitTransport{round: func(*http.Request) (*http.Response, error) {
		return budgetUnitResponse(http.StatusTooManyRequests, nil, "slow down"), nil
	}}
	b := &budgetUnitProbe{answer: func(int) (bool, time.Duration, error) { return true, 0, nil }}
	b.sleepErr = errors.New("cut short")
	p := budgetUnitProxy(b.budget(), tr)
	rec := budgetUnitCall(t, p, httptest.NewRequest(http.MethodPost, "http://harness.example.com/chat/completions", strings.NewReader(`{"model":"mercury-3"}`)))

	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Contains(t, rec.Body.String(), "the run ended while its request waited out a refusal")
	assert.Contains(t, rec.Body.String(), "cut short")
	assert.Len(t, tr.got, 1, "the one refusal was sent upstream, then the backoff ended the run")
	require.Len(t, b.released, 1, "the refused send still released its slot")
}

// A request body the proxy cannot read (providerbudget.go:186-189): 502 before the budget is
// taken, and Take is never called.
func TestAnUnreadableBodyIs502WithoutTakingBudget(t *testing.T) {
	t.Parallel()

	tr := &budgetUnitTransport{round: func(*http.Request) (*http.Response, error) {
		return budgetUnitResponse(http.StatusOK, nil, "ok"), nil
	}}
	b := &budgetUnitProbe{answer: func(int) (bool, time.Duration, error) { return true, 0, nil }}
	p := budgetUnitProxy(b.budget(), tr)
	req := httptest.NewRequest(http.MethodPost, "http://harness.example.com/chat/completions", iotest.ErrReader(errors.New("boom")))
	rec := budgetUnitCall(t, p, req)

	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Contains(t, rec.Body.String(), "the request could not be read")
	assert.Empty(t, b.takes, "the budget is never taken for an unreadable request")
	assert.Empty(t, tr.got, "nothing reached upstream")
}

// A send that errors out (providerbudget.go:276-283): 502, no retry, and the send's slot is
// still released.
func TestARoundTripperErrorIs502AndReleases(t *testing.T) {
	t.Parallel()

	tr := &budgetUnitTransport{round: func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection reset")
	}}
	b := &budgetUnitProbe{answer: func(int) (bool, time.Duration, error) { return true, 0, nil }}
	p := budgetUnitProxy(b.budget(), tr)
	rec := budgetUnitCall(t, p, httptest.NewRequest(http.MethodPost, "http://harness.example.com/chat/completions", strings.NewReader(`{"model":"mercury-3"}`)))

	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Len(t, tr.got, 1, "no retry after an error")
	require.Len(t, b.takes, 1)
	require.Len(t, b.released, 1)
	assert.Equal(t, "card.g1.r1", b.released[0])
}

// A request's slot is renewed while it runs (providerbudget.go:52-55, forwardBudgeted's renewer):
// with a short RenewEvery and an upstream that blocks until it sees a renewal, Renew is called
// with the send's holder.
func TestRenewIsCalledForTheSendHolder(t *testing.T) {
	t.Parallel()

	renewed := make(chan struct{}, 1)
	b := &budgetUnitProbe{answer: func(int) (bool, time.Duration, error) { return true, 0, nil }}
	b.renewCh = renewed
	bg := b.budget()
	bg.RenewEvery = time.Millisecond
	tr := &budgetUnitTransport{round: func(*http.Request) (*http.Response, error) {
		<-renewed
		return budgetUnitResponse(http.StatusOK, nil, "ok"), nil
	}}
	p := budgetUnitProxy(bg, tr)
	rec := budgetUnitCall(t, p, httptest.NewRequest(http.MethodPost, "http://harness.example.com/chat/completions", strings.NewReader(`{"model":"mercury-3"}`)))

	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotEmpty(t, b.renewedHolders(), "the slot was renewed while the request ran")
	assert.Equal(t, "card.g1.r1", b.renewedHolders()[0], "Renew carries the send's holder")
}

// A budget with no Renew (providerbudget.go:52-55: Renew may be nil) runs a request without
// panicking.
func TestNilRenewDoesNotPanic(t *testing.T) {
	t.Parallel()

	b := &budgetUnitProbe{answer: func(int) (bool, time.Duration, error) { return true, 0, nil }}
	bg := b.budget()
	bg.Renew = nil
	tr := &budgetUnitTransport{round: func(*http.Request) (*http.Response, error) {
		return budgetUnitResponse(http.StatusOK, nil, "ok"), nil
	}}
	p := budgetUnitProxy(bg, tr)
	rec := budgetUnitCall(t, p, httptest.NewRequest(http.MethodPost, "http://harness.example.com/chat/completions", strings.NewReader(`{"model":"mercury-3"}`)))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, b.renewedHolders())
}
