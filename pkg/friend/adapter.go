package friend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Harnesses are the harness names run and install take, in the order the
// help lists them; OpenCode, Codex, Antigravity, DSH, Gemini, Grok and tmux (a TUI hosted by nova-friend host) have a
// deliver command, the rest refuse honestly (Stub), the surveyed ones with
// their reason.
var Harnesses = append([]string{"opencode", "codex", "claude", "antigravity", "dsh", "gemini", "grok", "tmux"}, RefusedHarnesses...)

// Deliverer pushes one text into the friend's running session as a turn
// and normally blocks until the turn ends. Codex queue instead confirms
// enqueue acceptance; the app runs it after the active turn ends. Exit 0
// acks the bus message (SPEC-FRIEND.md, the deliver command and Codex).
type Deliverer interface {
	Deliver(ctx context.Context, text string) (exit int, err error)
}

// TextLimit is the most bytes of text d takes as one turn: its own
// TextLimit() when it has one above zero, else BatchBytes. The daemon's
// envelope is cut to it (Envelope).
func TextLimit(d Deliverer) int {
	if l, ok := d.(interface{ TextLimit() int }); ok && l.TextLimit() > 0 {
		return l.TextLimit()
	}
	return BatchBytes
}

// Deferred is a Deliverer's answer when the session cannot take a turn now
// and nothing has failed (for example, neither Codex queue nor resume can
// accept it). The daemon keeps the message in hand, tries again
// after RecheckEvery, counts nothing toward MaxDeliveries and acks nothing,
// so a chat open all day loses no message. Remedy is set when no retry
// can succeed, because the adapter cannot drive the session at all (dsh: a
// session under an agent preset): what the friend does instead, which
// nova-friend install and run refuse with (PushProof).
type Deferred struct{ Reason, Remedy string }

func (d Deferred) Error() string { return "deferred: " + d.Reason }

// SessionRefused is a Deliverer's answer when the turn's output says the
// session cannot take a turn at all, whatever the exit code (dsh: a session
// under an agent preset, a missing provider key; DSHRefusal): a failed
// delivery, never a delivered one. Reason is one line naming the session and
// why, what the status and the friend's row say; Detail what to do; Remedy as
// in Deferred. The daemon marks the session broken with Reason on the first
// such turn, keeps every message pending and tries again every RecheckEvery,
// and a turn that succeeds clears it (docs/SPEC-FRIEND.md, "A turn the
// session cannot take"). As a Deferred it is the same message kept, so a
// reader of Deferred (PushProof, the check) still sees its remedy.
type SessionRefused struct{ Session, Reason, Detail, Remedy string }

func (s SessionRefused) Error() string {
	return "the session cannot take a turn: " + s.Reason + "; " + s.Detail
}

// As answers a SessionRefused as the Deferred it also is: the message stays
// pending, and a session it cannot drive carries its Remedy.
func (s SessionRefused) As(target any) bool {
	d, ok := target.(*Deferred)
	if ok {
		*d = Deferred{Reason: s.Reason + "; " + s.Detail, Remedy: s.Remedy}
	}
	return ok
}

// Exec runs one command for an adapter: the program, its arguments and its
// working directory, with the text on stdin, answering what it printed and
// its exit code. The daemon passes the real one (RealExec); a test its own.
type Exec func(ctx context.Context, dir, name string, args []string, stdin string) (stdout string, exit int, err error)

// OutputKept bounds how much of a turn's output the daemon keeps in its
// record: the head, enough to see what the session did with the message.
const OutputKept = 2048

// outputKey carries, in a delivery's context, what to call when the command
// prints: the daemon's watch on a running turn (WithOutputSeen).
type outputKey struct{}

// WithOutputSeen is ctx carrying seen, called each time the command a
// delivery runs prints to stdout or stderr: a turn that prints is working,
// and only a turn silent past the daemon's SilentStop is stopped.
func WithOutputSeen(ctx context.Context, seen func()) context.Context {
	return context.WithValue(ctx, outputKey{}, seen)
}

// tailKey carries, in a delivery's context, what to hand each write the command prints:
// the daemon's tail of a lane's output, the lines a capped lane's report quotes
// (WithOutputTail, lane_cap.go).
type tailKey struct{}

// WithOutputTail is ctx carrying tail, handed each write the command a delivery runs
// prints to stdout or stderr.
func WithOutputTail(ctx context.Context, tail func([]byte)) context.Context {
	return context.WithValue(ctx, tailKey{}, tail)
}

