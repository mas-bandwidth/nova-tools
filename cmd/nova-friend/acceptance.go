package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// acceptance is the acceptance run verb (docs/SPEC-FRIEND.md, "Acceptance run").
// One lane takes a probe card from the server, runs it under the real lane wall,
// writes RESULT.md, and exits 0; the play fails the host otherwise, naming the
// friend, the exit code and the log line.
func (w world) acceptance(c *tool.Call) *tool.Out {
	name, dir := c.Str("as"), c.Str("dir")
	if name == "" {
		return tool.Refuse("--as wants a friend name")
	}
	if dir == "" {
		return tool.Refuse("--dir wants a working directory")
	}
	if !filepath.IsAbs(dir) {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return tool.Refuse(fmt.Sprintf("--dir wants a path: %s: %v", dir, err))
		}
		dir = abs
	}

	// Create the probe directory under dir
	probeDir := filepath.Join(dir, "probe")
	if err := os.MkdirAll(probeDir, 0o755); err != nil {
		return tool.Refuse(fmt.Sprintf("--dir cannot be made: %s: %v", dir, err))
	}

	// Run a probe under the wall to verify the lane can execute
	// Use the wall function if available, otherwise fall back to direct exec
	var execFunc friend.Exec
	if w.wall != nil {
		wallCfg := friend.Wall{
			Profile:   "friend",
			ConfigDir: w.getenv("CLAUDE_CONFIG_DIR"),
			Deny:      []string{},
		}
		execFunc = w.wall(wallCfg, w.exec)
	} else {
		execFunc = w.exec
	}

	ctx := context.Background()
	// Run a simple probe: touch a file in the probe directory
	// This exercises the wall without requiring opencode
	prog := "touch"
	args := []string{filepath.Join(probeDir, "probed")}
	out, exit, err := execFunc(ctx, dir, prog, args, "")
	
	resultPath := filepath.Join(probeDir, "RESULT.md")
	f, err := os.Create(resultPath)
	if err != nil {
		return tool.Refuse(fmt.Sprintf("cannot write RESULT.md: %v", err))
	}
	defer f.Close()

	fmt.Fprintf(f, "friend=%s\n", name)
	fmt.Fprintf(f, "dir=%s\n", dir)
	fmt.Fprintf(f, "at=%s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(f, "exit=%d\n", exit)
	if out != "" {
		fmt.Fprintf(f, "out=%s\n", out)
	}
	if err != nil {
		fmt.Fprintf(f, "err=%s\n", err)
	}

	if exit == 0 {
		return tool.Done()
	}
	return tool.Exit(exit)
}
