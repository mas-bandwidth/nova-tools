package deal_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

func setupRankTestRedis(t *testing.T) (*store.Store, *redis.Client) {
	t.Helper()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(context.Background(), client); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	return store.New(client), client
}

// TestRankDealsHeadOfLongestChainFirst pins the spec fixture:
// Friend f1's open queue holds F, L, X1, T1, T2, U.
// Stella holds X2 (est 105, DEPENDS-ON X1) and X3 (est 120, DEPENDS-ON X2), so X1 ranks 345.
// Expected take order: F, X1, L, T1, T2, U.
func TestRankDealsHeadOfLongestChainFirst(t *testing.T) {
	st, client := setupRankTestRedis(t)
	ctx := context.Background()
	sprint := "s-test-rank"

	client.HSet(ctx, "s:"+sprint, "status", "open")
	client.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: sprint})
	client.SAdd(ctx, "friends", "f1", "stella")
	client.HSet(ctx, "friend:f1:desired", "slots", 10, "paused", "0")
	client.HSet(ctx, "friend:f1:beat", "host", "fixture")
	client.HSet(ctx, "friend:stella:desired", "slots", 10, "paused", "0")
	client.HSet(ctx, "friend:stella:beat", "host", "fixture")

	// Tasks in f1's open queue:
	// F: front yes, prio 5, est 10, pushed 1 -> score = -5
	client.HSet(ctx, "task:F",
		"state", "open", "title", "task F", "priority", "5", "pushed_at", "1", "est", "10", "owner", "")
	client.ZAdd(ctx, "s:"+sprint+":open:f1", redis.Z{Score: -5, Member: "F"})
	client.SAdd(ctx, "s:"+sprint+":idx:task:open", "F")

	// L: front no, prio 5, est 30, pushed 2 -> score = 5
	client.HSet(ctx, "task:L",
		"state", "open", "title", "task L", "priority", "5", "pushed_at", "2", "est", "30", "owner", "")
	client.ZAdd(ctx, "s:"+sprint+":open:f1", redis.Z{Score: 5, Member: "L"})
	client.SAdd(ctx, "s:"+sprint+":idx:task:open", "L")

	// X1: front no, prio 5, est 120, pushed 3 -> score = 5
	client.HSet(ctx, "task:X1",
		"state", "open", "title", "task X1", "priority", "5", "pushed_at", "3", "est", "120", "owner", "")
	client.ZAdd(ctx, "s:"+sprint+":open:f1", redis.Z{Score: 5, Member: "X1"})
	client.SAdd(ctx, "s:"+sprint+":idx:task:open", "X1")

	// T1: front no, prio 5, est 30, pushed 6 -> score = 5
	client.HSet(ctx, "task:T1",
		"state", "open", "title", "task T1", "priority", "5", "pushed_at", "6", "est", "30", "owner", "")
	client.ZAdd(ctx, "s:"+sprint+":open:f1", redis.Z{Score: 5, Member: "T1"})
	client.SAdd(ctx, "s:"+sprint+":idx:task:open", "T1")

	// T2: front no, prio 5, est 30, pushed 7 -> score = 5
	client.HSet(ctx, "task:T2",
		"state", "open", "title", "task T2", "priority", "5", "pushed_at", "7", "est", "30", "owner", "")
	client.ZAdd(ctx, "s:"+sprint+":open:f1", redis.Z{Score: 5, Member: "T2"})
	client.SAdd(ctx, "s:"+sprint+":idx:task:open", "T2")

	// U: front no, prio 5, est none, pushed 8 -> score = 5
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

	ranks, err := deal.RankTasks(ctx, st, sprint, "f1", nil)
	if err != nil {
		t.Fatalf("RankTasks: %v", err)
	}

	wantOrder := []string{"F", "X1", "L", "T1", "T2", "U"}
	if len(ranks) != len(wantOrder) {
		t.Fatalf("got %d ranked tasks, want %d", len(ranks), len(wantOrder))
	}

	gotOrder := make([]string, len(ranks))
	for i, r := range ranks {
		gotOrder[i] = r.ID
	}

	for i, wantID := range wantOrder {
		if gotOrder[i] != wantID {
			t.Fatalf("take order at %d: got %s, want %s (full order: %v)", i, gotOrder[i], wantID, gotOrder)
		}
	}

	// Verify X1's rank and chain
	x1 := ranks[1]
	if x1.ID != "X1" || x1.Rank != 345 {
		t.Errorf("X1 rank = %d, want 345", x1.Rank)
	}
	if x1.Chain != "X1>X2>X3" {
		t.Errorf("X1 chain = %q, want X1>X2>X3", x1.Chain)
	}
	if x1.Front != false {
		t.Errorf("X1 front = %t, want false", x1.Front)
	}

	// Verify F (front)
	f := ranks[0]
	if f.ID != "F" || f.Front != true || f.Rank != 10 {
		t.Errorf("F = %+v; want front=true rank=10", f)
	}

	// Verify U (est=?, rank=0)
	u := ranks[5]
	if u.ID != "U" || u.Est != "?" || u.Rank != 0 {
		t.Errorf("U = %+v; want est=? rank=0", u)
	}

	// Control: Today's raw ZRANGE order puts L (pushed 2) before X1 (pushed 3).
	// Rank ordering must put X1 before L.
	rawZRange, err := client.ZRange(ctx, "s:"+sprint+":open:f1", 0, -1).Result()
	if err != nil {
		t.Fatalf("zrange: %v", err)
	}
	lIdx, x1Idx := -1, -1
	for i, id := range rawZRange {
		if id == "L" {
			lIdx = i
		}
		if id == "X1" {
			x1Idx = i
		}
	}
	if lIdx > x1Idx {
		t.Errorf("control assumption failed: in ZRange, L (%d) should be before X1 (%d)", lIdx, x1Idx)
	}
}

