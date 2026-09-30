package redisconn

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/redis/go-redis/v9"
)

// Trips counts the round trips a client makes to the store from the moment
// it is attached, in two parts. N is the caller's: each single command is
// one, and each pipeline or transaction is one however many commands it
// carries; a command that fails is counted, because it was sent. Setup is
// the connections': each command of a handshake (HELLO, which carries the
// login, and AUTH or the probe when a store older than HELLO needs them)
// that go-redis runs inside the first command to use a new connection. The
// dial is not a round trip of the store's and is not counted. Total is the
// two together: what the fleet pays on the wire.
//
// A tool prints String on its receipt, trips=<Total> setup=<Setup>, so the
// number on the line is what the verb cost, and a test pins it, so that a
// change which turns one batch into a chain of reads shows as a number.
// "Always batch, one round trip per verb" is Total minus Setup: one; and
// Setup is one for a verb on a connection Open made (Conn.Trips), its
// handshake, HELLO alone. Every method is safe to call from many
// goroutines, and on a nil *Trips, which has counted nothing.
type Trips struct {
	n      atomic.Int64
	setup  atomic.Int64
	mu     sync.Mutex
	labels map[string]int64
}

// CountTrips attaches a new counter to the client and returns it. The count
// starts at zero and covers every goroutine that uses the client; the span
// of work a caller asks about is the difference of two readings of N (or of
// Of, for one label, or of Total). More than one counter may be attached to
// a client; each counts on its own. The handshake of the connection Open
// made was before any counter a caller attaches: Conn.Trips is the counter
// that holds it.
func CountTrips(client *redis.Client) *Trips {
	t := &Trips{}
	client.AddHook(t)
	return t
}

// N is the number of the caller's round trips counted so far.
func (t *Trips) N() int64 {
	if t == nil {
		return 0
	}
	return t.n.Load()
}

// Setup is the number of round trips of the connections' handshakes counted
// so far.
func (t *Trips) Setup() int64 {
	if t == nil {
		return 0
	}
	return t.setup.Load()
}

// Total is N and Setup together: every round trip counted so far.
func (t *Trips) Total() int64 { return t.N() + t.Setup() }

// String is the field a receipt prints: trips=<Total> setup=<Setup>.
func (t *Trips) String() string {
	return "trips=" + strconv.FormatInt(t.Total(), 10) + " setup=" + strconv.FormatInt(t.Setup(), 10)
}

type tripLabelKey struct{}

// WithTripLabel returns a context that carries label. Every round trip made
// with that context, or with one derived from it, is counted under the label
// as well as in N, so one counter can say which part of the work made which
// trips. An empty label is no label.
func WithTripLabel(ctx context.Context, label string) context.Context {
	return context.WithValue(ctx, tripLabelKey{}, label)
}

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
	for label, n := range t.labels {
		out[label] = n
	}
	return out
}

// insideTripKey marks the context of a trip that is under way, for the one
// counter it names. go-redis sets a new connection up inside the command that
// needs it, with that command's context, and runs the setup's own commands
// through the same hooks; so a command that arrives carrying the mark is a
// connection's setup, whatever its name, counted in Setup, and a command the
// caller sends is counted in N, whatever its name.
type insideTripKey struct{ t *Trips }

// enter counts one trip under ctx's label and returns the context marked as
// inside it. A context that is inside a trip of this counter already is a
// connection's setup: it is counted in Setup and returned as it is.
func (t *Trips) enter(ctx context.Context) context.Context {
	if ctx.Value(insideTripKey{t}) != nil {
		t.setup.Add(1)
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
// trip for a command the caller sent, in N, and one for each command of a
// connection's setup, in Setup.
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