// seenWriter is a Builder that says each write to the context's watch and tail.
type seenWriter struct {
	b    strings.Builder
	seen func()
	tail func([]byte)
}

func (w *seenWriter) Write(p []byte) (int, error) {
	if len(p) > 0 && w.seen != nil {
		w.seen()
	}
	if len(p) > 0 && w.tail != nil {
		w.tail(p)
	}
	return w.b.Write(p)
}

// RealExec runs the command through os/exec: the program directly, never a
// shell, so a message's text is never interpolated. No clock bounds it: a
// turn that prints keeps running however long it takes, and the daemon
// stops one silent past its SilentStop by cancelling ctx (the finding of
// 2026-10-04: a fixed ten-minute cap killed real work mid-turn). Every write
// to stdout or stderr is said to the watch in ctx (WithOutputSeen). The
// command is its own session leader (Setsid), and on a cancel the whole
// group is signalled, SIGTERM then SIGKILL after KillDelay: a harness that
// forks (opencode run does) leaves no orphan behind a stop. On a nonzero
// exit the output carries the head of stderr after stdout: a harness says
// why it refused there (dsh does).
func RealExec(ctx context.Context, dir, name string, args []string, stdin string) (string, int, error) {
	return realExec(ctx, KillDelay, dir, name, args, stdin)
}

func realExec(ctx context.Context, killDelay time.Duration, dir, name string, args []string, stdin string) (string, int, error) {
	seen, _ := ctx.Value(outputKey{}).(func())
	tail, _ := ctx.Value(tailKey{}).(func([]byte))
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	} // else /dev/null: a headless opencode run with stdin left open hangs at init (measured 2026-10-04)
	out, stderr := &seenWriter{seen: seen, tail: tail}, &seenWriter{seen: seen, tail: tail}
	cmd.Stdout = out
	cmd.Stderr = stderr
	ownGroup(cmd)
	cmd.WaitDelay = killDelay // the pipes close this long after the group is signalled
	err := cmd.Run()
	if ctx.Err() != nil && cmd.Process != nil {
		killGroup(cmd.Process.Pid) // the leader is dead by now; what it forked is not
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if ctx.Err() != nil {
			return out.b.String(), exitErr.ExitCode(), errors.New("the delivery was stopped with its process group")
		}
		return out.b.String() + Head(stderr.b.String(), OutputKept), exitErr.ExitCode(), nil
	}
	return out.b.String(), 0, err
}

// ProviderRefused is a Deliverer's answer when the turn reached the
// session's model provider and the provider refused the request itself
// (an invalid_request_error, an authentication_error): the session is at
// fault, not the message. The daemon counts it toward nothing a message
// owns; the same refusal on BrokenAfter turns in a row marks the session
// broken (the finding of 2026-10-04: a friend's session refused every turn
// for two hours and nothing said so).
type ProviderRefused struct{ Session, Reason string }

func (p ProviderRefused) Error() string {
	return "the provider refused the turn in session " + p.Session + ": " + p.Reason
}

// providerErrorType is a provider's JSON error, the shape OpenAI- and
// Anthropic-style APIs print and harnesses pass on: "type":"<x>_error".
var (
	providerErrorType    = regexp.MustCompile(`"type"\s*:\s*"([a-z_]+_error)"`)
	providerErrorMessage = regexp.MustCompile(`"message"\s*:\s*"([^"]{0,200})`)
)

// transientProviderErrors are provider errors that pass by themselves: a
// rate limit, an overload, the provider's own fault. They are failures of
// a turn, never a refusal of the session.
var transientProviderErrors = map[string]bool{"rate_limit_error": true, "overloaded_error": true, "api_error": true}

// ProviderRefusal reads a failed turn's output for a provider's refusal:
// the error type and the head of its message, one line; ok is false when
// the output carries none, or only a transient one.
func ProviderRefusal(out string) (reason string, ok bool) {
	m := providerErrorType.FindStringSubmatch(out)
	if m == nil || transientProviderErrors[m[1]] {
		return "", false
	}
	reason = m[1]
	if msg := providerErrorMessage.FindStringSubmatch(out); msg != nil {
		reason += ": " + strings.Join(strings.Fields(msg[1]), " ")
	}
	return reason, true
}

// refused is the answer of an adapter whose turn exited nonzero: the
// provider's refusal when the output carries one, else the exit as it was.
func refused(session string, out string, exit int, err error) (int, error) {
	if exit != 0 && err == nil {
		if reason, ok := ProviderRefusal(out); ok {
			return exit, ProviderRefused{Session: session, Reason: reason}
		}
	}
	return exit, err
}

