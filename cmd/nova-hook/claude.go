package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/harness"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

const (
	claudeGenerationName = "generation"
	claudeBoundaryScript = "nova-hook-claude-boundary.sh"
	claudeSettingsFile   = "settings.json"
)

// claudeSetup is one Claude Code install. The harness word is harness.Claude.
type claudeSetup struct {
	as, role        string
	state, settings string
	boundary        bool
}

// claudeDelivery is one coalesced notice. Text is closed local wording.
// Wake is a label: true when the client treats the session as idle.
type claudeDelivery struct {
	Text  string
	Wake  bool
	Count int
}

// claudeNotice is the pending notice. tla/ClaudeCodeNotice.tla (Arrive,
// Deliver, Boundary). At most one delivery is held. A burst folds into the
// queue and leaves in that one delivery. TLC is owed.
type claudeNotice struct {
	queued    int
	prove     string
	delivered bool
	idle      bool
	askIdle   func() bool
	deliver   func(claudeDelivery) error
	genPath   string
	seen      int
	genErr    string
}

func newClaudeNotice() *claudeNotice {
	return &claudeNotice{idle: true}
}

// offer folds n messages into the one pending notice. prove replaces any
// older challenge line. n is the whole read, not one message at a time
// followed by a take.
func (a *claudeNotice) offer(n int, prove string) {
	if a == nil || n <= 0 {
		return
	}
	a.queued += n
	if prove != "" {
		a.prove = prove
	}
}

// take emits when the turn does not already hold a notice and the queue is
// nonempty. The emitted count leaves the queue. Further offers wait for a
// boundary.
func (a *claudeNotice) take() (claudeDelivery, bool) {
	if a == nil || a.delivered || a.queued <= 0 {
		return claudeDelivery{}, false
	}
	d := claudeDelivery{Text: claudeNoticeText(a.queued, a.prove), Wake: a.isIdle(), Count: a.queued}
	a.queued = 0
	a.prove = ""
	a.delivered = true
	a.idle = false
	return d, true
}

// boundary is a Stop-hook turn edge: the session is idle, the hold drops, and
// a folded queue emits once.
func (a *claudeNotice) boundary() (claudeDelivery, bool) {
	if a == nil {
		return claudeDelivery{}, false
	}
	a.delivered = false
	a.idle = true
	return a.take()
}

func (a *claudeNotice) isIdle() bool {
	if a.askIdle != nil {
		return a.askIdle()
	}
	return a.idle
}

// claudeNoticeText is the closed line. One ordinary message names the bus.
// A larger count names how many besides the first folded in. A challenge is
// the prove line, prefixed when other messages folded with it.
func claudeNoticeText(count int, prove string) string {
	if count <= 0 {
		return ""
	}
	more := ""
	switch {
	case count == 1 && prove == "":
		more = "bus message; read the bus"
	case count > 1:
		more = fmt.Sprintf("%d more messages; read the bus", count-1)
	}
	if prove == "" {
		return more
	}
	if more == "" {
		return prove
	}
	return prove + "; " + more
}

// claudeProve is the prove line for a bus challenge, or empty. The body and
// the rest of the subject stay out of the notice.
func claudeProve(as string, m bus.Message) string {
	nonce, ok := strings.CutPrefix(m.Subject, "HOOK CHALLENGE nonce=")
	if !ok || nonce == "" || strings.ContainsAny(nonce, " \t\r\n") {
		return ""
	}
	return "run: nova-hook prove --as " + as + " --nonce " + nonce
}

// claudeBatch counts one read. The cursor advances over every entry, including
// the client's own non-challenge messages, which are not part of the count.
func claudeBatch(as, cursor string, got []bus.Entry) (string, int, string) {
	n := 0
	prove := ""
	for _, e := range got {
		cursor = e.Entry
		m := e.Message()
		if m.From == as && !strings.HasPrefix(m.Subject, "HOOK CHALLENGE ") {
			continue
		}
		n++
		if p := claudeProve(as, m); p != "" {
			prove = p
		}
	}
	return cursor, n, prove
}

func (a *claudeNotice) sawBoundary() (bool, error) {
	if a == nil || a.genPath == "" {
		return false, nil
	}
	g, err := readClaudeGen(a.genPath)
	if err != nil {
		return false, err
	}
	if g == a.seen {
		return false, nil
	}
	if g < a.seen {
		return false, errors.New("claude generation went backwards")
	}
	a.seen = g
	a.delivered = false
	a.idle = true
	return true, nil
}

