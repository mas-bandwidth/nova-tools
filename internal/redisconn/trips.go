package redisconn

import (
	"context"
	"maps"
	"sync"
	"sync/atomic"

	"github.com/redis/go-redis/v9"
)

// Trips counts the round trips a client makes to the store from the moment
// CountTrips attaches it, and this is what it counts: each single command is
// one, and each pipeline or transaction is one however many commands it
// carries. A connection's own setup is not counted: the dial, and the
// handshake (HELLO, which carries the login) that go-redis runs inside the
// first command to use a new connection. A command that fails is counted,
// because it was sent.
//
// A tool prints the count on its receipt, or a test pins it, so that a
// change which turns one batch into a chain of reads shows as a number.
// Every method is safe to call from many goroutines, and on a nil *Trips,
// which has counted nothing.
type Trips struct {
	n      atomic.Int64
	mu     sync.Mutex
	labels map[string]int64
}

// CountTrips attaches a new counter to the client and returns it. The count
// starts at zero and covers every goroutine that uses the client; the span
// of work a caller asks about is the difference of two readings of N (or of
// Of, for one label). More than one counter may be attached to a client;
// each counts on its own.
func CountTrips(client *redis.Client) *Trips {
	t := &Trips{}
	client.AddHook(t)
	return t
}

// N is the number of round trips counted so far.
func (t *Trips) N() int64 {
	if t == nil {
		return 0
	}
	return t.n.Load()
}

type tripLabelKey struct{}

// TripLabel is the label ctx carries, "" when it carries none.
func TripLabel(ctx context.Context) string {
	label, _ := ctx.Value(tripLabelKey{}).(string)
	return label
}

// Of is the number of round trips counted under label so far; zero for a
// label nothing was counted under.
func (t *Trips) Of(label string) int64 {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.labels[label]
}

// ByLabel is every label's count, in a map of the caller's own: changing it
// changes nothing here. Trips made under no label are in N only.
func (t *Trips) ByLabel() map[string]int64 {
	out := map[string]int64{}
	if t == nil {
		return out
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	maps.Copy(out, t.labels)
	return out
}

// insideTripKey marks the context of a trip that is under way, for the one
// counter it names. go-redis sets a new connection up inside the command that
// needs it, with that command's context, and runs the setup's own commands
// through the same hooks; so a command that arrives carrying the mark is a
// connection's setup, whatever its name, and a command the caller sends is
// counted, whatever its name.
type insideTripKey struct{ t *Trips }

// enter counts one trip under ctx's label and returns the context marked as
// inside it. A context that is inside a trip of this counter already is
// returned as it is, and nothing is counted.
func (t *Trips) enter(ctx context.Context) context.Context {
	if ctx.Value(insideTripKey{t}) != nil {
		return ctx
	}
	t.n.Add(1)
	if label := TripLabel(ctx); label != "" {
		t.mu.Lock()
		if t.labels == nil {
			t.labels = map[string]int64{}
		}
		t.labels[label]++
		t.mu.Unlock()
	}
	return context.WithValue(ctx, insideTripKey{t}, true)
}

// DialHook is the go-redis hook for a dial. It passes the dial through: a
// dial is not a round trip.
func (t *Trips) DialHook(next redis.DialHook) redis.DialHook { return next }

// ProcessHook is the go-redis hook for a single command. It counts one round
// trip for a command the caller sent and none for a connection's setup.
func (t *Trips) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		return next(t.enter(ctx), cmd)
	}
}

// ProcessPipelineHook is the go-redis hook for a pipeline and for a
// transaction. It counts one round trip for the whole of it.
func (t *Trips) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		return next(t.enter(ctx), cmds)
	}
}
