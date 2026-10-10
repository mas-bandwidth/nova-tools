package redisconn

import (
	"context"
	"net"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testguard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGuardedDial(t *testing.T) {
	t.Parallel()

	g := testguard.NewGuard(true)

	var dialed []string
	fakeDial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = append(dialed, addr)
		return nil, net.ErrClosed
	}

	guarded := guardDial(g, fakeDial)

	t.Run("OffLoopbackPanics", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() {
			_, _ = guarded(context.Background(), "tcp", "example.com:6379")
		})
	})

	t.Run("LoopbackReachesDial", func(t *testing.T) {
		t.Parallel()
		dialed = nil
		_, err := guarded(context.Background(), "tcp", "127.0.0.1:6379")
		assert.ErrorIs(t, err, net.ErrClosed)
		require.Len(t, dialed, 1)
		assert.Equal(t, "127.0.0.1:6379", dialed[0])
	})
}
