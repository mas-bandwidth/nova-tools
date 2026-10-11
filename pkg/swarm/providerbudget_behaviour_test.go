package swarm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

// The global rate budget on the wire (providerbudget.go:18-34, nova-sprint
// tla/RateBudget.tla): a limited route's every request takes the budget first,
// a take told to wait stalls the request inside the proxy, and a limit refusal
// is sent again after a backoff. These unit tests drive (*ProviderProxy).ServeHTTP
// (providerproxy.go:423) with a budget set through takeBudget, serveBudgeted and
// forwardBudgeted (providerbudget.go:107-228) and assert on the recorder's answer,
// what reached the in-process RoundTripper, and the calls the fakes saw. The proxy
// is built in package, as coverProxy builds it, never with ListenProviderProxy (a
// socket): upstream, ctx and cancel, a stall channel, a client whose Transport is
// an in-process RoundTripper, and a ProxyBudget whose fakes are the test's.

// unitBudget is a ProxyBudget whose Take, Release, Renew, Log and Sleep are recorded
// fakes. takeFn answers each take in turn; sleepFn, when set, replaces the sleep's
// return so a test can stall or interrupt the loop without a real clock.
type unitBudget struct {
	mu       sync.Mutex
	takes    []string
	sleeps   []time.Duration
	released []string
	renewed  []string
	lines    []string
	takeFn   func(i int) (bool, time.Duration, error)
	sleepFn  func(ctx context.Context, d time.Duration) error
	b        *ProxyBudget
}

// newUnitBudget wires the recorded fakes into a ProxyBudget. takeFn nil grants every
// take; sleepFn nil sleeps nothing.
func newUnitBudget() *unitBudget {
	u := &unitBudget{}
	u.b = &ProxyBudget{
		Holder: "card.g1",
		Take: func(_ context.Context, h string) (bool, time.Duration, error) {
			u.mu.Lock()
			defer u.mu.Unlock()
			u.takes = append(u.takes, h)
			if u.takeFn == nil {
				return true, 0, nil
			}
			return u.takeFn(len(u.takes) - 1)
		},
		Release: func(h string) { u.mu.Lock(); u.released = append(u.released, h); u.mu.Unlock() },
		Renew:   func(_ context.Context, h string) { u.mu.Lock(); u.renewed = append(u.renewed, h); u.mu.Unlock() },
		Log:     func(l string) { u.mu.Lock(); u.lines = append(u.lines, l); u.mu.Unlock() },
		Sleep: func(ctx context.Context, d time.Duration) error {
			u.mu.Lock()
			u.sleeps = append(u.sleeps, d)
			fn := u.sleepFn
			u.mu.Unlock()
			if fn != nil {
				return fn(ctx, d)
			}
			return nil
		},
	}
	return u
}

// got returns copies of every recorded call, safe to read after the request ends.
func (u *unitBudget) got() (takes, released, renewed, lines []string, sleeps []time.Duration) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.takes...),
		append([]string(nil), u.released...),
		append([]string(nil), u.renewed...),
		append([]string(nil), u.lines...),
		append([]time.Duration(nil), u.sleeps...)
}

// unitRoundTripFunc turns a function into an http.RoundTripper, so a test's
// upstream is an in-process call, never a socket.
type unitRoundTripFunc func(*http.Request) (*http.Response, error)

func (f unitRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// unitBudgetProxy builds a ProviderProxy in package, the way coverProxy builds one:
// no listener, no real clock, a client whose Transport is the given in-process
// RoundTripper. cancel ends the proxy's run context.
func unitBudgetProxy(t *testing.T, b *ProxyBudget, rt http.RoundTripper) (*ProviderProxy, context.CancelFunc) {
	t.Helper()
	u, err := url.Parse("http://provider.invalid")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	p := &ProviderProxy{
		upstream: u,
		ctx:      ctx,
		cancel:   cancel,
		stall:    make(chan struct{}),
		silence:  time.Minute,
		budget:   b,
		client: &http.Client{
			Transport:     rt,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	t.Cleanup(cancel)
	return p, cancel
}

// unitResponse is the upstream's answer handed back by an in-process RoundTripper.
func unitResponse(status int, header http.Header, body []byte) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     header,
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}

// A request whose budget answers wait, wait, then grant goes upstream exactly once
// (providerbudget.go:106-143, "one grant ... and one of its provider's concurrency
// slots"): each wait is clamped into [10 ms, BudgetPollMax], and the holder's slot is
// released once after the send.
func TestABudgetedRequestWaitsThenGrants(t *testing.T) {
	t.Parallel()

	u := newUnitBudget()
	u.takeFn = func(i int) (bool, time.Duration, error) {
		switch i {
		case 0:
			return false, 6 * time.Second, nil // clamped down to BudgetPollMax
		case 1:
			return false, 0, nil // clamped up to the 10 ms floor
		default:
			return true, 0, nil
		}
	}
	var upstream int
	rt := unitRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		upstream++
		return unitResponse(http.StatusOK, nil, []byte("ok")), nil
	})
	p, _ := unitBudgetProxy(t, u.b, rt)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://example.com/chat/completions", strings.NewReader(`{"model":"mercury-3"}`))
	p.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "status %d, want 200", rec.Code)
	assert.Equal(t, "ok", rec.Body.String(), "body %q, want ok", rec.Body.String())
	assert.Equal(t, 1, upstream, "one upstream request")

	takes, released, _, _, sleeps := u.got()
	assert.Len(t, takes, 3, "three takes")
	assert.Equal(t, []time.Duration{BudgetPollMax, 10 * time.Millisecond}, sleeps,
		"each wait clamped into [10 ms, BudgetPollMax]")
	assert.Equal(t, []string{"card.g1.r1"}, released, "the holder released once")
}

