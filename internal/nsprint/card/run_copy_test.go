package card_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// TestRunCopyResolvesModernRecordWithNoSKeys verifies that card.Run on the
// copy path (Sprint=copies) routes to task:<copy> directly without reading
// s:copies:card:<label>, even when RunConfig.CopyID was unset (#4234).
func TestRunCopyResolvesModernRecordWithNoSKeys(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	copyID := "quack-001~1"
	err := client.HSet(ctx, "task:"+copyID, map[string]any{
		"stream":   "cards",
		"sprint":   "s0",
		"repo":     "mas-bandwidth/nova-tools",
		"base":     "origin/dev",
		"base_sha": "0123456789abcdef0123456789abcdef01234567",
		"est":      "15m",
		"tier":     "flash",
		"where":    "working",
		"token":    "1." + strings.Repeat("e", 32),
		"consumer": "bench:test-bench",
		"attempt":  "1",
	}).Err()
	if err != nil {
		t.Fatal(err)
	}

	// Verify initially zero s:* keys in Redis
	sKeys, err := client.Keys(ctx, "s:*").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(sKeys) != 0 {
		t.Fatalf("expected 0 s:* keys in Redis, got %v", sKeys)
	}

	dir := t.TempDir()
	cfg := card.RunConfig{
		Sprint:     card.CopySprint,
		Label:      "quack-001-c1",
		Attempt:    1,
		Bench:      "test-bench",
		JobDir:     filepath.Join(dir, "job"),
		OutDir:     filepath.Join(dir, "out"),
		Home:       dir,
		HarnessBin: "/bin/echo",
		Deadline:   "10m",
		Tokens:     "1000",
		Yield:      func() error { return nil },
	}

	st := store.New(client)
	rep := card.Run(ctx, st, cfg)

	// Even if runner fails (no runner binary configured), it must NOT fail
	// with "no payload_sha on s:copies:card:..."
	if strings.Contains(rep.Why, "no payload_sha") || strings.Contains(rep.Why, "s:copies:card:") {
		t.Fatalf("copy run attempted to read s:copies:card: why=%q", rep.Why)
	}

	// Assert zero s:* keys were created in Redis
	sKeys, err = client.Keys(ctx, "s:*").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(sKeys) != 0 {
		t.Fatalf("expected 0 s:* keys in Redis after copy run, got %v", sKeys)
	}
}

// TestRunCopyMissingRecordDoesNotSayNoPayloadSHA verifies that when a copy
// record is missing, card.Run refuses with "no copy" rather than falling
// through to an obsolete s:copies:card:<label> reader (#4234).
func TestRunCopyMissingRecordDoesNotSayNoPayloadSHA(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	dir := t.TempDir()
	cfg := card.RunConfig{
		Sprint:     card.CopySprint,
		Label:      "missing-copy-c1",
		Attempt:    1,
		Bench:      "test-bench",
		JobDir:     filepath.Join(dir, "job"),
		OutDir:     filepath.Join(dir, "out"),
		Home:       dir,
		HarnessBin: "/bin/echo",
		Deadline:   "10m",
		Tokens:     "1000",
		Yield:      func() error { return nil },
	}

	st := store.New(client)
	rep := card.Run(ctx, st, cfg)

	if rep.Code != card.RunExitRefused {
		t.Fatalf("expected exit refused (%d), got code=%d", card.RunExitRefused, rep.Code)
	}
	if strings.Contains(rep.Why, "no payload_sha") || strings.Contains(rep.Why, "s:copies:card:") {
		t.Fatalf("refusal must not mention payload_sha or s:copies:card: why=%q", rep.Why)
	}
	if !strings.Contains(rep.Why, "no copy") {
		t.Fatalf("expected refusal mentioning 'no copy', got why=%q", rep.Why)
	}

	// Assert zero s:* keys in Redis
	sKeys, err := client.Keys(ctx, "s:*").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(sKeys) != 0 {
		t.Fatalf("expected 0 s:* keys in Redis, got %v", sKeys)
	}
}
