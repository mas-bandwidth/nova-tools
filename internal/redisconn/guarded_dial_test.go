package redisconn

import (
	"context"
	"net"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testguard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dialRecorder is a fake dialFunc that records every address it is asked to
// dial and answers net.ErrClosed. Its mutex makes the recording safe to read
// while the rows run in parallel, and each row holds its own so the
// off-loopback row's empty assertion cannot see the loopback row's dial.
type dialRecorder struct {
	mu   sync.Mutex
	seen []string
}

func (r *dialRecorder) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	r.mu.Lock()
	r.seen = append(r.seen, addr)
	r.mu.Unlock()
	return nil, net.ErrClosed
}

func (r *dialRecorder) calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.seen...)
}

func TestGuardedDial(t *testing.T) {
	t.Parallel()

	g := testguard.NewGuard(true)

	t.Run("OffLoopbackPanics", func(t *testing.T) {
		t.Parallel()
		rec := &dialRecorder{}
		guarded := guardDial(g, rec.dial)
		assert.Panics(t, func() {
			_, _ = guarded(context.Background(), "tcp", "example.com:6379")
		})
		require.Empty(t, rec.calls(),
			"an off-loopback address must be refused before the fake dials (nova-tools#4193)")
	})

	t.Run("LoopbackReachesDial", func(t *testing.T) {
		t.Parallel()
		rec := &dialRecorder{}
		guarded := guardDial(g, rec.dial)
		_, err := guarded(context.Background(), "tcp", "127.0.0.1:6379")
		assert.ErrorIs(t, err, net.ErrClosed)
		require.Equal(t, []string{"127.0.0.1:6379"}, rec.calls(),
			"a loopback address must reach the fake with the address it was given")
	})
}
