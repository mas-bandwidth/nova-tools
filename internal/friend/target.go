package friend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// A session the daemon was told to deliver into (--session) can end: the
// friend archives the chat, deletes it, or it belongs to another directory.
// Every retry into it then fails the same way for ever (the finding of
// 2026-10-06, a friend's own diagnosis: her service named an archived
// Codex thread, every delivery was deferred and retried for hours, and
// nothing named the cause). An adapter that can read its named session's
// lifecycle is a TargetChecker; the daemon asks it before every retry (Gone),
// before each session check and at run's start, and a target found in one of
// its harness's TerminalStates is TargetInvalid: told once, never retried
// (docs/SPEC-FRIEND.md, "A gone session target"; tla/Delivery.tla).

// The terminal states of a named session: the words a TargetInvalid carries.
const (
	TargetArchived = "archived" // the harness keeps it, archived: no turn goes in until the friend unarchives it herself
	TargetDeleted  = "deleted"  // the harness has no session by that id, live or archived
	TargetMoved    = "moved"    // the session belongs to another working directory than the friend's
)

// TerminalStates is, per harness, the states of a named session the adapter
// reads as gone, and how it reads them; a harness absent here names no
// session the daemon can read (docs/SPEC-FRIEND.md, the table; a test covers
// each row). Nothing in it guesses: an id the harness lists is live.
var TerminalStates = map[string][]TerminalState{
	"codex": {
		{TargetArchived, "the thread's rollout is under CODEX_HOME/archived_sessions and not under sessions"},
		{TargetDeleted, "no rollout of the thread under CODEX_HOME/sessions or archived_sessions"},
		{TargetMoved, "the thread's rollout header names another working directory (cwd)"},
	},
	"opencode": {
		{TargetDeleted, "opencode session list --format json lists no session by that id"},
		{TargetMoved, "the listed session's directory is another directory"},
	},
	"dsh": {
		{TargetDeleted, "no directory of that id under the sessions root, for any working directory"},
		{TargetMoved, "the session's directory is under another working directory's key"},
	},
	"grok": {
		{TargetDeleted, "the named wake file (--session) does not exist"},
	},
	"tmux": {
		{TargetDeleted, "a named pane (%<n>) tmux no longer lists; a session name is not terminal, host makes it again"},
	},
}

// NoTerminalStates is why a harness names no target the daemon reads.
var NoTerminalStates = map[string]string{
	"claude":      "the daemon is passive: --session names the wake file's friend, and the session's own wait reads the bus",
	"gemini":      "gemini --resume takes an index or latest, which the harness resolves itself; nothing names a fixed session",
	"antigravity": "the adapter delivers into the app's open conversation, found at each turn; nothing names a fixed session",
}

// TargetSuperseded is the state of a named session her nova-config row no
// longer names: she rebound to another (nova-friend rebind, or install with a
// new --session), and a daemon started on the old id, from a service
// reinstalled with an old command line, delivers nothing into it. Every
// harness that names a session has it beside its TerminalStates.
const TargetSuperseded = "superseded"

// TerminalState is one row of TerminalStates.
type TerminalState struct{ State, How string }

// TargetInvalid is the answer when the named session is in a terminal state:
// nothing can be delivered into it and no retry can succeed. The daemon never
// unarchives it and never picks another session in its place: the friend
// rebinds (RebindLine).
type TargetInvalid struct {
	Harness, Target, State, Detail string
}

func (e TargetInvalid) Error() string {
	return fmt.Sprintf("%s session %s is %s: %s", e.Harness, e.Target, e.State, e.Detail)
}

// Said is the state found and its detail on one line, at most 300 bytes: what
// her beat says of it (friend beat --target-state).
func (e TargetInvalid) Said() string {
	return oneLine(e.State+": "+e.Detail, 300)
}

// RebindLine is the command a friend runs to name her session again.
func RebindLine(friend string) string {
	return "nova-friend rebind --as " + friend + " --session <id>"
}

