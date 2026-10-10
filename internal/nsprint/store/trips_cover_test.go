package store

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/seatcred"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tripsStore opens a store whose client dials a fake store over net.Pipe: no
// socket, no clock, HELLO accepted, every verb command answered. It is the
// rig TestConnectIsHelloAlone runs on, so a round trip counts with no live
// store behind it.
func tripsStore(t *testing.T) *Store {
	t.Helper()
	s, err := openWith(context.Background(), "store.test:6379", &seatcred.Selection{}, func(o *redis.Options) {
		o.PoolSize, o.Dialer = 1, func(context.Context, string, string) (net.Conn, error) {
			client, server := net.Pipe()
			go func() {
				defer func() { _ = server.Close() }() // ignored: a test fixture's connection ends when its client hangs up
				r := bufio.NewReader(server)
				for {
					cmd, err := readCommand(r)
					if err != nil {
						return
					}
					reply := "+OK\r\n"
					switch strings.ToUpper(cmd[0]) {
					case "HELLO":
						reply = helloAccepted
					case "GET":
						reply = "_\r\n"
					}
					if _, err := io.WriteString(server, reply); err != nil {
						return
					}
				}
			}()
			return client, nil
		}
	})
	require.NoError(t, err, "open: %v", err)
	t.Cleanup(func() {
		// ignored: cleanup; the test's assertions are the report
		_ = s.Close()
	})
	return s
}

// TestTripsCoverCountTripsCountsFromAttach: CountTrips attaches a counter to
// the store's client, the connect itself (the dial and its HELLO) counts
// nothing, one command is one round trip and a pipeline Exec is one whatever
// it carries.
func TestTripsCoverCountTripsCountsFromAttach(t *testing.T) {
	t.Parallel()
	s := tripsStore(t)
	trips := s.CountTrips()
	require.NotNil(t, trips, "CountTrips returned no counter")
	assert.Equal(t, int64(0), trips.N(), "the connect's dial and HELLO are not round trips")
	ctx := context.Background()
	err := s.Client().Get(ctx, "k").Err()
	require.ErrorIs(t, err, redis.Nil, "get: %v", err)
	assert.Equal(t, int64(1), trips.N(), "one GET is one round trip")
	require.NoError(t, s.Client().Set(ctx, "k", "v", 0).Err(), "set: %v")
	assert.Equal(t, int64(2), trips.N(), "a second command is a second round trip")
	pipe := s.Client().Pipeline()
	pipe.Set(ctx, "k", "v2", 0)
	pipe.Set(ctx, "k", "v3", 0)
	_, err = pipe.Exec(ctx)
	require.NoError(t, err, "pipeline: %v", err)
	assert.Equal(t, int64(3), trips.N(), "a pipeline Exec is one round trip however many commands it carries")
}

// TestTripsCoverNCountsWhatWasCounted: N answers the count so far, and a nil
// counter has none.
func TestTripsCoverNCountsWhatWasCounted(t *testing.T) {
	t.Parallel()
	var nilTrips *Trips
	assert.Equal(t, int64(0), nilTrips.N(), "a nil counter counted nothing")
	trips := &Trips{}
	assert.Equal(t, int64(0), trips.N(), "a fresh counter counted nothing")
	trips.n.Add(3)
	assert.Equal(t, int64(3), trips.N(), "N answers what was counted")
}

// TestTripsCoverDialHookPassesTheDialThrough: a dial is not a command round
// trip, so DialHook hands the next dial hook through untouched.
func TestTripsCoverDialHookPassesTheDialThrough(t *testing.T) {
	t.Parallel()
	trips := &Trips{}
	client, server := net.Pipe()
	t.Cleanup(func() {
		// ignored: cleanup; the connection's use is the report
		_ = client.Close()
		_ = server.Close()
	})
	var network, addr string
	next := func(ctx context.Context, n, a string) (net.Conn, error) {
		network, addr = n, a
		return client, nil
	}
	conn, err := trips.DialHook(next)(context.Background(), "tcp", "store.test:6379")
	require.NoError(t, err, "dial: %v", err)
	assert.Equal(t, client, conn, "the dial hook passes the connection through")
	assert.Equal(t, "tcp", network, "the dial hook passes the network through")
	assert.Equal(t, "store.test:6379", addr, "the dial hook passes the address through")
	assert.Equal(t, int64(0), trips.N(), "a dial is not a round trip")
}

