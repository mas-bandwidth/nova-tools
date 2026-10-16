package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/filelock"
	"github.com/mas-bandwidth/nova-tools/pkg/harness/notice"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// claudeRoute is --harness claude's flags: the generation file's directory,
// the settings directory the Stop hook goes in, and whether this run is the
// Stop hook itself.
type claudeRoute struct {
	state, settings string
	boundary, watch bool
}

// claudeConfig checks the Claude Code route before any hook record is written.
func claudeConfig(c *tool.Call, native *codexRoute) (*claudeRoute, error) {
	if native != nil {
		return nil, errors.New("claude delivery and codex delivery do not run in one hook")
	}
	if c.Bool("once") && !c.Bool("claude-boundary") && !c.Bool("claude-watch") {
		return nil, errors.New("claude delivery wants a running hook client; omit --once")
	}
	state, err := existingDir(c.Str("claude-state"), "--claude-state")
	if err != nil {
		return nil, err
	}
	settings, err := existingDir(c.Str("claude-settings"), "--claude-settings")
	if err != nil {
		return nil, err
	}
	r := &claudeRoute{state: state, settings: settings, boundary: c.Bool("claude-boundary"), watch: c.Bool("claude-watch")}
	if r.boundary && r.watch {
		return nil, errors.New("--claude-boundary and --claude-watch are two commands; give one")
	}
	return r, nil
}

func existingDir(path, flag string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("claude delivery wants %s", flag)
	}
	clean := filepath.Clean(path)
	info, err := os.Stat(clean)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("claude delivery wants %s to be an existing directory", flag)
	}
	return clean, nil
}

// installClaude installs the Stop hook and the notice directory, then
// answers the shim.
func (w world) installClaude(c *tool.Call, r *claudeRoute, role string) (*tool.Out, notice.Shim) {
	bin, err := os.Executable()
	if err == nil && w.claudeInstall != nil {
		err = w.claudeInstall(bin, c.Str("as"), role, r.state, r.settings)
	} else if err == nil {
		err = errors.New("no installer")
	}
	notices := ""
	if err == nil {
		notices, err = notice.MakeDir(filepath.Join(r.state, "notices"))
	}
	if err != nil {
		return tool.Refuse("claude setup could not be installed: " + err.Error()), nil
	}
	line(c, tool.Done().As("STATE").Fact("state", "claude-installed").Fact("settings", r.settings).Note("a Monitor on this process's stdout delivers each PUSH line; the Stop hook is the turn boundary"))
	return nil, &notice.Claude{State: r.state, Notices: notices}
}

// claudeWatchEvery is how often the watcher reads the hook service's log.
const claudeWatchEvery = 250 * time.Millisecond

// claudeHook answers one Claude Code hook event (the boundary script's
// input; tla/ClaudeHookNotice.tla). A headless claude child's event is no
// event of the session's: nothing is read or written for it.
func (w world) claudeHook(c *tool.Call, r *claudeRoute) *tool.Out {
	e, err := notice.ReadClaudeEvent(c.Stdin)
	if err != nil {
		return claudeHookFail(err.Error())
	}
	if w.getenv(notice.ClaudeChildEnv) != "" {
		if e.Name != "Stop" {
			return tool.Exit(0)
		}
		n, err := notice.ClaudeGeneration(r.state)
		if err != nil {
			return claudeHookFail("claude generation could not be read: " + err.Error())
		}
		return tool.Done().As("STATE").Fact("state", "boundary-skipped").Fact("generation", n).Note("a headless claude child's Stop (" + notice.ClaudeChildEnv + " set) is no turn of the session's")
	}
	watch := notice.ClaudeWatchScript(r.settings)
	switch e.Name {
	case "Stop":
		d, err := notice.ClaudeStop(r.state, watch)
		if err != nil {
			return claudeHookFail("claude stop could not be decided: " + err.Error())
		}
		if d.Block {
			return claudeJSON(c, map[string]any{"decision": "block", "reason": d.Reason})
		}
		return tool.Done().As("STATE").Fact("state", "boundary").Fact("generation", d.Generation)
	case "PostToolUse":
		text, err := notice.ClaudeToolUse(r.state)
		if err != nil {
			return claudeHookFail("claude pending challenge could not be read: " + err.Error())
		}
		if text == "" {
			return tool.Exit(0)
		}
		return claudeJSON(c, map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "PostToolUse", "additionalContext": text}})
	case "SessionStart":
		text, err := notice.ClaudeStart(r.state, watch)
		if err != nil {
			return claudeHookFail("claude session start could not be told: " + err.Error())
		}
		return claudeJSON(c, map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "SessionStart", "additionalContext": text}})
	}
	return tool.Exit(0) // an event this route did not install
}

// claudeHookFail is a hook's failure: exit 1, which Claude Code shows and never
// treats as a block. A Refuse's exit 2 would block a Stop, and a failure that
// stands would then keep the model from ever ending its turn.
func claudeHookFail(why string) *tool.Out {
	return tool.Fail(why)
}

// claudeJSON writes a hook's one JSON answer, the whole of its output.
func claudeJSON(c *tool.Call, v any) *tool.Out {
	b, err := json.Marshal(v)
	if err != nil {
		return claudeHookFail(err.Error())
	}
	fmt.Fprintf(c.Stdout, "%s\n", b)
	return tool.Exit(0)
}

