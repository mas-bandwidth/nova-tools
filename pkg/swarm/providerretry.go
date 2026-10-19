package swarm

import (
	"context"
	"math/rand/v2"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// A PROVIDER'S TRANSIENT REFUSAL IS SENT AGAIN, NEVER HANDED TO THE HARNESS FIRST
// (tla/ProviderRetry.tla; the owner 2026-10-19, "fix ultramarine so it stops failing").
//
// A provider answers a request it will take later with a 429 (its requests-per-minute or
// concurrency limit: Ultramarine's "The request exceeds your organization's requests per
// minute. Retry in 1 s."), a 503, a 529, or a body saying it is overloaded. Handed to the
// harness, one such answer ended a whole read with no verdict and wasted its slot. The proxy
// now keeps the request's body and sends it again: it waits the provider's own hint (the
// Retry-After header, retry-after-ms, or "retry in N s" in the body) or the exponential
// backoff (RefusalRetryBase doubling, at most RefusalRetryCap), whichever is longer, plus up
// to a quarter more at random so callers told the same wait do not return together. After
// RefusalRetryAttempts sends the last refusal is handed to the harness whole, status, headers
// and body, and the card fails as it did before: a provider that never recovers is still a
// provider failure, only later.
//
// A limited route (providerbudget.go) keeps its own rule for a LIMIT refusal (stall, never
// fail: the owner, 2026-10-11), with the hint now a floor under its backoff; its other
// transient refusals (503, 529, overloaded) take this bounded retry. Out of funds is never a
// transient refusal, whatever its status: it ends the take as before.

// The bounded retry's defaults.
const (
	// RefusalRetryAttempts is how many sends one harness request gets at most.
	RefusalRetryAttempts = 8
	// RefusalRetryBase is the first backoff; it doubles after each refusal.
	RefusalRetryBase = time.Second
	// RefusalRetryCap is the longest backoff.
	RefusalRetryCap = 60 * time.Second
	// RefusalHintMax is the longest provider hint honoured; a longer one is cut to it.
	RefusalHintMax = 5 * time.Minute
)

// refusalKind is what a provider's answer is to the retry.
type refusalKind int

const (
	// refusalNone is any answer that is not sent again: a success, or a refusal the
	// harness is to see (a 400, an auth refusal, out of funds).
	refusalNone refusalKind = iota
	// refusalLimit is the provider's rate or concurrency limit (limitRefusal).
	refusalLimit
	// refusalBusy is the provider unavailable or overloaded: a 503, a 529, or a 5xx whose
	// body says overloaded.
	refusalBusy
)

// classifyRefusal says what an upstream answer is to the retry. body is what was read of it.
func classifyRefusal(status int, body []byte) refusalKind {
	if status < 400 {
		return refusalNone
	}
	if limitRefusal(status, body) {
		return refusalLimit
	}
	if causeCreditRE.Match(body) {
		return refusalNone
	}
	low := strings.ToLower(string(body))
	if strings.Contains(low, "balance") || strings.Contains(low, "credit") {
		return refusalNone
	}
	if status == http.StatusServiceUnavailable || status == 529 {
		return refusalBusy
	}
	if status >= 500 && strings.Contains(low, "overloaded") {
		return refusalBusy
	}
	return refusalNone
}

// retryHintRE finds a provider's wait in a refusal's words: "Retry in 1 s", "retry after 2.5
// seconds", "try again in 300ms".
var retryHintRE = regexp.MustCompile(`(?i)(?:retry|try again)\s+(?:in|after)\s+([0-9]+(?:\.[0-9]+)?)\s*(ms|milliseconds?|s|secs?|seconds?)\b`)

// refusalHint is the wait the provider asked for, from the Retry-After header (seconds or an
// HTTP date, read against now), the retry-after-ms header, or the body's words; zero when it
// named none. A hint longer than RefusalHintMax is cut to it.
func refusalHint(h http.Header, body []byte, now time.Time) time.Duration {
	var d time.Duration
	if v := strings.TrimSpace(h.Get("Retry-After-Ms")); v != "" {
		if ms, err := strconv.ParseFloat(v, 64); err == nil && ms > 0 {
			d = time.Duration(ms * float64(time.Millisecond))
		}
	}
	if v := strings.TrimSpace(h.Get("Retry-After")); d == 0 && v != "" {
		if s, err := strconv.ParseFloat(v, 64); err == nil {
			if s > 0 {
				d = time.Duration(s * float64(time.Second))
			}
		} else if at, err := http.ParseTime(v); err == nil && at.After(now) {
			d = at.Sub(now)
		}
	}
	if d == 0 {
		if m := retryHintRE.FindSubmatch(body); m != nil {
			n, _ := strconv.ParseFloat(string(m[1]), 64)
			unit := time.Second
			if strings.HasPrefix(strings.ToLower(string(m[2])), "m") {
				unit = time.Millisecond
			}
			d = time.Duration(n * float64(unit))
		}
	}
	return min(d, RefusalHintMax)
}

// RefusalRetry is the proxy's bounded retry of a transient refusal. The zero value is the
// production policy; a test sets Sleep, Jitter and Now so no real clock runs.
type RefusalRetry struct {
	// Attempts is the most sends of one request; zero is RefusalRetryAttempts.
	Attempts int
	// Base and Cap are the backoff's first wait and its longest; zero is the default.
	Base, Cap time.Duration
	// Sleep waits d or until ctx ends; nil is a real timer.
	Sleep func(ctx context.Context, d time.Duration) error
	// Jitter is the random extra added to a wait d; nil is up to d/4 at random.
	Jitter func(d time.Duration) time.Duration
	// Now is the clock a Retry-After date is read against; nil is time.Now.
	Now func() time.Time
}

func (r *RefusalRetry) attempts() int {
	if r == nil || r.Attempts <= 0 {
		return RefusalRetryAttempts
	}
	return r.Attempts
}

func (r *RefusalRetry) now() time.Time {
	if r == nil || r.Now == nil {
		return time.Now()
	}
	return r.Now()
}

// wait is the backoff after the n-th refusal (n from 1): the exponential backoff or the
// provider's hint, whichever is longer, plus the jitter.
func (r *RefusalRetry) wait(n int, hint time.Duration) time.Duration {
	base, capped := RefusalRetryBase, RefusalRetryCap
	if r != nil && r.Base > 0 {
		base = r.Base
	}
	if r != nil && r.Cap > 0 {
		capped = r.Cap
	}
	d := base
	for i := 1; i < n && d < capped; i++ {
		d *= 2
	}
	d = max(min(d, capped), hint)
	if r != nil && r.Jitter != nil {
		return d + r.Jitter(d)
	}
	return d + rand.N(d/4+1)
}

func (r *RefusalRetry) sleep(ctx context.Context, d time.Duration) error {
	if r != nil && r.Sleep != nil {
		return r.Sleep(ctx, d)
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
