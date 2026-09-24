package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// TestCapacityAsActorControlReceipt exercises the specified --as control flag
// through the CLI, not merely through the Go capacity API. The actor must be
// preserved in the server-timed cap:log receipt.
func TestCapacityAsActorControlReceipt(t *testing.T) {
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := runCapacity(ctx, []string{"machine", "--redis", addr, "--as", "operator", "ctl-machine", "64"}, &out, &errOut); code != 0 {
		t.Fatalf("capacity machine code=%d stderr=%q", code, errOut.String())
	}
	client.SAdd(ctx, "friends", "alice")
	out.Reset()
	errOut.Reset()
	if code := runCapacity(ctx, []string{"friend", "--redis", addr, "--as", "operator", "--machine", "ctl-machine", "alice", "32"}, &out, &errOut); code != 0 {
		t.Fatalf("capacity friend code=%d stderr=%q", code, errOut.String())
	}
	entries, err := client.XRange(ctx, "cap:log", "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("receipts=%d want 2", len(entries))
	}
	for _, entry := range entries {
		if entry.Values["actor"] != "operator" {
			t.Fatalf("receipt actor=%v", entry.Values["actor"])
		}
	}
}
