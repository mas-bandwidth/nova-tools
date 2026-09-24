package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/redisq"
)

func openPullRedisTest(t *testing.T) (*miniredis.Miniredis, *redisq.Queue) {
	t.Helper()
	m := miniredis.RunT(t)
	q, err := redisq.Open(m.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = q.Close() })
	return m, q
}

func pendingPullCard(t *testing.T, q *redisq.Queue, fields map[string]string) *redisq.Card {
	t.Helper()
	ctx := context.Background()
	const stream = "nova:queue:code:red"
	if err := q.EnsureGroup(ctx, stream); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Add(ctx, stream, fields); err != nil {
		t.Fatal(err)
	}
	card, err := q.Pull(ctx, stream, "bench-a", 0)
	if err != nil || card == nil {
		t.Fatalf("pull card: card=%v err=%v", card, err)
	}
	return card
}

func TestPullRedisCompletionUsesTheWorkerFence(t *testing.T) {
	_, q := openPullRedisTest(t)
	ctx := context.Background()
	card := pendingPullCard(t, q, map[string]string{"card": "card-1"})
	old, ok, err := q.TakeLease(ctx, "code", "bench-a", time.Minute)
	if err != nil || !ok {
		t.Fatalf("take old lease: ok=%v err=%v", ok, err)
	}
	if released, err := q.ReleaseLease(ctx, old); err != nil || !released {
		t.Fatalf("release old lease: released=%v err=%v", released, err)
	}
	if _, ok, err := q.TakeLease(ctx, "code", "bench-a", time.Minute); err != nil || !ok {
		t.Fatalf("retake lease: ok=%v err=%v", ok, err)
	}

	if ok, err := q.FencedAck(ctx, old, card.Stream, card.ID); err != nil || ok {
		t.Fatalf("stale worker acknowledgement ok=%v err=%v, want refusal", ok, err)
	}
	pending, err := q.PendingIDs(ctx, card.Stream, "bench-a")
	if err != nil || len(pending) != 1 || pending[0] != card.ID {
		t.Fatalf("stale worker acknowledged the card: pending=%v err=%v", pending, err)
	}
}

func TestPullRedisLivePathAcknowledgesUnderItsWorkerLease(t *testing.T) {
	m, q := openPullRedisTest(t)
	ctx := context.Background()
	card := pendingPullCard(t, q, map[string]string{"card": "card-live"})
	// Put the card back on a fresh group so cmdPull, rather than this fixture,
	// becomes its consumer.
	if err := q.Ack(ctx, card.Stream, card.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Add(ctx, card.Stream, map[string]string{"card": "card-live-2"}); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := invokePull("pull", "--stream", "code", "--lane", "red", "--bench", "bench-live", "--redis", m.Addr())
	if code != 0 || !strings.Contains(stdout, "card=") {
		t.Fatalf("live redis pull: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	pending, err := q.PendingIDs(ctx, card.Stream, "bench-live")
	if err != nil || len(pending) != 0 {
		t.Fatalf("live path left its acknowledged card pending: pending=%v err=%v", pending, err)
	}
	if token, err := q.LeaseToken(ctx, q.LeaseKey("code", "bench-live")); err != nil || token != "" {
		t.Fatalf("live path did not release its worker lease: token=%q err=%v", token, err)
	}
}

func TestPullRedisStillRefusesTwoStores(t *testing.T) {
	m := miniredis.RunT(t)
	root := t.TempDir()
	code, _, stderr := invokePull("pull", "--stream", "code", "--bench", "bench-a", "--redis", m.Addr(), "--dir", root)
	if code != 2 || !strings.Contains(stderr, "two stores") {
		t.Fatalf("redis plus directory: code=%d stderr=%q", code, stderr)
	}
}
