package redisconn

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOpenCoverRefusesWhatResolveRefuses: Open, the one entry a tool calls,
// refuses before it dials what Resolve refuses, with Resolve's own refusal,
// and hands back no connection. Through the public Open, whose dialer is the
// package's own netDial and needs no store, no socket and no process.
func TestOpenCoverRefusesWhatResolveRefuses(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		o      Options
		getenv func(string) string
	}{
		{"no address anywhere", Options{}, nothing},
		{"an address without a port", Options{Addr: "store.test"}, nothing},
		{"a user with no password variable named", Options{Addr: storeAddr, User: "bench"}, nothing},
		{"a password variable whose name is not a name", Options{Addr: storeAddr, PasswordEnv: "hunter2"}, nothing},
		{"a password variable that is named and empty", Options{Addr: storeAddr, PasswordEnv: "PW"}, environment(map[string]string{"PW": ""})},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, want := Resolve(c.o, c.getenv)
			require.NotNil(t, want, "%s: Resolve = nil; the case must be a refusal", c.name)
			conn, err := Open(context.Background(), c.o, c.getenv)
			assert.Nil(t, conn, "%s: Open = %v; want no connection", c.name, conn)
			require.Error(t, err, "%s: Open = nil; want %v", c.name, want)
			assert.Equal(t, want.Error(), err.Error(), "%s: Open refused %q; want Resolve's refusal %q", c.name, err.Error(), want.Error())
			assert.Equal(t, Classify(want), Classify(err), "%s: class of %v; want %v like %v", c.name, err, Classify(want), want)
		})
	}
}

// TestOpenCoverFailsACallerWhoCancelled: through the public Open, a caller
// whose context is already done is failed at the package's own netDial — the
// dial answers before it connects, so the test owns no socket and touches no
// network — and Open hands back the caller's cancellation, bounded, as one
// line that opens with who was tried, with no connection.
func TestOpenCoverFailsACallerWhoCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	conn, err := Open(ctx, Options{Addr: "127.0.0.1:1"}, nothing)
	assert.Nil(t, conn, "Open = %v; want no connection", conn)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled, "%v does not unwrap to the caller's cancellation", err)
	assert.Truef(t, strings.HasPrefix(err.Error(), "redis at 127.0.0.1:1 as the default user, no password: "), "%v does not open with the tried line", err)
}

// TestOpenCoverNetDialRefusesBeforeItConnects: netDial, the dialer Open
// hands go-redis, hands back the dialer's own answer. With the caller's
// context already done it answers before it connects — no socket, no
// network, no name looked up — which is how a unit test may reach it.
func TestOpenCoverNetDialRefusesBeforeItConnects(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		network string
		addr    func(t *testing.T) string
	}{
		{"tcp to a literal address", "tcp", func(t *testing.T) string { return "127.0.0.1:1" }},
		{"unix to an absent path", "unix", func(t *testing.T) string { return filepath.Join(t.TempDir(), "absent.sock") }},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			conn, err := netDial(ctx, c.network, c.addr(t))
			assert.Nil(t, conn, "%s: netDial = %v; want no connection", c.name, conn)
			require.Error(t, err, "%s: netDial = nil; want the context's refusal", c.name)
			assert.ErrorIs(t, err, context.Canceled, "%s: %v does not unwrap to the caller's cancellation", c.name, err)
		})
	}
}

// TestOpenCoverTheQuietLoggerWritesNothing: the logger Open hands go-redis
// once, on the first Open, is go-redis's own logger and writes nothing: a
// store's chatter never crosses the one line a tool prints, and a failure
// comes back as an error instead.
func TestOpenCoverTheQuietLoggerWritesNothing(t *testing.T) {
	t.Parallel()
	var _ interface {
		Printf(context.Context, string, ...interface{})
	} = quiet{}
	var q quiet
	assert.NotPanics(t, func() { q.Printf(context.Background(), "go-redis says %s %d", "ERR", 7) }, "quiet.Printf panicked")
}
