//go:build functional

package ci

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// TestSpecDoneAtNineWithOneReaderUp proves the spec gate (#4400):
// 1. With one reader up (or alone), a single score >= 9 moves the spec from working to done.
// 2. With two readers up, a single score of 9 leaves the spec working, and a second distinct reader scoring >= 9 finishes it.
// 3. A score < 9 does not qualify.
// 4. The spec owner does not count toward readers-up.
func TestSpecDoneAtNineWithOneReaderUp(t *testing.T) {
	t.Parallel()

	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load fn library: %v", err)
	}

	nowMs := time.Now().UnixMilli()

	markSpec := func(repo, n, who string, rev, score int) (answer, state string, qualifying int) {
		line := fmt.Sprintf("SPEC who=%s rev=%d score=%d", who, rev, score)
		raw, err := c.FCall(ctx, "ns_spec_mark", nil, repo, n, who, strconv.Itoa(rev), strconv.Itoa(score), "dev", line, "", who).Result()
		if err != nil {
			t.Fatalf("ns_spec_mark %s#%s by %s: %v", repo, n, who, err)
		}
		v, ok := raw.([]any)
		if !ok || len(v) < 3 {
			t.Fatalf("unexpected reply: %v", raw)
		}
		ans := fmt.Sprint(v[0])
		st := fmt.Sprint(v[1])
		q, _ := strconv.Atoi(fmt.Sprint(v[2]))
		return ans, st, q
	}

	// Case 1: 1 reader up. Quorum falls to 1. Single 9 marks done.
	c.SAdd(ctx, "friends", "reader1")
	c.HSet(ctx, "friend:reader1:beat", "host", "box", "at", strconv.FormatInt(nowMs, 10))

	ans, st, q := markSpec("nova-tools", "101", "reader1", 1, 9)
	if ans != "DONE" || st != "done" || q != 1 {
		t.Fatalf("expected 1 reader up to finish at score 9: got answer=%s state=%s qualifying=%d", ans, st, q)
	}

	// Case 2: 2 readers up. Quorum is 2. Single 9 leaves it working. Score 8 does not count. Second 9 finishes.
	c.SAdd(ctx, "friends", "reader2")
	c.HSet(ctx, "friend:reader2:beat", "host", "box", "at", strconv.FormatInt(nowMs, 10))

	ans, st, q = markSpec("nova-tools", "102", "reader1", 1, 9)
	if ans != "RECORDED" || st != "working" || q != 1 {
		t.Fatalf("expected 2 readers up to leave spec working after 1st 9: got answer=%s state=%s qualifying=%d", ans, st, q)
	}

	// Score 8 does not qualify.
	ans, st, q = markSpec("nova-tools", "102", "reader2", 1, 8)
	if st != "working" || q != 1 {
		t.Fatalf("expected score 8 to not qualify: got state=%s qualifying=%d", st, q)
	}

	// Second reader scores 9: finishes spec.
	ans, st, q = markSpec("nova-tools", "102", "reader2", 1, 9)
	if ans != "DONE" || st != "done" || q != 2 {
		t.Fatalf("expected 2nd 9 to finish spec: got answer=%s state=%s qualifying=%d", ans, st, q)
	}

	// Case 3: Owner is excluded from readers_up count.
	// We have reader1 and reader2 in friends.
	// If owner of #103 is reader1, then live readers excluding owner is only reader2 (count = 1).
	// So quorum falls to 1!
	c.HSet(ctx, "pr:nova-tools:103", "owner", "reader1")
	ans, st, q = markSpec("nova-tools", "103", "reader2", 1, 9)
	if ans != "DONE" || st != "done" || q != 1 {
		t.Fatalf("expected quorum=1 when owner excluded leaves 1 reader up: got answer=%s state=%s qualifying=%d", ans, st, q)
	}
}
