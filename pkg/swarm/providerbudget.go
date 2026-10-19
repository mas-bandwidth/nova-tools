package swarm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// THE GLOBAL RATE BUDGET, HELD AT THE ONE PLACE EVERY MODEL REQUEST PASSES (nova-sprint
// routes limit, budget take; nova-sprint tla/RateBudget.tla; the owner, 2026-10-11: "the
// rate limited should just stall out delay, not fail", "i don't want this rate limiter
// causing errors").
//
// A limited route's card runs with a ProxyBudget on its provider proxy. Before EVERY
// upstream request the proxy takes the global budget from the sprint's server (budget
// take): one grant of the model's requests-per-minute window and one of its provider's
// concurrency slots. A take told to wait STALLS the request inside the proxy (the harness
// is still waiting on its HTTP answer), bounded only by the run's own context, the card's
// deadline; while it stalls the card is not idle (Stalling, read by the idle watch). A take
// the server cannot answer (an error, a timeout, an old server that does not know the
// verb) is granted OPEN: one line logged and the request goes. A 429, or a provider's
// words for a request or concurrency limit, that still comes back is never handed to the
// harness: the proxy waits a backoff (60 s, doubling, capped at 15 min) and sends the same
// request again, inside the same attempt; the harness never sees the refusal and the card
// spends nothing. A route with no limit has no ProxyBudget and the proxy is as it was.

// The 429 backoff: BudgetBackoff first, doubling, at most BudgetBackoffCap.
const (
	BudgetBackoff    = 60 * time.Second
	BudgetBackoffCap = 15 * time.Minute
	// BudgetPollMax is the longest one stall sleeps before asking again.
	BudgetPollMax = 5 * time.Second
	// BudgetRenewEvery renews a request's slot while it runs (the lease is 2 minutes).
	BudgetRenewEvery = 40 * time.Second
	// budgetErrorBody is how much of a refusal's body is read to decide it is a limit.
	budgetErrorBody = 64 << 10
)

// ProxyBudget is a limited route's budget on its provider proxy.
type ProxyBudget struct {
	// Take asks the sprint's server for one grant for holder: granted, or the wait before
	// asking again. An error is a budget not read: the request proceeds open.
	Take func(ctx context.Context, holder string) (granted bool, wait time.Duration, err error)
	// Renew renews holder's slot while its request runs; Release frees it when it ends.
	// Either may be nil; neither returns an error the request could fail on.
	Renew   func(ctx context.Context, holder string)
	Release func(holder string)
	// Holder is the prefix of every request's holder (the launch's name): one holder per
	// request, so two requests of one run are two slots.
	Holder string
	// Log receives one line per stall (its first wait and every 30 s), per grant after a
	// stall, per open grant and per refusal retried; nil logs nothing.
	Log func(string)
	// Sleep, Backoff, BackoffCap, RenewEvery are the clock: nil and zero are the real ones.
	Sleep      func(ctx context.Context, d time.Duration) error
	Backoff    time.Duration
	BackoffCap time.Duration
	RenewEvery time.Duration
}

func (b *ProxyBudget) log(format string, args ...any) {
	if b.Log != nil {
		b.Log(fmt.Sprintf(format, args...))
	}
}

func (b *ProxyBudget) sleep(ctx context.Context, d time.Duration) error {
	if b.Sleep != nil {
		return b.Sleep(ctx, d)
	}
	// up to a fifth more, at random: callers on many machines told the same wait do not all
	// ask again in the same instant, and none is always first (tla/RateBudget.tla,
	// StalledIsGranted: the store keeps no queue)
	if d > 0 {
		d += rand.N(d/5 + 1)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// budgetState is the proxy's count of requests stalled on their budget or a backoff.
// Every proxy has one, limited or not: a transient refusal's backoff is a stall too.
type budgetState struct {
	stalled atomic.Int64
	seq     atomic.Int64
}

// Stalling says a request is waiting on its budget or a refusal's backoff (a limited
// route's, or any route's transient refusal, providerretry.go): the card is waiting, not
// idle (the idle watch's Stalled).
func (p *ProviderProxy) Stalling() bool { return p.bstate.stalled.Load() > 0 }

// takeBudget stalls until the budget grants holder (or grants open); false when ctx ended.
func (p *ProviderProxy) takeBudget(ctx context.Context, holder string) bool {
	b := p.budget
	p.bstate.stalled.Add(1)
	defer p.bstate.stalled.Add(-1)
	start := time.Now()
	var logged time.Time
	waits := 0
	for {
		if ctx.Err() != nil {
			return false
		}
		tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		granted, wait, err := b.Take(tctx, holder)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return false
			}
			b.log("RATE BUDGET OPEN %s: the budget could not be read (%s); the request goes unlimited", holder, oneLineErr(err))
			return true
		}
		if granted {
			if waits > 0 {
				b.log("RATE BUDGET GRANTED %s after %s (%d waits)", holder, time.Since(start).Round(time.Second), waits)
			}
			return true
		}
		waits++
		if logged.IsZero() || time.Since(logged) >= 30*time.Second {
			logged = time.Now()
			b.log("RATE BUDGET WAIT %s: spent; waiting %s (waited %s so far); the card stalls, it does not fail", holder, wait.Round(time.Millisecond), time.Since(start).Round(time.Second))
		}
		if err := b.sleep(ctx, min(max(wait, 10*time.Millisecond), BudgetPollMax)); err != nil {
			return false
		}
	}
}

