//go:build functional

package taskcard_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

func newRealRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestFunctionalCardTell(t *testing.T) {
	t.Parallel()
	c := newRealRedis(t)
	ctx := context.Background()

	copyID := "f-copy-1~1"
	copyKey := "task:" + copyID
	if err := c.HSet(ctx, copyKey, "where", "working", "state", "working").Err(); err != nil {
		t.Fatal(err)
	}

	res, err := taskcard.Tell(ctx, c, taskcard.TellRequest{
		CopyID: copyID,
		Text:   "real redis stream message",
		By:     "f-reviewer",
	})
	if err != nil {
		t.Fatalf("Tell failed: %v", err)
	}
	if res.CopyID != copyID {
		t.Errorf("got copyID %s, want %s", res.CopyID, copyID)
	}

	entries, err := c.XRange(ctx, "copy:"+copyID+":tell", "-", "+").Result()
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected 1 entry in stream, got %v (err: %v)", entries, err)
	}
	if entries[0].Values["text"] != "real redis stream message" {
		t.Errorf("stream text = %v, want 'real redis stream message'", entries[0].Values["text"])
	}
}

func TestFunctionalCardReport(t *testing.T) {
	t.Parallel()
	c := newRealRedis(t)
	ctx := context.Background()

	tmpDir := t.TempDir()
	content := "## Functional Result\nAll passed on real redis"
	if err := os.WriteFile(filepath.Join(tmpDir, "RESULT.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	copyID := "f-copy-2~1"
	c.HSet(ctx, "task:"+copyID, map[string]any{
		"primary":  "f-copy-2",
		"consumer": "friend:worker",
		"where":    "ok",
		"model":    "test-model",
		"repo":     "example-org/repo-f",
		"pr":       "88",
		"head":     "sha88",
		"results":  tmpDir,
	})

	c.HSet(ctx, "pr:repo-f:88", map[string]any{
		"pr_title": "PR 88 title",
		"reads":    "SCORE 10/10 by reviewer",
	})
	c.HSet(ctx, "ci:sha88", map[string]any{
		"verdict": "pass",
	})

	rep, err := taskcard.Report(ctx, c, copyID)
	if err != nil {
		t.Fatalf("Report failed: %v", err)
	}
	rendered := rep.Render()

	wantPR := "#88: " + "https://" + "github.com/example-org/repo-f/pull/88 (PR 88 title)"
	for _, expected := range []string{
		"== CARD REPORT: f-copy-2~1",
		"PRIMARY:   f-copy-2",
		"CONSUMER:  friend:worker",
		"STATE:     ok",
		"MODEL:     test-model",
		wantPR,
		"STATUS: pass",
		"SCORE 10/10 by reviewer",
		"## Functional Result",
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("missing %q in rendered output:\n%s", expected, rendered)
		}
	}
}

func TestFunctionalCardListCopies(t *testing.T) {
	t.Parallel()
	c := newRealRedis(t)
	ctx := context.Background()

	consumer := "friend:f-worker"
	now := time.Now()

	c.ZAdd(ctx, ws.ConsumerKeyAt(0, consumer, "working"), redis.Z{Score: 1, Member: "c-live~1"})
	c.HSet(ctx, "task:c-live~1", map[string]any{
		"primary":   "c-live",
		"where":     "working",
		"model":     "model-live",
		"child":     "child-live",
		"leased_at": strconv.FormatInt(now.Add(-20*time.Second).UnixMilli(), 10),
		"beat_at":   strconv.FormatInt(now.Add(-3*time.Second).UnixMilli(), 10),
	})

	c.ZAdd(ctx, ws.ConsumerKeyAt(0, consumer, "ok"), redis.Z{Score: 2, Member: "c-ok~1"})
	c.HSet(ctx, "task:c-ok~1", map[string]any{
		"primary":    "c-ok",
		"where":      "ok",
		"model":      "model-ok",
		"child":      "child-ok",
		"created_at": strconv.FormatInt(now.Add(-60*time.Second).UnixMilli(), 10),
	})

	// Live only
	liveList, err := taskcard.ListCopies(ctx, c, taskcard.ListCopiesOptions{
		Consumer: consumer,
		Live:     true,
		Now:      now,
	})
	if err != nil {
		t.Fatalf("ListCopies live failed: %v", err)
	}
	if len(liveList) != 1 || liveList[0].ID != "c-live~1" {
		t.Fatalf("expected [c-live~1], got %v", liveList)
	}

	// All
	allList, err := taskcard.ListCopies(ctx, c, taskcard.ListCopiesOptions{
		Consumer: consumer,
		Live:     false,
		Now:      now,
	})
	if err != nil {
		t.Fatalf("ListCopies all failed: %v", err)
	}
	if len(allList) != 2 {
		t.Fatalf("expected 2 copies, got %v", allList)
	}
}
