package taskcard_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

func newTestRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	return mr, c
}

func TestCardTellAppendsToStreamAndUpdatesLastTold(t *testing.T) {
	t.Parallel()
	_, c := newTestRedis(t)
	ctx := context.Background()

	copyID := "card-1~1"
	copyKey := "task:" + copyID
	if err := c.HSet(ctx, copyKey, "where", "working", "state", "working").Err(); err != nil {
		t.Fatal(err)
	}

	now := time.Unix(1700000000, 0)
	res, err := taskcard.Tell(ctx, c, taskcard.TellRequest{
		CopyID: copyID,
		Text:   "need more tests",
		By:     "reviewer",
		At:     now,
	})
	if err != nil {
		t.Fatalf("Tell failed: %v", err)
	}

	if res.CopyID != copyID {
		t.Errorf("got copyID %s, want %s", res.CopyID, copyID)
	}
	wantStreams := []string{"copy:" + copyID + ":tell", "copy:" + copyID + ":inbox"}
	if len(res.Streams) != 2 || res.Streams[0] != wantStreams[0] || res.Streams[1] != wantStreams[1] {
		t.Errorf("got streams %v, want %v", res.Streams, wantStreams)
	}
	if res.LastTold != now.UnixMilli() {
		t.Errorf("got lastTold %d, want %d", res.LastTold, now.UnixMilli())
	}

	// Verify entries in both streams
	for _, stream := range wantStreams {
		entries, err := c.XRange(ctx, stream, "-", "+").Result()
		if err != nil {
			t.Fatalf("XRange %s failed: %v", stream, err)
		}
		if len(entries) != 1 {
			t.Fatalf("%s has %d entries, want 1", stream, len(entries))
		}
		vals := entries[0].Values
		if vals["text"] != "need more tests" {
			t.Errorf("%s text = %v, want 'need more tests'", stream, vals["text"])
		}
		if vals["by"] != "reviewer" {
			t.Errorf("%s by = %v, want 'reviewer'", stream, vals["by"])
		}
		if vals["at"] != strconv.FormatInt(now.UnixMilli(), 10) {
			t.Errorf("%s at = %v, want %d", stream, vals["at"], now.UnixMilli())
		}
	}

	// Verify last_told hash field on task:<id> and copy:<id>
	toldStr := strconv.FormatInt(now.UnixMilli(), 10)
	if val := c.HGet(ctx, copyKey, "last_told").Val(); val != toldStr {
		t.Errorf("task:%s last_told = %s, want %s", copyID, val, toldStr)
	}
	if val := c.HGet(ctx, "copy:"+copyID, "last_told").Val(); val != toldStr {
		t.Errorf("copy:%s last_told = %s, want %s", copyID, val, toldStr)
	}
}

func TestCardTellRefusesNonWorkingOrMissingCopy(t *testing.T) {
	t.Parallel()
	_, c := newTestRedis(t)
	ctx := context.Background()

	// Missing ID
	if _, err := taskcard.Tell(ctx, c, taskcard.TellRequest{Text: "foo"}); err == nil {
		t.Error("expected error for missing ID")
	}

	// Missing Text
	if _, err := taskcard.Tell(ctx, c, taskcard.TellRequest{CopyID: "c1~1"}); err == nil {
		t.Error("expected error for missing Text")
	}

	// Non-existent copy
	if _, err := taskcard.Tell(ctx, c, taskcard.TellRequest{CopyID: "missing~1", Text: "foo"}); err == nil {
		t.Error("expected error for non-existent copy")
	}

	// Non-working copy (e.g. ready or ok)
	c.HSet(ctx, "task:c2~1", "where", "ready")
	if _, err := taskcard.Tell(ctx, c, taskcard.TellRequest{CopyID: "c2~1", Text: "foo"}); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Errorf("expected not running error, got: %v", err)
	}

	c.HSet(ctx, "task:c3~1", "where", "ok")
	if _, err := taskcard.Tell(ctx, c, taskcard.TellRequest{CopyID: "c3~1", Text: "foo"}); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Errorf("expected not running error, got: %v", err)
	}
}

func TestCardReportRendersSummary(t *testing.T) {
	t.Parallel()
	_, c := newTestRedis(t)
	ctx := context.Background()

	copyID := "task-10~1"
	c.HSet(ctx, "task:"+copyID, map[string]any{
		"primary":   "task-10",
		"consumer":  "friend:worker",
		"where":     "working",
		"model":     "claude",
		"child":     "proc-42",
		"branch":    "feat/branch",
		"commit":    "abc1234",
		"last_told": "1700000000000",
	})

	rep, err := taskcard.Report(ctx, c, copyID)
	if err != nil {
		t.Fatalf("Report failed: %v", err)
	}

	rendered := rep.Render()
	for _, expected := range []string{
		"== CARD REPORT: task-10~1",
		"PRIMARY:   task-10",
		"CONSUMER:  friend:worker",
		"STATE:     working",
		"MODEL:     claude",
		"CHILD:     proc-42",
		"BRANCH:    feat/branch",
		"HEAD:      abc1234",
		"LAST-TOLD: 1700000000000",
		"== PR\nnone",
		"== CI\nSTATUS: -",
		"== READ SCORES\nnone",
		"== RESULT.md\n(no RESULT.md)",
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("rendered output missing %q, got:\n%s", expected, rendered)
		}
	}
}