// TargetChecker is an adapter that reads its named session's lifecycle:
// nil while the target is live or unnamed (the newest session is resolved
// at each turn), TargetInvalid when it is gone, any other error when the
// read itself failed (the delivery goes ahead and answers for itself).
type TargetChecker interface {
	CheckTarget(ctx context.Context) error
}

// Gone is the read of d's named session before a hand-in, through the gates
// the daemon puts around its adapter (CheckerOf): the TargetInvalid when it
// is gone, nil otherwise, and nil for an adapter that reads none (a read that
// failed lets the delivery answer for itself). The daemon reads it before
// the batch turn, each retry of it, each session check and the push proof at
// run's start.
func Gone(ctx context.Context, d Deliverer) error {
	c, ok := CheckerOf(d)
	if !ok {
		return nil
	}
	var gone TargetInvalid
	if err := c.CheckTarget(ctx); errors.As(err, &gone) {
		return gone
	}
	return nil
}

// CheckerOf is the TargetChecker under d's gates (the session check's and
// the limit's), when d's adapter is one.
func CheckerOf(d Deliverer) (TargetChecker, bool) {
	for {
		switch g := d.(type) {
		case TargetChecker:
			return g, true
		case turnGatedLanes:
			d = g.Deliverer
		case turnGated:
			d = g.Deliverer
		case *gatedLanes:
			d = g.d
		case *gated:
			d = g.d
		default:
			return nil, false
		}
	}
}

// CheckTarget is the Codex thread's lifecycle (TerminalStates["codex"]).
func (c *Codex) CheckTarget(context.Context) error {
	if c.Session == "" {
		return nil
	}
	return CodexThreadState(c.home(), c.Dir, c.Session)
}

// CodexThreadState reads thread's rollout under home: live in sessions/ in
// dir, else archived, deleted or moved (TargetInvalid).
func CodexThreadState(home, dir, thread string) error {
	invalid := func(state, detail string) error {
		return TargetInvalid{Harness: "codex", Target: thread, State: state, Detail: detail}
	}
	live, err := findRollout(filepath.Join(home, "sessions"), thread)
	if err != nil {
		return err
	}
	if live == "" {
		archived, err := findRollout(filepath.Join(home, "archived_sessions"), thread)
		if err != nil {
			return err
		}
		if archived != "" {
			return invalid(TargetArchived, "its rollout is "+archived)
		}
		return invalid(TargetDeleted, "no rollout of it under "+filepath.Join(home, "sessions")+" or archived_sessions")
	}
	cwd, err := rolloutCwd(live)
	if err != nil || cwd == "" {
		return err // a header that cannot be read names no directory: the delivery answers for itself
	}
	if !sameDir(cwd, dir) {
		return invalid(TargetMoved, "it was started in "+cwd+", not "+dir)
	}
	return nil
}

// findRollout is the rollout file of thread under root, "" when none.
func findRollout(root, thread string) (string, error) {
	found := ""
	err := filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !e.IsDir() && strings.HasSuffix(e.Name(), "-"+thread+".jsonl") {
			found = path
			return fs.SkipAll
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("codex saved sessions: %w", err)
	}
	return found, nil
}

