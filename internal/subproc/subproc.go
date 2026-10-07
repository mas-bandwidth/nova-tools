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
	GitBudget = 60 * time.Second
	// GitLongBudget is a git that moves a whole repository or tree: clone, fetch, pull,
	// push, ls-remote (any network git), and a status, add, checkout, reset or clean of a
	// large worktree.
	GitLongBudget = 300 * time.Second
	GHBudget      = 120 * time.Second
	SSHBudget     = 300 * time.Second
	GoBudget      = 300 * time.Second
	ToolBudget    = 60 * time.Second
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

// GitBudgetFor is the budget of one git command line: GitLongBudget for a subcommand
// that goes to the network (clone, fetch, pull, push, ls-remote, submodule), GitBudget
// for the rest. Leading options are skipped, and one that takes a path or a config
// assignment as its value (-C dir, -c key=value, --git-dir dir, --work-tree dir, in the
// separated form; the equals form is a plain flag) consumes that value, so a repository
// path is never read as the subcommand.
func GitBudgetFor(args []string) time.Duration {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-C" || a == "-c" || a == "--git-dir" || a == "--work-tree":
			i++
		case strings.HasPrefix(a, "-"):
		default:
			switch a {
			case "clone", "fetch", "pull", "push", "ls-remote", "submodule":
				return GitLongBudget
			}
			return GitBudget
		}
	}
	return GitBudget
}

// BudgetOf is the default budget of a program run by name with these arguments: its
// kind's, and for git the command line's (GitBudgetFor).
func BudgetOf(name string, args []string) time.Duration {
	if k := KindOf(name); k != Git {
		return k.Budget()
	}
	return GitBudgetFor(args)
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

// BoundFor narrows ctx to budget. A context that already ends sooner is kept as it is.
// A nil ctx is context.Background. The cancel function is always non-nil and is called
// when the child has been waited for.
func BoundFor(ctx context.Context, budget time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if callerSooner(ctx, budget) {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, budget)
}

// callerSooner reports whether ctx already ends within budget.
func callerSooner(ctx context.Context, budget time.Duration) bool {
	d, ok := ctx.Deadline()
	return ok && time.Until(d) <= budget
}

// Bounded is one child built under a bound, not yet started.
type Bounded struct {
	Cmd *exec.Cmd
	// Ctx is the bound context; Expired(Ctx) says the deadline ended the child.
	Ctx context.Context
	// Budget is the time the child was given, zero when the caller's own deadline was
	// the sooner one, so a message never names a budget that did not apply.
	Budget time.Duration
	// Cancel is called once the child has been waited for.
	Cancel context.CancelFunc
}

// Prepare builds a child under BoundFor(ctx, budget) with WaitDelay set.
func Prepare(ctx context.Context, budget time.Duration, name string, args ...string) Bounded {
	if ctx == nil {
		ctx = context.Background()
	}
	named := budget
	if callerSooner(ctx, budget) {
		named = 0
	}
	bound, cancel := BoundFor(ctx, budget)
	return Bounded{Cmd: Context(bound, name, args...), Ctx: bound, Budget: named, Cancel: cancel}
}

// Wrap returns err as a *TimeoutError when the deadline ended the child, and err
// unchanged otherwise (including nil).
func (b Bounded) Wrap(what string, err error) error {
	if err != nil && Expired(b.Ctx) {
		return &TimeoutError{What: what, Budget: b.Budget, Err: err}
	}
	return err
}

// Command is exec.CommandContext under Bound(ctx, k) with WaitDelay set. The returned
// cancel is called when the child has been waited for (defer it).
func Command(ctx context.Context, k Kind, name string, args ...string) (*exec.Cmd, context.CancelFunc) {
	return CommandFor(ctx, k.Budget(), name, args...)
}

// CommandFor is Command with an explicit budget.
func CommandFor(ctx context.Context, budget time.Duration, name string, args ...string) (*exec.Cmd, context.CancelFunc) {
	b := Prepare(ctx, budget, name, args...)
	return b.Cmd, b.Cancel
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

// Long is the constructor of a long-lived child: Context under the caller's cancellable
// context, no deadline. A nil ctx is context.Background, which never ends the child.
func Long(ctx context.Context, name string, args ...string) *exec.Cmd {
	if ctx == nil {
		ctx = context.Background()
	}
	return Context(ctx, name, args...)
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
