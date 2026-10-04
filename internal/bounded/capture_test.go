package bounded

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCaptureCancelsAtExactLimitWithoutKeepingACompletePrefix(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := NewCapture(4, cancel)
	n, err := b.Write([]byte("abc"))
	require.NoError(t, err, "%v %v %v", n, err, b.Hit())
	require.Equal(t, 3, n, "%v %v %v", n, err, b.Hit())
	require.False(t, b.Hit(), "%v %v %v", n, err, b.Hit())

	n, err = b.Write([]byte("defgh"))
	require.NoError(t, err, "%v %v %v", n, err, b.Hit())
	require.Equal(t, 5, n, "%v %v %v", n, err, b.Hit())
	require.True(t, b.Hit(), "%v %v %v", n, err, b.Hit())

	require.Equal(t, "abcd", string(b.Bytes()), "capture not capped/cancelled")
	require.Error(t, ctx.Err(), "capture not capped/cancelled")
}
