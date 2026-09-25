package store

import (
	"context"
	"strings"
	"sync/atomic"

	"github.com/redis/go-redis/v9"
)

// Trips counts the network round trips a Store's client makes from the moment
// CountTrips attaches it: each single command is one, each pipeline Exec is
// one however many commands it carries. Verbs print the count on their
// receipt (`trips=<n>`, #3261 and #3265) so a regression to serial reads
// shows in the output, not only in a test.
type Trips struct{ n atomic.Int64 }

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

// DialHook passes dials through; a dial is not a command round trip.
func (t *Trips) DialHook(next redis.DialHook) redis.DialHook { return next }

// ProcessHook counts one round trip per single command.
func (t *Trips) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if !handshake(cmd) {
			t.n.Add(1)
		}
		return next(ctx, cmd)
	}
}

// ProcessPipelineHook counts one round trip per pipeline Exec.
func (t *Trips) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		for _, c := range cmds {
			if !handshake(c) {
				t.n.Add(1)
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
