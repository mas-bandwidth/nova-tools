package taskcard_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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

func TestRunArgumentValidation(t *testing.T) {
	t.Parallel()

	_, c := newTestRedis(t)
	ctx := context.Background()

	as, err := taskcard.ParseConsumer("friend:worker-a")
	if err != nil {
		t.Fatalf("parse consumer: %v", err)
	}

	// 1. Non-friend consumer
	benchCons, _ := taskcard.ParseConsumer("bench:b1")
	if _, err := taskcard.Run(ctx, c, taskcard.RunConfig{
		As: benchCons, ID: "t1~1", Model: "m1", Harness: "h1",
	}); err == nil || !strings.Contains(err.Error(), "--as friend:<name> is required") {
		t.Errorf("expected non-friend refusal, got %v", err)
	}

	// 2. Bad copy ID
	if _, err := taskcard.Run(ctx, c, taskcard.RunConfig{
		As: as, ID: "t1", Model: "m1", Harness: "h1",
	}); err == nil || !strings.Contains(err.Error(), "not a copy id") {
		t.Errorf("expected bad copy ID refusal, got %v", err)
	}

	// 3. Missing model
	if _, err := taskcard.Run(ctx, c, taskcard.RunConfig{
		As: as, ID: "t1~1", Model: "", Harness: "h1",
	}); err == nil || !strings.Contains(err.Error(), "--model is required") {
		t.Errorf("expected missing model refusal, got %v", err)
	}

	// 4. Missing harness
	if _, err := taskcard.Run(ctx, c, taskcard.RunConfig{
		As: as, ID: "t1~1", Model: "m1", Harness: "",
	}); err == nil || !strings.Contains(err.Error(), "--harness is required") {
		t.Errorf("expected missing harness refusal, got %v", err)
	}

	// 5. Multi-word model
	if _, err := taskcard.Run(ctx, c, taskcard.RunConfig{
		As: as, ID: "t1~1", Model: "m1 extra", Harness: "h1",
	}); err == nil || !strings.Contains(err.Error(), "--model wants one word") {
		t.Errorf("expected multi-word model refusal, got %v", err)
	}

	// 6. Non-existent copy (NOCOPY)
	if _, err := taskcard.Run(ctx, c, taskcard.RunConfig{
		As: as, ID: "t1~1", Model: "m1", Harness: "h1",
	}); err == nil || !strings.Contains(err.Error(), "NOCOPY") {
		t.Errorf("expected NOCOPY refusal, got %v", err)
	}

	// 7. Not mine copy
	c.HSet(ctx, "task:t1~1", "consumer", "friend:other-worker", "where", "working")
	if _, err := taskcard.Run(ctx, c, taskcard.RunConfig{
		As: as, ID: "t1~1", Model: "m1", Harness: "h1",
	}); err == nil || !strings.Contains(err.Error(), "NOTMINE") {
		t.Errorf("expected NOTMINE refusal, got %v", err)
	}

	// 8. Copy in invalid state
	c.HSet(ctx, "task:t2~1", "consumer", as.String(), "where", "merging")
	if _, err := taskcard.Run(ctx, c, taskcard.RunConfig{
		As: as, ID: "t2~1", Model: "m1", Harness: "h1",
	}); err == nil || !strings.Contains(err.Error(), "not ready or working") {
		t.Errorf("expected invalid where state refusal, got %v", err)
	}
}

