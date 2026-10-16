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

// Push records the generation the notice met and names the notice's file as
// pending for the session's hooks (claude_hooks.go); the Monitor line is the
// push.
func (c *Claude) Push(_ context.Context, n Notice) (string, error) {
	g, err := ClaudeGeneration(c.State)
	if err != nil {
		return "", err
	}
	if err := writeWord(filepath.Join(c.State, claudePending), n.Path); err != nil {
		return "", err
	}
	c.seen = g
	return "monitor", nil
}

// ClaudeEvents is the claude route's event file in its state directory: each
// PUSH line the Core says, which the watcher follows (claudeEventsMax bounds
// it; the watcher reads a replaced file from its start).
const ClaudeEvents = "events.log"

const claudeEventsMax = 1 << 20

// Record appends one said line to the event file.
func (c *Claude) Record(text string) error {
	path := filepath.Join(c.State, ClaudeEvents)
	if fi, err := os.Stat(path); err == nil && fi.Size()+int64(len(text)) > claudeEventsMax {
		return atomicfile.Write(path, []byte(text), 0o600, atomicfile.ExactMode())
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(text); err != nil {
		// ignored: the write error is the one returned
		_ = f.Close()
		return err
	}
	return f.Close()
}

// Clear ends the pending notice for the session's hooks once it is consumed.
func (c *Claude) Clear(context.Context, Notice) error {
	if err := os.Remove(filepath.Join(c.State, claudePending)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
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

// ClaudeHookEvents are the Claude Code events the boundary script answers
// (claude_hooks.go): its input names which.
var ClaudeHookEvents = []string{"Stop", "PostToolUse", "SessionStart"}

// ClaudeWatchScript is the watcher's script in settings: the one command a
// session's Monitor runs.
func ClaudeWatchScript(settings string) string {
	return filepath.Join(settings, "nova-hook-claude-watch.sh")
}

// InstallClaude writes the boundary and watch scripts into settings and adds
// the boundary script to settings.json once for each of ClaudeHookEvents. The
// boundary script runs bin with --claude-boundary, which reads the event from
// its input: a Stop advances the generation file Consumed reads. The watch
// script runs bin with --claude-watch, under the session's Monitor.
func InstallClaude(bin, as, role, state, settings string) error {
	run := "#!/bin/sh\nexec " + shQuote(bin) + " hook --as " + shQuote(as) + " --role " + shQuote(role) +
		" --harness claude --claude-state " + shQuote(state) + " --claude-settings " + shQuote(settings)
	script := filepath.Join(settings, "nova-hook-claude-boundary.sh")
	if err := atomicfile.Write(script, []byte(run+" --claude-boundary\n"), 0o700, atomicfile.ExactMode()); err != nil {
		return err
	}
	if err := atomicfile.Write(ClaudeWatchScript(settings), []byte(run+" --claude-watch\n"), 0o700, atomicfile.ExactMode()); err != nil {
		return err
	}
	for _, event := range ClaudeHookEvents {
		if err := mergeClaudeHook(filepath.Join(settings, "settings.json"), event, shQuote(script)); err != nil {
			return err
		}
	}
	// The session's commands, the model's nova-hook prove among them, run with
	// this env: prove records the proved nonce where the hooks read it.
	return mergeClaudeEnv(filepath.Join(settings, "settings.json"), ClaudeStateEnv, state)
}

// ClaudeStateEnv names the claude state directory in a session's environment.
const ClaudeStateEnv = "NOVA_HOOK_CLAUDE_STATE"

// ClaudeChildEnv names the environment variable set on a headless claude child
// (a friend lane's card, read or harness proof). Its Stop hook still runs when
// the child's CLAUDE_CONFIG_DIR is the directory the session's Stop hook was
// installed in, and the boundary then leaves the generation alone: a child's
// end is no turn of the session's (2026-10-16: the daemon's proof advanced the
// session's generation every minute, each counting as the session consuming a
// notice it never saw).
const ClaudeChildEnv = "NOVA_HOOK_CLAUDE_CHILD"

// ClaudeBoundary advances the generation file: the Stop hook's one write.
func ClaudeBoundary(state string) (int, error) {
	n, err := ClaudeGeneration(state)
	if err != nil {
		return 0, err
	}
	n++
	return n, atomicfile.Write(filepath.Join(state, "generation"), []byte(strconv.Itoa(n)+"\n"), 0o600, atomicfile.ExactMode())
}
