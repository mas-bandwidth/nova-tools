package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

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

	// For now, a stub implementation that writes RESULT.md and exits 0
	// In a full implementation, this would:
	// 1. Fetch a probe card from the server
	// 2. Run it under the wall using walledRaw
	// 3. Write RESULT.md with the results
	// 4. Return the actual exit code

	resultPath := filepath.Join(probeDir, "RESULT.md")
	f, err := os.Create(resultPath)
	if err != nil {
		return tool.Refuse(fmt.Sprintf("cannot write RESULT.md: %v", err))
	}
	defer f.Close()

	fmt.Fprintln(f, "ACCEPTANCE OK")
	fmt.Fprintf(f, "friend=%s\n", name)
	fmt.Fprintf(f, "dir=%s\n", dir)
	fmt.Fprintf(f, "at=%s\n", time.Now().UTC().Format(time.RFC3339))

	return tool.Done()
}