func TestRunSuccessAndMetadata(t *testing.T) {
	t.Parallel()

	_, c := newTestRedis(t)
	ctx := context.Background()

	as, err := taskcard.ParseConsumer("friend:worker-b")
	if err != nil {
		t.Fatalf("parse consumer: %v", err)
	}

	copyID := "card-test-1~1"
	wtDir := filepath.Join(t.TempDir(), "wt-1")

	c.HSet(ctx, "task:"+copyID,
		"title", "test task",
		"where", "working",
		"consumer", as.String(),
	)

	launchedCmd := ""
	launcher := func(cmd *exec.Cmd) (int, error) {
		launchedCmd = strings.Join(cmd.Args, " ")
		return 8888, nil
	}

	res, err := taskcard.Run(ctx, c, taskcard.RunConfig{
		As:       as,
		ID:       copyID,
		Model:    "model-alpha",
		Harness:  "harness-alpha",
		Worktree: wtDir,
		Launcher: launcher,
	})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if res.CopyID != copyID || res.PID != 8888 || res.Model != "model-alpha" || res.Harness != "harness-alpha" {
		t.Fatalf("unexpected RunResult: %+v", res)
	}

	if !strings.Contains(launchedCmd, "harness-alpha run --model model-alpha --") {
		t.Errorf("unexpected launched command: %q", launchedCmd)
	}

	// Verify PROMPT.md was written
	promptPath := filepath.Join(wtDir, "PROMPT.md")
	data, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatalf("read prompt: %v", err)
	}
	if !strings.Contains(string(data), copyID) {
		t.Errorf("prompt missing copyID: %s", string(data))
	}

	// Verify metadata recorded on copy:<copyID>
	cpRec := c.HGetAll(ctx, "copy:"+copyID).Val()
	if cpRec["model"] != "model-alpha" || cpRec["harness"] != "harness-alpha" || cpRec["child"] != "8888" || cpRec["pid"] != "8888" {
		t.Errorf("copy record missing metadata: %+v", cpRec)
	}
	if cpRec["worktree"] != wtDir {
		t.Errorf("copy record worktree mismatch: got %q, want %q", cpRec["worktree"], wtDir)
	}
}

func TestWaitArgumentValidation(t *testing.T) {
	t.Parallel()

	_, c := newTestRedis(t)
	ctx := context.Background()

	benchCons, _ := taskcard.ParseConsumer("bench:b1")
	if _, err := taskcard.Wait(ctx, c, taskcard.WaitConfig{As: benchCons}); err == nil || !strings.Contains(err.Error(), "--as friend:<name> is required") {
		t.Errorf("expected non-friend refusal, got %v", err)
	}
}

func TestWaitImmediateIfEnded(t *testing.T) {
	t.Parallel()

	_, c := newTestRedis(t)
	ctx := context.Background()

	as, err := taskcard.ParseConsumer("friend:worker-c")
	if err != nil {
		t.Fatalf("parse consumer: %v", err)
	}

	copyID := "card-test-2~1"
	wtDir := t.TempDir()

	resultMD := "# Success\nAll tests green and verification completed.\n"
	if err := os.WriteFile(filepath.Join(wtDir, "RESULT.md"), []byte(resultMD), 0o644); err != nil {
		t.Fatalf("write RESULT.md: %v", err)
	}

	epoch, _ := ws.Epoch(ctx, c)
	okKey := ws.ConsumerKeyAt(epoch, as.String(), "ok")

	c.ZAdd(ctx, okKey, redis.Z{Score: 1000, Member: copyID})
	c.HSet(ctx, "task:"+copyID,
		"consumer", as.String(),
		"where", "ok",
		"outcome", "ok",
		"pr", "owner/repo#42",
		"worktree", wtDir,
	)

	res, err := taskcard.Wait(ctx, c, taskcard.WaitConfig{
		As: as,
	})
	if err != nil {
		t.Fatalf("wait failed: %v", err)
	}

	if res.CopyID != copyID || res.Outcome != "ok" || res.PR != "owner/repo#42" {
		t.Fatalf("unexpected WaitResult: %+v", res)
	}
	if !strings.Contains(res.Result, "All tests green") {
		t.Errorf("expected result summary from RESULT.md, got %q", res.Result)
	}

	line := res.Line()
	wantPrefix := copyID + " outcome=ok pr=owner/repo#42"
	if !strings.HasPrefix(line, wantPrefix) {
		t.Errorf("line = %q, want prefix %q", line, wantPrefix)
	}

	// Verify notified flag is set
	if c.HGet(ctx, "copy:"+copyID, "notified").Val() != "1" {
		t.Errorf("expected notified flag = 1 on copy:%s", copyID)
	}
}

func TestWaitTimeoutWithInjectedClock(t *testing.T) {
	t.Parallel()

	_, c := newTestRedis(t)
	ctx := context.Background()

	as, err := taskcard.ParseConsumer("friend:worker-d")
	if err != nil {
		t.Fatalf("parse consumer: %v", err)
	}

	var mu sync.Mutex
	fakeTime := time.Unix(1000, 0)
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		fakeTime = fakeTime.Add(200 * time.Millisecond)
		return fakeTime
	}

	_, err = taskcard.Wait(ctx, c, taskcard.WaitConfig{
		As:       as,
		Timeout:  100 * time.Millisecond,
		Clock:    clock,
		Interval: time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("expected timeout error, got %v", err)
	}
}
