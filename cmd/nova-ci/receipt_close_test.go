package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// closingHook answers the XADD with an entry id and closes the client, so the
// verb's own close of the store afterwards fails.
type closingHook struct{ c *redis.Client }

func (closingHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h closingHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if sc, ok := cmd.(*redis.StringCmd); ok {
			sc.SetVal("1-0")
		}
		return h.c.Close()
	}
}

func (closingHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func TestReceiptReportsAStoreCloseErrorAndKeepsTheWrittenExit(t *testing.T) {
	t.Parallel()
	open := func(ctx context.Context, addr string) (*store.Store, error) {
		c := redis.NewClient(&redis.Options{Addr: addr})
		c.AddHook(closingHook{c: c})
		return store.New(c), nil
	}
	var out, errb bytes.Buffer
	code := cmdReceipt(context.Background(), []string{"--from-runner", "--redis", "127.0.0.1:1", "--repo", "mas-bandwidth/nova-tools",
		"--sha", receiptSHA, "--run-id", "42", "--workflow", "CI", "--conclusion", "success"},
		&out, &errb, noEnv, open)
	require.Equal(t, 0, code, "the write was confirmed; stdout %q stderr %q", out.String(), errb.String())
	assert.Contains(t, out.String(), "ev=1-0")
	assert.Contains(t, errb.String(), "nova-ci github receipt NOTE: store close failed")
}
