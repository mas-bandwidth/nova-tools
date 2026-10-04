package bus2

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	maintnotifications "github.com/redis/go-redis/v9/maintnotifications"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stalled is a store that accepts every connection and then never answers: a
// host too overloaded to reply. Each dial makes one net.Pipe (no socket, no
// server), the server end reads what the client writes and writes nothing
// back, so a call waits its deadline out and the connection is dropped.
// dials is how many connections were made, which is how many times a
// command was sent: what the retry rule is read with.
type stalled struct {
	mu    sync.Mutex
	dials int
}

// dial hands the client one pipe into the stalled store.
func (s *stalled) dial(context.Context, string, string) (net.Conn, error) {
	client, server := net.Pipe()
	s.mu.Lock()
	s.dials++
	s.mu.Unlock()
	go func() {
		_, _ = io.Copy(io.Discard, server) // the client's writes land; its reads wait
		_ = server.Close()
	}()
	return client, nil
}

// sent is how many connections the store was dialed for.
func (s *stalled) sent() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dials
}

// stalledStore is the bus's Redis store over the stalled store, the
// connection's own deadlines short so the test waits no wall clock (the
// tool's client carries the same shape at 5 s, internal/redisconn): one
// attempt of the client's own, no identity, no maintenance asks.
func stalledStore(t *testing.T, s *stalled) Redis {
	t.Helper()
	c := redis.NewClient(&redis.Options{
		Addr:                  "127.0.0.1:6381",
		Dialer:                s.dial,
		ReadTimeout:           50 * time.Millisecond,
		WriteTimeout:          50 * time.Millisecond,
		MaxRetries:            -1,
		ContextTimeoutEnabled: true,
		Protocol:              3,
		DisableIdentity:       true,
		MaintNotificationsConfig: &maintnotifications.Config{
			Mode:         maintnotifications.ModeDisabled,
			EndpointType: maintnotifications.EndpointTypeNone,
		},
	})
	t.Cleanup(func() { _ = c.Close() })
	return Redis{C: c}
}

// TestARedisCallThatStallsFailsWithinTheNamedTimeout pins the deadlines
// (SPEC-BUS2.md, the deadlines): a store that accepts and then stalls is
// refused within the deadline the call was named, in one line that says the
// host may be overloaded and to try again and names the address, never a
// login; a read that changes nothing is sent once more, and a write and a
// blocking read are sent at most once, so a stalled store can duplicate
// nothing.
func TestARedisCallThatStallsFailsWithinTheNamedTimeout(t *testing.T) {
	t.Parallel()
	// the one refusal, the deadline each case names filled in.
	refused := func(d string) string {
		return "redis did not answer within " + d + " at 127.0.0.1:6381: the host may be overloaded (load average), try again"
	}

	t.Run("a read that answers at once is refused within the named timeout and sent once more", func(t *testing.T) {
		t.Parallel()
		var s stalled
		r := stalledStore(t, &s)
		_, _, err := r.Roster(context.Background()) // names' read, one pipeline
		require.Error(t, err)
		assert.EqualError(t, err, refused("5s"))
		assert.Equal(t, 2, s.sent(), "a read that changes nothing is sent twice: once, and once more")
	})

	t.Run("a send is never sent twice", func(t *testing.T) {
		t.Parallel()
		var s stalled
		r := stalledStore(t, &s)
		err := r.AddAll(context.Background(), []string{LogKey}, map[string]string{"id": "01ARZ3NDEKTSV4RRFFQ69G5FAV"})
		require.Error(t, err)
		assert.EqualError(t, err, refused("5s"))
		assert.Equal(t, 1, s.sent(), "a write is sent at most once, so a stalled store duplicates nothing")
	})

	t.Run("a blocking read is bounded by its block plus the margin, and sent at most once", func(t *testing.T) {
		t.Parallel()
		var s stalled
		r := stalledStore(t, &s)
		bound := 400 * time.Millisecond // a variable, the caller's own context, shorter than the call's deadline
		ctx, cancel := context.WithTimeout(context.Background(), bound)
		defer cancel()
		_, err := r.Read(ctx, StreamOf("ada"), "ada", Consumer, 2*time.Second, 1)
		require.Error(t, err)
		assert.EqualError(t, err, refused("12s"), "the deadline the call names is the block (2s) plus the margin (10s)")
		assert.Equal(t, 1, s.sent(), "a blocking read delivers, so it is sent at most once")
	})
}
