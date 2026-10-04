//go:build functional

package cireceipt

import (
	"context"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ghevent"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/mas-bandwidth/nova-tools/internal/testredis"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// TestReceiptWrittenIsReadByTheEventReader writes the row on a throwaway
// redis-server and reads it back through ghevent's reader: both sides of the
// ev:github row pinned in one test.
func TestReceiptWrittenIsReadByTheEventReader(t *testing.T) {
	t.Parallel()
	addr := testredis.Start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()

	ev, err := ghevent.OpenReader(ctx, redisconn.Options{Addr: addr}, nil)
	require.NoError(t, err)
	defer ev.Close()
	cursor, err := ev.Tip(ctx)
	require.NoError(t, err, "tip of an empty stream: %q %v", cursor, err)
	require.Equal(t, "0-0", cursor, "tip of an empty stream: %q %v", cursor, err)

	r := full()
	id, err := Write(ctx, rdb, r)
	require.NoError(t, err)
	n, err := rdb.DBSize(ctx).Result()
	require.NoError(t, err, "the receipt wrote %d keys (%v); want exactly ev:github", n, err)
	require.Equal(t, int64(1), n, "the receipt wrote %d keys (%v); want exactly ev:github", n, err)
	got, err := ev.Read(ctx, cursor, 10, time.Second)
	require.NoError(t, err)
	want := ghevent.Event{ID: id, Repo: "mas-bandwidth/nova-tools", Number: "4493", Kind: "workflow_run",
		Action: "completed", Head: sha, Sender: "runner", At: "2026-09-28T02:00:00Z"}
	require.Len(t, got, 1, "the reader read %+v, want [%+v]", got, want)
	require.Equal(t, want, got[0], "the reader read %+v, want [%+v]", got, want)
	tip, _ := ev.Tip(ctx)
	require.Equal(t, id, tip, "tip %q, want the receipt %q", tip, id)
}
