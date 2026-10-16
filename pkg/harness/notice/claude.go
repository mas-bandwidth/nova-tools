package notice

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
)

// Claude is the Claude Code shim (supersedes #5776's adapter). Into a running
// turn and waking an idle session are one mechanism: the Core's one PUSH line
// on the hook's stdout, which the session's Monitor injects. Consumption is
// the installed Stop hook advancing the generation file in State.
type Claude struct {
	State, Notices string
	seen           int
}

// Name is the harness word.
func (c *Claude) Name() string { return "claude" }

// Dir is the private notice directory.
func (c *Claude) Dir() string { return c.Notices }

// Push records the generation the notice met; the Monitor line is the push.
func (c *Claude) Push(context.Context, Notice) (string, error) {
	g, err := ClaudeGeneration(c.State)
	if err != nil {
		return "", err
	}
	c.seen = g
	return "monitor", nil
}

// Consumed is a Stop since the push.
func (c *Claude) Consumed(context.Context, Notice) (bool, error) {
	g, err := ClaudeGeneration(c.State)
	if err != nil {
		return false, err
	}
	if g < c.seen {
		return false, errors.New("claude generation went backwards")
	}
	return g > c.seen, nil
}

// ClaudeGeneration reads the Stop hook's count; a missing file is 0.
func ClaudeGeneration(state string) (int, error) {
	b, err := os.ReadFile(filepath.Join(state, "generation"))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, errors.New("claude generation wants a decimal count")
	}
	return n, nil
}

// InstallClaude writes the Stop hook script into settings and adds it to
// settings.json once. The script runs bin with --claude-boundary, which
// advances the generation file Consumed reads.
func InstallClaude(bin, as, role, state, settings string) error {
	script := filepath.Join(settings, "nova-hook-claude-boundary.sh")
	body := "#!/bin/sh\nexec " + shQuote(bin) + " hook --as " + shQuote(as) + " --role " + shQuote(role) +
		" --harness claude --claude-state " + shQuote(state) + " --claude-settings " + shQuote(settings) + " --claude-boundary\n"
	if err := atomicfile.Write(script, []byte(body), 0o700, atomicfile.ExactMode()); err != nil {
		return err
	}
	return mergeClaudeStopHook(filepath.Join(settings, "settings.json"), shQuote(script))
}

// ClaudeBoundary advances the generation file: the Stop hook's one write.
func ClaudeBoundary(state string) (int, error) {
	n, err := ClaudeGeneration(state)
	if err != nil {
		return 0, err
	}
	n++
	return n, atomicfile.Write(filepath.Join(state, "generation"), []byte(strconv.Itoa(n)+"\n"), 0o600, atomicfile.ExactMode())
}
