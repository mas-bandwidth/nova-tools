package swarm

import (
	"bytes"
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

// unitBudget is a ProxyBudget whose Take, Release, Renew, Log and Sleep are the test's
// fakes: it records every call and answers Take from answer. It mirrors the burst file's
// minuteBudget but lives only in the unit tier (no socket, no real clock).
type unitBudget struct {
	mu       sync.Mutex
	holder   string
	takes    []string
	sleeps   []time.Duration
	released []string
	lines    []string
	renews   []string

	backoff    time.Duration
	backoffCap time.Duration
	renewEvery time.Duration

	answer  func(i int) (bool, time.Duration, error)
	sleepFn func(ctx context.Context, d time.Duration) error
	renewFn func(ctx context.Context, h string)
}

func (b *unitBudget) budget() *ProxyBudget {
	return &ProxyBudget{
		Holder: b.holder,
		Take: func(_ context.Context, h string) (bool, time.Duration, error) {
			b.mu.Lock()
			b.takes = append(b.takes, h)
			i := len(b.takes) - 1
			b.mu.Unlock()
			return b.answer(i)
		},
		Release: func(h string) {
			b.mu.Lock()
			b.released = append(b.released, h)
			b.mu.Unlock()
		},
		Renew: b.renewFn,
		Log: func(l string) {
			b.mu.Lock()
			b.lines = append(b.lines, l)
			b.mu.Unlock()
		},
		Sleep: func(ctx context.Context, d time.Duration) error {
			b.mu.Lock()
			b.sleeps = append(b.sleeps, d)
			fn := b.sleepFn
			b.mu.Unlock()
			if fn != nil {
				return fn(ctx, d)
			}
			return nil
		},
		Backoff:    b.backoff,
		BackoffCap: b.backoffCap,
		RenewEvery: b.renewEvery,
	}
}

// unitSend is one request the in-process RoundTripper saw.
type unitSend struct {
	method string
	path   string
	query  string
	body   string
	header http.Header
	clen   int64
}

// unitTransport is the proxy's client transport, in process: it records every request and
// answers it from reply, so no socket and no real provider is opened.
type unitTransport struct {
	mu    sync.Mutex
	sends []unitSend
	reply func(i int, r *http.Request) (int, http.Header, []byte, error)
}

func (rt *unitTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	_ = r.Body.Close()
	rt.mu.Lock()
	rt.sends = append(rt.sends, unitSend{r.Method, r.URL.Path, r.URL.RawQuery, string(body), r.Header.Clone(), r.ContentLength})
	i := len(rt.sends) - 1
	rt.mu.Unlock()
	code, hdr, payload, err := rt.reply(i, r)
	if err != nil {
		return nil, err
	}
	if hdr == nil {
		hdr = http.Header{}
	}
	return &http.Response{StatusCode: code, Header: hdr, Body: io.NopCloser(bytes.NewReader(payload)), Request: r}, nil
}

func (rt *unitTransport) count() int {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return len(rt.sends)
}

func (rt *unitTransport) send(i int) unitSend {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.sends[i]
}

// unitProxy builds a ProviderProxy in process the way coverProxy does, never through
// ListenProviderProxy: upstream, ctx and cancel, stall, a transport that answers in
// process, and the caller's budget. after never fires, so a body read wins its select.
func unitProxy(t *testing.T, rt http.RoundTripper, b *ProxyBudget) *ProviderProxy {
	t.Helper()
	up, err := url.Parse("http://provider.invalid")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &ProviderProxy{
		upstream: up,
		silence:  time.Minute,
		after:    func(time.Duration) <-chan time.Time { return make(chan time.Time) },
		headerWt: time.Second,
		client:   &http.Client{Transport: rt},
		stall:    make(chan struct{}),
		ctx:      ctx,
		cancel:   cancel,
		budget:   b,
	}
}

func unitRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "http://proxy.local/chat/completions?stream=1", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer k")
	r.Header.Set("Connection", "keep-alive")
	r.Header.Set("Keep-Alive", "timeout=5")
	return r
}

func unitDo(p *ProviderProxy, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, r)
	return rec
}

// unitErrReader is a request body that cannot be read.
type unitErrReader struct{}

func (unitErrReader) Read([]byte) (int, error) { return 0, errors.New("body unreadable") }
func (unitErrReader) Close() error             { return nil }