// A take that errors is granted open (providerbudget.go:121-127): the request goes at
// once, one line says so, and nothing sleeps.
func TestABudgetTakeErrorGoesOpen(t *testing.T) {
	t.Parallel()

	u := newUnitBudget()
	u.takeFn = func(int) (bool, time.Duration, error) { return false, 0, errors.New("connection refused") }
	var upstream int
	rt := unitRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		upstream++
		return unitResponse(http.StatusOK, nil, []byte("ok")), nil
	})
	p, _ := unitBudgetProxy(t, u.b, rt)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://example.com/chat/completions", strings.NewReader(`{"model":"mercury-3"}`))
	p.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "an unreadable budget does not stop the request")
	assert.Equal(t, 1, upstream, "one upstream request")

	takes, released, _, lines, sleeps := u.got()
	assert.Len(t, takes, 1, "one take")
	assert.Equal(t, []string{"card.g1.r1"}, released, "the holder released once")
	assert.Empty(t, sleeps, "no sleep")
	require.Len(t, lines, 1, "one log line")
	assert.Contains(t, lines[0], "RATE BUDGET OPEN", "the open grant is logged")
	assert.Contains(t, lines[0], "card.g1.r1", "the line names the holder")
}

// A stall logs one RATE BUDGET WAIT line for the whole wait, not one per poll, and then
// one RATE BUDGET GRANTED line with the holder and the number of waits
// (providerbudget.go:128-138).
func TestAStallLogsOneWaitThenTheGrant(t *testing.T) {
	t.Parallel()

	u := newUnitBudget()
	u.takeFn = func(i int) (bool, time.Duration, error) {
		if i < 3 {
			return false, time.Second, nil
		}
		return true, 0, nil
	}
	rt := unitRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return unitResponse(http.StatusOK, nil, []byte("ok")), nil
	})
	p, _ := unitBudgetProxy(t, u.b, rt)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://example.com/chat/completions", strings.NewReader(`{"model":"mercury-3"}`))
	p.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	_, _, _, lines, sleeps := u.got()
	assert.Len(t, sleeps, 3, "three waits")
	var waits, grants int
	for _, l := range lines {
		switch {
		case strings.Contains(l, "RATE BUDGET WAIT"):
			waits++
		case strings.Contains(l, "RATE BUDGET GRANTED"):
			grants++
		}
	}
	assert.Equal(t, 1, waits, "one WAIT line for the whole stall, not one per poll")
	assert.Equal(t, 1, grants, "one GRANTED line")
	for _, l := range lines {
		if strings.Contains(l, "RATE BUDGET GRANTED") {
			assert.Contains(t, l, "card.g1.r1", "the grant names the holder")
			assert.Contains(t, l, "(3 waits)", "the grant says how many waits")
		}
	}
}

