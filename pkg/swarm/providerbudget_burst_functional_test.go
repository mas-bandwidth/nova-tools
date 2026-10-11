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

// minuteBudget is the sprint's rpm bucket in small: budget grants per window, and a take
// past it answers the wait to the window's end (nova-sprint tla/RateBudget.tla). Its clock
// moves only when a stalled request sleeps, and the first sleep blocks until the test
// releases it, so the test can look at the proxy while the third request is stalled.
type minuteBudget struct {
	mu      sync.Mutex
	now     time.Time
	start   time.Time
	used    int
	budget  int
	window  time.Duration
	granted []time.Time
	lines   []string

	stalled chan struct{}
	release chan struct{}
	once    sync.Once
}

func (m *minuteBudget) proxyBudget() *ProxyBudget {
	return &ProxyBudget{
		Holder: "card.g1",
		Take: func(context.Context, string) (bool, time.Duration, error) {
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.now.Sub(m.start) >= m.window {
				m.start, m.used = m.now, 0
			}
			if m.used < m.budget {
				m.used++
				m.granted = append(m.granted, m.now)
				return true, 0, nil
			}
			return false, m.start.Add(m.window).Sub(m.now), nil
		},
		Log: func(l string) { m.mu.Lock(); m.lines = append(m.lines, l); m.mu.Unlock() },
		Sleep: func(ctx context.Context, d time.Duration) error {
			m.once.Do(func() { close(m.stalled) })
			select {
			case <-m.release:
			case <-ctx.Done():
				return ctx.Err()
			}
			m.mu.Lock()
			m.now = m.now.Add(d)
			m.mu.Unlock()
			return nil
		},
	}
}

// A BURST OVER THE BUDGET STALLS, IT NEVER FAILS (the owner, 2026-10-11: "the rate limited
// should just stall out delay, not fail"). A fake inception endpoint behind the proxy with
// a budget of 2 a minute: three requests at once, two go at once, the third waits inside the
// proxy until the window turns and then goes; every one answers 200, none an error, and
// each reaches the endpoint with the harness's own key.
func TestABurstOverTheBudgetStallsAndNeverFails(t *testing.T) {
	t.Parallel()
	var hits atomic.Int64
	var badAuth atomic.Int64
	inception := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-inception-key" {
			badAuth.Add(1)
		}
		hits.Add(1)
		_, err := io.WriteString(w, `{"choices":[]}`)
		assert.NoError(t, err)
	}))
	t.Cleanup(inception.Close)

	m := &minuteBudget{budget: 2, window: time.Minute, now: time.Unix(0, 0), start: time.Unix(0, 0),
		stalled: make(chan struct{}), release: make(chan struct{})}
	p, err := ListenProviderProxy(ProviderProxyConfig{Upstream: inception.URL + "/v1", Budget: m.proxyBudget(), HeaderWait: 5 * time.Second, Silence: 5 * time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, p.Close()) })

	type answer struct {
		code int
		err  error
	}
	answers := make(chan answer, 3)
	for range 3 {
		go func() {
			req, err := http.NewRequest(http.MethodPost, p.HarnessURL()+"/chat/completions", strings.NewReader(`{"model":"mercury-3-preview-1002"}`))
			if err != nil {
				answers <- answer{err: err}
				return
			}
			req.Header.Set("Authorization", "Bearer test-inception-key")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				answers <- answer{err: err}
				return
			}
			_, rerr := io.Copy(io.Discard, resp.Body)
			cerr := resp.Body.Close()
			answers <- answer{code: resp.StatusCode, err: errors.Join(rerr, cerr)}
		}()
	}

	select {
	case <-m.stalled:
	case <-time.After(20 * time.Second):
		t.Fatal("no request stalled on the spent budget")
	}
	first := []answer{<-answers, <-answers}
	assert.Equal(t, []answer{{code: 200}, {code: 200}}, first, "two requests go at once, inside the budget")
	assert.Equal(t, int64(2), hits.Load(), "the endpoint saw two requests while the third stalled")
	assert.True(t, p.Stalling(), "the third request is stalled inside the proxy, the card waiting, not idle")
	select {
	case a := <-answers:
		t.Fatalf("the third request answered before its budget turned: %+v", a)
	default:
	}

	close(m.release)
	select {
	case third := <-answers:
		assert.Equal(t, answer{code: 200}, third, "the third request waits and then passes, no error")
	case <-time.After(20 * time.Second):
		t.Fatal("the stalled request never went after the window turned")
	}
	assert.Equal(t, int64(3), hits.Load(), "the endpoint saw three requests in all")
	assert.Zero(t, badAuth.Load(), "every request reached the endpoint's path with the harness's own key")
	m.mu.Lock()
	defer m.mu.Unlock()
	require.Len(t, m.granted, 3, "three grants")
	assert.GreaterOrEqual(t, m.granted[2].Sub(m.granted[0]), time.Minute, "the third grant came a window after the first")
	assert.Contains(t, strings.Join(m.lines, "\n"), "the card stalls, it does not fail", "the stall is logged as a stall")
	assert.False(t, p.Stalling(), "nothing stalls once the third has gone")
}
