package testredis

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/redis/go-redis/v9"
)

// How much of a round trip a failure names. A pipeline of ten thousand
// commands is named by its first few, and a function that made a thousand
// round trips by the first few of them: the count is the evidence, the names
// are the clue.
const (
	tripNamesShown = 6
	tripsShown     = 12
)

// Counter counts the round trips a go-redis client makes: the times it sends
// something to the server and waits for the answer. RoundTrips makes one.
//
// A round trip is one command, one pipeline or one transaction. A pipeline of
// a hundred commands is one, and so is a transaction: go-redis hands MULTI, the
// commands and EXEC to a hook as one batch and sends them together. A pipeline
// with no commands sends nothing and is none. The setup of a connection is not
// a round trip the caller made and is not counted (see RoundTrips).
//
// A Counter is safe for use by many goroutines.
type Counter struct {
	mu    sync.Mutex
	trips []string // one entry per round trip: what was sent, by command name only
}

// RoundTrips returns a Counter and the hook that feeds it. The test adds the
// hook to the client it wants counted:
//
//	trips, hook := testredis.RoundTrips(t)
//	client.AddHook(hook)
//	trips.Expect(t, 1, func() { verb(ctx, client) })
//
// This is the question "did my verb batch": how many times did the client
// send something and wait, however many commands that carried. CommandCounter
// asks another: how many commands reached a listener, every one of them, a
// connection's handshake included.
//
// WHAT IS COUNTED is every command, pipeline and transaction the caller sends
// the client, once each, when it comes back from the connection, whether or
// not it succeeded. A failure names them in that order, which on one
// connection is the order they were sent. A command go-redis sends again after
// a network error is one round trip here: the retry happens below the hooks.
//
// SETUP IS NOT COUNTED, BY WHO SENT IT AND NEVER BY NAME. go-redis connects
// inside the first command that needs a connection, with that command's
// context, and runs the handshake (on go-redis v9 HELLO, then CLIENT SETINFO
// in a pipeline) through the hooks; every new connection of a pool does it
// again. The hook marks the context of each round trip it counts, so a command
// that arrives carrying the mark is a connection's setup and is not counted,
// whatever its name, and a command the caller sends is counted, whatever its
// name: the caller's own HELLO or CLIENT SETNAME is one round trip. So a cold
// client's first PING is one round trip, and the count of work done by many
// goroutines does not depend on how many connections the pool opens to do it.
// This is the definition internal/redisconn.Trips gives a receipt.
//
// ADD IT LAST. go-redis runs the hook added first outermost and the hook added
// last nearest the connection. A hook added before this one that sends a
// pipeline again (a library loader that retries) makes two round trips, and
// this one, added last, counts two. Added first, it would count one: the
// second sending would happen below it, and a command a hook below it sends
// with the context it was given would carry the mark and be taken for setup.
//
// Only the names of commands are written down, never their arguments, so a
// failure message carries no key, value or password.
//
// RoundTrips panics outside a test binary, as the rest of the package does.
func RoundTrips(t testing.TB) (*Counter, redis.Hook) {
	t.Helper()
	return real.roundTrips(t)
}

func (l launch) roundTrips(t testing.TB) (*Counter, redis.Hook) {
	t.Helper()
	l.refuse()
	c := &Counter{}
	return c, roundTripHook{c}
}

// Expect runs do and fails the test unless the client made exactly n round
// trips while it ran. The failure says how many were expected and how many
// were counted, and names the ones counted, so a test that expects one round
// trip and counted a handshake and a verb reads it at once. Only the round
// trips that happen while do runs are counted: those before Expect and those
// after it are not. Round trips made by other goroutines while do runs are
// counted as well.
//
// A failure ends the test with t.Fatalf. If do itself fails the test or
// panics, that is the failure and the count is not read.
func (c *Counter) Expect(t testing.TB, n int, do func()) {
	t.Helper()
	if n < 0 {
		t.Fatalf("testredis: Expect was asked for %d round trips; a number of round trips is not negative", n)
	}
	if do == nil {
		t.Fatal("testredis: Expect was given no function to count the round trips of")
	}
	before := c.count()
	do()
	seen := c.since(before)
	if len(seen) == n {
		return
	}
	t.Fatalf("testredis: expected %s, counted %s%s", countOf(n), countOf(len(seen)), tripList(seen))
}

