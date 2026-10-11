package main

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/redisconn"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countTrip runs one counted round trip through the counter's own hook, the
// way a client's command would count one, so a window is tested against a
// counter that has really counted, with no store.
func countTrip(t *testing.T, counter *redisconn.Trips) {
	t.Helper()
	next := counter.ProcessHook(func(_ context.Context, _ redis.Cmder) error { return nil })
	require.NoError(t, next(context.Background(), redis.NewCmd(context.Background())))
}

// TestSessionCoverDialHookPassesThrough: the session's dial hook is the
// transparent one: the client's dial goes to the next hook unchanged.
func TestSessionCoverDialHookPassesThrough(t *testing.T) {
	t.Parallel()

	dialed := ""
	sentinel := redis.DialHook(func(_ context.Context, _, addr string) (net.Conn, error) {
		dialed = addr
		return nil, nil
	})
	got := (&connection{}).DialHook(sentinel)
	require.NotNil(t, got, "the client keeps a dial hook")
	_, err := got(context.Background(), "tcp", "example.test:6379")
	require.NoError(t, err)
	assert.Equal(t, "example.test:6379", dialed, "the dial went to the next hook unchanged")
}

// TestSessionCoverProcessHook: a hook wraps one command. The answer and the
// store's own refusal pass through untouched and leave the connection
// reusable; a lost reply — a closed client, a refused dial — marks the
// connection broken, so the next line that needs the store reopens it once.
func TestSessionCoverProcessHook(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		next       error
		wantBroken bool
	}{
		{name: "an answer leaves the connection whole", next: nil, wantBroken: false},
		{name: "the store's own refusal does not break it", next: errors.New("ERR the table's epoch moved"), wantBroken: false},
		{name: "a closed client breaks it", next: redis.ErrClosed, wantBroken: true},
		{name: "a refused dial breaks it", next: errors.New("dial tcp: connection refused"), wantBroken: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := &connection{}
			var seen redis.Cmder
			hook := c.ProcessHook(func(_ context.Context, cmd redis.Cmder) error {
				seen = cmd
				return tc.next
			})
			cmd := redis.NewCmd(context.Background())
			err := hook(context.Background(), cmd)
			assert.Same(t, cmd, seen, "the command goes to the next hook unchanged")
			if tc.next == nil {
				require.NoError(t, err, "the answer passes through")
			} else {
				require.ErrorIs(t, err, tc.next, "the command's error passes through")
			}
			assert.Equal(t, tc.wantBroken, c.broken.Load(), "broken")
		})
	}
}

// TestSessionCoverPrepare: prepare opens nothing while the connection is
// whole — none yet, or one present and unbroken — and a reopen that fails is
// the refusal, leaving the connection as it was. Replacing a broken
// connection is the functional tier's work: only redisconn.Open, over a live
// store, builds the dialed conn the swap hands out.
func TestSessionCoverPrepare(t *testing.T) {
	t.Parallel()

	t.Run("no reopen, nothing to do", func(t *testing.T) {
		t.Parallel()

		assert.NoError(t, (&connection{}).prepare())
	})

	t.Run("an unbroken connection is kept", func(t *testing.T) {
		t.Parallel()

		conn := &redisconn.Conn{}
		c := &connection{Conn: conn, reopen: func() (*redisconn.Conn, error) {
			return nil, errors.New("a whole connection is never reopened")
		}}
		require.NoError(t, c.prepare())
		assert.Same(t, conn, c.Conn, "the connection is the one it came with")
	})

	t.Run("a reopen that fails is refused", func(t *testing.T) {
		t.Parallel()

		want := errors.New("the store is gone")
		c := &connection{reopen: func() (*redisconn.Conn, error) { return nil, want }}
		c.broken.Store(true)
		err := c.prepare()
		require.ErrorIs(t, err, want, "reopen's error is the refusal")
		assert.Nil(t, c.Conn, "a failed reopen replaces nothing")
	})
}

// TestSessionCoverClose: a shell's connection is closed by its owner, never
// by a session that borrowed it; a verb's own connection is closed by the
// verb, and a conn that is already gone closes nil.
func TestSessionCoverClose(t *testing.T) {
	t.Parallel()

	t.Run("a shared connection is not closed", func(t *testing.T) {
		t.Parallel()

		assert.NoError(t, (&connection{shared: true}).Close())
	})

	t.Run("a verb's own connection closes its conn", func(t *testing.T) {
		t.Parallel()

		assert.NoError(t, (&connection{}).Close(), "a conn that is already gone closes nil")
	})
}

// TestSessionCoverCountTripsReusesOneCounter: a session attaches one trip
// counter and every window reads the same one; a window opened after two
// trips counts only the trips that come after it.
func TestSessionCoverCountTripsReusesOneCounter(t *testing.T) {
	t.Parallel()

	counter := &redisconn.Trips{}
	countTrip(t, counter)
	countTrip(t, counter)
	c := &connection{counter: counter}

	window := c.CountTrips()
	assert.Same(t, counter, window.counter, "one counter for the whole session")
	assert.Zero(t, window.N(), "a window just opened counts no trips of its own")

	countTrip(t, counter)
	assert.Equal(t, int64(1), window.N(), "one more trip is one more than the window opened with")

	again := c.CountTrips()
	assert.Same(t, counter, again.counter, "no second counter is attached")
	assert.Zero(t, again.N(), "a fresh window starts where the counter stands")
}

// TestSessionCoverOpenShellStoreRefusesAnEmptyAddress: the shell's one
// connection is opened the one way, and a missing address is the usage
// refusal, named before anything is dialed. The dial itself is the
// functional tier's: only redisconn.Open, over a live store, returns a conn.
func TestSessionCoverOpenShellStoreRefusesAnEmptyAddress(t *testing.T) {
	t.Parallel()

	_, err := openShellStore("", func(string) string { return "" })
	require.Error(t, err)
	assert.ErrorContains(t, err, "--redis <addr> is required")
}