// Take answers wait, wait, then grant: one upstream request, the harness gets its status
// and body, the waits are clamped to BudgetPollMax and a zero wait gets the floor, and the
// holder is released once (providerbudget.go:107, "frees its slot after").
func TestABudgetedRequestWaitsThenGoes(t *testing.T) {
	t.Parallel()
	b := &unitBudget{holder: "covercard.g1", answer: func(i int) (bool, time.Duration, error) {
		switch i {
		case 0:
			return false, 6 * time.Second, nil
		case 1:
			return false, 0, nil
		default:
			return true, 0, nil
		}
	}}
	rt := &unitTransport{reply: func(int, *http.Request) (int, http.Header, []byte, error) {
		return 200, http.Header{}, []byte("ok"), nil
	}}
	p := unitProxy(t, rt, b.budget())
	rec := unitDo(p, unitRequest(t, `{"model":"mercury-3"}`))

	assert.Equal(t, 200, rec.Code)
	assert.Equal(t, "ok", rec.Body.String())
	assert.Equal(t, 1, rt.count(), "one upstream request")
	assert.Len(t, b.takes, 3, "three takes")
	assert.Equal(t, []time.Duration{BudgetPollMax, 10 * time.Millisecond}, b.sleeps, "a long wait clamped to the poll, a zero wait at the floor")
	assert.Equal(t, []string{"covercard.g1.r1"}, b.released, "the request's holder released once")
	assert.False(t, p.Stalling(), "not stalling after the grant")
}

// A take the server cannot answer is granted open: the request goes at once with one
// RATE BUDGET OPEN line naming the holder, and no sleep (providerbudget.go:121-127).
func TestABudgetNotReadGoesOpen(t *testing.T) {
	t.Parallel()
	b := &unitBudget{holder: "covercard.g1", answer: func(int) (bool, time.Duration, error) {
		return false, 0, errors.New("connection refused")
	}}
	rt := &unitTransport{reply: func(int, *http.Request) (int, http.Header, []byte, error) {
		return 200, http.Header{}, []byte("ok"), nil
	}}
	p := unitProxy(t, rt, b.budget())
	rec := unitDo(p, unitRequest(t, `{"model":"mercury-3"}`))

	assert.Equal(t, 200, rec.Code, "an unreadable budget does not stop the request")
	assert.Equal(t, 1, rt.count())
	assert.Empty(t, b.sleeps, "no sleep on an open grant")
	require.Len(t, b.lines, 1)
	assert.Contains(t, b.lines[0], "RATE BUDGET OPEN")
	assert.Contains(t, b.lines[0], "covercard.g1", "the line names the holder")
}

// A stall logs one WAIT line, not one per poll, then one GRANTED line counting the waits
// (providerbudget.go:129-138).
func TestAStallLogsOneWaitThenTheGrant(t *testing.T) {
	t.Parallel()
	b := &unitBudget{holder: "covercard.g1", answer: func(i int) (bool, time.Duration, error) {
		if i < 3 {
			return false, 6 * time.Second, nil
		}
		return true, 0, nil
	}}
	rt := &unitTransport{reply: func(int, *http.Request) (int, http.Header, []byte, error) {
		return 200, http.Header{}, []byte("ok"), nil
	}}
	p := unitProxy(t, rt, b.budget())
	rec := unitDo(p, unitRequest(t, `{"model":"mercury-3"}`))
	assert.Equal(t, 200, rec.Code)

	waits, granted := 0, ""
	for _, l := range b.lines {
		if strings.Contains(l, "RATE BUDGET WAIT") {
			waits++
		}
		if strings.Contains(l, "RATE BUDGET GRANTED") {
			granted = l
		}
	}
	assert.Equal(t, 1, waits, "one wait line for the whole stall, not one per poll")
	assert.Contains(t, granted, "covercard.g1", "the grant names the holder")
	assert.Contains(t, granted, "(3 waits)", "the grant counts the waits")
}

