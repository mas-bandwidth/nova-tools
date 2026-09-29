//go:build functional

package read_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/read"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
	"github.com/redis/go-redis/v9"
)

func TestOwedValidation(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()

	// 1. Invalid PR number
	_, err := read.Owed(ctx, c, read.OwedOptions{PR: 0})
	if err == nil || !strings.Contains(err.Error(), "positive PR number") {
		t.Fatalf("invalid PR: err %v", err)
	}

	// 2. PR record missing
	_, err = read.Owed(ctx, c, read.OwedOptions{PR: 42})
	if err == nil || !strings.Contains(err.Error(), "no record") {
		t.Fatalf("missing record: err %v", err)
	}

	head := strings.Repeat("a", 40)
	// Seed open PR
	c.HSet(ctx, stream.PRKey("nova-tools", 42), "state", "open", "head", head)
	_, err = read.Owed(ctx, c, read.OwedOptions{PR: 42})
	if err == nil || !strings.Contains(err.Error(), "not merged") {
		t.Fatalf("unmerged PR: err %v", err)
	}

	// Now mark merged, but no score at head
	c.HSet(ctx, stream.PRKey("nova-tools", 42), "state", "merged")
	_, err = read.Owed(ctx, c, read.OwedOptions{PR: 42})
	if err == nil || !strings.Contains(err.Error(), "no score recorded") {
		t.Fatalf("no score: err %v", err)
	}

	// Score >= 8 (e.g. 8/10): no owed work
	c.RPush(ctx, stream.LinesKey("nova-tools", 42), fmt.Sprintf("SCORE who=opus head=%s score=8/10", head))
	_, err = read.Owed(ctx, c, read.OwedOptions{PR: 42})
	if err == nil || !strings.Contains(err.Error(), "no owed work") {
		t.Fatalf("score >= 8: err %v", err)
	}

	// Score < 8 (e.g. 7/10), but no Owed: sentence
	c.Del(ctx, stream.LinesKey("nova-tools", 42))
	c.RPush(ctx, stream.LinesKey("nova-tools", 42), fmt.Sprintf("SCORE who=opus head=%s score=7/10", head))
	_, err = read.Owed(ctx, c, read.OwedOptions{PR: 42})
	if err == nil || !strings.Contains(err.Error(), "no Owed: sentence") {
		t.Fatalf("no owed sentence: err %v", err)
	}
}

func TestOwedCutsFixCard(t *testing.T) {
	t.Parallel()

	_, c := wstest.Start(t)
	ctx := context.Background()
	c.SAdd(ctx, "friends", "rowan", "stella")

	head := strings.Repeat("b", 40)
	c.HSet(ctx, stream.PRKey("nova-tools", 99),
		"state", "merged",
		"head", head,
		"task", "build-100",
		"stream", "landing",
	)
	c.HSet(ctx, "task:build-100", "sprint", "S1", "stream", "landing")
	c.RPush(ctx, stream.LinesKey("nova-tools", 99),
		fmt.Sprintf("SCORE who=opus head=%s score=6/10", head),
		"Owed: fix edge case when reader reaches EOF early",
	)

	res, err := read.Owed(ctx, c, read.OwedOptions{
		Repo: "nova-tools",
		PR:   99,
		By:   "rowan",
	})
	if err != nil {
		t.Fatalf("read.Owed: %v", err)
	}

	if res.PR != 99 || res.Score != 6 || res.Card != "fix-build-100" || res.Stream != "landing" || res.Behind != "build-100" || res.Body != "fix edge case when reader reaches EOF early" {
		t.Fatalf("unexpected res: %+v", res)
	}

	// Verify the fix card was created in Redis
	card := c.HGetAll(ctx, "task:fix-build-100").Val()
	if card["where"] != "waiting" || card["blocked_on"] != "build-100" || card["body"] != "fix edge case when reader reaches EOF early" {
		t.Fatalf("fix card fields: %+v", card)
	}
}