// count is the number of round trips so far.
func (c *Counter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.trips)
}

// since is the round trips after the first n, in the order they were made.
func (c *Counter) since(n int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.trips[n:]...)
}

// add writes down one round trip.
func (c *Counter) add(trip string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.trips = append(c.trips, trip)
}

// countOf is a number of round trips as a person says it.
func countOf(n int) string {
	if n == 1 {
		return "1 round trip"
	}
	return fmt.Sprintf("%d round trips", n)
}

// tripList is the round trips of a failure: a colon and the first of them, or
// nothing when there were none.
func tripList(trips []string) string {
	if len(trips) == 0 {
		return ""
	}
	shown := trips[:min(len(trips), tripsShown)]
	out := ": " + strings.Join(shown, "; ")
	if more := len(trips) - len(shown); more > 0 {
		out += fmt.Sprintf("; and %d more", more)
	}
	return out
}

// insideTripKey marks the context of a round trip that is under way, for the
// one Counter it names. go-redis sets a new connection up inside the command
// that needs it, with that command's context, and runs the setup's own
// commands through the same hooks; so a command that arrives carrying the mark
// is setup, whatever its name.
type insideTripKey struct{ c *Counter }

// enter reads the context of a command or a pipeline that has reached the hook.
// It returns the context to pass on and whether this is a round trip the
// caller made. A caller's is marked as under way, so that what go-redis sends
// inside it arrives marked and is not counted; one that is marked already is
// setup and is passed on as it is.
func (c *Counter) enter(ctx context.Context) (inside context.Context, callers bool) {
	if ctx.Value(insideTripKey{c}) != nil {
		return ctx, false
	}
	return context.WithValue(ctx, insideTripKey{c}, true), true
}

// roundTripHook is the hook of one Counter.
type roundTripHook struct{ c *Counter }

// DialHook passes a dial on: opening a connection is not a round trip. The
// handshake that follows it is setup, and is not counted either.
func (h roundTripHook) DialHook(next redis.DialHook) redis.DialHook { return next }

// ProcessHook counts one round trip for one command the caller sent, and none
// for a command that is a connection's setup.
func (h roundTripHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		inside, callers := h.c.enter(ctx)
		err := next(inside, cmd)
		if callers {
			h.c.add(cmd.Name())
		}
		return err
	}
}

// ProcessPipelineHook counts one round trip for a pipeline or a transaction
// the caller sent, whatever it holds, and none for a pipeline that is a
// connection's setup. go-redis hands a transaction to this hook as the batch
// MULTI, the commands, EXEC.
func (h roundTripHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		inside, callers := h.c.enter(ctx)
		err := next(inside, cmds)
		if callers && len(cmds) > 0 {
			h.c.add(batchName(cmds))
		}
		return err
	}
}

// batchName names a pipeline or a transaction by the command names it holds,
// the first tripNamesShown of them.
func batchName(cmds []redis.Cmder) string {
	kind := "pipeline"
	if n := len(cmds); n >= 2 && cmds[0].Name() == "multi" && cmds[n-1].Name() == "exec" {
		kind, cmds = "transaction", cmds[1:n-1]
	}
	shown := cmds[:min(len(cmds), tripNamesShown)]
	names := make([]string, len(shown))
	for i, cmd := range shown {
		names[i] = cmd.Name()
	}
	list := strings.Join(names, ", ")
	if more := len(cmds) - len(shown); more > 0 {
		list += fmt.Sprintf(", and %d more", more)
	}
	return kind + "(" + list + ")"
}
