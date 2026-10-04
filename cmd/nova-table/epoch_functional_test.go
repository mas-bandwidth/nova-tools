//go:build functional

package main

import (
	"context"
	"github.com/redis/go-redis/v9"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCLIObservedEpochHistoryAndReceipt(t *testing.T) {
	t.Parallel()
	addr := firstRunStore(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() {
		assert.NoError(t, c.Close())
	})
	ctx := context.Background()
	success := func(args ...string) string {
		t.Helper()
		code, out, errout := runTable(at(addr, args...)...)
		require.EqualValues(t, 0, code, "%v: exit=%d out=%q err=%q", args, code, out, errout)
		require.Empty(t, errout, "%v: exit=%d out=%q err=%q", args, code, out, errout)
		return out
	}
	success("create", "epoch-cli", "--columns", "row:text:none,ready", "--epoch-key", "domain:epoch")
	success("row", "add", "epoch-cli", "build")
	success("cell", "add", "epoch-cli", "build", "ready", "old", "--score", "7")
	require.NoError(t, c.HSet(ctx, "domain:epoch", "n", 1).Err())
	events := c.XLen(ctx, "table:epoch-cli:changes").Val()
	code, _, errout := runTable(at(addr, "clear", "epoch-cli")...)
	require.EqualValues(t, 1, code, "stale CLI = %d %s", code, errout)
	require.Contains(t, errout, "requested epoch is stale", "stale CLI = %d %s", code, errout)
	{
		got := c.XLen(ctx, "table:epoch-cli:changes").Val()
		require.Equal(t, events, got, "%v", "stale CLI emitted a receipt")
	}
	out := success("row", "add", "epoch-cli", "build", "--epoch", "1", "--actor", "Stella", "--receipt")
	require.Contains(t, out, "TABLE RECEIPT event=", "missing committed receipt: %s", out)
	require.Contains(t, out, "epoch=1", "missing committed receipt: %s", out)
	success("member", "create", "epoch-cli", "new", "--epoch", "1")
	success("cell", "add", "epoch-cli", "build", "ready", "new", "--score", "9", "--epoch", "1")
	history := success("show", "epoch-cli", "--at-epoch", "0")
	require.Contains(t, history, "epoch=0 revision=3", "historical snapshot: %s", history)
	require.Contains(t, history, "ready=1", "historical snapshot: %s", history)
	{
		out := success("check", "epoch-cli")
		require.Contains(t, out, "epoch=1", "current check: %s", out)
		require.Contains(t, out, "members=1", "current check: %s", out)
	}
	success("drop", "epoch-cli", "--epoch", "1", "--definition")
	// the epoch's definition stays readable; its rows go with the table
	{
		out := success("render", "epoch-cli", "--at-epoch", "0")
		require.Contains(t, out, "ready", "template deletion: the definition of epoch 0 or its rows: %s", out)
		require.NotContains(t, out, "build", "template deletion: the definition of epoch 0 or its rows: %s", out)
	}
}
