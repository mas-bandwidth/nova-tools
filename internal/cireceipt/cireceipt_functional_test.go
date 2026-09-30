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
	if err != nil {
		t.Fatal(err)
	}
	defer ev.Close()
	cursor, err := ev.Tip(ctx)
	if err != nil || cursor != "0-0" {
		t.Fatalf("tip of an empty stream: %q %v", cursor, err)
	}

	r := full()
	id, err := Write(ctx, rdb, r)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := rdb.DBSize(ctx).Result(); err != nil || n != 1 {
		t.Fatalf("the receipt wrote %d keys (%v); want exactly ev:github", n, err)
	}
	got, err := ev.Read(ctx, cursor, 10, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	want := ghevent.Event{ID: id, Repo: "mas-bandwidth/nova-tools", Number: "4493", Kind: "workflow_run",
		Action: "completed", Head: sha, Sender: "runner", At: "2026-09-28T02:00:00Z"}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("the reader read %+v, want [%+v]", got, want)
	}
	if tip, _ := ev.Tip(ctx); tip != id {
		t.Fatalf("tip %q, want the receipt %q", tip, id)
	}
}
