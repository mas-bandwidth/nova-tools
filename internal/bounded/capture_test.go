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
	if n, e := b.Write([]byte("abc")); n != 3 || e != nil || b.Hit() {
		t.Fatal(n, e, b.Hit())
	}
	if n, e := b.Write([]byte("defgh")); n != 5 || e != nil || !b.Hit() {
		t.Fatal(n, e, b.Hit())
	}
	require.Equal(t, "abcd", string(b.Bytes()), "capture not capped/cancelled")
	require.Error(t, ctx.Err(), "capture not capped/cancelled")
}
