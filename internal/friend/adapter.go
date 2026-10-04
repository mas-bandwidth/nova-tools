package friend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Harnesses are the harness names run and install take, in the order the
// help lists them; OpenCode, Codex, Antigravity, DSH, Gemini and Grok have a
// deliver command, the rest refuse honestly (Stub), the surveyed ones with
// their reason.
var Harnesses = append([]string{"opencode", "codex", "claude", "antigravity", "dsh", "gemini", "grok"}, RefusedHarnesses...)

// Deliverer pushes one text into the friend's running session as a turn
// and blocks until the turn ends: its exit code is the harness's, 0 acking
// the message on the bus (SPEC-FRIEND.md, the deliver command).
type Deliverer interface {
	Deliver(ctx context.Context, text string) (exit int, err error)
}

// Deferred is a Deliverer's answer when the session cannot take a turn now
// and nothing has failed (the Codex chat open in the app, holding the
// thread's writer lock). The daemon keeps the message in hand, tries again
// after RecheckEvery, counts nothing toward MaxDeliveries and acks nothing,
// so a chat open all day loses no message.
type Deferred struct{ Reason string }

func (d Deferred) Error() string { return "deferred: " + d.Reason }

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

// seenWriter is a Builder that says each write to the context's watch.
type seenWriter struct {
	b    strings.Builder
	seen func()
}

func (w *seenWriter) Write(p []byte) (int, error) {
	if len(p) > 0 && w.seen != nil {
		w.seen()
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
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	} // else /dev/null: a headless opencode run with stdin left open hangs at init (measured 2026-10-04)
	out, stderr := &seenWriter{seen: seen}, &seenWriter{seen: seen}
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
		return Stub{Harness: harness}, nil
	case "dsh":
		return &DSH{Dir: dir, Session: session, Run: run, Out: out}, nil
	case "gemini":
		return &Gemini{Dir: dir, Session: session, Run: run, Out: out}, nil
	}
	if reason, ok := Refusals[harness]; ok {
		return Stub{Harness: harness, Reason: reason}, nil
	}
	return nil, fmt.Errorf("%q is no harness; the harnesses are %s", harness, strings.Join(Harnesses, ", "))
}

// OpenCode delivers through `opencode run --session <id> --dir <dir> <text>`,
// which blocks for the whole turn; without a session named, the newest
// session whose directory is Dir, from `opencode session list --format json`,
// so a friend who starts a fresh session is still reached.
type OpenCode struct {
	Dir, Session string
	Run          Exec
	Program      string    // "opencode" when empty
	Out          io.Writer // where the turn's output goes, when set: the daemon's record
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

// NewestSession picks the most recently updated session of dir from the
// listing's JSON.
func NewestSession(listing, dir string) (string, error) {
	var rows []session
	if err := json.Unmarshal([]byte(listing), &rows); err != nil {
		return "", fmt.Errorf("opencode session list: not a JSON list: %v", err)
	}
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
	id := o.Session
	if id == "" {
		listing, exit, err := o.Run(ctx, o.Dir, o.program(), []string{"session", "list", "--format", "json"}, "")
		if err != nil {
			return 0, fmt.Errorf("opencode session list: %w", err)
		}
		if exit != 0 {
			return 0, fmt.Errorf("opencode session list exited %d", exit)
		}
		if id, err = NewestSession(listing, o.Dir); err != nil {
			return 0, err
		}
	}
	out, exit, err := o.Run(ctx, o.Dir, o.program(), []string{"run", "--session", id, "--dir", o.Dir, text}, "")
	if o.Out != nil && out != "" {
		fmt.Fprintln(o.Out, strings.TrimRight(Head(out, OutputKept), "\n"))
	}
	return refused(id, out, exit, err)
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

// Known says whether harness is one of Harnesses.
func Known(harness string) bool { return slices.Contains(Harnesses, harness) }