// TestRankCycleTerminates verifies that cyclic dependencies terminate,
// write CYCLE <cycle> to stderr, and rank cycle members at their own est.
func TestRankCycleTerminates(t *testing.T) {
	st, client := setupRankTestRedis(t)
	ctx := context.Background()
	sprint := "s-test-cycle"

	client.HSet(ctx, "s:"+sprint, "status", "open")
	client.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: sprint})
	client.SAdd(ctx, "friends", "f1")

	// Task A depends on B, Task B depends on A
	client.HSet(ctx, "task:A",
		"state", "open", "title", "task A | DEPENDS-ON: B", "priority", "5", "pushed_at", "1", "est", "20", "owner", "")
	client.ZAdd(ctx, "s:"+sprint+":open:f1", redis.Z{Score: 5, Member: "A"})
	client.SAdd(ctx, "s:"+sprint+":idx:task:open", "A")

	client.HSet(ctx, "task:B",
		"state", "open", "title", "task B | DEPENDS-ON: A", "priority", "5", "pushed_at", "2", "est", "30", "owner", "")
	client.ZAdd(ctx, "s:"+sprint+":open:f1", redis.Z{Score: 5, Member: "B"})
	client.SAdd(ctx, "s:"+sprint+":idx:task:open", "B")

	var stderr bytes.Buffer
	ranks, err := deal.RankTasks(ctx, st, sprint, "f1", &stderr)
	if err != nil {
		t.Fatalf("RankTasks failed: %v", err)
	}

	if len(ranks) != 2 {
		t.Fatalf("got %d ranks, want 2", len(ranks))
	}

	stderrStr := stderr.String()
	if !strings.Contains(stderrStr, "CYCLE ") {
		t.Errorf("stderr missing CYCLE message; got %q", stderrStr)
	}
	if !strings.Contains(stderrStr, "A>B>A") && !strings.Contains(stderrStr, "B>A>B") {
		t.Errorf("stderr cycle missing expected cycle string; got %q", stderrStr)
	}

	// Members of cycle rank at their own est
	for _, r := range ranks {
		if r.ID == "A" && r.Rank != 20 {
			t.Errorf("A rank = %d, want own est 20", r.Rank)
		}
		if r.ID == "B" && r.Rank != 30 {
			t.Errorf("B rank = %d, want own est 30", r.Rank)
		}
	}
}
