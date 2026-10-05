package friend

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Codex uses the writer lock to choose its first delivery route. An open chat
// receives codex queue; a closed chat uses exec resume. A failed route tries
// the other once, then defers without losing the message (SPEC-FRIEND.md,
// Codex). Queue acceptance is not a completed answer: the app may start the
// queued input only after its current turn ends.
// Without a named session, resolve the newest saved thread for Dir first.
type Codex struct {
	Dir, Session string
	Run          Exec
	Program      string                 // "codex" when empty
	Home         string                 // CODEX_HOME; $CODEX_HOME or ~/.codex when empty
	Held         func(lock string) bool // whether the thread's writer lock is held; FlockHeld when nil
	Env          func(string) string    // getenv; os.Getenv when nil
	Out          io.Writer              // where the turn's output goes, when set: the daemon's record
}

func (c *Codex) program() string {
	if c.Program == "" {
		return "codex"
	}
	return c.Program
}

func (c *Codex) home() string {
	if c.Home != "" {
		return c.Home
	}
	getenv := c.Env
	if getenv == nil {
		getenv = os.Getenv
	}
	if h := getenv("CODEX_HOME"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".codex")
}

func (c *Codex) held() func(string) bool {
	if c.Held == nil {
		return FlockHeld
	}
	return c.Held
}

// ResumeArgs is the codex command line that resumes session (empty: the
// newest thread of the working directory) with text as its next turn.
func ResumeArgs(session, text string) []string {
	if session == "" {
		return []string{"exec", "resume", "--skip-git-repo-check", "--last", text}
	}
	return []string{"exec", "resume", "--skip-git-repo-check", session, text}
}

// LockPath is the writer lock codex holds on thread while a process has it
// open for writing, under home (CODEX_HOME).
func LockPath(home, thread string) string {
	return filepath.Join(home, "thread-writer-locks", thread+".lock")
}

// ResumeLabel is the line the record carries for a turn answered by resume.
const ResumeLabel = "answered by resume, not by the open chat"

func (c *Codex) Deliver(ctx context.Context, text string) (int, error) {
	session := c.Session
	if session == "" {
		var err error
		session, err = NewestCodexSession(c.home(), c.Dir)
		if err != nil {
			return 1, err
		}
	}
	queue := []string{"queue", "--thread", session, "--message", text}
	resume := ResumeArgs(session, text)
	routes := [][]string{resume, queue}
	if c.held()(LockPath(c.home(), session)) {
		routes[0], routes[1] = queue, resume
	}
	var failures []string
	var refusal error // a provider's refusal on either route: the session, not the moment, is at fault
	for _, args := range routes {
		out, exit, err := c.Run(ctx, c.Dir, c.program(), args, "")
		if exit != 0 || err != nil {
			if _, r := refused(session, out, exit, err); refusal == nil {
				if _, ok := r.(ProviderRefused); ok {
					refusal = r
				}
			}
			failures = append(failures, fmt.Sprintf("%s exit=%d error=%v output=%q", args[0], exit, err, strings.TrimSpace(Head(out, OutputKept))))
			continue
		}
		if c.Out != nil {
			if args[0] == "queue" {
				fmt.Fprintf(c.Out, "queued for open chat: codex queue --thread %s --message <text> (accepted, not answered)\n", session)
			} else {
				fmt.Fprintln(c.Out, ResumeLabel+": codex "+strings.Join(ResumeArgs(session, "<text>"), " "))
			}
			if out != "" {
				fmt.Fprintln(c.Out, strings.TrimRight(Head(out, OutputKept), "\n"))
			}
		}
		return 0, nil
	}
	if refusal != nil {
		return 1, refusal
	}
	return 0, Deferred{Reason: fmt.Sprintf("thread %s: neither delivery route accepted the message (%s); keep it pending and retry when Codex is available", session, strings.Join(failures, "; "))}
}

// writableRootsLine is the one line of config.toml that names extra writable
// roots. Install writes a single line; a value it cannot read as one line is
// left untouched and said as drift.
var writableRootsLine = regexp.MustCompile(`(?m)^[ \t]*writable_roots[ \t]*=[ \t]*\[([^]\n]*)\][ \t]*$`)

// planCodex adds the friend's directory to writable_roots in
// <CODEX_HOME|home>/.codex/config.toml. The directory must be a real
// directory: a symlink is refused and nothing is written (the finding of
// 2026-10-05: a symlinked writable root and the session did no work).
func planCodex(p *Prepared) error {
	dir := filepath.Clean(p.settings.Dir)
	if err := requireRealDir(dir); err != nil {
		return fmt.Errorf("codex writable root: %w", err)
	}
	configDir := p.settings.CodexHome
	if configDir == "" {
		configDir = filepath.Join(p.settings.Home, ".codex")
	}
	if err := stageDir(p, configDir); err != nil {
		return fmt.Errorf("codex: %w", err)
	}
	path := filepath.Join(configDir, "config.toml")
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	text := string(raw)
	if strings.Contains(text, "writable_roots") && !writableRootsLine.MatchString(text) {
		p.drifts = append(p.drifts, "codex writable_roots: "+path+" has a writable_roots install cannot read as one line")
		p.Files[path] = text
		return nil
	}
	roots, _ := parseWritableRoots(text)
	var kept []string
	for _, root := range roots {
		if err := requireRealDir(root); err != nil {
			p.drifts = append(p.drifts, "codex writable_roots: "+err.Error())
			continue
		}
		kept = append(kept, root)
	}
	if !slicesContains(kept, dir) {
		installed := "missing"
		if writableRootsLine.MatchString(text) {
			installed = formatRoots(roots)
		}
		p.drifts = append(p.drifts, fmt.Sprintf("codex writable_roots: installed %s, want %s", installed, strconv.Quote(dir)))
		kept = append(kept, dir)
	}
	next := mergeWritableRoots(text, kept)
	if text == next {
		p.Files[path] = text
	} else {
		p.Files[path] = next
	}
	return nil
}

func slicesContains(list []string, want string) bool {
	for _, s := range list {
		if filepath.Clean(s) == want {
			return true
		}
	}
	return false
}

func parseWritableRoots(text string) ([]string, bool) {
	m := writableRootsLine.FindStringSubmatch(text)
	if m == nil {
		if strings.Contains(text, "writable_roots") {
			return nil, false
		}
		return nil, true // nothing there yet: install can add the line
	}
	var roots []string
	for _, part := range strings.Split(m[1], ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		s, err := strconv.Unquote(part)
		if err != nil {
			return nil, false
		}
		roots = append(roots, s)
	}
	return roots, true
}

func formatRoots(roots []string) string {
	quoted := make([]string, len(roots))
	for i, root := range roots {
		quoted[i] = strconv.Quote(root)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// mergeWritableRoots returns text with writable_roots set to roots, and
// sandbox_mode workspace-write when the file does not already name one
// (writable_roots apply in that mode). Other lines are kept.
func mergeWritableRoots(text string, roots []string) string {
	line := "writable_roots = " + formatRoots(roots)
	if writableRootsLine.MatchString(text) {
		return writableRootsLine.ReplaceAllString(text, line)
	}
	block := ""
	if !regexp.MustCompile(`(?m)^[ \t]*sandbox_mode[ \t]*=`).MatchString(text) {
		block = "sandbox_mode = \"workspace-write\"\n\n"
	}
	block += "[sandbox_workspace_write]\n" + line + "\n"
	if text == "" {
		return block
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text + "\n" + block
}
