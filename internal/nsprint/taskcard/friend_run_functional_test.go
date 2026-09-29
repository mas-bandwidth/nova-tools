//go:build functional

package taskcard_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
	"github.com/redis/go-redis/v9"
)

func TestFriendRunAndWaitFunctional(t *testing.T) {
	t.Parallel()

	_, c := wstest.Start(t)
	ctx := context.Background()

	friendName := "worker-fn"
	c.SAdd(ctx, "friends", friendName)

	as, err := taskcard.ParseConsumer("friend:" + friendName)
	if err != nil {
		t.Fatalf("parse consumer: %v", err)
	}

	epoch, err := ws.Epoch(ctx, c)
	if err != nil {
		t.Fatalf("read epoch: %v", err)
	}

	copyID := "fn-task-1~1"
	primID := "fn-task-1"
	wtDir := filepath.Join(t.TempDir(), "wt-fn")

	// Setup primary and copy records
	c.HSet(ctx, taskcard.Key(primID),
		"title", "Functional test primary task",
		"where", "working",
		"pr", "acme/repo#99",
	)
	c.HSet(ctx, taskcard.Key(copyID),
		"title", "Functional test copy task",
		"where", "ready",
		"consumer", as.String(),
		"primary", primID,
	)

	// Set copy in ready ZSET
	readyKey := ws.ConsumerKeyAt(epoch, as.String(), "ready")
	c.ZAdd(ctx, readyKey, redis.Z{Score: 1000, Member: copyID})
	c.HSet(ctx, as.DesiredKey(), "slots", "4")

	launcher := func(cmd *exec.Cmd) (int, error) {
		return 9999, nil
	}

	// 1. Run moves ready -> working and writes metadata
	runRes, err := taskcard.Run(ctx, c, taskcard.RunConfig{
		As:       as,
		ID:       copyID,
		Model:    "model-fn",
		Harness:  "harness-fn",
		Worktree: wtDir,
		Launcher: launcher,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if runRes.CopyID != copyID || runRes.PID != 9999 {
		t.Fatalf("unexpected run result: %+v", runRes)
	}

	// Verify copy is now working
	rec := c.HGetAll(ctx, taskcard.Key(copyID)).Val()
	if rec["where"] != "working" {
		t.Fatalf("copy where = %q, want working", rec["where"])
	}
	cpMeta := c.HGetAll(ctx, "copy:"+copyID).Val()
	if cpMeta["model"] != "model-fn" || cpMeta["child"] != "9999" || cpMeta["harness"] != "harness-fn" {
		t.Fatalf("metadata missing on copy record: %+v", cpMeta)
	}

	// Verify PROMPT.md was written
	if _, err := os.Stat(filepath.Join(wtDir, "PROMPT.md")); err != nil {
		t.Fatalf("PROMPT.md missing: %v", err)
	}

	// Write RESULT.md in worktree
	resultContent := "## Summary\nFunctional verification passed completely.\n"
	if err := os.WriteFile(filepath.Join(wtDir, "RESULT.md"), []byte(resultContent), 0o644); err != nil {
		t.Fatalf("write RESULT.md: %v", err)
	}

	// Simulate copy ending: move working -> ok
	workingKey := ws.ConsumerKeyAt(epoch, as.String(), "working")
	okKey := ws.ConsumerKeyAt(epoch, as.String(), "ok")
	c.ZRem(ctx, workingKey, copyID)
	c.ZAdd(ctx, okKey, redis.Z{Score: float64(time.Now().UnixMilli()), Member: copyID})
	c.HSet(ctx, taskcard.Key(copyID),
		"where", "ok",
		"outcome", "ok",
	)

	// Also append move to ws:log
	c.XAdd(ctx, &redis.XAddArgs{
		Stream: "ws:log",
		Values: []any{
			"id", copyID,
			"consumer", as.String(),
			"from", as.String() + ":working",
			"to", as.String() + ":ok",
		},
	})

	// 2. Wait blocks/catches the ended copy and returns its summary and PR
	waitRes, err := taskcard.Wait(ctx, c, taskcard.WaitConfig{
		As:      as,
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	if waitRes.CopyID != copyID || waitRes.Outcome != "ok" || waitRes.PR != "acme/repo#99" {
		t.Fatalf("unexpected wait result: %+v", waitRes)
	}
	if !strings.Contains(waitRes.Result, "Functional verification passed completely") {
		t.Errorf("unexpected summary: %q", waitRes.Result)
	}

	line := waitRes.Line()
	if !strings.Contains(line, copyID+" outcome=ok pr=acme/repo#99") {
		t.Errorf("unexpected Line(): %q", line)
	}
}
