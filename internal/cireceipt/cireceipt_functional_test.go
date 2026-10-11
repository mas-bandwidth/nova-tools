//go:build functional

package cireceipt

import (
	"context"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ghevent"
	"github.com/mas-bandwidth/nova-tools/pkg/testredis"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// TestReceiptWrittenIsTheOneRowOnEvGithub writes the receipt on a throwaway
// redis-server and reads the stream back raw: the ev:github row pinned in one
// test (the stream's reader was deleted as dead code; XRANGE is the check).
func TestReceiptWrittenIsTheOneRowOnEvGithub(t *testing.T) {
	t.Parallel()
	addr := testredis.Start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()

	r := full()
	id, err := Write(ctx, rdb, r)
	require.NoError(t, err)
	n, err := rdb.DBSize(ctx).Result()
	require.NoError(t, err, "the receipt wrote %d keys (%v); want exactly ev:github", n, err)
	require.Equal(t, int64(1), n, "the receipt wrote %d keys (%v); want exactly ev:github", n, err)
	got, err := rdb.XRange(ctx, ghevent.Stream, "-", "+").Result()
	require.NoError(t, err)
	require.Len(t, got, 1, "the stream holds %+v, want the one receipt", got)
	require.Equal(t, id, got[0].ID, "the stream holds %+v, want the receipt %q", got, id)
	want := map[string]interface{}{"repo": "mas-bandwidth/nova-tools", "number": "4493", "kind": "workflow_run",
		"action": "completed", "head": sha, "sender": "runner", "at": "2026-09-28T02:00:00Z"}
	for k, v := range want {
		require.Equal(t, v, got[0].Values[k], "field %s of %+v", k, got[0].Values)
	}
}
