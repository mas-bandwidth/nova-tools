package swarm

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// coverProxy is a ProviderProxy with no listener and no clock: the unit tests
// of the proxy's accessors and the header-wait arithmetic build the struct
// directly, so no socket and no real time is opened for them.
func coverProxy(silence time.Duration) *ProviderProxy {
	return &ProviderProxy{silence: silence, stall: make(chan struct{})}
}

// coverTimeoutError is the net.Error a transport answers with when its header
// wait runs out: Error, Timeout and Temporary, nothing else.
type coverTimeoutError struct{ timeout bool }

func (e *coverTimeoutError) Error() string   { return "cover timeout" }
func (e *coverTimeoutError) Timeout() bool   { return e.timeout }
func (e *coverTimeoutError) Temporary() bool { return false }

// TestProviderproxyCoverSilenceReportsTheArmedGap pins Silence: it reports the
// gap the body timer is armed with, exactly as it was armed, and invents
// nothing when no gap was configured.
func TestProviderproxyCoverSilenceReportsTheArmedGap(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name    string
		silence time.Duration
		want    time.Duration
	}{
		{name: "the configured gap is reported", silence: 7 * time.Second, want: 7 * time.Second},
		{name: "the constructor's default gap is reported", silence: ProviderBodySilence, want: ProviderBodySilence},
		{name: "no gap configured is reported as zero", silence: 0, want: 0},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			p := coverProxy(row.silence)
			assert.Equal(t, row.want, p.Silence(), "silence=%s, want %s", p.Silence(), row.want)
		})
	}
}

// TestProviderproxyCoverStalledFiresOnlyWhenLost pins Stalled: the channel
// stays open while the request runs, is closed once by the loss that ends it,
// whether that loss is a body gap or an expired header wait, and a second
// signal changes nothing.
func TestProviderproxyCoverStalledFiresOnlyWhenLost(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name   string
		signal string
		want   bool
	}{
		{name: "a request still running has an open stall"},
		{name: "a body gap closes the stall", signal: "body", want: true},
		{name: "an expired header wait closes the stall", signal: "header", want: true},
		{name: "a second signal changes nothing", signal: "twice", want: true},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			p := coverProxy(time.Second)
			switch row.signal {
			case "body":
				p.markLost()
			case "header":
				p.lose(true)
			case "twice":
				p.markLost()
				p.lose(true)
			}
			fired := false
			select {
			case <-p.Stalled():
				fired = true
			default:
			}
			assert.Equal(t, row.want, fired, "stall fired=%v, want %v", fired, row.want)
			assert.Equal(t, row.want, p.Lost(), "lost=%v, want %v", p.Lost(), row.want)
		})
	}
}

// TestProviderproxyCoverHeaderWallMeasuresOnlyAnExpiredHeaderWait pins
// HeaderWall: the gap from the written request to the expired header wait,
// and zero whenever the request did not end that way.
func TestProviderproxyCoverHeaderWallMeasuresOnlyAnExpiredHeaderWait(t *testing.T) {
	t.Parallel()

	sent := time.Unix(0, 1)
	stalled := sent.Add(9 * time.Second)
	rows := []struct {
		name      string
		noHeaders bool
		sentAt    time.Time
		stalledAt time.Time
		want      time.Duration
	}{
		{name: "the expired header wait is measured from the written request",
			noHeaders: true, sentAt: sent, stalledAt: stalled, want: 9 * time.Second},
		{name: "a body gap is not a header wall",
			sentAt: sent, stalledAt: stalled, want: 0},
		{name: "a request never written has no wall",
			noHeaders: true, stalledAt: stalled, want: 0},
		{name: "a wait still running has no wall",
			noHeaders: true, sentAt: sent, want: 0},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			p := coverProxy(time.Second)
			p.noHeaders = row.noHeaders
			p.sentAt = row.sentAt
			p.stalledAt = row.stalledAt
			assert.Equal(t, row.want, p.HeaderWall(), "header wall=%s, want %s", p.HeaderWall(), row.want)
		})
	}
}

// TestProviderproxyCoverHeaderWaitExpiredNeedsASentTimeout pins
// headerWaitExpired: only a written request whose header wait ran out without
// a cancellation is the owned UNKNOWN; no error, a cancelled run, a request
// never written and a plain failure are not.
func TestProviderproxyCoverHeaderWaitExpiredNeedsASentTimeout(t *testing.T) {
	t.Parallel()

	live := context.Background()
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()

	timeout := &coverTimeoutError{timeout: true}
	rows := []struct {
		name string
		err  error
		ctx  context.Context
		sent bool
		want bool
	}{
		{name: "a written request whose header wait ran out", err: timeout, ctx: live, sent: true, want: true},
		{name: "a wrapped timeout is still an expiry", err: fmt.Errorf("write: %w", timeout), ctx: live, sent: true, want: true},
		{name: "no error is not an expiry", err: nil, ctx: live, sent: true, want: false},
		{name: "a cancelled run is not an expiry", err: timeout, ctx: cancelled, sent: true, want: false},
		{name: "a request never written is not an expiry", err: timeout, ctx: live, sent: false, want: false},
		{name: "a plain failure is not an expiry", err: errors.New("connection reset"), ctx: live, sent: true, want: false},
		{name: "a net error that is not a timeout is not an expiry", err: &coverTimeoutError{}, ctx: live, sent: true, want: false},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, row.want, headerWaitExpired(row.ctx, row.err, row.sent),
				"err=%v sent=%v expired=%v, want %v", row.err, row.sent, headerWaitExpired(row.ctx, row.err, row.sent), row.want)
		})
	}
}
