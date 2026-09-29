package taskcard

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/brief"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

// RunConfig configures card run for a friend's copy (#4351 Item A).
type RunConfig struct {
	As       Consumer                         // consumer identity (must be friend:<name>)
	ID       string                           // copy ID, e.g. "task-1~1"
	Model    string                           // model identity
	Harness  string                           // harness executable or path
	ChildID  string                           // optional child id
	Repo     string                           // optional base repo dir to clone/worktree from
	Worktree string                           // optional explicit worktree dir
	By       string                           // optional actor
	Now      func() time.Time                 // optional clock seam
	Launcher func(cmd *exec.Cmd) (int, error) // optional launcher seam for tests
}

// RunResult is the receipt of a successful card run.
type RunResult struct {
	CopyID   string
	PID      int
	Model    string
	Harness  string
	Worktree string
}

// Run launches the friend's harness child on this machine with the rendered
// card as its prompt and a worktree per copy, records model, harness, and child
// PID on the copy in Redis, and returns at once (#4351 Item A).
func Run(ctx context.Context, c redis.Cmdable, cfg RunConfig) (*RunResult, error) {
	if cfg.As.Kind != "friend" || cfg.As.Name == "" {
		return nil, &Refused{Why: "run: --as friend:<name> is required"}
	}
	if !IsCopy(cfg.ID) {
		return nil, &Refused{Why: fmt.Sprintf("run: %q is not a copy id (<primary>~<n>)", cfg.ID)}
	}
	who := Who{Model: cfg.Model, Harness: cfg.Harness}
	if cfg.Model == "" {
		return nil, &Refused{Why: "run: --model is required"}
	}
	if cfg.Harness == "" {
		return nil, &Refused{Why: "run: --harness is required"}
	}
	if err := who.Check(); err != nil {
		return nil, &Refused{Why: err.Error()}
	}

	rec, err := c.HGetAll(ctx, Key(cfg.ID)).Result()
	if err != nil {
		return nil, err
	}
	if len(rec) == 0 {
		return nil, &Refused{Why: "NOCOPY task:" + cfg.ID}
	}

	if recCons := rec["consumer"]; recCons != "" && recCons != cfg.As.String() {
		return nil, &Refused{Why: fmt.Sprintf("NOTMINE task:%s is %s's copy, not %s", cfg.ID, recCons, cfg.As)}
	}

	where := rec["where"]
	if where == "" {
		return nil, &Refused{Why: fmt.Sprintf("copy %s has no where status", cfg.ID)}
	}
	if where != "ready" && where != "working" {
		return nil, &Refused{Why: fmt.Sprintf("copy %s is %s, not ready or working", cfg.ID, where)}
	}

	by := cfg.By
	if by == "" {
		by = cfg.As.String()
	}

	// Move copy from ready to working if needed.
	if where == "ready" {
		worked, err := WorkAs(ctx, c, cfg.As, by, 1, false, who, cfg.ID)
		if err != nil {
			return nil, err
		}
		if len(worked.IDs) == 0 {
			return nil, &Refused{Why: fmt.Sprintf("failed to take copy %s into working", cfg.ID)}
		}
	}

	// Determine worktree directory.
	wtDir := cfg.Worktree
	if wtDir == "" {
		baseDir := os.Getenv("NOVA_WORKTREE_DIR")
		if baseDir == "" {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				homeDir = os.TempDir()
			}
			baseDir = filepath.Join(homeDir, ".nova-friend", cfg.As.Name, "worktrees")
		}
		wtDir = filepath.Join(baseDir, cfg.ID)
	}

	if err := os.MkdirAll(wtDir, 0o755); err != nil {
		return nil, fmt.Errorf("create worktree dir: %w", err)
	}

	// Setup git worktree if base repo is available.
	repoDir := cfg.Repo
	if repoDir != "" {
		if _, err := os.Stat(filepath.Join(wtDir, ".git")); os.IsNotExist(err) {
			baseRef := rec["commit"]
			if baseRef == "" {
				baseRef = rec["head"]
			}
			if baseRef == "" {
				baseRef = rec["base_sha"]
			}
			if baseRef == "" {
				baseRef = "HEAD"
			}
			_ = exec.Command("git", "-C", repoDir, "worktree", "add", "--detach", wtDir, baseRef).Run()
		}
	}

	// Render prompt file.
	var promptBytes []byte
	if b, err := brief.RenderCard(cfg.ID, rec, cfg.Model); err == nil && len(b) > 0 {
		promptBytes = b
	} else if h, err := RenderHeader(cfg.ID, rec); err == nil && len(h) > 0 {
		promptBytes = h
	} else {
		promptBytes = []byte(fmt.Sprintf("# Card %s\n\nTask: %s\nConsumer: %s\nModel: %s\n", cfg.ID, rec["title"], cfg.As, cfg.Model))
	}

	promptPath := filepath.Join(wtDir, "PROMPT.md")
	if err := os.WriteFile(promptPath, promptBytes, 0o644); err != nil {
		return nil, fmt.Errorf("write prompt: %w", err)
	}

	// Build and launch the harness command.
	var pid int
	args := []string{"run", "--model", cfg.Model, "--", promptPath}
	cmd := exec.Command(cfg.Harness, args...)
	cmd.Dir = wtDir

	if cfg.Launcher != nil {
		var lErr error
		pid, lErr = cfg.Launcher(cmd)
		if lErr != nil {
			return nil, fmt.Errorf("launch harness: %w", lErr)
		}
	} else {
		logFile, err := os.OpenFile(filepath.Join(wtDir, "harness.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err == nil {
			cmd.Stdout = logFile
			cmd.Stderr = logFile
			defer logFile.Close()
		}
		if err := cmd.Start(); err != nil {
			return nil, fmt.Errorf("launch harness %s: %w", cfg.Harness, err)
		}
		pid = cmd.Process.Pid
		go func() { _ = cmd.Wait() }()
	}

	// Record metadata in Redis.
	pidStr := strconv.Itoa(pid)
	now := time.Now()
	if cfg.Now != nil {
		now = cfg.Now()
	}
	msStr := strconv.FormatInt(now.UnixMilli(), 10)

	childID := cfg.ChildID
	if childID == "" {
		childID = pidStr
	}

	pipe := c.Pipeline()
	pipe.HSet(ctx, "copy:"+cfg.ID,
		"model", cfg.Model,
		"harness", cfg.Harness,
		"child", childID,
		"pid", pidStr,
		"worktree", wtDir,
		"launched_at", msStr,
	)
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("record copy metadata: %w", err)
	}

	return &RunResult{
		CopyID:   cfg.ID,
		PID:      pid,
		Model:    cfg.Model,
		Harness:  cfg.Harness,
		Worktree: wtDir,
	}, nil
}

