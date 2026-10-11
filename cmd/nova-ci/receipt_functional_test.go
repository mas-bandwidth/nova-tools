//go:build functional

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ghevent"
	"github.com/mas-bandwidth/nova-tools/pkg/testredis"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// TestReceiptVerbWritesTheRowAndARefusedWriteIsExitOne runs the verb as ci-ok
// does against a throwaway redis-server: one CI RECEIPT line, the row read back raw
// off the stream; then, with ev:github made a string, the same
// verb is refused WRONGTYPE and exits 1, the red ci-ok a receipt that did not
// happen must be.
func TestReceiptVerbWritesTheRowAndARefusedWriteIsExitOne(t *testing.T) {
	t.Parallel()
	addr := testredis.Start(t)
	args := receiptArgs()
	args[3] = addr
	var out, errOut bytes.Buffer
	code := cmdGitHub(args, &out, &errOut, noEnv)
	require.Equal(t, 0, code, "code %d out %q err %q", code, out.String(), errOut.String())
	require.Equal(t, 0, errOut.Len(), "code %d out %q err %q", code, out.String(), errOut.String())
	require.True(t, strings.HasPrefix(out.String(), "CI RECEIPT mas-bandwidth/nova-tools sha="+receiptSHA+" run=42 workflow=CI conclusion=success pr=7 ev="), "code %d out %q err %q", code, out.String(), errOut.String())
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()
	got, err := rdb.XRange(ctx, ghevent.Stream, "-", "+").Result()
	require.NoError(t, err)
	require.Len(t, got, 1, "the stream holds %+v", got)
	require.Equal(t, "7", got[0].Values["number"], "the stream holds %+v", got)
	require.Equal(t, receiptSHA, got[0].Values["head"], "the stream holds %+v", got)
	require.Equal(t, "runner", got[0].Values["sender"], "the stream holds %+v", got)
	require.NoError(t, rdb.Set(ctx, ghevent.Stream, "not a stream", 0).Err())
	out.Reset()
	errOut.Reset()
	code = cmdGitHub(args, &out, &errOut, noEnv)
	require.Equal(t, 1, code, "refused write: code %d out %q err %q", code, out.String(), errOut.String())
	require.Equal(t, 0, out.Len(), "refused write: code %d out %q err %q", code, out.String(), errOut.String())
	require.Contains(t, errOut.String(), "WRONGTYPE", "refused write: code %d out %q err %q", code, out.String(), errOut.String())
	require.Contains(t, errOut.String(), "receipt write could not be confirmed", "refused write: code %d out %q err %q", code, out.String(), errOut.String())
}
