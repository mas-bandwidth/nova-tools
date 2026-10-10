package redisconn

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// errFakeDial is what the fake dialer returns; a test asserts the wrapped
// dialer ran by finding this error.
var errFakeDial = errors.New("fake dialer reached")

// TestGuardedDialRefusesOffLoopbackBeforeTheFake pins that the wrapper Open
// hands its dialer refuses an off-loopback address in the guard, before the
// wrapped dialer is called, so no socket is opened (nova-tools#4193).
func TestGuardedDialRefusesOffLoopbackBeforeTheFake(t *testing.T) {
	t.Parallel()
	g := testguard.NewGuard(true)
	var calls int
	fake := func(context.Context, string, string) (net.Conn, error) {
		calls++
		return nil, errFakeDial
	}
	dial := guardDial(g, fake)
	assert.Panics(t, func() { _, _ = dial(context.Background(), "tcp", "store.invalid:6379") },
		"the guard must refuse an off-loopback address")
	assert.Zero(t, calls, "the guard must refuse before the wrapped dialer is called")
}

// TestGuardedDialLetsALoopbackAddressReachTheFake pins the other half: a
// loopback address passes the guard and reaches the wrapped dialer unchanged.
func TestGuardedDialLetsALoopbackAddressReachTheFake(t *testing.T) {
	t.Parallel()
	g := testguard.NewGuard(true)
	var gotNetwork, gotAddr string
	fake := func(_ context.Context, network, addr string) (net.Conn, error) {
		gotNetwork, gotAddr = network, addr
		return nil, errFakeDial
	}
	dial := guardDial(g, fake)
	_, err := dial(context.Background(), "tcp", "127.0.0.1:6379")
	require.ErrorIs(t, err, errFakeDial, "the wrapped dialer must run and its error come back")
	assert.Equal(t, "tcp", gotNetwork)
	assert.Equal(t, "127.0.0.1:6379", gotAddr)
}