// WaitConfig configures card wait (#4351 Item A).
type WaitConfig struct {
	As       Consumer         // consumer identity (must be friend:<name>)
	Timeout  time.Duration    // optional timeout (0 = block until copy ends)
	Clock    func() time.Time // clock seam for testing
	Interval time.Duration    // polling interval (default 50ms)
}

// WaitResult is the notification result when a copy ends.
type WaitResult struct {
	CopyID   string
	Outcome  string // ok | fail
	PR       string // PR number or repo#n
	Result   string // summary of RESULT.md or evidence
	Worktree string
}

// Line formats the notification line: <copy> outcome=... pr=... result=...
func (w *WaitResult) Line() string {
	pr := w.PR
	if pr == "" {
		pr = "-"
	}
	res := w.Result
	if res == "" {
		res = "-"
	}
	outcome := w.Outcome
	if outcome == "" {
		outcome = "-"
	}
	return fmt.Sprintf("%s outcome=%s pr=%s result=%s", w.CopyID, outcome, pr, res)
}

// Wait blocks on the bus / Redis queue until any of the friend's copies ends and
// returns its RESULT.md summary and PR (#4351 Item A).
func Wait(ctx context.Context, c redis.Cmdable, cfg WaitConfig) (*WaitResult, error) {
	if cfg.As.Kind != "friend" || cfg.As.Name == "" {
		return nil, &Refused{Why: "wait: --as friend:<name> is required"}
	}
	nowFn := cfg.Clock
	if nowFn == nil {
		nowFn = time.Now
	}
	start := nowFn()
	interval := cfg.Interval
	if interval <= 0 {
		interval = 50 * time.Millisecond
	}

	epoch, _ := ws.Epoch(ctx, c)
	okKey := ws.ConsumerKeyAt(epoch, cfg.As.String(), "ok")
	failKey := ws.ConsumerKeyAt(epoch, cfg.As.String(), "fail")

	lastID := "$"
	if revs, err := c.XRevRangeN(ctx, "ws:log", "+", "-", 1).Result(); err == nil && len(revs) > 0 {
		lastID = revs[0].ID
	}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if cfg.Timeout > 0 && nowFn().Sub(start) >= cfg.Timeout {
			return nil, &Refused{Why: "timeout"}
		}

		// Check ws:log stream with XRead.
		readTimeout := interval
		if cfg.Timeout > 0 {
			remaining := cfg.Timeout - nowFn().Sub(start)
			if remaining <= 0 {
				return nil, &Refused{Why: "timeout"}
			}
			if remaining < readTimeout {
				readTimeout = remaining
			}
		}

		streams, err := c.XRead(ctx, &redis.XReadArgs{
			Streams: []string{"ws:log", lastID},
			Count:   50,
			Block:   readTimeout,
		}).Result()

		if err == nil {
			for _, stream := range streams {
				for _, msg := range stream.Messages {
					lastID = msg.ID
					id, _ := msg.Values["id"].(string)
					to, _ := msg.Values["to"].(string)
					consumer, _ := msg.Values["consumer"].(string)
					if id == "" {
						continue
					}
					if consumer == cfg.As.String() || strings.HasPrefix(to, cfg.As.String()+":ok") || strings.HasPrefix(to, cfg.As.String()+":fail") {
						if strings.Contains(to, "ok") || strings.Contains(to, "fail") {
							return inspectEndedCopy(ctx, c, id)
						}
					}
				}
			}
		}

		// Also check ok and fail sets directly.
		for _, key := range []string{okKey, failKey} {
			members, _ := c.ZRange(ctx, key, 0, -1).Result()
			for _, m := range members {
				notified := c.HGet(ctx, "copy:"+m, "notified").Val()
				if notified == "1" {
					continue
				}
				rec, _ := c.HGetAll(ctx, Key(m)).Result()
				cpRec, _ := c.HGetAll(ctx, "copy:"+m).Result()
				where := rec["where"]
				if where == "" && cpRec != nil {
					where = cpRec["outcome"]
				}
				if where == "ok" || where == "fail" {
					return inspectEndedCopy(ctx, c, m)
				}
			}
		}

		if cfg.Timeout > 0 && nowFn().Sub(start) >= cfg.Timeout {
			return nil, &Refused{Why: "timeout"}
		}

		// Short pause if XRead did not block (or on next loop iteration).
		if cfg.Clock != nil {
			// In synthetic clock tests, yield execution.
			time.Sleep(time.Millisecond)
		}
	}
}

