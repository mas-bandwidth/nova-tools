package bus

import (
	"bytes"
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stalled is a store that answers the connection handshake and then never
// answers a command: every read after the handshake returns a timeout at once.
// The test drives the stall, never the wall clock, so the deadline it asserts
// is the one the code names and not the runner's load (SPEC-BUS.md, the
// deadlines). words counts the commands named by word that were written to it.
type stalled struct {
	dials atomic.Int32
	words map[string]*atomic.Int32
}

// stalledTimeout is the store's answer to a command it never reads: a timeout,
// the shape a real store's read deadline takes.
type stalledTimeout struct{}

func (stalledTimeout) Error() string   { return "i/o timeout" }
func (stalledTimeout) Timeout() bool   { return true }
func (stalledTimeout) Temporary() bool { return true }

// stalledAddr is the stalled store's address; nothing is dialled at it.
type stalledAddr struct{}

func (stalledAddr) Network() string { return "stalled" }
func (stalledAddr) String() string  { return "bus.test:6379" }

// stalledConn is the client's half of the stalled store. Write counts the
// commands as the client writes them, before the write returns, so a test
// reads the count without waiting on the store's side. The first Read answers
// the handshake (go-redis falls back to RESP2 on the refusal); every read
// after it is the stall, and it never touches the wall clock, which is what
// makes the retry count deterministic.
type stalledConn struct {
	s         *stalled
	handshake bool
}

func (c *stalledConn) Write(p []byte) (int, error) {
	lower := bytes.ToLower(p) // go-redis writes the command names in lower case
	for w, k := range c.s.words {
		k.Add(int32(bytes.Count(lower, bytes.ToLower([]byte(w)))))
	}
	return len(p), nil
}

func (c *stalledConn) Read(p []byte) (int, error) {
	if !c.handshake {
		c.handshake = true
		return copy(p, "-ERR unknown command 'hello'\r\n"), nil
	}
	return 0, stalledTimeout{}
}

func (c *stalledConn) Close() error                     { return nil }
func (c *stalledConn) LocalAddr() net.Addr              { return stalledAddr{} }
func (c *stalledConn) RemoteAddr() net.Addr             { return stalledAddr{} }
func (c *stalledConn) SetDeadline(time.Time) error      { return nil }
func (c *stalledConn) SetReadDeadline(time.Time) error  { return nil }
func (c *stalledConn) SetWriteDeadline(time.Time) error { return nil }

func newStalled(t *testing.T, words ...string) (*stalled, *redis.Client) {
	t.Helper()
	s := &stalled{words: map[string]*atomic.Int32{}}
	for _, w := range words {
		s.words[w] = new(atomic.Int32)
	}
	c := redis.NewClient(&redis.Options{
		Addr:     "bus.test:6379",
		Protocol: 2,
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			s.dials.Add(1)
			return &stalledConn{s: s}, nil
		},
		ContextTimeoutEnabled: true,
		MaxRetries:            -1,
		DisableIdentity:       true,
	})
	t.Cleanup(func() { _ = c.Close() }) // ignored: the test is done with the client
	return s, c
}

func TestARedisCallThatStallsFailsWithinTheNamedTimeout(t *testing.T) {
	t.Parallel()
	s, c := newStalled(t, "SMEMBERS", "XREADGROUP")
	r := Redis{C: c, Timeout: 20 * time.Millisecond, Margin: 20 * time.Millisecond}

	_, _, err := r.Roster(context.Background())
	var te *TimeoutError
	require.ErrorAs(t, err, &te)
	assert.Equal(t, "redis did not answer within 20ms at bus.test:6379: the host may be overloaded (load average), try again", err.Error())
	assert.EqualValues(t, 4, s.words["SMEMBERS"].Load(), "a read that changes nothing is tried once more, and once only: two SMEMBERS in the pipeline, sent twice")

	_, err = r.Read(context.Background(), "s", "g", "c", 10*time.Millisecond, 1)
	require.ErrorAs(t, err, &te)
	assert.Equal(t, 50*time.Millisecond, te.After, "a blocking read waits its block and the margin more")
	assert.EqualValues(t, 1, s.words["XREADGROUP"].Load(), "a read that hands out entries is never tried again")
}

func TestASendIsNotRetriedWhenTheStoreStalls(t *testing.T) {
	t.Parallel()
	s, c := newStalled(t, "EVALSHA", "XACK")
	r := Redis{C: c, Timeout: 20 * time.Millisecond}

	err := r.AddAll(context.Background(), []string{"bus2:bob"}, map[string]string{"k": "v"})
	var te *TimeoutError
	require.ErrorAs(t, err, &te)
	assert.EqualValues(t, 1, s.words["EVALSHA"].Load())

	_, err = r.Ack(context.Background(), "bus2:bob", "g", "1-0")
	require.ErrorAs(t, err, &te)
	assert.EqualValues(t, 1, s.words["XACK"].Load())
}

func TestACancelledCallIsNotATimeoutAndIsNotRetried(t *testing.T) {
	t.Parallel()
	s, c := newStalled(t, "SMEMBERS")
	r := Redis{C: c, Timeout: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := r.Roster(ctx)
	require.Error(t, err)
	var te *TimeoutError
	assert.False(t, errors.As(err, &te))
	assert.LessOrEqual(t, s.words["SMEMBERS"].Load(), int32(1))
}
