package friend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
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
// and normally blocks until the turn ends. Desktop Codex instead confirms
// admission to the owning app. Exit 0 acks the bus message in either case
// (SPEC-FRIEND.md, the deliver command and Codex).
type Deliverer interface {
	Deliver(ctx context.Context, text string) (exit int, err error)
}

// Deferred is a Deliverer's answer when the session cannot take a turn now
// and nothing has failed (for example, an open Codex chat whose app does
// not confirm admission). The daemon keeps the message in hand, tries again
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

// DeliverBudget bounds one delivery into a harness: a turn that runs longer
// is stuck, the message stays pending, and the next run of the loop hands
// it in again.
const DeliverBudget = 10 * time.Minute

// RealExec runs the command through os/exec, under DeliverBudget: the
// program directly, never a shell, so a message's text is never
// interpolated. The command is its own session leader (Setsid), and on the
// budget or a cancel the whole group is signalled, SIGTERM then SIGKILL after
// KillDelay: a harness that forks (opencode run does) leaves no orphan
// behind a timeout (the finding of 2026-10-04: subproc.Long sets no group).
// On a nonzero exit the output carries the head of stderr after stdout: a
// harness says why it refused there (dsh does).
func RealExec(ctx context.Context, dir, name string, args []string, stdin string) (string, int, error) {
	return realExec(ctx, DeliverBudget, KillDelay, dir, name, args, stdin)
}

func realExec(ctx context.Context, budget, killDelay time.Duration, dir, name string, args []string, stdin string) (string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	var out, stderr strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	ownGroup(cmd)
	cmd.WaitDelay = killDelay // the pipes close this long after the group is signalled
	err := cmd.Run()
	if ctx.Err() != nil && cmd.Process != nil {
		killGroup(cmd.Process.Pid) // the leader is dead by now; what it forked is not
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if ctx.Err() != nil {
			return out.String(), exitErr.ExitCode(), fmt.Errorf("the delivery ran past %s and was stopped with its process group", budget)
		}
		return out.String() + Head(stderr.String(), OutputKept), exitErr.ExitCode(), nil
	}
	return out.String(), 0, err
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
		return &Codex{Dir: dir, Session: session, Out: out}, nil
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
	return exit, err
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
