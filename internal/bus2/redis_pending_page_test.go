package bus2

import (
	"context"
	"net"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type pendingPageHook struct{ args []interface{} }

func (h *pendingPageHook) DialHook(redis.DialHook) redis.DialHook {
	return func(context.Context, string, string) (net.Conn, error) {
		panic("the pending-page fixture must not dial")
	}
}
func (h *pendingPageHook) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error {
		h.args = cmd.Args()
		cmd.(*redis.XPendingExtCmd).SetVal([]redis.XPendingExt{{ID: "9-0", Consumer: "daemon"}, {ID: "10-0", Consumer: "daemon"}})
		return nil
	}
}
func (h *pendingPageHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func TestRedisPendingPageUsesExclusiveCursorAndConsumerFilter(t *testing.T) {
	t.Parallel()
	for _, cursor := range []string{"", "8-0"} {
		t.Run(cursor, func(t *testing.T) {
			t.Parallel()
			c := redis.NewClient(&redis.Options{})
			t.Cleanup(func() { require.NoError(t, c.Close()) })
			hook := &pendingPageHook{}
			c.AddHook(hook)
			got, err := (Redis{C: c}).PendingPage(context.Background(), "stream", "group", "daemon", cursor, 137)
			require.NoError(t, err)
			assert.Equal(t, []string{"9-0", "10-0"}, got)
			start := "-"
			if cursor != "" {
				start = "(" + cursor
			}
			assert.Equal(t, []interface{}{"xpending", "stream", "group", start, "+", int64(137), "daemon"}, hook.args)
		})
	}
}
