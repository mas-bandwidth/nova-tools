package reconcile

import (
	"context"
	"strconv"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// Policy is a sprint's reconciler policy, s:<S>:policy (#2930 rev 5). Its
// writer is `sprint open` (#2939); an absent or unreadable field reads as its
// DefaultPolicy value. Open is twice the 30 s Open deadline, so a live PR open
// is never flipped to ambiguous; ExpireEvery is the expire duty's self-gate.
type Policy struct {
	Start       time.Duration // start_ms: a dealt card's launched-ack window
	Beat        time.Duration // beat_ms: silence before reconcile-required
	RetryMax    int           // retry_max: ns_card_retry feeds a card back while retries < this
	Open        time.Duration // open_ms: a pending PR open older than this is ambiguous
	ExpireEvery time.Duration // expire_every_ms: one expire sweep per sprint per this
}

// DefaultPolicy is 60000/180000/1/60000/10000.
var DefaultPolicy = Policy{
	Start:       DefaultWindows.Start,
	Beat:        DefaultWindows.Beat,
	RetryMax:    1,
	Open:        2 * openDeadline,
	ExpireEvery: 10 * time.Second,
}

// PolicyKey is s:<S>:policy.
func PolicyKey(sprint string) string { return "s:" + sprint + ":policy" }

// ParsePolicy reads the policy fields of an HGETALL of s:<S>:policy. A
// missing, non-numeric or out-of-range field keeps its default.
func ParsePolicy(h map[string]string) Policy {
	p := DefaultPolicy
	ms := func(field string, into *time.Duration) {
		if v, err := strconv.ParseInt(h[field], 10, 64); err == nil && v > 0 {
			*into = time.Duration(v) * time.Millisecond
		}
	}
	ms("start_ms", &p.Start)
	ms("beat_ms", &p.Beat)
	ms("open_ms", &p.Open)
	ms("expire_every_ms", &p.ExpireEvery)
	if v, err := strconv.Atoi(h["retry_max"]); err == nil && v >= 0 {
		p.RetryMax = v
	}
	return p
}

// Windows is the policy's reclaim windows.
func (p Policy) Windows() Windows { return Windows{Start: p.Start, Beat: p.Beat} }

// RetryRequest feeds one ended card back. RetryMax is the sprint policy's
// retry_max (zero never retries). Fence is the reconciler lease token.
type RetryRequest struct {
	Sprint   string
	Label    string
	Fence    string
	RetryMax int
}

// Retry runs ns_card_retry: a card ended FAILED with reason idle-killed, or
// crash with exit -1, no pushed sha and retries below RetryMax goes back to
// the pool (QUEUED, reason retry:<reason>, retry_of=<attempt>; the next deal
// takes attempt+1) with one receipt under the idem key
// retry:<S>/<label>/<attempt>. A replay returns OK with the first receipt;
// any other card is NOTHING (a test failure, a timeout, BLOCKED, ABSTAIN, a
// pushed sha, a finding: the child may have left an effect, so never again).
func Retry(ctx context.Context, st *store.Store, req RetryRequest) (Result, error) {
	return call(ctx, st, "card retry", req.Label, "ns_card_retry", req.Sprint, req.Label, req.Fence, req.RetryMax)
}

// Retryable is the expire sweep's filter over an ended card's fields
// (outcome, reason, exit, pushed_sha, retries): true when ns_card_retry would
// requeue it. The function re-checks every guard in its own call.
func Retryable(h map[string]string, retryMax int) bool {
	// "-" is the wrapper's no-commit mark on every non-DONE end: no effect.
	if h["outcome"] != "FAILED" || (h["pushed_sha"] != "" && h["pushed_sha"] != card.NoCommit) {
		return false
	}
	if h["reason"] != "idle-killed" && (h["reason"] != "crash" || h["exit"] != "-1") {
		return false
	}
	retries, _ := strconv.Atoi(h["retries"])
	return retries < retryMax
}
