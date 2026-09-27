package store

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/redis/go-redis/v9"
)

// Trips counts the network round trips a Store's client makes from the moment
// CountTrips attaches it: each single command is one, each pipeline Exec is
// one however many commands it carries. Verbs print the count on their
// receipt (`trips=<n>`, #3261 and #3265) so a regression to serial reads
// shows in the output, not only in a test.
type Trips struct {
	n      atomic.Int64
	mu     sync.Mutex
	labels map[string]int64
}

// CountTrips attaches a round-trip counter to the store's client.
func (s *Store) CountTrips() *Trips {
	t := &Trips{}
	s.client.AddHook(t)
	return t
}

// N is the number of round trips counted so far.
func (t *Trips) N() int64 {
	if t == nil {
		return 0
	}
	return t.n.Load()
}

// Labels (Glenn 2026-09-27: "You always need to batch redis. This is
// standard."). A caller that wants its trips attributed labels its context
// with WithTripLabel; every trip made with that context, or one derived
// from it, is counted under the label as well as in N. The reconciler's
// loop labels each duty and prints trips=<n> on its DUTY line, and a
// functional test pins each duty's budget so a chain of dependent reads
// cannot creep back in.

type tripLabelKey struct{}

// WithTripLabel returns ctx labelled label.
func WithTripLabel(ctx context.Context, label string) context.Context {
	return context.WithValue(ctx, tripLabelKey{}, label)
}

// TripLabel is the label ctx carries, "" when none.
func TripLabel(ctx context.Context) string {
	s, _ := ctx.Value(tripLabelKey{}).(string)
	return s
}

// Of is the number of round trips counted under label so far.
func (t *Trips) Of(label string) int64 {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.labels[label]
}

// ByLabel is every label's count, a copy.
func (t *Trips) ByLabel() map[string]int64 {
	out := map[string]int64{}
	if t == nil {
		return out
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for k, v := range t.labels {
		out[k] = v
	}
	return out
}

func (t *Trips) count(ctx context.Context) {
	t.n.Add(1)
	if l := TripLabel(ctx); l != "" {
		t.mu.Lock()
		if t.labels == nil {
			t.labels = map[string]int64{}
		}
		t.labels[l]++
		t.mu.Unlock()
	}
}

// DialHook passes dials through; a dial is not a command round trip.
func (t *Trips) DialHook(next redis.DialHook) redis.DialHook { return next }

// ProcessHook counts one round trip per single command.
func (t *Trips) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if !handshake(cmd) {
			t.count(ctx)
		}
		return next(ctx, cmd)
	}
}

// ProcessPipelineHook counts one round trip per pipeline Exec.
func (t *Trips) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		for _, c := range cmds {
			if !handshake(c) {
				t.count(ctx)
				break
			}
		}
		return next(ctx, cmds)
	}
}

// handshake reports a command go-redis sends while it sets up a new
// connection (HELLO, AUTH, CLIENT SETINFO, SELECT, READONLY). Open sends
// nothing (#3277), so the first command a verb issues also dials, and the
// connection's setup runs through these hooks; like the dial, it is not one
// of the verb's round trips.
func handshake(c redis.Cmder) bool {
	switch strings.ToLower(c.Name()) {
	case "hello", "auth", "client", "select", "readonly":
		return true
	}
	return false
}
