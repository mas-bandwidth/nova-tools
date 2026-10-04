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
// help lists them; one has a deliver command tonight, the rest refuse
// honestly (Stub).
var Harnesses = []string{"opencode", "codex", "claude", "antigravity", "dsh"}

// Deliverer pushes one text into the friend's running session as a turn
// and blocks until the turn ends: its exit code is the harness's, 0 acking
// the message on the bus (SPEC-FRIEND.md, the deliver command).
type Deliverer interface {
	Deliver(ctx context.Context, text string) (exit int, err error)
}

// Exec runs one command for an adapter: the program, its arguments and its
// working directory, with the text on stdin, answering what it printed and
// its exit code. The daemon passes the real one (RealExec); a test its own.
type Exec func(ctx context.Context, dir, name string, args []string, stdin string) (stdout string, exit int, err error)

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
func RealExec(ctx context.Context, dir, name string, args []string, stdin string) (string, int, error) {
	return realExec(ctx, DeliverBudget, KillDelay, dir, name, args, stdin)
}

func realExec(ctx context.Context, budget, killDelay time.Duration, dir, name string, args []string, stdin string) (string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	ownGroup(cmd, killDelay)
	err := cmd.Run()
	if ctx.Err() != nil && cmd.Process != nil {
		killGroup(cmd.Process.Pid) // the leader is dead by now; what it forked is not
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if ctx.Err() != nil {
			return out.String(), exitErr.ExitCode(), fmt.Errorf("the delivery ran past %s and was stopped with its process group", budget)
		}
		return out.String(), exitErr.ExitCode(), nil
	}
	return out.String(), 0, err
}

// KillDelay is how long a signalled group gets to end before SIGKILL.
const KillDelay = 5 * time.Second

// NewDeliverer is the adapter for harness, in the friend's directory, into
// session (empty: the newest session of that directory where the harness
// can name one). An unknown harness is refused with the names there are.
func NewDeliverer(harness, dir, session string, run Exec) (Deliverer, error) {
	switch harness {
	case "opencode":
		return &OpenCode{Dir: dir, Session: session, Run: run}, nil
	case "codex", "claude", "antigravity", "dsh":
		return Stub{Harness: harness}, nil
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
	Program      string // "opencode" when empty
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
	_, exit, err := o.Run(ctx, o.Dir, o.program(), []string{"run", "--session", id, "--dir", o.Dir, text}, "")
	return exit, err
}

// Stub is a harness with no deliver command yet: it refuses every delivery
// with the way a session of that harness still reads the bus, so the tool
// is honest and the message stays pending for that read.
type Stub struct{ Harness string }

func (s Stub) Deliver(context.Context, string) (int, error) {
	return 0, fmt.Errorf("no deliver command for %s yet; run the session's blocking read: nova-bus2 recv --as <friend>", s.Harness)
}

// Known says whether harness is one of Harnesses.
func Known(harness string) bool { return slices.Contains(Harnesses, harness) }