// A limit refusal is never handed to the harness (providerbudget.go:145-169): the proxy
// sends the same request again after a backoff that doubles, and every send carries the
// same method, path, query, body and ContentLength with hop-by-hop headers dropped.
func TestALimitRefusalIsRetriedWithADoublingBackoff(t *testing.T) {
	t.Parallel()

	u := newUnitBudget()
	u.b.Backoff = time.Second
	u.b.BackoffCap = 3 * time.Second

	type send struct {
		method string
		path   string
		query  string
		body   string
		length int64
		header http.Header
	}
	var mu sync.Mutex
	var sends []send
	rt := unitRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		sends = append(sends, send{
			method: r.Method,
			path:   r.URL.Path,
			query:  r.URL.RawQuery,
			body:   string(body),
			length: r.ContentLength,
			header: r.Header.Clone(),
		})
		n := len(sends)
		mu.Unlock()
		switch n {
		case 1:
			return unitResponse(http.StatusTooManyRequests, nil, []byte(`{"error":"Too many requests"}`)), nil
		case 2:
			return unitResponse(http.StatusBadRequest, nil, []byte(`{"error":{"message":"Your current concurrency is 9/12, which exceeds your concurrency limit"}}`)), nil
		default:
			return unitResponse(http.StatusOK, nil, []byte("done")), nil
		}
	})
	p, _ := unitBudgetProxy(t, u.b, rt)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://example.com/chat/completions?x=1&y=2", strings.NewReader("hello body"))
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Keep-Alive", "timeout=5")
	req.Header.Set("X-Custom", "kept")
	p.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "no refusal reached the harness")
	assert.Equal(t, "done", rec.Body.String(), "the harness sees only the 200")

	takes, released, _, _, sleeps := u.got()
	assert.Len(t, takes, 3, "each send takes its own slot")
	assert.Equal(t, []string{"card.g1.r1", "card.g1.r2", "card.g1.r3"}, released, "each send releases its own slot")
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second}, sleeps, "backoffs double")

	mu.Lock()
	got := append([]send(nil), sends...)
	mu.Unlock()
	require.Len(t, got, 3, "three sends upstream")
	for i, s := range got {
		assert.Equal(t, http.MethodPost, s.method, "send %d method", i)
		assert.Equal(t, "/chat/completions", s.path, "send %d path", i)
		assert.Equal(t, "x=1&y=2", s.query, "send %d query", i)
		assert.Equal(t, "hello body", s.body, "send %d body", i)
		assert.Equal(t, int64(len("hello body")), s.length, "send %d content length", i)
		assert.NotContains(t, s.header, "Connection", "send %d dropped Connection", i)
		assert.NotContains(t, s.header, "Keep-Alive", "send %d dropped Keep-Alive", i)
		assert.Equal(t, "kept", s.header.Get("X-Custom"), "send %d kept its own header", i)
	}
}

// Out of funds is never a rate limit (providerbudget.go:152-163): the answer is handed to
// the harness once, its status, headers and whole body byte for byte, even over 64 KiB,
// and nothing backs off.
func TestOutOfFundsPassesThroughWhole(t *testing.T) {
	t.Parallel()

	u := newUnitBudget()
	big := "Insufficient Balance" + strings.Repeat("a", 70<<10)
	var upstream int
	rt := unitRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		upstream++
		return unitResponse(http.StatusPaymentRequired, http.Header{"X-Quota": []string{"spent"}}, []byte(big)), nil
	})
	p, _ := unitBudgetProxy(t, u.b, rt)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://example.com/chat/completions", strings.NewReader(`{"model":"mercury-3"}`))
	p.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusPaymentRequired, rec.Code, "the 402 passes through")
	assert.Equal(t, "spent", rec.Header().Get("X-Quota"), "the provider's header passes through")
	assert.Equal(t, big, rec.Body.String(), "the whole body over 64 KiB arrives byte for byte")
	assert.Equal(t, 1, upstream, "one upstream request")

	_, released, _, _, sleeps := u.got()
	assert.Equal(t, []string{"card.g1.r1"}, released, "the holder released once")
	assert.Empty(t, sleeps, "no backoff for out of funds")
}

