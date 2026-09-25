package task_test

import (
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

func setupTakeTestRedis(t *testing.T) (*store.Store, *redis.Client) {
	t.Helper()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(context.Background(), client); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	return store.New(client), client
}

// TestTakeAvailableClaimsInRankOrder asserts that TakeAvailable claims available
// tasks in rank order: F, X1, L, T1, T2, U.
func TestTakeAvailableClaimsInRankOrder(t *testing.T) {
	st, client := setupTakeTestRedis(t)
	ctx := context.Background()
	sprint := "s-take-rank"

	client.HSet(ctx, "s:"+sprint, "status", "open")
	client.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: sprint})

	seedFriend(t, client, "f1", 10)
	seedFriend(t, client, "stella", 10)

	// Friend f1's open queue:
	// F: front yes, prio 5, est 10, pushed 1 -> score -5
	client.HSet(ctx, "task:F",
		"state", "open", "title", "task F", "priority", "5", "pushed_at", "1", "est", "10", "owner", "")
	client.ZAdd(ctx, "s:"+sprint+":open:f1", redis.Z{Score: -5, Member: "F"})
	client.SAdd(ctx, "s:"+sprint+":idx:task:open", "F")

	// L: front no, prio 5, est 30, pushed 2 -> score 5
	client.HSet(ctx, "task:L",
		"state", "open", "title", "task L", "priority", "5", "pushed_at", "2", "est", "30", "owner", "")
	client.ZAdd(ctx, "s:"+sprint+":open:f1", redis.Z{Score: 5, Member: "L"})
	client.SAdd(ctx, "s:"+sprint+":idx:task:open", "L")

	// X1: front no, prio 5, est 120, pushed 3 -> score 5
	client.HSet(ctx, "task:X1",
		"state", "open", "title", "task X1", "priority", "5", "pushed_at", "3", "est", "120", "owner", "")
	client.ZAdd(ctx, "s:"+sprint+":open:f1", redis.Z{Score: 5, Member: "X1"})
	client.SAdd(ctx, "s:"+sprint+":idx:task:open", "X1")

	// T1: front no, prio 5, est 30, pushed 6 -> score 5
	client.HSet(ctx, "task:T1",
		"state", "open", "title", "task T1", "priority", "5", "pushed_at", "6", "est", "30", "owner", "")
	client.ZAdd(ctx, "s:"+sprint+":open:f1", redis.Z{Score: 5, Member: "T1"})
	client.SAdd(ctx, "s:"+sprint+":idx:task:open", "T1")

	// T2: front no, prio 5, est 30, pushed 7 -> score 5
	client.HSet(ctx, "task:T2",
		"state", "open", "title", "task T2", "priority", "5", "pushed_at", "7", "est", "30", "owner", "")
	client.ZAdd(ctx, "s:"+sprint+":open:f1", redis.Z{Score: 5, Member: "T2"})
	client.SAdd(ctx, "s:"+sprint+":idx:task:open", "T2")

	// U: front no, prio 5, est none, pushed 8 -> score 5
	client.HSet(ctx, "task:U",
		"state", "open", "title", "task U", "priority", "5", "pushed_at", "8", "est", "", "owner", "")
	client.ZAdd(ctx, "s:"+sprint+":open:f1", redis.Z{Score: 5, Member: "U"})
	client.SAdd(ctx, "s:"+sprint+":idx:task:open", "U")

	// Stella holds X2 and X3:
	// X2: est 105, DEPENDS-ON X1
	client.HSet(ctx, "task:X2",
		"state", "working", "title", "task X2 | DEPENDS-ON: X1", "priority", "5", "pushed_at", "4", "est", "105", "owner", "stella")
	client.SAdd(ctx, "s:"+sprint+":idx:task:working", "X2")

	// X3: est 120, DEPENDS-ON X2
	client.HSet(ctx, "task:X3",
		"state", "working", "title", "task X3 | DEPENDS-ON: X2", "priority", "5", "pushed_at", "5", "est", "120", "owner", "stella")
	client.SAdd(ctx, "s:"+sprint+":idx:task:working", "X3")

	claims, err := task.TakeAvailable(ctx, st, "f1", sprint, "", 6, "actor", "idem")
	if err != nil {
		t.Fatalf("TakeAvailable failed: %v", err)
	}

	wantOrder := []string{"F", "X1", "L", "T1", "T2", "U"}
	if len(claims) != len(wantOrder) {
		t.Fatalf("got %d claims, want %d", len(claims), len(wantOrder))
	}

	for i, wantID := range wantOrder {
		if claims[i].ID != wantID {
			t.Fatalf("claim %d: got %s, want %s", i, claims[i].ID, wantID)
		}
	}
}
