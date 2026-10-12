package main

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/sprintwire"
)

// A member and a reader reach the sprint server through one dialer: every verb
// is sent to the server, and a verb the server did not answer is tried again
// next pass. The dialer is what the loop already does around that (nova-sprint/pkg/member,
// BeatLoop and Tick): after a connection refused or reset it backs off, one
// second doubling to RedialLast, and it re-dials with a client rebuilt from
// scratch, so it never parks on a connection the server no longer holds and its
// beat and tick go on (tla/LoopRedial.tla, ServerRestart, Redial, BeatsAgain).
//
// Why a client is rebuilt. A sprintwire.Client with no HTTP client is built on
// http.DefaultTransport, whose process-wide connection pool keeps the idle
// keep-alive connection. After the server restarts, the next verb can be written
// to that dead socket, and Go does not replay a POST once its bytes are on the
// wire: the verb has no answer and the loop waits on a connection the server
// dropped (tla/LoopRedial.tla, NoParkOnDropped). The dialer holds a client whose
// transport opens a fresh connection per verb (DisableKeepAlives) and replaces
// it after any error, so a dead connection is never reused.
const (
	// RedialFirst is the wait before the first re-dial after a refused or reset
	// connection (tla/LoopRedial.tla, BackoffBounded).
	RedialFirst = time.Second
	// RedialLast is the ceiling the wait doubles to (tla/LoopRedial.tla, BackoffBounded).
	RedialLast = 30 * time.Second
)

// sprintDialer is the sprint server's connection as the member and reader send
// through it: an address, a client rebuilt on error, and the backoff between
// asks while the server is not answering. A nil dial or wait is the production
// one (sprintwire.Client.Do, time.Sleep); a test sets them to watch the redial
// without a socket.
type sprintDialer struct {
	addr string
	// dial sends one batch on the client given (tla/LoopRedial.tla, Redial).
	dial func(ctx context.Context, addr string, hc *http.Client, verbs ...[]string) ([]sprintwire.Result, error)
	// wait blocks d or until ctx ends; false says ctx ended.
	wait func(ctx context.Context, d time.Duration) bool

	mu      sync.Mutex
	client  *http.Client
	backoff time.Duration // 0 while the server answers; else RedialFirst doubling to RedialLast
}

// newSprintDialer is the dialer for the sprint server at addr.
func newSprintDialer(addr string) *sprintDialer {
	d := &sprintDialer{addr: addr}
	d.client = d.fresh()
	return d
}

// fresh is a client for one round: its own transport, a fresh connection per
// verb and a bounded dial, over sprintwire's answer timeout.
func (d *sprintDialer) fresh() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext:       (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		},
		Timeout: sprintwire.Timeout,
	}
}

// do sends one batch of verbs to the server: it waits out the backoff while the
// server is not answering, dials on a client of its own, and, when the dial
// fails, backs off and rebuilds the client for the next try (tla/LoopRedial.tla,
// Redial, BackoffBounded). It is the Send a sprintwire.Worker is given.
func (d *sprintDialer) do(ctx context.Context, verbs ...[]string) ([]sprintwire.Result, error) {
	d.mu.Lock()
	backoff, client := d.backoff, d.client
	d.mu.Unlock()
	if backoff > 0 {
		wait := d.wait
		if wait == nil {
			wait = sleepCtx
		}
		if !wait(ctx, backoff) {
			return nil, ctx.Err()
		}
		d.mu.Lock()
		client = d.client // the client rebuilt after the last error
		d.mu.Unlock()
	}
	dial := d.dial
	if dial == nil {
		dial = func(ctx context.Context, addr string, hc *http.Client, verbs ...[]string) ([]sprintwire.Result, error) {
			return sprintwire.Client{Addr: addr, HTTP: hc}.Do(ctx, verbs...)
		}
	}
	res, err := dial(ctx, d.addr, client, verbs...)
	d.after(err)
	return res, err
}

// after records how the dial ended: an answer clears the backoff, an error
// doubles it up to RedialLast and replaces the client, so the next verb cannot
// be sent on the connection the server dropped (tla/LoopRedial.tla, Redial).
func (d *sprintDialer) after(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err == nil {
		d.backoff = 0
		return
	}
	if d.backoff == 0 {
		d.backoff = RedialFirst
	} else {
		d.backoff = min(2*d.backoff, RedialLast)
	}
	d.client = d.fresh()
}

// sleepCtx sleeps d, or returns false when ctx ends first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
