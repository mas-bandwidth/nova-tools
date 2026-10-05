package bus

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
// dials is how many connections were made, which is how many times a command
// was sent; wrote is whether the command's bytes reached the store, so the
// stall is the command's and not a handshake's; read and write are the
// longest socket deadline the client set, measured from the moment it set
// it, which is what says whether the bound that fires is the one named.
type stalled struct {
	mu          sync.Mutex
	dials       int
	wrote       bool
	read, write time.Duration
}

// dial hands the client one pipe into the stalled store.
func (s *stalled) dial(context.Context, string, string) (net.Conn, error) {
	client, server := net.Pipe()
	s.mu.Lock()
	s.dials++
	s.mu.Unlock()
	go func() {
		_, _ = io.Copy(noted{s}, server) // the client's writes land; its reads wait
		_ = server.Close()
	}()
	return &deadlines{Conn: client, s: s}, nil
}

// noted is where the stalled store's reads go: nowhere, but noted.
type noted struct{ s *stalled }

func (n noted) Write(p []byte) (int, error) {
	n.s.mu.Lock()
	n.s.wrote = true
	n.s.mu.Unlock()
	return len(p), nil
}

// deadlines is the client's end of a pipe that notes each socket deadline
// the client sets on it.
type deadlines struct {
	net.Conn
	s *stalled
}

func (c *deadlines) SetReadDeadline(t time.Time) error {
	c.note(t, &c.s.read)
	return c.Conn.SetReadDeadline(t)
}

func (c *deadlines) SetWriteDeadline(t time.Time) error {
	c.note(t, &c.s.write)
	return c.Conn.SetWriteDeadline(t)
}

func (c *deadlines) note(t time.Time, longest *time.Duration) {
	if t.IsZero() {
		return
	}
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	if d := time.Until(t); d > *longest {
		*longest = d
	}
}

// sent is how many connections the store was dialed for.
func (s *stalled) sent() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dials
}

// longest is the longest read and write deadline the client set.
func (s *stalled) longest() (read, write time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read, s.write
}

// stalledBound is the one deadline every call of the test is named: the
// store's Timeout, and what the refusal must say.
const stalledBound = 200 * time.Millisecond

// connBound is the client's own read and write bound in the test, shorter than
// stalledBound by a long way: the shipped client's is 5 s under a --timeout
// that may be 30 s, and a call that left the bound alone would be cut by it.
const connBound = time.Millisecond

// stalledStore is the bus's Redis store over the stalled store: Timeout is
// stalledBound, the client's read and write bounds connBound, the margin a
// short one so a blocking wait costs no wall clock. RESP2, so the first
// thing the client sends is the command and not a handshake.
func stalledStore(t *testing.T, s *stalled) Redis {
	t.Helper()
	c := redis.NewClient(&redis.Options{
		Addr:                  "127.0.0.1:6381",
		Dialer:                s.dial,
		ReadTimeout:           connBound,
		WriteTimeout:          connBound,
		MaxRetries:            -1,
		ContextTimeoutEnabled: true,
		Protocol:              2,
		DisableIdentity:       true,
		MaintNotificationsConfig: &maintnotifications.Config{
			Mode:         maintnotifications.ModeDisabled,
			EndpointType: maintnotifications.EndpointTypeNone,
		},
	})
	t.Cleanup(func() { _ = c.Close() })
	return Redis{C: c, Timeout: stalledBound, Margin: stalledBound / 2}
}

// refused is the one refusal, the deadline the call was named filled in.
func refused(d time.Duration) string {
	return "redis did not answer within " + d.String() + " at 127.0.0.1:6381: the host may be overloaded (load average), try again"
}

// reaches asserts the call's bound was the one that fired: the command was
// written, and the socket deadline the client set was the call's and not the
// connection's own (connBound), which would have cut it first.
func reaches(t *testing.T, s *stalled, d time.Duration) {
	t.Helper()
	read, write := s.longest()
	s.mu.Lock()
	wrote := s.wrote
	s.mu.Unlock()
	assert.True(t, wrote, "the command reached the store: the stall is the command's")
	assert.Greater(t, read, d/2, "the read deadline is the call's %s, not the connection's %s", d, connBound)
	assert.Greater(t, write, d/2, "the write deadline is the call's %s, not the connection's %s", d, connBound)
}

// TestARedisCallThatStallsFailsWithinTheNamedTimeout pins the deadlines
// (SPEC-BUS.md, the deadlines): a store that accepts and then stalls is
// refused within the deadline the call was named, and that deadline is the
// socket's as well as the context's; the refusal is one line that says the
// host may be overloaded and to try again and names the address, never a
// login; a read that changes nothing is sent once more, and a write and a
// blocking read are sent at most once, so a stalled store can duplicate
// nothing.
func TestARedisCallThatStallsFailsWithinTheNamedTimeout(t *testing.T) {
	t.Parallel()

	t.Run("a read that answers at once is refused within the named timeout and sent once more", func(t *testing.T) {
		t.Parallel()
		var s stalled
		r := stalledStore(t, &s)
		_, _, err := r.Roster(context.Background()) // names' read, one pipeline
		require.Error(t, err)
		assert.EqualError(t, err, refused(stalledBound))
		assert.Equal(t, 2, s.sent(), "a read that changes nothing is sent twice: once, and once more")
		reaches(t, &s, stalledBound)
	})

	t.Run("a send is never sent twice", func(t *testing.T) {
		t.Parallel()
		var s stalled
		r := stalledStore(t, &s)
		err := r.AddAll(context.Background(), []string{LogKey}, map[string]string{"id": "01ARZ3NDEKTSV4RRFFQ69G5FAV"})
		require.Error(t, err)
		assert.EqualError(t, err, refused(stalledBound))
		assert.Equal(t, 1, s.sent(), "a write is sent at most once, so a stalled store duplicates nothing")
		reaches(t, &s, stalledBound)
	})

	t.Run("an ack is never sent twice", func(t *testing.T) {
		t.Parallel()
		var s stalled
		r := stalledStore(t, &s)
		_, err := r.Ack(context.Background(), StreamOf("ada"), "ada", "1-0")
		require.Error(t, err)
		assert.EqualError(t, err, refused(stalledBound))
		assert.Equal(t, 1, s.sent())
	})

	t.Run("a blocking read is bounded by its block plus the margin, and sent at most once", func(t *testing.T) {
		t.Parallel()
		var s stalled
		r := stalledStore(t, &s)
		block := stalledBound / 2 // plus the margin of stalledBound/2: the call's deadline is stalledBound
		_, err := r.Read(context.Background(), StreamOf("ada"), "ada", Consumer, block, 1)
		require.Error(t, err)
		assert.EqualError(t, err, refused(block+r.margin()), "the deadline the call names is the block plus the margin")
		assert.Equal(t, 1, s.sent(), "a blocking read delivers, so it is sent at most once")
		reaches(t, &s, stalledBound)
	})

	t.Run("the connection's own bounds are put back after the call", func(t *testing.T) {
		t.Parallel()
		var s stalled
		r := stalledStore(t, &s)
		_, _, _ = r.Roster(context.Background())
		o := r.C.Options()
		assert.Equal(t, connBound, o.ReadTimeout)
		assert.Equal(t, connBound, o.WriteTimeout)
	})
}
