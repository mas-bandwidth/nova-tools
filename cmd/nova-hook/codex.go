package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/codexdesktop"
	"github.com/mas-bandwidth/nova-tools/pkg/harness/notice"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

const maxCodexEvent = notice.MaxSaved

// codexOwner checks whether the desktop currently has a native input owner for
// exactly the requested chat. It never starts a turn or changes the hook.
func (w world) codexOwner(c *tool.Call) *tool.Out {
	thread := c.Str("thread")
	if !codexdesktop.ValidThread(thread) {
		return tool.Refuse("invalid Codex thread ID")
	}
	socket := c.Str("socket")
	if socket == "" {
		home, err := w.codexHomeDir()
		if err != nil {
			return tool.Refuse("finding the Codex desktop socket: " + err.Error())
		}
		socket = filepath.Join(home, "ipc", "ipc.sock")
	}
	if c.DryRun() {
		return tool.Done().As("DRY-RUN").Fact("thread", thread).Fact("native", "unchecked").Note("no native owner discovery was sent")
	}
	if w.codexProbe == nil {
		return tool.Refuse("Codex desktop owner probe is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := w.codexProbe(ctx, socket, thread); err != nil {
		o := tool.Fail("no usable native Codex desktop owner for thread "+thread+": "+err.Error()).Fact("thread", thread).Fact("native", "unavailable")
		o.Remedy = "keep the current hook route; use a supported recipient or active monitored delivery, and prove model receipt separately"
		return o
	}
	return tool.Done().Fact("thread", thread).Fact("native", "available").Note("owner availability is not delivery, model acknowledgement, or idle wake")
}

// codexRoute is the Codex desktop route's flags, or the Antigravity route's
// (antigravity set): the shim's mechanism, and the native startup recovery.
type codexRoute struct {
	socket, thread, directory string
	antigravity               *antigravityRoute
	since                     time.Time
	// discover is the friend's current chat, read from the Codex home, when
	// --codex-thread was not given: the route follows the friend to a new chat.
	discover func() (string, error)
	// cutoverFile holds the defaulted cutover, written once at the first hook
	// and read at every later one, when --codex-since was not given.
	cutoverFile string
}

// codexHomeDir is CODEX_HOME, or ~/.codex.
func (w world) codexHomeDir() (string, error) {
	if home := w.getenv("CODEX_HOME"); home != "" {
		return home, nil
	}
	home, err := w.antigravityHome()
	if err != nil {
		return "", fmt.Errorf("the home directory: %w", err)
	}
	return filepath.Join(home, ".codex"), nil
}

// codexEventsDir is the default private events directory of a friend's
// Codex route: $XDG_STATE_HOME/nova-hook/<name>-codex, or ~/.local/state/...
func (w world) codexEventsDir(as string) (string, error) {
	state := w.getenv("XDG_STATE_HOME")
	if state == "" {
		home, err := w.antigravityHome()
		if err != nil {
			return "", fmt.Errorf("the home directory: %w", err)
		}
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "nova-hook", as+"-codex"), nil
}

// codexDefaults fills what the one-line setup leaves out:
// nova-hook hook --as <name> --role comms --harness codex. The socket is the
// desktop's (CODEX_HOME/ipc/ipc.sock), the events directory is the friend's
// own under the state home, the chat is the friend's current one (followed
// when the friend moves to a new chat), and, with the defaulted directory,
// the cutover is the store's time at the first hook, kept in that directory.
func (w world) codexDefaults(c *tool.Call, r *codexRoute, rawSince string) error {
	as := strings.TrimSpace(c.Str("as"))
	if r.socket == "" || r.thread == "" {
		home, err := w.codexHomeDir()
		if err != nil {
			return err
		}
		if r.socket == "" {
			r.socket = filepath.Join(home, "ipc", "ipc.sock")
		}
		if r.thread == "" {
			if as == "" {
				return errors.New("finding the friend's Codex chat wants --as <name>, or name the chat with --codex-thread")
			}
			r.discover = func() (string, error) {
				chats, err := w.codexChats(home)
				if err != nil {
					return "", err
				}
				chat, err := codexdesktop.CurrentChat(chats, as)
				return chat.ID, err
			}
			id, err := r.discover()
			if err != nil {
				return err
			}
			r.thread = id
		}
	}
	if r.directory == "" {
		if as == "" {
			return errors.New("the default events directory wants --as <name>, or name one with --codex-events-dir")
		}
		dir, err := w.codexEventsDir(as)
		if err != nil {
			return err
		}
		r.directory = dir
		if !c.DryRun() {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return fmt.Errorf("create the events directory: %w", err)
			}
		}
		// The defaulted directory is the route's own, so it keeps the
		// defaulted cutover. A named directory keeps the earlier rule: the
		// 24h window, or --codex-since.
		if rawSince == "" {
			r.cutoverFile = filepath.Join(dir, codexCutoverFile)
		}
	}
	return nil
}

// codexCutoverFile is the defaulted cutover's file in the events directory.
const codexCutoverFile = ".cutover"