func inspectEndedCopy(ctx context.Context, c redis.Cmdable, copyID string) (*WaitResult, error) {
	rec, err := c.HGetAll(ctx, Key(copyID)).Result()
	if err != nil {
		return nil, err
	}
	cpRec, _ := c.HGetAll(ctx, "copy:"+copyID).Result()
	if len(rec) == 0 && len(cpRec) == 0 {
		return nil, &Refused{Why: "NOCOPY task:" + copyID}
	}
	if len(rec) == 0 {
		rec = cpRec
	}
	_ = c.HSet(ctx, "copy:"+copyID, "notified", "1").Err()

	outcome := rec["outcome"]
	if outcome == "" && cpRec != nil {
		outcome = cpRec["outcome"]
	}
	if outcome == "" {
		outcome = rec["where"]
	}
	pr := rec["pr"]
	if pr == "" && cpRec != nil {
		pr = cpRec["pr"]
	}
	if pr == "" && rec["primary"] != "" {
		prim, _ := c.HGetAll(ctx, Key(rec["primary"])).Result()
		pr = prim["pr"]
	}
	wtDir := rec["worktree"]
	if wtDir == "" && cpRec != nil {
		wtDir = cpRec["worktree"]
	}
	resultSummary := rec["result"]
	if resultSummary == "" && cpRec != nil {
		resultSummary = cpRec["result"]
	}
	if resultSummary == "" {
		resultSummary = rec["evidence"]
	}
	if resultSummary == "" {
		resultSummary = rec["why"]
	}

	if wtDir != "" {
		resPath := filepath.Join(wtDir, "RESULT.md")
		if data, err := os.ReadFile(resPath); err == nil {
			content := strings.TrimSpace(string(data))
			lines := strings.Split(content, "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				if strings.HasPrefix(line, "#") {
					if resultSummary == "" || resultSummary == "-" {
						resultSummary = strings.TrimSpace(strings.TrimLeft(line, "#"))
					}
					continue
				}
				resultSummary = line
				break
			}
		}
	}
	if resultSummary == "" {
		resultSummary = "-"
	}

	return &WaitResult{
		CopyID:   copyID,
		Outcome:  outcome,
		PR:       pr,
		Result:   resultSummary,
		Worktree: wtDir,
	}, nil
}
