package friend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// A friend hosted in tmux: the friend's session is a terminal harness (any
// TUI) running in a detached tmux session named friend-<name>, the daemon
// types into its open chat exactly as a person would, and a person can
// attach and watch (`tmux attach -t friend-<name>`). Hosting is opt-in: a
// TUI started outside tmux keeps its own harness adapter (SPEC-FRIEND.md,
// "Hosted in tmux").

// TmuxPoll and TmuxWithin are how often, and for how long, Deliver looks at
// the pane for the prompt line to go once the line is typed.
const (
	TmuxPoll   = 500 * time.Millisecond
	TmuxWithin = time.Minute
)

// SessionPrefix starts every hosted session's name.
const SessionPrefix = "friend-"

// HostFile is the friend's hosting record in the state directory: the tmux
// session and the idle prompt pattern, saved by host so run and install need
// no flag for them.
const HostFile = "host.json"

// HostPrompts is each harness's idle prompt, a regular expression the last
// non-empty line of its pane matches while it waits for a message. They are
// data: `host --prompt` overrides one, and a harness not listed needs it.
// The shapes are the harnesses' documented input lines, not yet measured on
// a live pane.
var HostPrompts = map[string]string{
	"aider":    `^(?:[a-z0-9._-]+ )?>\s*$`,
	"opencode": `(?i)ask anything`,
	"grok":     `^\s*[›>❯]\s*$`,
}

// HostRecord is what host saves in HostFile.
type HostRecord struct {
	Session string `json:"session"`
	Prompt  string `json:"prompt"`
	Harness string `json:"harness"`
	Dir     string `json:"dir"`
}

// WriteHost saves the hosting record.
func WriteHost(stateDir string, h HostRecord) error {
	return write(filepath.Join(stateDir, HostFile), h)
}

// ReadHost is the saved hosting record; found is false when host never ran.
func ReadHost(stateDir string) (h HostRecord, found bool, err error) {
	found, err = read(filepath.Join(stateDir, HostFile), &h)
	return h, found, err
}

// Tmux delivers into the pane of a hosted friend through the tmux binary,
// every call behind Run: it is a function of the screens it captures and
// the clock it is given (SPEC-FRIEND.md, "Hosted in tmux").
//
// The pane's prompt drives the delivery machine's turn actions
// (tla/Friend.tla, Turn and TurnEnds): the prompt showing is the session
// idle, so Deliver may start a turn; the prompt absent is a turn in flight,
// so Deliver is Deferred and no second turn lands beside one.
type Tmux struct {
	Session  string         // the tmux session; SessionPrefix+Name when empty
	Name     string         // the friend, for the host line a missing session answers with
	Prompt   *regexp.Regexp // the idle prompt; read from the hosting record in StateDir when nil
	Dir      string         // the friend's directory, the working directory of every tmux call
	StateDir string         // where the hosting record is read from, when Session or Prompt is empty
	Run      Exec
	Out      io.Writer
	Now      func() time.Time                           // time.Now when nil
	Sleep    func(ctx context.Context, d time.Duration) // a timer when nil
}

func (t *Tmux) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