// KillDelay is how long a signalled group gets to end before SIGKILL.
const KillDelay = 5 * time.Second

// NewDeliverer is the adapter for harness, in the friend's directory, into
// session (empty: the newest session of that directory where the harness
// can name one). An unknown harness is refused with the names there are.
func NewDeliverer(harness, dir, session string, run Exec, out io.Writer) (Deliverer, error) {
	switch harness {
	case "opencode":
		return &OpenCode{Dir: dir, Session: session, Run: run, Out: out}, nil
	case "codex":
		return &Codex{Dir: dir, Session: session, Run: run, Out: out}, nil
	case "grok":
		return &Grok{Dir: dir, Wake: session, Run: run, Out: out}, nil
	case "antigravity":
		return &Antigravity{Dir: dir, Session: session, Run: run, Out: out}, nil
	case "claude":
		return &ClaudeWake{Dir: dir, Name: session, Out: out}, nil
	case "dsh":
		return &DSH{Dir: dir, Session: session, Run: run, Out: out}, nil
	case "gemini":
		return &Gemini{Dir: dir, Session: session, Run: run, Out: out}, nil
	case "tmux":
		return &Tmux{Dir: dir, Session: session, Run: run, Out: out}, nil
	}
	if reason, ok := Refusals[harness]; ok {
		return Stub{Harness: harness, Reason: reason}, nil
	}
	return nil, fmt.Errorf("%q is no harness; the harnesses are %s", harness, strings.Join(Harnesses, ", "))
}

// OpenCode delivers through `opencode run --session <id> <text>` run with Dir
// as its working directory (the process's, never a flag: opencode v2.0.20's
// run has no --dir, and every delivery that passed one exited 1, "Unrecognized
// flag: --dir", 2026-10-06), which blocks for the whole turn; without a
// session named, the newest session whose directory is Dir, from `opencode
// session list --format json`, so a friend who starts a fresh session is still
// reached. CheckRun, at the daemon's start, refuses an opencode whose run
// lacks a flag the adapter passes.
type OpenCode struct {
	Dir, Session string
	Run          Exec
	Program      string    // "opencode" when empty
	Out          io.Writer // where the turn's output goes, when set: the daemon's record
	// Allow is every other path the friend's directory is reached by (a
	// symlink in the home directory): with Dir and its real path, allowed in
	// the project config before a turn (AllowDirs), so a headless run never
	// auto-rejects a tool call there. Nil: the config is left alone.
	Allow []string
	// Standalone passes --standalone to every run: opencode 2.0.25 (the
	// background service, 2026-10-09) makes `opencode run` attach to a shared
	// background service (`opencode serve --service`, started once, detached)
	// instead of a private server, so a sealed provider key (nova-secrets exec)
	// reaches no provider ("Incorrect API key provided"); --standalone keeps the
	// run in this process, with this environment. CheckRun sets it when the
	// installed run lists the flag, so an older opencode is never handed it.
	Standalone bool

	turns SessionTurns // the session's last turns, its liveness (alive.go)
}

// runVerb is the run verb as this opencode takes it: `run`, and --standalone
// when CheckRun found it (Standalone).
func (o *OpenCode) runVerb(args ...string) []string {
	verb := []string{"run"}
	if o.Standalone {
		verb = append(verb, "--standalone")
	}
	return append(verb, args...)
}

// listVerb is `session list --format json`, and --standalone after it when
// CheckRun found the flag: without it opencode 2.0.25 starts its managed service
// on a fixed port, and a listing under a lane's wall (its own HOME) exits 1
// against the instance the daemon's HOME already holds ("Managed service port
// ... is already in use"). The flag is `session list`'s, not `session`'s: placed
// between them opencode answers "Unrecognized flag: --standalone in command
// opencode session" (measured on 2.0.20 and 2.0.25), so it goes last.
func (o *OpenCode) listVerb() []string {
	verb := []string{"session", "list", "--format", "json"}
	if o.Standalone {
		verb = append(verb, "--standalone")
	}
	return verb
}

func (o *OpenCode) program() string {
	if o.Program == "" {
		return "opencode"
	}
	return o.Program
}

// session is one row of `opencode session list --format json`.
type session struct {
	ID        string `json:"id"`
	Directory string `json:"directory"`
	Updated   int64  `json:"updated"`
}