// codexCutover is the route's fixed cutover when --codex-since was not given:
// the one in the events directory, or, at the first hook, the store's time,
// written there so every restart keeps the same boundary.
func codexCutover(s *session, file string) (time.Time, error) {
	if data, err := os.ReadFile(file); err == nil {
		raw := strings.TrimSpace(string(data))
		at, err := time.Parse(time.RFC3339, raw)
		if err != nil || raw != at.UTC().Format(time.RFC3339) {
			return time.Time{}, fmt.Errorf("%s holds no fixed UTC cutover; remove it to start a new one, or pass --codex-since", file)
		}
		return at, nil
	} else if !os.IsNotExist(err) {
		return time.Time{}, err
	}
	ctx, cancel := call()
	_, now, err := s.hooks.Read(ctx, s.as)
	cancel()
	if err != nil {
		return time.Time{}, fmt.Errorf("read the store clock: %w", err)
	}
	at := now.UTC().Truncate(time.Second)
	if err := os.WriteFile(file, []byte(at.Format(time.RFC3339)+"\n"), 0o600); err != nil {
		return time.Time{}, fmt.Errorf("keep the cutover: %w", err)
	}
	return at, nil
}

func (r *codexRoute) label() string {
	if r != nil && r.antigravity != nil {
		return "Antigravity"
	}
	return "Codex"
}

// codexConfig refuses partial routes before any hook record or lease is written.
func (w world) codexConfig(c *tool.Call) (*codexRoute, error) {
	harness := strings.TrimSpace(c.Str("harness"))
	hasCodex := c.Str("codex-socket") != "" || c.Str("codex-thread") != "" || c.Str("codex-events-dir") != "" || c.Str("codex-since") != ""
	hasAntigravity := c.Str("antigravity-dir") != "" || c.Str("antigravity-session") != "" || c.Str("session") != "" || c.Str("antigravity-server") != "" || c.Str("antigravity-events-dir") != "" || c.Str("antigravity-since") != ""
	if hasCodex && hasAntigravity {
		return nil, errors.New("cannot combine --codex-* and --antigravity-* flags")
	}
	if strings.EqualFold(harness, "codex") && hasAntigravity {
		return nil, errors.New("--harness codex cannot use --antigravity-* flags")
	}
	if strings.EqualFold(harness, "antigravity") && hasCodex {
		return nil, errors.New("--harness antigravity cannot use --codex-* flags")
	}
	if strings.EqualFold(harness, "muse") && hasAntigravity {
		return nil, errors.New("one harness at a time: --harness Muse or the Antigravity desktop route, not both")
	}
	if strings.EqualFold(harness, "antigravity") || hasAntigravity {
		if harness != "" && !strings.EqualFold(harness, "antigravity") {
			return nil, errors.New("--harness wants muse or antigravity (the Codex desktop route uses the --codex-* flags)")
		}
		return w.antigravityConfig(c)
	}
	r := &codexRoute{socket: c.Str("codex-socket"), thread: c.Str("codex-thread"), directory: c.Str("codex-events-dir")}
	rawSince := c.Str("codex-since")
	if rawSince != "" {
		var err error
		r.since, err = time.Parse(time.RFC3339, rawSince)
		if err != nil || rawSince != r.since.UTC().Format(time.RFC3339) {
			return nil, errors.New("codex cutover wants --codex-since as a fixed UTC timestamp, for example 2026-10-14T20:00:00Z; retain it in launch configuration across restarts")
		}
	}
	if r.socket == "" && r.thread == "" && r.directory == "" && rawSince == "" && !strings.EqualFold(harness, "codex") {
		return nil, nil
	}
	if c.Bool("once") {
		return nil, errors.New("codex delivery wants a running hook client; omit --once")
	}
	if w.codexLock == nil || w.codexValidate == nil || w.files == nil || w.codexDeliver == nil || w.codexChats == nil {
		return nil, errors.New("codex delivery wants a configured local native receiver")
	}
	if err := w.codexDefaults(c, r, rawSince); err != nil {
		return nil, err
	}
	if c.DryRun() {
		if _, err := codexdesktop.SavedPath(r.directory, "route"); err != nil {
			return nil, err
		}
		return r, nil
	}
	if err := w.codexValidate(r.socket, r.thread, r.directory); err != nil {
		return nil, err
	}
	return r, nil
}

// Reject oversized external fields before encoding can allocate another copy.
// The final encoded envelope is checked independently for JSON escaping overhead.
func codexMessageBytes(m bus.Message) int {
	total := len(m.ID) + len(m.From) + len(m.Subject) + len(m.Re) + len(m.Kind) + len(m.Body) + len(m.Token) + len(m.Once)
	for _, name := range m.To {
		total += len(name)
		if total > maxCodexEvent {
			return total
		}
	}
	for _, name := range m.CC {
		total += len(name)
		if total > maxCodexEvent {
			return total
		}
	}
	for _, mark := range m.Marks {
		total += len(mark.Key) + len(mark.Field) + len(mark.Value)
		if total > maxCodexEvent {
			return total
		}
	}
	return total
}