// rolloutCwd is the working directory a rollout's first line, its
// session_meta, names.
func rolloutCwd(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	first, _, _ := strings.Cut(string(data), "\n")
	var row struct {
		Payload struct {
			Cwd string `json:"cwd"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(first), &row); err != nil {
		return "", nil // ignored: no header, no directory to compare
	}
	return row.Payload.Cwd, nil
}

// sameDir says a and b are one directory, through symlinks where both resolve.
func sameDir(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// CheckTarget is the OpenCode session's lifecycle (TerminalStates["opencode"]),
// read from the session listing.
func (o *OpenCode) CheckTarget(ctx context.Context) error {
	if o.Session == "" {
		return nil
	}
	listing, exit, err := o.Run(ctx, o.Dir, o.program(), []string{"session", "list", "--format", "json"}, "")
	if err != nil || exit != 0 {
		return fmt.Errorf("opencode session list: exit %d: %v", exit, err)
	}
	var rows []session
	if err := json.Unmarshal([]byte(listing), &rows); err != nil {
		return fmt.Errorf("opencode session list: not a JSON list: %v", err)
	}
	i := slices.IndexFunc(rows, func(r session) bool { return r.ID == o.Session })
	switch {
	case i < 0:
		return TargetInvalid{Harness: "opencode", Target: o.Session, State: TargetDeleted, Detail: "opencode session list names no session by that id"}
	case rows[i].Directory != "" && !sameDir(rows[i].Directory, o.Dir):
		return TargetInvalid{Harness: "opencode", Target: o.Session, State: TargetMoved, Detail: "its directory is " + rows[i].Directory + ", not " + o.Dir}
	}
	return nil
}

// CheckTarget is the DSH session's lifecycle (TerminalStates["dsh"]): its
// directory under the sessions root, under the friend's directory's key.
func (d *DSH) CheckTarget(context.Context) error {
	if d.Session == "" {
		return nil
	}
	root := d.Sessions
	if root == "" {
		root = dshHome()
	}
	realDir, err := filepath.EvalSymlinks(d.Dir)
	if err != nil {
		return err
	}
	if fi, err := os.Stat(filepath.Join(root, DSHSessionKey(realDir), d.Session)); err == nil && fi.IsDir() {
		return nil
	}
	keys, err := os.ReadDir(root)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, k := range keys {
		if fi, err := os.Stat(filepath.Join(root, k.Name(), d.Session)); err == nil && fi.IsDir() {
			return TargetInvalid{Harness: "dsh", Target: d.Session, State: TargetMoved, Detail: "it is kept under " + k.Name() + ", not " + DSHSessionKey(realDir)}
		}
	}
	return TargetInvalid{Harness: "dsh", Target: d.Session, State: TargetDeleted, Detail: "no session directory of that id under " + root}
}

// CheckTarget is the grok wake file's lifecycle (TerminalStates["grok"]).
func (g *Grok) CheckTarget(context.Context) error {
	if g.Wake == "" {
		return nil
	}
	if _, err := os.Stat(g.Wake); errors.Is(err, fs.ErrNotExist) {
		return TargetInvalid{Harness: "grok", Target: g.Wake, State: TargetDeleted, Detail: "the wake file does not exist; nova-friend install makes it"}
	}
	return nil
}

// CheckTarget is the tmux target's lifecycle (TerminalStates["tmux"]): only a
// pane id is terminal when gone; a session name is made again by host, so
// its absence stays a deferral.
func (t *Tmux) CheckTarget(ctx context.Context) error {
	if !strings.HasPrefix(t.Session, "%") {
		return nil
	}
	out, exit, err := t.Run(ctx, t.Dir, "tmux", []string{"list-panes", "-a", "-F", "#{pane_id}"}, "")
	if err != nil || exit != 0 {
		return fmt.Errorf("tmux list-panes: exit %d: %v", exit, err)
	}
	if slices.Contains(strings.Fields(out), t.Session) {
		return nil
	}
	return TargetInvalid{Harness: "tmux", Target: t.Session, State: TargetDeleted, Detail: "tmux lists no pane by that id"}
}

// Target is the friend's bound session as rebind or install last recorded it
// in her state directory (TargetFile): the session, and every session she
// was bound to before, which run and install refuse, so a service reinstalled
// from an old command line cannot resurrect a target she left.
type Target struct {
	Friend  string    `json:"friend"`
	Harness string    `json:"harness"`
	Session string    `json:"session"`
	Retired []string  `json:"retired,omitempty"`
	At      time.Time `json:"at"`
}

// TargetFile is the bound target's file in the state directory.
const TargetFile = "target.json"

// ReadTarget is the bound target in state; found is false when none was recorded.
func ReadTarget(state string) (t Target, found bool, err error) {
	found, err = read(filepath.Join(state, TargetFile), &t)
	return t, found, err
}

// WriteTarget records t in state.
func WriteTarget(state string, t Target) error {
	return write(filepath.Join(state, TargetFile), t)
}

// Bind is t with session bound: the session it replaces retired, and session
// itself no longer retired (a friend may go back to a session that is live).
func (t Target) Bind(friend, harness, session string, now time.Time) Target {
	if t.Session != "" && t.Session != session && !slices.Contains(t.Retired, t.Session) {
		t.Retired = append(t.Retired, t.Session)
	}
	t.Retired = slices.DeleteFunc(t.Retired, func(s string) bool { return s == session })
	t.Friend, t.Harness, t.Session, t.At = friend, harness, session, now
	return t
}

// Refuses is why session may not be started or installed against the bound
// target t, "" when it may: a session the friend rebound away from.
func (t Target) Refuses(session string) string {
	if session == "" || !slices.Contains(t.Retired, session) {
		return ""
	}
	return fmt.Sprintf("session %s was rebound away from (now %s, %s); a service carrying the old id cannot bring it back: %s",
		session, dash(t.Session), t.At.UTC().Format(time.RFC3339), RebindLine(t.Friend))
}

// RowSession reads the friend row's session off her beat's answer
// (row_session=<id>, as friend sync last wrote her nova-config row); "" when
// the answer names none.
func RowSession(answer string) string {
	for _, w := range strings.Fields(answer) {
		if v, found := strings.CutPrefix(w, "row_session="); found {
			return v
		}
	}
	return ""
}

// Supersedes is the TargetInvalid when her row's session (row, "" when it
// names none) is not session, the one the daemon delivers into, nil when it
// may go on: the row names none, names session, or lags a rebind made here
// (t, the bound target in her state directory, names session and has retired
// the row's), which friend sync carries to her row within a pass.
func (t Target) Supersedes(harness, session, row string) *TargetInvalid {
	if session == "" || row == "" || row == session {
		return nil
	}
	if t.Session == session && slices.Contains(t.Retired, row) {
		return nil
	}
	return &TargetInvalid{Harness: harness, Target: session, State: TargetSuperseded,
		Detail: "her nova-config friend row names session " + row + "; a daemon started on another id delivers nothing into it"}
}

// FlagOf is the value of --name in args, the daemon's part of a plist's
// command line (PlistArgs); "" when absent.
func FlagOf(args []string, name string) string {
	part := daemonPart(args)
	for i := 0; i+1 < len(part); i++ {
		if part[i] == "--"+name {
			return part[i+1]
		}
	}
	return ""
}

// RebindPlist is plist with its daemon's --session set to session (added
// after --dir's value when the plist named none), and the session it named
// before ("" for none). A plist with no run in its ProgramArguments is
// refused: there is no daemon to rebind.
func RebindPlist(plist, session string) (rebound, old string, err error) {
	args := PlistArgs(plist)
	run := slices.Index(args, "run")
	if run < 0 {
		return "", "", errors.New("the plist runs no nova-friend run: no daemon to rebind")
	}
	next := slices.Clone(args)
	if i := slices.Index(next[run:], "--session"); i >= 0 && run+i+1 < len(next) {
		old = next[run+i+1]
		next[run+i+1] = session
	} else {
		at := len(next)
		if d := slices.Index(next[run:], "--dir"); d >= 0 && run+d+2 <= len(next) {
			at = run + d + 2
		}
		next = slices.Insert(next, at, "--session", session)
	}
	start := strings.Index(plist, "<key>ProgramArguments</key>")
	open := strings.Index(plist[max(start, 0):], "<array>")
	if start < 0 || open < 0 {
		return "", "", errors.New("the plist has no ProgramArguments array")
	}
	open += start + len("<array>")
	end := strings.Index(plist[open:], "</array>")
	if end < 0 {
		return "", "", errors.New("the plist's ProgramArguments array is not closed")
	}
	end += open
	var b strings.Builder
	b.WriteString("\n")
	for _, a := range next {
		b.WriteString("    <string>" + esc(a) + "</string>\n")
	}
	b.WriteString("  ")
	return plist[:open] + b.String() + plist[end:], old, nil
}