// decodeSessions reads the listing's JSON. A fresh install with no session
// yet prints nothing at all (opencode 1.18.20, 2026-10-08, zero bytes), so an
// empty or whitespace-only listing is an empty list: no session is the
// friend's state, never a broken harness. Anything else that is not a JSON
// list is refused with the JSON error alone (it names the offending character
// and its offset); the listing's own text never enters the error, since the
// harness's stdout can carry anything (docs/SPEC-CI.md, secrets in errors).
// Its first line goes to the daemon's record instead (recordListing).
func decodeSessions(listing string) ([]session, error) {
	if strings.TrimSpace(listing) == "" {
		return nil, nil
	}
	var rows []session
	if err := json.Unmarshal([]byte(listing), &rows); err != nil {
		return nil, fmt.Errorf("opencode session list: not a JSON list: %v", err)
	}
	return rows, nil
}

// recordListing writes the first line of a listing that was not a JSON list
// to the daemon's record, when there is one, so the operator sees what the
// harness printed without it ever entering an error.
func recordListing(out io.Writer, listing string) {
	if out == nil {
		return
	}
	first, _, _ := strings.Cut(strings.TrimSpace(listing), "\n")
	fmt.Fprintf(out, "opencode session list: not a JSON list; its first line: %q\n", Head(first, OutputKept))
}

// newestOf picks the most recently updated session of dir from decoded rows.
func newestOf(rows []session, dir string) (string, error) {
	best := session{}
	for _, r := range rows {
		if r.Directory == dir && r.Updated > best.Updated {
			best = r
		}
	}
	if best.ID == "" {
		return "", fmt.Errorf("no opencode session for %s; start one there, or name one with --session", dir)
	}
	return best.ID, nil
}

func (o *OpenCode) Deliver(ctx context.Context, text string) (int, error) {
	if o.Allow != nil {
		o.allow()
	}
	id := o.Session
	if id == "" {
		listing, exit, err := o.Run(ctx, o.Dir, o.program(), o.listVerb(), "")
		if err != nil {
			return 0, fmt.Errorf("opencode session list: %w", err)
		}
		if exit != 0 {
			return 0, fmt.Errorf("opencode session list exited %d", exit)
		}
		rows, err := decodeSessions(listing)
		if err != nil {
			recordListing(o.Out, listing)
			return 0, err
		}
		if id, err = newestOf(rows, o.Dir); err != nil {
			return 0, err
		}
	}
	out, exit, err := o.Run(ctx, o.Dir, o.program(), o.runVerb("--session", id, text), "")
	if o.Out != nil && out != "" {
		fmt.Fprintln(o.Out, strings.TrimRight(Head(out, OutputKept), "\n"))
	}
	exit, err = refused(id, out, exit, err)
	o.turns.saw(id, exit, err)
	return exit, err
}

// OpenCodeRunFlags are the flags of `opencode run` the adapter passes: the
// session of a turn, and the model of a read. The directory is never one: it
// is the process's working directory.
var OpenCodeRunFlags = []string{"--session", "--model"}

// optionFlag is a flag as a help text lists it: two dashes, a letter, then
// letters, digits and dashes.
var optionFlag = regexp.MustCompile(`--[a-z][a-z0-9-]*`)