// TestTripsCoverProcessHookCountsCommandsNotHandshakes: one round trip per
// single command; the handshake commands a connection's setup sends (HELLO,
// AUTH, CLIENT, SELECT, READONLY) cost nothing, and next still runs and its
// error comes back.
func TestTripsCoverProcessHookCountsCommandsNotHandshakes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		args    []any
		wantAdd int64
	}{
		{name: "a read is one round trip", args: []any{"get", "k"}, wantAdd: 1},
		{name: "a write is one round trip", args: []any{"set", "k", "v"}, wantAdd: 1},
		{name: "HELLO is the handshake", args: []any{"hello"}, wantAdd: 0},
		{name: "AUTH is the handshake", args: []any{"auth", "pw"}, wantAdd: 0},
		{name: "CLIENT SETINFO is the handshake", args: []any{"client", "setinfo"}, wantAdd: 0},
		{name: "SELECT is the handshake", args: []any{"select", "0"}, wantAdd: 0},
		{name: "READONLY is the handshake", args: []any{"readonly"}, wantAdd: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			trips := &Trips{}
			nextErr := errors.New("next: refused")
			var passed redis.Cmder
			next := func(ctx context.Context, cmd redis.Cmder) error {
				passed = cmd
				return nextErr
			}
			cmd := redis.NewCmd(context.Background(), tc.args...)
			err := trips.ProcessHook(next)(context.Background(), cmd)
			assert.ErrorIs(t, err, nextErr, "the wrapped hook returns next's error")
			assert.Equal(t, cmd, passed, "next receives the command unchanged")
			assert.Equal(t, tc.wantAdd, trips.N(), "%s is worth %d round trips", cmd.Name(), tc.wantAdd)
		})
	}
}

// TestTripsCoverProcessPipelineHookCountsOneExec: one round trip per pipeline
// Exec however many commands it carries; a batch of handshakes and an empty
// batch cost nothing, and next receives the batch unchanged.
func TestTripsCoverProcessPipelineHookCountsOneExec(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		cmds    [][]any
		wantAdd int64
	}{
		{name: "two commands in one Exec are one round trip", cmds: [][]any{{"get", "a"}, {"get", "b"}}, wantAdd: 1},
		{name: "three commands still are one round trip", cmds: [][]any{{"set", "a", "1"}, {"set", "b", "2"}, {"get", "a"}}, wantAdd: 1},
		{name: "a batch of handshakes is not a round trip", cmds: [][]any{{"hello"}, {"select", "0"}}, wantAdd: 0},
		{name: "an empty batch is not a round trip", cmds: nil, wantAdd: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			trips := &Trips{}
			var passed []redis.Cmder
			next := func(ctx context.Context, cmds []redis.Cmder) error {
				passed = cmds
				return nil
			}
			var cmds []redis.Cmder
			for _, args := range tc.cmds {
				cmds = append(cmds, redis.NewCmd(context.Background(), args...))
			}
			require.NoError(t, trips.ProcessPipelineHook(next)(context.Background(), cmds), "the wrapped hook returns next's result")
			assert.Equal(t, cmds, passed, "next receives the batch unchanged")
			assert.Equal(t, tc.wantAdd, trips.N(), "the batch is worth %d round trips", tc.wantAdd)
		})
	}
}

// TestTripsCoverHandshakeNamesConnectionSetup: the commands go-redis sends
// while it sets up a new connection are the handshake, and a verb command is
// not one however it is spelled.
func TestTripsCoverHandshakeNamesConnectionSetup(t *testing.T) {
	t.Parallel()
	cases := []struct {
		args []any
		want bool
	}{
		{args: []any{"hello"}, want: true},
		{args: []any{"HELLO"}, want: true},
		{args: []any{"auth", "pw"}, want: true},
		{args: []any{"client", "setinfo"}, want: true},
		{args: []any{"select", "0"}, want: true},
		{args: []any{"readonly"}, want: true},
		{args: []any{"get", "k"}, want: false},
		{args: []any{"set", "k", "v"}, want: false},
		{args: nil, want: false},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.args), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, handshake(redis.NewCmd(context.Background(), tc.args...)), "handshake(%v)", tc.args)
		})
	}
}