// A limit refusal is never handed to the harness: the same request is sent again after a
// doubling backoff, with a fresh holder each send, and every send carries the harness's
// method, path, query, body and ContentLength with the hop-by-hop headers dropped
// (providerbudget.go:181-223, :250-270).
func TestALimitRefusalIsRetriedWithBackoff(t *testing.T) {
	t.Parallel()
	const wantBody = `{"model":"mercury-3"}`
	b := &unitBudget{holder: "covercard.g1", answer: func(int) (bool, time.Duration, error) {
		return true, 0, nil
	}, backoff: time.Second, backoffCap: 3 * time.Second}
	rt := &unitTransport{reply: func(i int, _ *http.Request) (int, http.Header, []byte, error) {
		switch i {
		case 0:
			return 429, http.Header{}, []byte("Too many requests"), nil
		case 1:
			return 400, http.Header{}, []byte("Your current concurrency is 9/12, which exceeds your concurrency limit"), nil
		default:
			return 200, http.Header{}, []byte("done"), nil
		}
	}}
	p := unitProxy(t, rt, b.budget())
	rec := unitDo(p, unitRequest(t, wantBody))

	assert.Equal(t, 200, rec.Code, "no refusal reached the harness")
	assert.Equal(t, "done", rec.Body.String())
	assert.Equal(t, 3, rt.count(), "the same request sent until it was answered")
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second}, b.sleeps, "the backoff doubles")
	assert.Equal(t, []string{"covercard.g1.r1", "covercard.g1.r2", "covercard.g1.r3"}, b.released, "each send frees its own holder")
	for i := 0; i < rt.count(); i++ {
		s := rt.send(i)
		assert.Equal(t, http.MethodPost, s.method)
		assert.Equal(t, "/chat/completions", s.path)
		assert.Equal(t, "stream=1", s.query)
		assert.Equal(t, wantBody, s.body, "send %d carries the harness's whole body", i)
		assert.Equal(t, int64(len(wantBody)), s.clen, "send %d carries its ContentLength", i)
		assert.Equal(t, "Bearer k", s.header.Get("Authorization"), "send %d keeps an end-to-end header", i)
		assert.Empty(t, s.header.Get("Connection"), "send %d drops Connection", i)
		assert.Empty(t, s.header.Get("Keep-Alive"), "send %d drops Keep-Alive", i)
	}
}

// A 429 that says the quota is spent is out of credit, not a rate limit: it passes through
// once with its status, headers and whole body, a body over 64 KiB byte for byte, and no
// backoff (providerbudget.go:152-169, :292-300).
func TestAQuotaRefusalPassesThroughWhole(t *testing.T) {
	t.Parallel()
	big := append([]byte(`{"error":{"type":"insufficient_quota","message":"out of quota"}}`), bytes.Repeat([]byte("a"), 70<<10)...)
	hdr := http.Header{}
	hdr.Set("X-Provider-Body", "yes")
	b := &unitBudget{holder: "covercard.g1", answer: func(int) (bool, time.Duration, error) {
		return true, 0, nil
	}}
	rt := &unitTransport{reply: func(int, *http.Request) (int, http.Header, []byte, error) {
		return 429, hdr, big, nil
	}}
	p := unitProxy(t, rt, b.budget())
	rec := unitDo(p, unitRequest(t, `{"model":"mercury-3"}`))

	assert.Equal(t, 429, rec.Code)
	assert.Equal(t, "yes", rec.Header().Get("X-Provider-Body"), "the provider's header passes through")
	assert.Equal(t, string(big), rec.Body.String(), "the whole body arrives byte for byte")
	assert.Equal(t, 1, rt.count(), "one upstream request")
	assert.Empty(t, b.sleeps, "no backoff for out of credit")
}

// The run's context ends during a stall: 502 and nothing reached upstream
// (providerbudget.go:202-205).
func TestARunEndedDuringAStallIs502(t *testing.T) {
	t.Parallel()
	runCtx, cancelRun := context.WithCancel(context.Background())
	b := &unitBudget{holder: "covercard.g1", answer: func(int) (bool, time.Duration, error) {
		return false, time.Minute, nil
	}, sleepFn: func(ctx context.Context, _ time.Duration) error {
		cancelRun()
		return ctx.Err()
	}}
	rt := &unitTransport{reply: func(int, *http.Request) (int, http.Header, []byte, error) {
		return 200, http.Header{}, []byte("ok"), nil
	}}
	p := unitProxy(t, rt, b.budget())
	rec := unitDo(p, unitRequest(t, `{"model":"mercury-3"}`).WithContext(runCtx))

	assert.Equal(t, 502, rec.Code)
	assert.Contains(t, rec.Body.String(), "the run ended while its request waited for its budget")
	assert.Equal(t, 0, rt.count(), "nothing reached upstream")
}

