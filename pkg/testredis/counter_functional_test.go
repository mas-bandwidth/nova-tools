//go:build functional

package testredis

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The counter is not vacuous: a client that sends a PING is counted, and one
// that only opens is not.
func TestCommandCounterCountsAPing(t *testing.T) {
	t.Parallel()

	addr, count := CommandCounter(t)
	if host, _, err := net.SplitHostPort(addr); err != nil || host != "127.0.0.1" {
		require.Failf(t, "", "the counter listens on %q; want 127.0.0.1 and a port", addr)
	}
	c := redis.NewClient(&redis.Options{Addr: addr})
	// ignored: a test client's close; the test's own assertions are the report
	defer func() { _ = c.Close() }()
	if n := count(); n != 0 {
		require.Zero(t, n, "NewClient sent %d commands; want 0", n)
	}
	if err := c.Ping(context.Background()).Err(); err != nil {
		require.NoError(t, err, "ping: %v", err)
	}
	if n := count(); n < 1 {
		require.GreaterOrEqual(t, n, int64(1), "after a PING the counter reads %d; want at least 1", n)
	}
}

func TestCommandCounterAnswersEveryCommandAndCountsIt(t *testing.T) {
	t.Parallel()

	addr, count := CommandCounter(t)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		require.NoError(t, err, err)
	}
	// ignored: a test client's close; the test's own assertions are the report
	defer func() { _ = conn.Close() }()
	r := bufio.NewReader(conn)
	for i, step := range []struct{ send, want string }{
		{"*1\r\n$4\r\nPING\r\n", "+PONG\r\n"},
		{"*1\r\n$4\r\nping\r\n", "+PONG\r\n"},
		{"*2\r\n$5\r\nHELLO\r\n$1\r\n3\r\n", "-ERR unknown command 'HELLO'\r\n"},
		{"*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$0\r\n\r\n", "+OK\r\n"},
	} {
		if _, err := io.WriteString(conn, step.send); err != nil {
			require.NoError(t, err, err)
		}
		if got, err := r.ReadString('\n'); err != nil || got != step.want {
			require.Failf(t, "", "%q was answered %q, %v; want %q", step.send, got, err, step.want)
		}
		if n := count(); n != int64(i+1) {
			require.Equal(t, int64(i+1), n, "after %d commands the counter reads %d", i+1, n)
		}
	}
}

func TestCommandCounterHangsUpOnWhatIsNotACommand(t *testing.T) {
	t.Parallel()

	addr, count := CommandCounter(t)
	for name, send := range malformed {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			require.NoError(t, err, err)
		}
		if _, err := io.WriteString(conn, send); err != nil {
			require.NoError(t, err, err)
		}
		// This side has said all it will: what is cut short stays short.
		if err := conn.(*net.TCPConn).CloseWrite(); err != nil {
			require.NoError(t, err, err)
		}
		if got, err := io.ReadAll(conn); err != nil || len(got) != 0 {
			assert.Failf(t, "", "%s: answered %q, %v; want the counter to hang up and say nothing", name, got, err)
		}
		_ = conn.Close()
	}
	if n := count(); n != 0 {
		require.Zero(t, n, "what is not a command was counted: %d", n)
	}
}

// The cleanup hangs up on a client the test left open, closes its listener,
// and ends. The listener is asked, not the port: once it is closed the port
// is anyone's, and a parallel test may take it and answer there.
func TestCommandCounterHangsUpOnItsClientsAtCleanup(t *testing.T) {
	t.Parallel()

	var left net.Conn
	var ln net.Listener
	t.Run("a test that leaves its client open", func(t *testing.T) {
		addr, count := real.counter(t, func(network, address string) (net.Listener, error) {
			var err error
			ln, err = net.Listen(network, address)
			return ln, err
		})
		var err error
		if left, err = net.Dial("tcp", addr); err != nil {
			require.NoError(t, err, err)
		}
		if _, err := io.WriteString(left, "*1\r\n$4\r\nPING\r\n"); err != nil {
			require.NoError(t, err, err)
		}
		if got, err := bufio.NewReader(left).ReadString('\n'); err != nil || got != "+PONG\r\n" || count() != 1 {
			require.Failf(t, "", "answered %q, %v, counted %d", got, err, count())
		}
	})
	// ignored: a test client's close; the test's own assertions are the report
	defer func() { _ = left.Close() }()
	// The end of the stream: ReadAll reads it as no error and nothing more.
	if got, err := io.ReadAll(left); err != nil || len(got) != 0 {
		require.Failf(t, "", "after the cleanup the client read %q, %v; want the end and nothing before it", got, err)
	}
	if conn, err := ln.Accept(); !errors.Is(err, net.ErrClosed) {
		if conn != nil {
			_ = conn.Close()
		}
		require.ErrorIs(t, err, net.ErrClosed, "after the cleanup the counter's listener accepts: %v; want it closed", err)
	}
}