// CheckRun reads the installed opencode once, at the daemon's start: its
// version (`opencode --version`) and its run verb's flags (`opencode run
// --help`), and is a one-line refusal naming the version when the help lacks
// a flag the adapter passes (OpenCodeRunFlags), so the next change of the CLI
// is a named refusal and not a silent exit 1 on every delivery (the finding
// of 2026-10-06: --dir, gone from run in v2.0.20). A help that lists no flag
// at all, or that cannot be read, cannot tell, and is nil: the deliveries say
// what they meet.
func (o *OpenCode) CheckRun(ctx context.Context) error {
	if o.Run == nil {
		return nil
	}
	version := "(version unknown)"
	if out, exit, err := o.Run(ctx, o.Dir, o.program(), []string{"--version"}, ""); err == nil && exit == 0 && strings.TrimSpace(out) != "" {
		version = strings.Fields(out)[0]
	}
	help, _, err := o.Run(ctx, o.Dir, o.program(), []string{"run", "--help"}, "")
	if err != nil {
		return nil
	}
	listed := map[string]bool{}
	for _, f := range optionFlag.FindAllString(help, -1) {
		listed[f] = true
	}
	if len(listed) == 0 {
		return nil
	}
	o.Standalone = listed["--standalone"]
	var missing []string
	for _, f := range OpenCodeRunFlags {
		if !listed[f] {
			missing = append(missing, f)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("opencode %s: its run verb has no %s (opencode run --help), which the adapter passes; no delivery can run until opencode takes it", version, strings.Join(missing, ", no "))
}

// Head is the first n bytes of s, with a note when it was cut.
func Head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("\n[... %d more bytes]", len(s)-n)
}

// Stub is a harness with no deliver command yet: it refuses every delivery
// with the way a session of that harness still reads the bus, so the tool
// is honest. It is Passive: the daemon takes nothing off the stream for it
// (the session's own blocking read does), only peeks, so a ping is still
// answered by the daemon at once and the beat is real.
type Stub struct{ Harness, Reason string }

func (s Stub) Deliver(context.Context, string) (int, error) {
	if s.Reason != "" {
		return 0, fmt.Errorf("no deliver command for %s: %s; run the session's blocking read: nova-bus recv --as <friend>", s.Harness, s.Reason)
	}
	return 0, fmt.Errorf("no deliver command for %s yet; run the session's blocking read: nova-bus recv --as <friend>", s.Harness)
}

// Passive marks a Deliverer that cannot deliver: the daemon reads nothing
// for it.
func (Stub) Passive() {}

// ClaudeWaitLine is the one line a claude session runs as a background task
// (docs/SPEC-FRIEND.md, the Claude paragraph): the session's own blocking
// read of the bus, re-armed with the cursor it printed each time it returns.
// Claude Code has no command that puts a turn into a running session from
// outside; a background task's exit re-invokes the session. It is a command
// run once inside the session, not a flag, an environment variable or a
// wrapper at app start. wake is the file the daemon appends one line to per
// message, so a wait that missed nothing still returns.
func ClaudeWaitLine(friend, wake string) string {
	return "run as a background task, and re-run it with the cursor it printed each time it returns: nova-bus wait --as " + friend + " --after <cursor> --wake-file " + wake
}

// ClaudeWakePath is the wake file of a claude friend: <state>/<friend>.wake,
// in the daemon's state directory, named on the line the session runs.
func ClaudeWakePath(stateDir, friend string) string {
	return filepath.Join(stateDir, friend+".wake")
}

// ClaudeInstallLine is the NOTE install prints for harness claude; every
// other harness gets none.
func ClaudeInstallLine(harness, friend, wake string) string {
	if harness != "claude" {
		return ""
	}
	return ClaudeWaitLine(friend, wake)
}

// ClaudeWake is the claude adapter: Claude Code has no command that puts a
// turn into a running session from outside, so Deliver puts nothing in. It
// appends one line per push to the wake file in Dir, the friend's state
// directory (ClaudeWakePath(Dir, Name)): the clock, then the pushed text on
// one line, which carries the nonce or message id and the path of what was
// pushed. The session's own wait (ClaudeWaitLine), running as a background
// task, returns when the file grows, and its exit re-invokes the session.
// The file is made when absent, synced, and never truncated; a missing Dir
// is a refusal naming it, and nothing is made. Name is the session's name
// for the file, else the friend of Dir's status file. It is Passive: the
// daemon takes nothing off the stream for claude.
type ClaudeWake struct {
	Dir, Name string
	Now       func() time.Time // time.Now when nil
	Out       io.Writer        // the daemon's record, when set
}

func (c *ClaudeWake) Deliver(_ context.Context, text string) (int, error) {
	if fi, err := os.Stat(c.Dir); err != nil || !fi.IsDir() {
		return 0, fmt.Errorf("no state directory %s: the claude wake file is kept there; start the friend's daemon there first, or name the directory it keeps", c.Dir)
	}
	name := c.Name
	if name == "" {
		s, _, _ := ReadStatus(c.Dir) // ignored: an unreadable status names no friend, refused below
		name = s.Friend
	}
	if name == "" {
		return 0, fmt.Errorf("no friend named for the claude wake file in %s: name the session, or start the friend's daemon there", c.Dir)
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	wake := ClaudeWakePath(c.Dir, name)
	f, err := os.OpenFile(wake, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return 0, err
	}
	_, err = f.WriteString(now().UTC().Format(time.RFC3339Nano) + " " + WakeLine(text) + "\n")
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, err
	}
	if c.Out != nil {
		fmt.Fprintln(c.Out, "one line appended to "+wake+"; the session's wait returns and the turn runs after this")
	}
	return 0, nil
}

// Passive marks the claude adapter: the session's own wait reads the stream.
func (*ClaudeWake) Passive() {}

// Known says whether harness is one of Harnesses.
func Known(harness string) bool { return slices.Contains(Harnesses, harness) }
