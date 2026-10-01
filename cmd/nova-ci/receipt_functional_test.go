//go:build functional

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ghevent"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/mas-bandwidth/nova-tools/internal/testredis"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// TestReceiptVerbWritesTheRowAndARefusedWriteIsExitOne runs the verb as ci-ok
// does against a throwaway redis-server: one CI RECEIPT line, the row read back
// through ghevent's reader; then, with ev:github made a string, the same
// verb is refused WRONGTYPE and exits 1, the red ci-ok a receipt that did not
// happen must be.
func TestReceiptVerbWritesTheRowAndARefusedWriteIsExitOne(t *testing.T) {
	t.Parallel()
	addr := testredis.Start(t)
	args := receiptArgs()
	args[3] = addr
	var out, errOut bytes.Buffer
	if code := cmdGitHub(args, &out, &errOut, noEnv); code != 0 || errOut.Len() != 0 ||
		!strings.HasPrefix(out.String(), "CI RECEIPT mas-bandwidth/nova-tools sha="+receiptSHA+" run=42 workflow=CI conclusion=success pr=7 ev=") {
		t.Fatalf("code %d out %q err %q", code, out.String(), errOut.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ev, err := ghevent.OpenReader(ctx, redisconn.Options{Addr: addr}, nil)
	require.NoError(t, err)
	defer ev.Close()
	got, err := ev.Read(ctx, "0-0", 10, time.Second)
	if err != nil || len(got) != 1 || got[0].Number != "7" || got[0].Head != receiptSHA || got[0].Sender != "runner" {
		t.Fatalf("the reader read %+v %v", got, err)
	}

	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()
	require.NoError(t, rdb.Set(ctx, ghevent.Stream, "not a stream", 0).Err())
	out.Reset()
	errOut.Reset()
	if code := cmdGitHub(args, &out, &errOut, noEnv); code != 1 || out.Len() != 0 ||
		!strings.Contains(errOut.String(), "WRONGTYPE") || !strings.Contains(errOut.String(), "receipt write could not be confirmed") {
		t.Fatalf("refused write: code %d out %q err %q", code, out.String(), errOut.String())
	}
}