// limitRefusal says an upstream answer is the provider's rate or concurrency limit: a 429,
// or a 4xx or 5xx whose body says so (DeepSeek's "Your current concurrency is 9/12, which
// exceeds your concurrency limit"). body is what was read of it. A refusal whose body says
// the credit or quota is spent is NEVER one, whatever its status, a 429 included (OpenAI's
// insufficient_quota, "exceeded your current quota"): the provider classifier's own words
// for credit (causeCreditRE, ClassifyProvider's CauseCredit) decide it, so the take ends as
// before and the sprint writes the provider's credit rest.
func limitRefusal(status int, body []byte) bool {
	if status < 400 {
		return false
	}
	text := string(body)
	if causeCreditRE.MatchString(text) {
		return false
	}
	low := strings.ToLower(text)
	if strings.Contains(low, "balance") || strings.Contains(low, "credit") {
		return false // out of funds is never a rate limit: it ends the take as before
	}
	if status == http.StatusTooManyRequests {
		return true
	}
	return strings.Contains(low, "too many requests") || strings.Contains(low, "rate limit") ||
		strings.Contains(low, "concurrency") && strings.Contains(low, "exceed")
}

func oneLineErr(err error) string {
	s := strings.NewReplacer("\n", " ", "\r", " ").Replace(err.Error())
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// serveRetried is ServeHTTP for every request: the request's body is kept so a refusal can
// send it again. A limited route's every send takes the budget first and frees its slot
// after; its limit refusals wait a doubling backoff (the provider's hint a floor under it)
// and are never handed to the harness. Every other transient refusal (providerretry.go) is
// sent again at most RefusalRetry.Attempts times in all, and the last is handed on whole.
func (p *ProviderProxy) serveRetried(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(p.ctx, cancel)
	defer stop()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "upstream: the request could not be read: "+oneLineErr(err), http.StatusBadGateway)
		return
	}
	b := p.budget
	var backoff, capped time.Duration
	if b != nil {
		backoff, capped = b.Backoff, b.BackoffCap
		if backoff <= 0 {
			backoff = BudgetBackoff
		}
		if capped <= 0 {
			capped = BudgetBackoffCap
		}
	}
	attempts := p.retry.attempts()
	sends := 0
	for {
		holder := ""
		if b != nil {
			holder = fmt.Sprintf("%s.r%d", b.Holder, p.bstate.seq.Add(1))
			if !p.takeBudget(ctx, holder) {
				http.Error(w, "the run ended while its request waited for its budget", http.StatusBadGateway)
				return
			}
		}
		sends++
		// a limited route's limit refusal is never final; every other transient refusal
		// is final on the last send
		final := func(k refusalKind) bool { return sends >= attempts && !(b != nil && k == refusalLimit) }
		kind, status, hint := p.forward(ctx, w, r, body, holder, final)
		if b != nil && b.Release != nil {
			b.Release(holder)
		}
		if kind == refusalNone {
			return
		}
		var wait time.Duration
		if b != nil && kind == refusalLimit {
			// a limit refusal on a limited route does not count against the bounded retry
			sends--
			wait = max(backoff, hint)
			b.log("RATE BUDGET REFUSED %s: the provider refused for its limit; the same request is sent again in %s (the card stalls, it does not fail)", holder, wait)
			backoff = min(2*backoff, capped)
		} else {
			wait = p.retry.wait(sends, hint)
			p.logf("PROVIDER REFUSED status=%d send=%d/%d hint=%s: a transient refusal; the same request is sent again in %s (the harness does not see it)", status, sends, attempts, hint, wait.Round(time.Millisecond))
		}
		p.bstate.stalled.Add(1)
		if b != nil && kind == refusalLimit {
			err = b.sleep(ctx, wait)
		} else {
			err = p.retry.sleep(ctx, wait)
		}
		p.bstate.stalled.Add(-1)
		if err != nil {
			http.Error(w, "the run ended while its request waited out a refusal: "+oneLineErr(err), http.StatusBadGateway)
			return
		}
	}
}