func (t *Tmux) sleep(ctx context.Context, d time.Duration) {
	if t.Sleep != nil {
		t.Sleep(ctx, d)
		return
	}
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// resolve fills Session and Prompt from the friend's name and the saved
// hosting record, where they are not set.
func (t *Tmux) resolve() error {
	if (t.Session == "" || t.Prompt == nil) && t.StateDir != "" {
		if h, found, err := ReadHost(t.StateDir); err != nil {
			return err
		} else if found {
			if t.Session == "" {
				t.Session = h.Session
			}
			if t.Prompt == nil && h.Prompt != "" {
				re, err := regexp.Compile(h.Prompt)
				if err != nil {
					return fmt.Errorf("the saved idle prompt %q: %w", h.Prompt, err)
				}
				t.Prompt = re
			}
		}
	}
	if t.Session == "" && t.Name != "" {
		t.Session = SessionPrefix + t.Name
	}
	return nil
}

// target is the exact session, its active pane: a bare name is a prefix
// match in tmux, so friend-ada would reach friend-adam's pane.
func (t *Tmux) target() string { return "=" + t.Session + ":" }

func (t *Tmux) hostLine() string {
	name := t.Name
	if name == "" {
		name = strings.TrimPrefix(t.Session, SessionPrefix)
	}
	if name == "" {
		name = "<me>"
	}
	dir := t.Dir
	if dir == "" {
		dir = "<d>"
	}
	return "nova-friend host --as " + name + " --harness <h> --dir " + dir + " -- <launch command...>"
}

// tmuxGone is what tmux says when the session, or the server, is not there.
var tmuxGone = regexp.MustCompile(`can't find|no server running|no sessions|error connecting`)

// capture is the pane's screen; gone is true when the session is missing.
func (t *Tmux) capture(ctx context.Context) (screen string, gone bool, err error) {
	out, exit, err := t.Run(ctx, t.Dir, "tmux", []string{"capture-pane", "-p", "-t", t.target()}, "")
	if err != nil {
		return "", false, fmt.Errorf("tmux capture-pane: %w", err)
	}
	if exit != 0 {
		if tmuxGone.MatchString(out) {
			return "", true, nil
		}
		return "", false, fmt.Errorf("tmux capture-pane exited %d: %s", exit, strings.TrimSpace(Head(out, 200)))
	}
	return out, false, nil
}

// idle says whether the last non-empty line of screen is the idle prompt.
func (t *Tmux) idle(screen string) bool {
	lines := strings.Split(strings.TrimRight(screen, " \t\r\n"), "\n")
	return t.Prompt.MatchString(strings.TrimRight(lines[len(lines)-1], " \t\r"))
}

// TmuxLine is text as the one line typed into the pane: a newline would
// send the line, so each one is shown as " ⏎ " (the Grok adapter's rule).
func TmuxLine(text string) string {
	text = strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	return strings.ReplaceAll(text, "\n", " ⏎ ")
}

// ready captures the pane and answers whether a turn may start: nil error
// and idle true, or a Deferred (no session, no prompt known, a turn
// running), or a real failure.
func (t *Tmux) ready(ctx context.Context) error {
	if err := t.resolve(); err != nil {
		return err
	}
	if t.Run == nil {
		return errors.New("tmux: no command runner")
	}
	if t.Session == "" || t.Prompt == nil {
		return Deferred{Reason: "no hosted session is recorded for this friend; start one: " + t.hostLine()}
	}
	screen, gone, err := t.capture(ctx)
	if err != nil {
		return err
	}
	if gone {
		return Deferred{Reason: "no tmux session " + t.Session + "; start it: " + t.hostLine()}
	}
	if !t.idle(screen) {
		return Deferred{Reason: "a turn runs in " + t.Session + ": its idle prompt is not showing"}
	}
	return nil
}

// Busy says whether a turn runs in the hosted pane now: the session exists
// and its idle prompt is not showing. A missing session is not busy.
func (t *Tmux) Busy(ctx context.Context) (bool, error) {
	err := t.ready(ctx)
	var d Deferred
	switch {
	case err == nil:
		return false, nil
	case errors.As(err, &d):
		return strings.HasPrefix(d.Reason, "a turn runs"), nil
	}
	return false, err
}

// Alive: the hosted session exists in tmux. A session that is gone is a harness
// not running (SPEC-FRIEND.md, the harness check).
func (t *Tmux) Alive(ctx context.Context) Liveness {
	if err := t.resolve(); err != nil || t.Session == "" || t.Run == nil {
		return Liveness{Why: "no hosted session is recorded for this friend; start one: " + t.hostLine()}
	}
	out, exit, err := t.Run(ctx, t.Dir, "tmux", []string{"has-session", "-t", "=" + t.Session}, "")
	switch {
	case err != nil:
		return Liveness{Why: "tmux: " + err.Error()}
	case exit == 0:
		return Liveness{Known: true, Running: true, Why: "the tmux session " + t.Session + " runs"}
	case tmuxGone.MatchString(out):
		return Liveness{Known: true, Why: "no tmux session " + t.Session + "; start it: " + t.hostLine()}
	}
	return Liveness{Why: fmt.Sprintf("tmux has-session exited %d", exit)}
}

// Deliver types text into the idle pane as one line and presses Enter, and
// is accepted once the prompt line has gone (the turn started), polled each
// TmuxPoll for TmuxWithin. The turn runs after Deliver returns.
func (t *Tmux) Deliver(ctx context.Context, text string) (int, error) {
	if err := t.ready(ctx); err != nil {
		return 0, err
	}
	// "-l" types the line literally and "--" ends the flags, so a text that begins with a dash is text
	if _, exit, err := t.Run(ctx, t.Dir, "tmux", []string{"send-keys", "-t", t.target(), "-l", "--", TmuxLine(text)}, ""); err != nil || exit != 0 {
		return 0, tmuxSendFailed(exit, err)
	}
	if _, exit, err := t.Run(ctx, t.Dir, "tmux", []string{"send-keys", "-t", t.target(), "Enter"}, ""); err != nil || exit != 0 {
		return 0, tmuxSendFailed(exit, err)
	}
	deadline := t.now().Add(TmuxWithin)
	for {
		t.sleep(ctx, TmuxPoll)
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		screen, gone, err := t.capture(ctx)
		switch {
		case err != nil:
			return 0, err
		case gone:
			return 0, fmt.Errorf("the tmux session %s ended after the line was typed", t.Session)
		case !t.idle(screen):
			if t.Out != nil {
				fmt.Fprintln(t.Out, "typed into "+t.Session+"; the turn runs after this")
			}
			return 0, nil
		case !t.now().Before(deadline):
			return 0, fmt.Errorf("the idle prompt of %s was still showing %s after the line was typed; it is not typed again", t.Session, TmuxWithin)
		}
	}
}

func tmuxSendFailed(exit int, err error) error {
	if err != nil {
		return fmt.Errorf("tmux send-keys: %w", err)
	}
	return fmt.Errorf("tmux send-keys exited %d", exit)
}

// HostSpec is one hosted session to start.
type HostSpec struct {
	Name, Harness, Dir, Prompt string
	Command                    []string
}

// ErrHostRuns is Host's refusal when the session exists already.
var ErrHostRuns = errors.New("the hosted session runs already")

// Session is the tmux session the friend is hosted in.
func (s HostSpec) Session() string { return SessionPrefix + s.Name }

// Attach is what a person runs to watch.
func (s HostSpec) Attach() string { return "tmux attach -t " + s.Session() }

// Argv is the tmux call that starts the session, detached, in Dir.
func (s HostSpec) Argv() []string {
	return append([]string{"new-session", "-d", "-s", s.Session(), "-c", s.Dir, "--"}, s.Command...)
}

// Said is Argv as the command a person could paste: each word that needs it
// single-quoted.
func (s HostSpec) Said() string {
	words := []string{"tmux"}
	for _, a := range s.Argv() {
		if a == "" || strings.ContainsAny(a, " \t\n'\"\\$`*?;&|<>(){}[]#~!") {
			a = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
		words = append(words, a)
	}
	return strings.Join(words, " ")
}

// Host starts the hosted session: it refuses with ErrHostRuns (wrapped) when
// the session exists, and with dry set runs nothing.
func Host(ctx context.Context, run Exec, s HostSpec, dry bool) error {
	if dry {
		return nil
	}
	out, exit, err := run(ctx, s.Dir, "tmux", []string{"has-session", "-t", "=" + s.Session()}, "")
	switch {
	case err != nil:
		return fmt.Errorf("tmux has-session: %w", err)
	case exit == 0:
		return fmt.Errorf("%w: %s runs already", ErrHostRuns, s.Session())
	case !tmuxGone.MatchString(out):
		return fmt.Errorf("tmux has-session exited %d: %s", exit, strings.TrimSpace(Head(out, 200)))
	}
	out, exit, err = run(ctx, s.Dir, "tmux", s.Argv(), "")
	if err != nil {
		return fmt.Errorf("tmux new-session: %w", err)
	}
	if exit != 0 {
		return fmt.Errorf("tmux new-session exited %d: %s", exit, strings.TrimSpace(Head(out, 200)))
	}
	return nil
}
