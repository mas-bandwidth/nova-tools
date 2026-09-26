//go:build functional

package typedrec_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// 8. TestResultWriteOnce verifies write-once semantics of ns_card_result.
func TestResultWriteOnce(t *testing.T) {
	t.Parallel()

	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatalf("load fn: %v", err)
	}

	sprint := "s1"
	label := "c1"
	attempt := "1"
	token := "tok123"

	// Setup card
	cardKey := "s:" + sprint + ":card:" + label
	if err := client.HSet(ctx, cardKey, "state", "running", "token", token, "attempt", attempt, "repo", "mas-bandwidth/nova-tools").Err(); err != nil {
		t.Fatalf("setup card: %v", err)
	}

	shaA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	argsA := []any{sprint, label, attempt, token, "v2", "fix", "1", "", "", "0", shaA, "raw", "/results"}
	// First write: OK
	reply1, err := client.FCall(ctx, "ns_card_result", nil, argsA...).Text()
	if err != nil {
		t.Fatalf("fcall 1: %v", err)
	}
	if !strings.HasPrefix(reply1, "0|OK") {
		t.Fatalf("reply1 = %q, want 0|OK", reply1)
	}

	// Second write with identical sha: idempotent 0|OK
	reply2, err := client.FCall(ctx, "ns_card_result", nil, argsA...).Text()
	if err != nil {
		t.Fatalf("fcall 2: %v", err)
	}
	if !strings.HasPrefix(reply2, "0|OK") {
		t.Fatalf("reply2 = %q, want 0|OK", reply2)
	}

	// Third write with different sha: 4|CONFLICT
	argsB := []any{sprint, label, attempt, token, "v2", "fix", "1", "", "", "0", shaB, "raw_different", "/results"}
	reply3, err := client.FCall(ctx, "ns_card_result", nil, argsB...).Text()
	if err != nil {
		t.Fatalf("fcall 3: %v", err)
	}
	if !strings.HasPrefix(reply3, "4|CONFLICT") {
		t.Fatalf("reply3 = %q, want 4|CONFLICT", reply3)
	}
}
