package testkit

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPipeStoreReachesTheFakeOverPipes: the client writes and reads through a
// net.Pipe, never over TCP, so the miniredis fake's loopback listener is not
// dialled.
func TestPipeStoreReachesTheFakeOverPipes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mr, client := PipeStore(t)
	err := client.Set(ctx, "k", "v", 0).Err()
	require.NoError(t, err)
	got, err := mr.Get("k")
	require.NoError(t, err)
	assert.Equal(t, "v", got)
}