// The run ends while its request stalls, or while it sleeps out a refusal, and the
// harness is answered 502 with nothing sent upstream (providerbudget.go:202-219).
func TestAStallAndARefusalEndWithTheRun(t *testing.T) {
	t.Parallel()

	t.Run("the run ends during a stall", func(t *testing.T) {
		t.Parallel()
		u := newUnitBudget()
		u.takeFn = func(int) (bool, time.Duration, error) { return false, time.Minute, nil }
		var inStall sync.Once
		stalled := make(chan struct{})
		u.sleepFn = func(ctx context.Context, d time.Duration) error {
			inStall.Do(func() { close(stalled) })
			<-ctx.Done()
			return ctx.Err()
		}
		var upstream int
		rt := unitRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			upstream++
			return unitResponse(http.StatusOK, nil, nil), nil
		})
		p, cancel := unitBudgetProxy(t, u.b, rt)

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "http://example.com/chat/completions", strings.NewReader(`{"model":"mercury-3"}`))
		done := make(chan struct{})
		go func() {
			defer close(done)
			p.ServeHTTP(rec, req)
		}()
		<-stalled
		cancel()
		<-done

		assert.Equal(t, http.StatusBadGateway, rec.Code, "status %d, want 502", rec.Code)
		assert.Contains(t, rec.Body.String(), "the run ended while its request waited for its budget")
		assert.Equal(t, 0, upstream, "nothing reached upstream while the budget was spent")
	})

	t.Run("the sleep during a refusal's backoff errors", func(t *testing.T) {
		t.Parallel()
		u := newUnitBudget()
		u.sleepFn = func(context.Context, time.Duration) error { return errors.New("the run ended") }
		rt := unitRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			return unitResponse(http.StatusTooManyRequests, nil, []byte(`{"error":"Too many requests"}`)), nil
		})
		p, _ := unitBudgetProxy(t, u.b, rt)

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "http://example.com/chat/completions", strings.NewReader(`{"model":"mercury-3"}`))
		p.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusBadGateway, rec.Code, "status %d, want 502", rec.Code)
		assert.Contains(t, rec.Body.String(), "waited out a refusal", "the answer says it waited out a refusal")
		assert.Contains(t, rec.Body.String(), "the run ended", "the answer carries the sleep's error")
		_, released, _, _, _ := u.got()
		assert.Equal(t, []string{"card.g1.r1"}, released, "the refused send released its slot before the backoff")
	})
}

// A request whose body cannot be read is answered 502 before any budget is taken
// (providerbudget.go:186-190).
func TestAnUnreadableBodyNeverTakesTheBudget(t *testing.T) {
	t.Parallel()

	u := newUnitBudget()
	rt := unitRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return unitResponse(http.StatusOK, nil, nil), nil
	})
	p, _ := unitBudgetProxy(t, u.b, rt)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://example.com/chat/completions", errReader{})
	p.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadGateway, rec.Code, "status %d, want 502", rec.Code)
	assert.Contains(t, rec.Body.String(), "the request could not be read")

	takes, _, _, _, _ := u.got()
	assert.Empty(t, takes, "the budget is never taken for an unreadable body")
}

// A send the transport refuses is answered 502 with no retry, and the slot is still
// released (providerbudget.go:276-283).
func TestATransportErrorAnswers502AndReleases(t *testing.T) {
	t.Parallel()

	u := newUnitBudget()
	rt := unitRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	})
	p, _ := unitBudgetProxy(t, u.b, rt)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://example.com/chat/completions", strings.NewReader(`{"model":"mercury-3"}`))
	p.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadGateway, rec.Code, "status %d, want 502", rec.Code)
	takes, released, _, _, sleeps := u.got()
	assert.Len(t, takes, 1, "one take")
	assert.Equal(t, []string{"card.g1.r1"}, released, "the slot is released even on a failed send")
	assert.Empty(t, sleeps, "no backoff for a transport error")
}

// A send renews its holder's slot while it runs (providerbudget.go:230-248): with a short
// RenewEvery and a transport that waits for the renew, Renew is called with the send's
// holder; a nil Renew panics nothing.
func TestASendRenewsItsSlotWhileItRuns(t *testing.T) {
	t.Parallel()

	t.Run("renew is called with the send's holder", func(t *testing.T) {
		t.Parallel()
		u := newUnitBudget()
		u.b.RenewEvery = time.Millisecond
		renewed := make(chan struct{})
		var once sync.Once
		u.b.Renew = func(_ context.Context, h string) {
			u.mu.Lock()
			u.renewed = append(u.renewed, h)
			u.mu.Unlock()
			once.Do(func() { close(renewed) })
		}
		rt := unitRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			<-renewed
			return unitResponse(http.StatusOK, nil, []byte("ok")), nil
		})
		p, _ := unitBudgetProxy(t, u.b, rt)

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "http://example.com/chat/completions", strings.NewReader(`{"model":"mercury-3"}`))
		p.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		_, _, renewedGot, _, _ := u.got()
		require.NotEmpty(t, renewedGot, "Renew was called")
		assert.Equal(t, "card.g1.r1", renewedGot[0], "Renew is called with the send's holder")
	})

	t.Run("a nil renew is fine", func(t *testing.T) {
		t.Parallel()
		u := newUnitBudget()
		u.b.Renew = nil
		rt := unitRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			return unitResponse(http.StatusOK, nil, []byte("ok")), nil
		})
		p, _ := unitBudgetProxy(t, u.b, rt)

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "http://example.com/chat/completions", strings.NewReader(`{"model":"mercury-3"}`))
		require.NotPanics(t, func() { p.ServeHTTP(rec, req) })
		assert.Equal(t, http.StatusOK, rec.Code)
	})
}

// errReader is a request body that always fails to read.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }
