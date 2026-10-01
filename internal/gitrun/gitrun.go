// Package gitrun is the one runner for a one-shot git child.
//
// Every git this repository starts for an answer goes through here: it is bounded by
// the caller's context or by a named default (subproc.GitBudget, 60 s; subproc.GitLongBudget,
// 300 s for a command that goes to the network), it carries WaitDelay so a killed git
// cannot hang its caller on the pipe that git's own child (an ssh, a credential helper, a
// pager) holds open, and it reports a kill as a *subproc.TimeoutError. The environment is
// the caller's choice: a caller that scrubs it passes the scrubbed list in Options.Env,
// and a caller that must leave it intact (a hook that was handed GIT_INDEX_FILE) leaves
// Env nil.
package gitrun

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// DefaultTimeout is how long one local git may run when the caller's context has no
// sooner deadline and Options.Timeout is zero; a network command gets
// subproc.GitLongBudget (see subproc.GitBudgetFor).
const DefaultTimeout = subproc.GitBudget

// Options is how one git runs.
type Options struct {
	// Bin is the git program; empty is "git" on PATH.
	Bin string
	// C, when set, is passed as `git -C C`.
	C string
	// Dir, when set, is the child's working directory.
	Dir string
	// Env is the child's whole environment; nil inherits the caller's.
	Env []string
	// Stdin, when set, is the child's standard input.
	Stdin io.Reader
	// Timeout overrides the default budget when positive.
	Timeout time.Duration
	// WaitDelay overrides subproc.WaitDelay when positive.
	WaitDelay time.Duration
}

// Command builds the git child: -C, directory, environment, deadline and WaitDelay
// applied, nothing started. The returned cancel is called once the child is waited for.
func Command(ctx context.Context, o Options, args ...string) (*exec.Cmd, context.CancelFunc) {
	b := build(ctx, o, args)
	return b.Cmd, b.Cancel
}

// Prepare is Command for a caller that reads the stream itself and must tell a
// deadline kill from its own failure: the Bounded carries the bound context, whose
// subproc.Expired says the deadline ended the child.
func Prepare(ctx context.Context, o Options, args ...string) subproc.Bounded {
	return build(ctx, o, args)
}

func build(ctx context.Context, o Options, args []string) subproc.Bounded {
	budget := o.Timeout
	if budget <= 0 {
		budget = subproc.GitBudgetFor(args)
	}
	bin := o.Bin
	if bin == "" {
		bin = "git"
	}
	full := args
	if o.C != "" {
		full = append([]string{"-C", o.C}, args...)
	}
	b := subproc.Prepare(ctx, budget, bin, full...)
	if o.WaitDelay > 0 {
		b.Cmd.WaitDelay = o.WaitDelay
	}
	b.Cmd.Dir = o.Dir
	if o.Env != nil {
		b.Cmd.Env = o.Env
	}
	if o.Stdin != nil {
		b.Cmd.Stdin = o.Stdin
	}
	return b
}

// Result is the two streams of one finished git.
type Result struct {
	Stdout, Stderr []byte
}

// Run runs git and returns its stdout and stderr apart. A git killed at its deadline
// returns a *subproc.TimeoutError.
func Run(ctx context.Context, o Options, args ...string) (Result, error) {
	b := build(ctx, o, args)
	defer b.Cancel()
	var out, errb bytes.Buffer
	b.Cmd.Stdout, b.Cmd.Stderr = &out, &errb
	err := b.Cmd.Run()
	return Result{Stdout: out.Bytes(), Stderr: errb.Bytes()}, b.Wrap("git "+strings.Join(args, " "), err)
}

// Combined runs git and returns its stdout and stderr interleaved, as one stream.
func Combined(ctx context.Context, o Options, args ...string) ([]byte, error) {
	b := build(ctx, o, args)
	defer b.Cancel()
	out, err := b.Cmd.CombinedOutput()
	return out, b.Wrap("git "+strings.Join(args, " "), err)
}

// Error is a failed git as Output reports it: the command, the cause and git's own stderr.
type Error struct {
	Args   []string
	Err    error
	Stderr string
}

func (e *Error) Error() string {
	if e.Stderr == "" {
		return fmt.Sprintf("git %s: %v", strings.Join(e.Args, " "), e.Err)
	}
	return fmt.Sprintf("git %s: %v: %s", strings.Join(e.Args, " "), e.Err, e.Stderr)
}

func (e *Error) Unwrap() error { return e.Err }

// Output runs git and returns its stdout with its leading and trailing blanks trimmed. A failure is an
// *Error (which unwraps to the cause, so a *subproc.TimeoutError is still found).
func Output(ctx context.Context, o Options, args ...string) (string, error) {
	res, err := Run(ctx, o, args...)
	if err != nil {
		return "", &Error{Args: args, Err: err, Stderr: strings.TrimSpace(string(res.Stderr))}
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}
