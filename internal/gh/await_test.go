package gh

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ghevent"
	"github.com/redis/go-redis/v9"
)

func add(t *testing.T, rdb *redis.Client, kind, repo, number, head string) string {
	t.Helper()
	id, err := rdb.XAdd(context.Background(), &redis.XAddArgs{Stream: ghevent.Stream, Values: map[string]any{
		"repo": repo, "kind": kind, "number": number, "head": head, "action": "completed",
	}}).Result()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestAwaitWakesOnTheHeadsEvent: the wait returns on the first entry for
// the head, leaves later entries for the next read, and a miss advances
// the cursor without a wake. Nothing here blocks: the entries are on the
// stream before the read, and a miss uses no BLOCK.
func TestAwaitWakesOnTheHeadsEvent(t *testing.T) {
	t.Parallel()
	rdb := newStore(t)
	ctx := context.Background()
	head := strings.Repeat("a", 40)
	other := strings.Repeat("b", 40)
	tip, err := Tip(ctx, rdb)
	if err != nil || tip != "0-0" {
		t.Fatalf("empty tip %q %v", tip, err)
	}
	add(t, rdb, "issue_comment", "o/r", "1", "")
	tip, err = Tip(ctx, rdb)
	if err != nil || tip == "0-0" {
		t.Fatalf("tip %q %v", tip, err)
	}
	// A miss: an entry for another head, no block.
	add(t, rdb, "check_run", "o/r", "1", other)
	tip2, hit, err := Await(ctx, rdb, tip, 0, HeadEvent(head))
	if err != nil || hit || tip2 == tip {
		t.Fatalf("miss: tip %q->%q hit=%v err=%v", tip, tip2, hit, err)
	}
	// A hit, then a later entry is left for the next read.
	want := add(t, rdb, "workflow_run", "o/r", "1", head)
	add(t, rdb, "pull_request", "o/r", "1", head)
	tip3, hit, err := Await(ctx, rdb, tip2, 0, HeadEvent(head))
	if err != nil || !hit || tip3 != want {
		t.Fatalf("hit: tip %q want %q hit=%v err=%v", tip3, want, hit, err)
	}
	tip4, hit, err := Await(ctx, rdb, tip3, 0, PREvent("o/r", "1"))
	if err != nil || !hit || tip4 == tip3 {
		t.Fatalf("pr event: %q hit=%v err=%v", tip4, hit, err)
	}
	// Nothing after: no block, no hit, the cursor stands.
	tip5, hit, err := Await(ctx, rdb, tip4, 0, PREvent("o/r", "1"))
	if err != nil || hit || tip5 != tip4 {
		t.Fatalf("drained: %q hit=%v err=%v", tip5, hit, err)
	}
}