func TestCardReportWithPRAndCIScores(t *testing.T) {
	t.Parallel()
	_, c := newTestRedis(t)
	ctx := context.Background()

	copyID := "task-20~1"
	c.HSet(ctx, "task:"+copyID, map[string]any{
		"primary":   "task-20",
		"consumer":  "friend:tester",
		"where":     "ok",
		"repo":      "example-org/repo-x",
		"pr":        "55",
		"head":      "headsha99",
		"score":     "10/10",
		"finding":   "clean commit",
		"result_md": "# All Good\nPassed 42 tests",
	})

	c.HSet(ctx, "pr:repo-x:55", map[string]any{
		"pr_title": "Implement feature X",
		"reads":    "SCORE 10/10 by rowan\nSCORE 9/10 by stella",
	})

	c.HSet(ctx, "ci:headsha99", map[string]any{
		"verdict": "pass",
	})

	rep, err := taskcard.Report(ctx, c, copyID)
	if err != nil {
		t.Fatalf("Report failed: %v", err)
	}

	rendered := rep.Render()
	wantPR := "#55: " + "https://" + "github.com/example-org/repo-x/pull/55 (Implement feature X)"
	for _, expected := range []string{
		"== CARD REPORT: task-20~1",
		wantPR,
		"STATUS: pass",
		"SCORE 10/10 by rowan",
		"SCORE 9/10 by stella",
		"SCORE 10/10 finding=clean commit",
		"# All Good\nPassed 42 tests",
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("rendered output missing %q, got:\n%s", expected, rendered)
		}
	}
}

func TestCardReportFromDiskResultMD(t *testing.T) {
	t.Parallel()
	_, c := newTestRedis(t)
	ctx := context.Background()

	tmpDir := t.TempDir()
	content := "## Disk Result\nGenerated by child process"
	if err := os.WriteFile(filepath.Join(tmpDir, "RESULT.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	copyID := "task-30~1"
	c.HSet(ctx, "task:"+copyID, map[string]any{
		"primary": "task-30",
		"where":   "done",
		"results": tmpDir,
	})

	rep, err := taskcard.Report(ctx, c, copyID)
	if err != nil {
		t.Fatalf("Report failed: %v", err)
	}

	if !rep.HasResultMD || rep.ResultMD != content {
		t.Errorf("got resultMD %q, want %q", rep.ResultMD, content)
	}
	if !strings.Contains(rep.Render(), content) {
		t.Errorf("Render missing disk content, got:\n%s", rep.Render())
	}
}

func TestCardListCopiesWithLiveAndAllSets(t *testing.T) {
	t.Parallel()
	_, c := newTestRedis(t)
	ctx := context.Background()

	consumer := "friend:peer"
	now := time.Unix(1700000000, 0)
	epoch := uint64(0)

	// Add working copy
	c.ZAdd(ctx, ws.ConsumerKeyAt(epoch, consumer, "working"), redis.Z{Score: 1, Member: "c1~1"})
	c.HSet(ctx, "task:c1~1", map[string]any{
		"primary":   "c1",
		"where":     "working",
		"model":     "fast-model",
		"child":     "child-1",
		"pr":        "123",
		"ci":        "pass",
		"leased_at": strconv.FormatInt(now.Add(-45*time.Second).UnixMilli(), 10),
		"beat_at":   strconv.FormatInt(now.Add(-10*time.Second).UnixMilli(), 10),
	})

	// Add ok copy
	c.ZAdd(ctx, ws.ConsumerKeyAt(epoch, consumer, "ok"), redis.Z{Score: 2, Member: "c2~1"})
	c.HSet(ctx, "task:c2~1", map[string]any{
		"primary":    "c2",
		"where":      "ok",
		"created_at": strconv.FormatInt(now.Add(-120*time.Second).UnixMilli(), 10),
	})

	// Live = true should return only working set
	liveList, err := taskcard.ListCopies(ctx, c, taskcard.ListCopiesOptions{
		Consumer: consumer,
		Live:     true,
		Epoch:    epoch,
		Now:      now,
	})
	if err != nil {
		t.Fatalf("ListCopies live failed: %v", err)
	}
	if len(liveList) != 1 {
		t.Fatalf("liveList len = %d, want 1", len(liveList))
	}
	if liveList[0].ID != "c1~1" {
		t.Errorf("liveList[0].ID = %s, want c1~1", liveList[0].ID)
	}
	if liveList[0].Age != "45s" {
		t.Errorf("liveList[0].Age = %s, want 45s", liveList[0].Age)
	}
	if liveList[0].LastBeat != "10s" {
		t.Errorf("liveList[0].LastBeat = %s, want 10s", liveList[0].LastBeat)
	}
	if liveList[0].PR != "#123" {
		t.Errorf("liveList[0].PR = %s, want #123", liveList[0].PR)
	}
	if liveList[0].CIStatus != "pass" {
		t.Errorf("liveList[0].CIStatus = %s, want pass", liveList[0].CIStatus)
	}
	wantLine := "COPY c1~1 primary=c1 state=working model=fast-model child=child-1 age=45s beat=10s pr=#123 ci=pass"
	if liveList[0].Line() != wantLine {
		t.Errorf("Line() = %q, want %q", liveList[0].Line(), wantLine)
	}

	// Live = false should return both working and ok
	allList, err := taskcard.ListCopies(ctx, c, taskcard.ListCopiesOptions{
		Consumer: consumer,
		Live:     false,
		Epoch:    epoch,
		Now:      now,
	})
	if err != nil {
		t.Fatalf("ListCopies all failed: %v", err)
	}
	if len(allList) != 2 {
		t.Fatalf("allList len = %d, want 2", len(allList))
	}
}

func TestCardListCopiesRefused(t *testing.T) {
	t.Parallel()
	_, c := newTestRedis(t)
	ctx := context.Background()

	if _, err := taskcard.ListCopies(ctx, c, taskcard.ListCopiesOptions{}); err == nil {
		t.Error("expected refusal for missing consumer")
	}
}