func readClaudeGen(path string) (int, error) {
	if path == "" {
		return 0, nil
	}
	b, err := os.ReadFile(path)
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

// claudeConflicts refuses Claude Code together with a Codex route before
// either route is configured.
func claudeConflicts(c *tool.Call) error {
	if c.Str("harness") != harness.Claude {
		return nil
	}
	if c.Str("codex-socket") != "" || c.Str("codex-thread") != "" || c.Str("codex-events-dir") != "" || c.Str("codex-since") != "" {
		return errors.New("claude delivery and codex delivery do not run in one hook")
	}
	return nil
}

// claudeConfig checks the Claude Code route before any hook record is written.
// A hook with no claude flag returns nil.
func (w world) claudeConfig(c *tool.Call) (*claudeSetup, error) {
	name := c.Str("harness")
	boundary := c.Bool("claude-boundary")
	state := c.Str("claude-state")
	settings := c.Str("claude-settings")
	if name == "" && !boundary && state == "" && settings == "" {
		return nil, nil
	}
	if name != harness.Claude {
		if name == "" {
			return nil, errors.New("claude delivery wants --harness claude")
		}
		return nil, fmt.Errorf("hook harness wants %s", harness.Claude)
	}
	if c.Str("as") == "" {
		return nil, errors.New("claude delivery wants --as")
	}
	if c.Bool("once") {
		return nil, errors.New("claude delivery wants a running hook client; omit --once")
	}
	state, err := existingDir(state, "--claude-state")
	if err != nil {
		return nil, err
	}
	settings, err = existingDir(settings, "--claude-settings")
	if err != nil {
		return nil, err
	}
	return &claudeSetup{as: c.Str("as"), role: c.Str("role"), state: state, settings: settings, boundary: boundary}, nil
}

func existingDir(path, flag string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("claude delivery wants %s", flag)
	}
	clean := filepath.Clean(path)
	info, err := os.Stat(clean)
	if err != nil {
		return "", fmt.Errorf("claude delivery wants %s to be an existing directory: %w", flag, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("claude delivery wants %s to be a directory", flag)
	}
	return clean, nil
}

func armClaude(s *claudeSetup, deliver func(claudeDelivery) error, idle func() bool) (*claudeNotice, error) {
	a := newClaudeNotice()
	a.deliver = deliver
	a.askIdle = idle
	a.genPath = filepath.Join(s.state, claudeGenerationName)
	seen, err := readClaudeGen(a.genPath)
	if err != nil {
		return nil, err
	}
	a.seen = seen
	return a, nil
}

func claudeBump(stateDir string) *tool.Out {
	path := filepath.Join(stateDir, claudeGenerationName)
	n, err := readClaudeGen(path)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	next := n + 1
	if err := atomicfile.Write(path, []byte(strconv.Itoa(next)+"\n"), 0o600, atomicfile.ExactMode()); err != nil {
		return tool.Refuse("claude generation could not be written: " + err.Error())
	}
	return tool.Done().As("STATE").Fact("state", "boundary").Fact("generation", next)
}

func (w world) emitClaude(c *tool.Call) *tool.Out {
	if w.claude == nil {
		return nil
	}
	d, ok := w.claude.take()
	if !ok {
		return nil
	}
	line(c, tool.Done().As("PUSH").Fact("push", "claude").Fact("wake", d.Wake).Fact("notice", tool.Text(d.Text)))
	if w.claude.deliver != nil {
		if err := w.claude.deliver(d); err != nil {
			return tool.Refuse("claude notice was not delivered: " + err.Error())
		}
	}
	return nil
}

func (w world) pollClaude(c *tool.Call) *tool.Out {
	if w.claude == nil {
		return nil
	}
	changed, err := w.claude.sawBoundary()
	if err != nil {
		if err.Error() != w.claude.genErr {
			w.claude.genErr = err.Error()
			line(c, tool.Done().As("STATE").Fact("state", "boundary-unread").Note(w.claude.genErr))
		}
		return nil
	}
	w.claude.genErr = ""
	if !changed {
		return nil
	}
	return w.emitClaude(c)
}

// installClaudeFiles writes the Stop hook. The script's command is nova-hook
// with the required --as and --role, and --claude-boundary advances the
// generation file. settings.json gains that command once.
func installClaudeFiles(s claudeSetup) error {
	bin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("claude setup could not name its own binary: %w", err)
	}
	script := filepath.Join(s.settings, claudeBoundaryScript)
	body := "#!/bin/sh\nexec " + shQuote(bin) + " hook --as " + shQuote(s.as) + " --role " + shQuote(s.role) +
		" --harness " + shQuote(harness.Claude) + " --claude-state " + shQuote(s.state) +
		" --claude-settings " + shQuote(s.settings) + " --claude-boundary\n"
	if err := atomicfile.Write(script, []byte(body), 0o700, atomicfile.ExactMode()); err != nil {
		return err
	}
	return mergeClaudeStopHook(filepath.Join(s.settings, claudeSettingsFile), script)
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