// The run's context ends while a refusal backoff is waited out: 502
// (providerbudget.go:215-220).
func TestARunEndedWaitingOutARefusalIs502(t *testing.T) {
	t.Parallel()
	b := &unitBudget{holder: "covercard.g1", answer: func(int) (bool, time.Duration, error) {
		return true, 0, nil
	}, backoff: time.Second, sleepFn: func(context.Context, time.Duration) error {
		return context.Canceled
	}}
	rt := &unitTransport{reply: func(i int, _ *http.Request) (int, http.Header, []byte, error) {
		if i == 0 {
			return 429, http.Header{}, []byte("Too many requests"), nil
		}
		return 200, http.Header{}, []byte("done"), nil
	}}
	p := unitProxy(t, rt, b.budget())
	rec := unitDo(p, unitRequest(t, `{"model":"mercury-3"}`))

	assert.Equal(t, 502, rec.Code)
	assert.Contains(t, rec.Body.String(), "waited out a refusal")
	assert.Equal(t, 1, rt.count(), "the refusal was not sent again")
}

// A request body that cannot be read is 502 and the budget is never asked
// (providerbudget.go:186-190).
func TestAnUnreadableRequestBodyIs502WithoutTake(t *testing.T) {
	t.Parallel()
	b := &unitBudget{holder: "covercard.g1", answer: func(int) (bool, time.Duration, error) {
		return true, 0, nil
	}}
	rt := &unitTransport{reply: func(int, *http.Request) (int, http.Header, []byte, error) {
		return 200, http.Header{}, []byte("ok"), nil
	}}
	p := unitProxy(t, rt, b.budget())
	r := unitRequest(t, `{"model":"mercury-3"}`)
	r.Body = unitErrReader{}
	rec := unitDo(p, r)

	assert.Equal(t, 502, rec.Code)
	assert.Contains(t, rec.Body.String(), "the request could not be read")
	assert.Empty(t, b.takes, "Take is never called for an unreadable body")
}

// The transport errors: 502, no retry, and the slot is still freed
// (providerbudget.go:206-209, :276-282).
func TestAForwardErrorIs502AndStillReleases(t *testing.T) {
	t.Parallel()
	b := &unitBudget{holder: "covercard.g1", answer: func(int) (bool, time.Duration, error) {
		return true, 0, nil
	}}
	rt := &unitTransport{reply: func(int, *http.Request) (int, http.Header, []byte, error) {
		return 0, nil, nil, errors.New("dial refused")
	}}
	p := unitProxy(t, rt, b.budget())
	rec := unitDo(p, unitRequest(t, `{"model":"mercury-3"}`))

	assert.Equal(t, 502, rec.Code)
	assert.Equal(t, 1, rt.count(), "no retry")
	assert.Empty(t, b.sleeps, "no backoff on a plain failure")
	assert.Equal(t, []string{"covercard.g1.r1"}, b.released, "the slot is freed after the send")
}

// Renew runs while the send is in flight, called with the send's holder; a nil Renew is
// fine (providerbudget.go:230-249).
func TestRenewRunsWhileTheSendIsInFlight(t *testing.T) {
	t.Parallel()
	renewed := make(chan string, 1)
	b := &unitBudget{holder: "covercard.g1", answer: func(int) (bool, time.Duration, error) {
		return true, 0, nil
	}, renewEvery: 2 * time.Millisecond, renewFn: func(_ context.Context, h string) {
		select {
		case renewed <- h:
		default:
		}
	}}
	rt := &unitTransport{reply: func(_ int, _ *http.Request) (int, http.Header, []byte, error) {
		select {
		case h := <-renewed:
			return 200, http.Header{"X-Provider-Renew": []string{h}}, []byte("ok"), nil
		case <-time.After(5 * time.Second):
			return 500, http.Header{}, []byte("no renew"), nil
		}
	}}
	p := unitProxy(t, rt, b.budget())
	rec := unitDo(p, unitRequest(t, `{"model":"mercury-3"}`))

	assert.Equal(t, 200, rec.Code)
	assert.Equal(t, "covercard.g1.r1", rec.Header().Get("X-Provider-Renew"), "Renew is called with the send's holder")
}

// With Renew nil the send never panics.
func TestRenewNilDoesNotPanic(t *testing.T) {
	t.Parallel()
	b := &unitBudget{holder: "covercard.g1", answer: func(int) (bool, time.Duration, error) {
		return true, 0, nil
	}}
	rt := &unitTransport{reply: func(int, *http.Request) (int, http.Header, []byte, error) {
		return 200, http.Header{}, []byte("ok"), nil
	}}
	p := unitProxy(t, rt, b.budget())
	require.NotPanics(t, func() {
		rec := unitDo(p, unitRequest(t, `{"model":"mercury-3"}`))
		assert.Equal(t, 200, rec.Code)
	})
}
