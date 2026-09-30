// Package subproc is the one place a child process gets its bound.
//
// A short-lived child (git, gh, ssh, sops, tailscale, go tooling, ps) is started through
// Command: it runs under a context whose deadline is the caller's own when the caller
// has one sooner, and otherwise the named default of its kind (Kind.Budget), and it
// carries WaitDelay so that a killed child cannot hang its caller on a pipe. Without
// WaitDelay the kill is not enough: Output and CombinedOutput read until the pipe
// closes, and a grandchild (an ssh under git, a credential helper, a pager) that
// inherited the pipe holds it open after the child is gone.
//
// A long-lived child (a harness run, a member's native child, a server) is started
// through Long: it runs under the caller's cancellable context and has no deadline,
// because how long it runs is not this package's call. WaitDelay is set there too, so
// the wait after its cancellation or exit is bounded, never the run itself.
package subproc

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// WaitDelay is how long a killed or exited child has to close its pipes before Wait
// closes them and returns.
const WaitDelay = 5 * time.Second

// Kind names the default budget of a short-lived child.
type Kind int

const (
	// Git is git in any form: rev-parse, ls-tree, config, grep, show, diff.
	Git Kind = iota
	// GH is the GitHub CLI: one API round trip or a short listing.
	GH
	// SSH is ssh, scp and rsync: a connection, then a file or a short remote command.
	SSH
	// Go is go tooling run as a one-shot: env, list, tool.
	Go
	// Tool is every other one-shot: sops, age-keygen, tailscale, ps, lsof, sysctl,
	// diskutil, stty, taskkill and the sandbox's own check.
	Tool
)

// Budgets by kind. A caller with its own context deadline sooner than the kind's budget
// keeps it; a caller that needs longer passes a context with no deadline to Long, or
// sets the option its own runner exposes.
const (
	GitBudget  = 60 * time.Second
	GHBudget   = 120 * time.Second
	SSHBudget  = 300 * time.Second
	GoBudget   = 300 * time.Second
	ToolBudget = 60 * time.Second
)

// Budget is the named default of the kind.
func (k Kind) Budget() time.Duration {
	switch k {
	case Git:
		return GitBudget
	case GH:
		return GHBudget
	case SSH:
		return SSHBudget
	case Go:
		return GoBudget
	default:
		return ToolBudget
	}
}

func (k Kind) String() string {
	switch k {
	case Git:
		return "git"
	case GH:
		return "gh"
	case SSH:
		return "ssh"
	case Go:
		return "go"
	default:
		return "tool"
	}
}

// KindOf is the kind of a program by its base name, for a caller that runs whatever
// name it is handed (a test seam's real implementation).
func KindOf(name string) Kind {
	base := strings.ToLower(filepath.Base(name))
	base = strings.TrimSuffix(base, ".exe")
	switch base {
	case "git":
		return Git
	case "gh":
		return GH
	case "ssh", "scp", "rsync":
		return SSH
	case "go":
		return Go
	default:
		return Tool
	}
}

// Bound narrows ctx to the kind's budget. A context that already ends sooner is kept
// as it is. A nil ctx is context.Background. The cancel function is always non-nil and
// is called when the child has been waited for.
func Bound(ctx context.Context, k Kind) (context.Context, context.CancelFunc) {
	return BoundFor(ctx, k.Budget())
}

// BoundFor is Bound with an explicit budget, for a caller that holds its own number.
func BoundFor(ctx context.Context, budget time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if d, ok := ctx.Deadline(); ok && time.Until(d) <= budget {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, budget)
}

// Command is exec.CommandContext under Bound(ctx, k) with WaitDelay set. The returned
// cancel is called when the child has been waited for (defer it).
func Command(ctx context.Context, k Kind, name string, args ...string) (*exec.Cmd, context.CancelFunc) {
	return CommandFor(ctx, k.Budget(), name, args...)
}

// CommandFor is Command with an explicit budget.
func CommandFor(ctx context.Context, budget time.Duration, name string, args ...string) (*exec.Cmd, context.CancelFunc) {
	bound, cancel := BoundFor(ctx, budget)
	cmd := exec.CommandContext(bound, name, args...)
	cmd.WaitDelay = WaitDelay
	return cmd, cancel
}

// Context is exec.CommandContext under the caller's own context, unchanged, with
// WaitDelay set: for a caller whose context already carries the deadline it wants (a
// release build's budget, a probe's timeout) and only needs the pipe not to hang after
// the kill.
func Context(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = WaitDelay
	return cmd
}

// Long is the constructor of a long-lived child: exec.CommandContext under the
// caller's cancellable context, no deadline, WaitDelay set. A nil ctx is
// context.Background, which never ends the child.
func Long(ctx context.Context, name string, args ...string) *exec.Cmd {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = WaitDelay
	return cmd
}

// Expired reports whether a deadline, and not the caller's cancellation, ended ctx.
func Expired(ctx context.Context) bool {
	return errors.Is(ctx.Err(), context.DeadlineExceeded)
}

// TimeoutError is what a child killed at its deadline is reported as.
type TimeoutError struct {
	// What is the command line, for the message.
	What string
	// Budget is the time the child was given, when the caller knows it (zero otherwise).
	Budget time.Duration
	// Err is what the wait returned, usually "signal: killed".
	Err error
}

func (e *TimeoutError) Error() string {
	if e.Budget > 0 {
		return fmt.Sprintf("%s did not finish within %s and was killed", e.What, e.Budget)
	}
	return fmt.Sprintf("%s did not finish before its deadline and was killed", e.What)
}

func (e *TimeoutError) Unwrap() error { return e.Err }

// Wrap returns err as a *TimeoutError when ctx was ended by its deadline, and err
// unchanged otherwise (including nil).
func Wrap(ctx context.Context, what string, budget time.Duration, err error) error {
	if err != nil && Expired(ctx) {
		return &TimeoutError{What: what, Budget: budget, Err: err}
	}
	return err
}