// claudeLog is the file the watcher follows: --claude-log, else the claude
// route's own event file in its state directory, written on every system
// (newCore), where the service's stdout may be a journal.
func (w world) claudeLog(c *tool.Call, r *claudeRoute) string {
	if p := c.Str("claude-log"); p != "" {
		return filepath.Clean(p)
	}
	return filepath.Join(r.state, notice.ClaudeEvents)
}

// claudeWatch is the watcher a session's Monitor runs: it holds the watch
// lock (the Stop hook's sign that pushes can wake this session) and prints
// each line of the hook service's log a model acts on, from the log's end,
// until its Monitor ends it.
func (w world) claudeWatch(c *tool.Call, r *claudeRoute) *tool.Out {
	log := w.claudeLog(c, r)
	l, err := notice.ClaudeWatch(r.state)
	if err != nil {
		if _, held := filelock.AsHeldError(err); held {
			return tool.Refuse("another watcher already holds " + filepath.Join(r.state, notice.ClaudeWatchLock) + "; one Monitor at a time: keep that one, or stop it before arming this")
		}
		return tool.Refuse("claude watch lock: " + err.Error())
	}
	// ignored: the lock is the kernel's flock; it goes with the descriptor when this process ends next
	defer func() { _ = l.Unlock() }()
	t := &claudeTail{path: log}
	if err := t.start(); err != nil {
		return tool.Refuse("claude watch: " + err.Error())
	}
	line(c, tool.Done().As("STATE").Fact("state", "watching").Fact("log", log).Note("each PUSH line is a notice for the model; only the model proves"))
	// A notice pushed before this watcher held the lock was never seen: the log
	// is read from its end, so the pending one is told now.
	if p, err := notice.ReadClaudePending(r.state); err == nil && p.Path != "" {
		line(c, tool.Done().As("PENDING").Fact("nonce", cmpOr(p.Unproved(), "-")).Fact("notice", tool.Text(notice.Notice{Path: p.Path}.Text())).Note("pushed before this watcher was armed; only the model proves"))
		w.claudeShown(c, r, p.Path)
	}
	tick := time.NewTicker(claudeWatchEvery)
	defer tick.Stop()
	failing := "" // the read failure last said: each is said once, each line being a notification
	for {
		select {
		case <-c.Ctx.Done():
			return tool.Exit(0)
		case <-tick.C:
		}
		lines, err := t.next()
		if err != nil {
			if err.Error() != failing {
				failing = err.Error()
				line(c, tool.Done().As("STATE").Fact("state", "watch-read-failed").Note(failing+"; read again each tick, said again only when it changes"))
			}
			continue
		}
		if failing != "" {
			failing = ""
			line(c, tool.Done().As("STATE").Fact("state", "watching").Fact("log", log))
		}
		for _, s := range lines {
			if claudeWatched(s) {
				fmt.Fprintln(c.Stdout, s)
				if p := notice.ClaudeNoticePath(s); p != "" {
					w.claudeShown(c, r, p)
				}
			}
		}
	}
}

// claudeShown records a notice the watcher just printed: its delivery to the
// session, which the Stop hook reads before a turn's end consumes it.
func (w world) claudeShown(c *tool.Call, r *claudeRoute, path string) {
	if err := notice.ClaudeShown(r.state, path); err != nil {
		line(c, tool.Done().As("STATE").Fact("state", "claude-shown-unwritten").Note(err.Error()+"; the Stop hook names this notice once more"))
	}
}

func cmpOr(s, or string) string {
	if s == "" {
		return or
	}
	return s
}

// claudeWatched is a service log line the model acts on: every HOOK line but
// the notes, which are the service's own commentary.
func claudeWatched(s string) bool {
	return strings.HasPrefix(s, "HOOK ") && !strings.HasPrefix(s, "HOOK NOTE ")
}

// claudeTail follows one log file from its end, as tail -F does: a file that
// shrinks or is replaced is read again from its start, and a partial last line
// waits for its newline.
type claudeTail struct {
	path string
	off  int64
	id   os.FileInfo
}

func (t *claudeTail) start() error {
	fi, err := os.Stat(t.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil // the service has not written yet: read it from its start when it appears
	}
	if err != nil {
		return err
	}
	t.off, t.id = fi.Size(), fi
	return nil
}

func (t *claudeTail) next() ([]string, error) {
	f, err := os.Open(t.path)
	if errors.Is(err, os.ErrNotExist) {
		t.off, t.id = 0, nil
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close() // ignored: a read-only file; nothing written is lost on a failed close
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if (t.id != nil && !os.SameFile(t.id, fi)) || fi.Size() < t.off {
		t.off = 0 // replaced or truncated
	}
	t.id = fi
	if fi.Size() == t.off {
		return nil, nil
	}
	buf := make([]byte, fi.Size()-t.off)
	n, err := f.ReadAt(buf, t.off)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	buf = buf[:n]
	end := bytes.LastIndexByte(buf, '\n')
	if end < 0 {
		return nil, nil
	}
	t.off += int64(end + 1)
	return strings.Split(string(buf[:end]), "\n"), nil
}
