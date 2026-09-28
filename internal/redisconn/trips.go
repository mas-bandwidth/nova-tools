package redisconn

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/redis/go-redis/v9"
)

// Trips counts the round trips a client makes to the store from the moment
// it is attached, in two parts. N is the caller's: each single command is
// one, and each pipeline or transaction is one however many commands it
// carries; a command that fails is counted, because it was sent. A call
// that returns before any byte of it is written is not counted, a cancelled
// command and a command on a closed client among them: the fleet did not
// pay it. Setup is the connections': each command of a handshake (HELLO,
// which carries the login, and AUTH or the probe when a store older than
// HELLO needs them) that go-redis runs inside the first command to use a
// new connection. The dial is not a round trip of the store's and is not
// counted. Total is the two together: what the fleet pays on the wire.
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
// counter it names. The value is the *tripCall of that trip. Open's probe
// sets it to true before the hook runs: the probe is setup, and the hook
// puts the *tripCall in its place. go-redis sets a new connection up inside
// the command that needs it, with that command's context, and runs the
// setup's own commands through the same hooks; so a command that arrives
// carrying the mark is a connection's setup, whatever its name, counted in
// Setup when it is written, and a command the caller sends is counted in N,
// whatever its name, when it is written.
type insideTripKey struct{ t *Trips }

// tripCall is one caller command, or Open's probe. A setup command runs
// inside it and leaves its error here when it fails. That error is the
// caller's too when the caller's own bytes were never written. A setup
// command that succeeds clears it: go-redis goes on, and the caller command
// can still be written. authFail is the same mark when the refusal was a
// login and the hider has cut the error chain that would have carried it.
type tripCall struct {
	mu       sync.Mutex
	blocking error
	authFail bool
}

// begin marks ctx as inside this counter's trip. inside reports that this
// command is setup, not the caller's. owner reports that this command is
// the one the tripCall was opened for: the caller's, or Open's probe. A
// setup command that runs inside it is not the owner, so its own result is
// not mistaken for the setup error it is recording. begin does not count.
// The count is after the command returns, and only when it was written.
func (t *Trips) begin(ctx context.Context) (context.Context, *tripCall, bool, bool) {
	switch v := ctx.Value(insideTripKey{t}).(type) {
	case *tripCall:
		return ctx, v, true, false
	case bool:
		k := &tripCall{}
		return context.WithValue(ctx, insideTripKey{t}, k), k, true, true
	default:
		k := &tripCall{}
		return context.WithValue(ctx, insideTripKey{t}, k), k, false, true
	}
}

// nested records the error a setup command returned, or that the setup went
// on when err is nil. The caller's command has not run yet.
func (k *tripCall) nested(err error) {
	if k == nil {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if err == nil {
		k.blocking = nil
		k.authFail = false
		return
	}
	k.blocking = err
	k.authFail = Classify(err) == AuthRefused
}

// blocks reports whether err is the setup command that failed last, so the
// command this call is counting was not written. A login the hider withheld
// no longer unwraps to that error; the class is what still says so.
func (k *tripCall) blocks(err error) bool {
	if k == nil || err == nil {
		return false
	}
	k.mu.Lock()
	blocking, auth := k.blocking, k.authFail
	k.mu.Unlock()
	if blocking == nil {
		return false
	}
	if errors.Is(err, blocking) {
		return true
	}
	var made *failure
	return errors.As(err, &made) && made.class == AuthRefused && auth
}

// sent reports whether this command was written. nil was, and so was a
// refusal the store wrote and a transport error that is not one of the
// returns go-redis v9.22.0 makes before WithWriter (a cancelled context, a
// closed client, a wait for a connection that ran out, a failed dial). When
// owner is set, an error this call already recorded as setup was not this
// command. A setup command does not consult that: it is the record, and its
// own bytes are its own.
func (k *tripCall) sent(err error, owner bool) bool {
	if err == nil {
		return true
	}
	if (owner && k.blocks(err)) || beforeWrite(err) {
		return false
	}
	var made *failure
	if errors.As(err, &made) {
		// Unconfirmed: the write may have gone out. AuthRefused: the store
		// answered this command. Unreachable: nothing of it was written.
		return made.class == Unconfirmed || made.class == AuthRefused
	}
	var reply redis.Error
	if errors.As(err, &reply) {
		return true
	}
	return afterWrite(err)
}

// beforeWrite reports whether err is one go-redis returns without writing
// the command: the context had ended (pool.waitTurn, queuedNewConn), the
// client or the pool was closed, no connection was free, or the dial failed.
func beforeWrite(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, redis.ErrClosed) || errors.Is(err, redis.ErrPoolTimeout) ||
		errors.Is(err, redis.ErrPoolExhausted) {
		return true
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return true
	}
	var dns *net.DNSError
	return errors.As(err, &dns)
}

// afterWrite reports whether err is a transport failure of a command whose
// write already happened or may have: the read of the reply, or the write
// itself failing on the socket. A dial's error is beforeWrite, not this.
func afterWrite(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.ErrClosedPipe)
}

// add counts one written command: the caller's, under its label, or a
// connection's setup.
func (t *Trips) add(ctx context.Context, inside bool) {
	if inside {
		t.setup.Add(1)
		return
	}
	t.n.Add(1)
	label := TripLabel(ctx)
	if label == "" {
		return
	}
	t.mu.Lock()
	if t.labels == nil {
		t.labels = map[string]int64{}
	}
	t.labels[label]++
	t.mu.Unlock()
}

// finish counts the command when it was written. A setup command that is not
// the owner records its error for the command that is still outside it.
func (t *Trips) finish(ctx context.Context, k *tripCall, inside, owner bool, err error) {
	if k.sent(err, owner) {
		t.add(ctx, inside)
	}
	if !owner {
		k.nested(err)
	}
}

// DialHook is the go-redis hook for a dial. A dial is not a round trip. A
// dial that fails is the caller command not being written, when the dial
// runs with that command's context.
func (t *Trips) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		nc, err := next(ctx, network, addr)
		if err != nil {
			if k, ok := ctx.Value(insideTripKey{t}).(*tripCall); ok {
				k.nested(err)
			}
		}
		return nc, err
	}
}

// ProcessHook is the go-redis hook for a single command. It counts one round
// trip for a command the caller sent, in N, and one for each command of a
// connection's setup, in Setup. The count is after the command returns, and
// only when it was written.
func (t *Trips) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		ctx, k, inside, owner := t.begin(ctx)
		err := next(ctx, cmd)
		t.finish(ctx, k, inside, owner, err)
		return err
	}
}

// ProcessPipelineHook is the go-redis hook for a pipeline and for a
// transaction. It counts one round trip for the whole of it, when it was
// written.
func (t *Trips) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		ctx, k, inside, owner := t.begin(ctx)
		err := next(ctx, cmds)
		t.finish(ctx, k, inside, owner, err)
		return err
	}
}