// forward is one send of the request upstream, renewing a limited route's holder slot while
// it runs. It answers the refusal's kind, status and the provider's hint, having written
// nothing to the harness, when the provider refused transiently and final(kind) is false:
// the request is to be sent again. Every other answer is handed to the harness and forward
// answers refusalNone.
func (p *ProviderProxy) forward(ctx context.Context, w http.ResponseWriter, r *http.Request, body []byte, holder string, final func(refusalKind) bool) (refusalKind, int, time.Duration) {
	b := p.budget
	if b != nil && b.Renew != nil {
		every := b.RenewEvery
		if every <= 0 {
			every = BudgetRenewEvery
		}
		rctx, rcancel := context.WithCancel(ctx)
		defer rcancel()
		go func() {
			t := time.NewTicker(every)
			defer t.Stop()
			for {
				select {
				case <-rctx.Done():
					return
				case <-t.C:
					b.Renew(rctx, holder)
				}
			}
		}()
	}
	target := url.URL{Scheme: p.upstream.Scheme, Host: p.upstream.Host, User: p.upstream.User, Path: r.URL.Path, RawQuery: r.URL.RawQuery}
	if target.Path == "" {
		target.Path = "/"
	}
	var wrote atomic.Bool
	traced := httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err == nil {
				wrote.Store(true)
				p.sent()
			}
		},
	})
	out, err := http.NewRequestWithContext(traced, r.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		http.Error(w, "upstream", http.StatusBadGateway)
		return refusalNone, 0, 0
	}
	out.Header = r.Header.Clone()
	dropHop(out.Header)
	out.ContentLength = int64(len(body))
	// Count only a request that is about to be forwarded. A request after the
	// body gap is refused here and does not touch the upstream.
	if !p.begin() {
		http.Error(w, "lost", http.StatusBadGateway)
		return refusalNone, 0, 0
	}
	resp, err := p.client.Do(out)
	if err != nil {
		if headerWaitExpired(ctx, err, wrote.Load()) {
			// The request was written and no headers came back inside the
			// owned wait. It may have been accepted: the same UNKNOWN as a
			// silent body, signalled before the abort, and no second
			// upstream request after it.
			p.lose(true)
			panic(http.ErrAbortHandler)
		}
		http.Error(w, "upstream", http.StatusBadGateway)
		return refusalNone, 0, 0
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			p.logf("PROVIDER NOTE %s: the provider's body did not close: %s", holder, oneLineErr(err))
		}
	}()
	p.sawHeaders()
	src := io.Reader(resp.Body)
	if resp.StatusCode >= 400 {
		// a refusal's body is read to tell a transient refusal from any other; what is not
		// sent again is handed to the harness whole, the part read and then the rest
		head, _ := io.ReadAll(io.LimitReader(resp.Body, budgetErrorBody))
		if kind := classifyRefusal(resp.StatusCode, head); kind != refusalNone && !final(kind) {
			return kind, resp.StatusCode, refusalHint(resp.Header, head, p.retry.now())
		}
		src = io.MultiReader(bytes.NewReader(head), resp.Body)
	}
	copyProxyHeader(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	if err := copyWithinSilence(ctx, w, src, p.silence, p.after); errors.Is(err, errBodySilence) {
		// A normal return would finish a chunked body and the harness would
		// read a clean EOF. Abort the connection so the stall is a broken
		// read, and signal the run. The signal is sent before the panic so
		// it is not lost when the server recovers ErrAbortHandler.
		p.markLost()
		panic(http.ErrAbortHandler)
	}
	return refusalNone, 0, 0
}
