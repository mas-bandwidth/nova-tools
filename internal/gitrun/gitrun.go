// Package gitrun is the one runner for a one-shot git child.
//
// Every git this repository starts for an answer goes through here: it is bounded by
// the caller's context or by DefaultTimeout (subproc.GitBudget, 60 s), it carries
// WaitDelay so a killed git cannot hang its caller on the pipe that git's own child
// (an ssh, a credential helper, a pager) holds open, and it reports a kill as a
// *subproc.TimeoutError. The environment is the caller's choice: a caller that scrubs
// it passes the scrubbed list in Options.Env, and a caller that must leave it intact
// (a hook that was handed GIT_INDEX_FILE) leaves Env nil.
package gitrun

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// DefaultTimeout is how long one git may run when the caller's context has no sooner
// deadline and Options.Timeout is zero.
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
	// Timeout overrides DefaultTimeout when positive.
	Timeout time.Duration
	// WaitDelay overrides subproc.WaitDelay when positive.
	WaitDelay time.Duration
}

// Command builds the git child: -C, directory, environment, deadline and WaitDelay
// applied, nothing started. The returned cancel is called once the child is waited for.
func Command(ctx context.Context, o Options, args ...string) (*exec.Cmd, context.CancelFunc) {
	cmd, _, _, cancel := build(ctx, o, args)
	return cmd, cancel
}

// Prepare is Command for a caller that reads the stream itself and must tell a
// deadline kill from its own failure: it also returns the bound context, whose Err is
// context.DeadlineExceeded (subproc.Expired) when the deadline ended the child.
func Prepare(ctx context.Context, o Options, args ...string) (*exec.Cmd, context.Context, context.CancelFunc) {
	cmd, bound, _, cancel := build(ctx, o, args)
	return cmd, bound, cancel
}

// build returns the child, its bound context, the budget it was given (zero when the
// caller's own deadline is the sooner one, so a message never names a budget that did
// not apply) and the cancel.
func build(ctx context.Context, o Options, args []string) (*exec.Cmd, context.Context, time.Duration, context.CancelFunc) {
	budget := o.Timeout
	if budget <= 0 {
		budget = DefaultTimeout
	}
	named := budget
	if ctx != nil {
		if d, ok := ctx.Deadline(); ok && time.Until(d) <= budget {
			named = 0
		}
	}
	full := args
	if o.C != "" {
		full = append([]string{"-C", o.C}, args...)
	}
	bin := o.Bin
	if bin == "" {
		bin = "git"
	}
	bound, cancel := subproc.BoundFor(ctx, budget)
	cmd := exec.CommandContext(bound, bin, full...)
	cmd.WaitDelay = subproc.WaitDelay
	if o.WaitDelay > 0 {
		cmd.WaitDelay = o.WaitDelay
	}
	if o.Dir != "" {
		cmd.Dir = o.Dir
	}
	if o.Env != nil {
		cmd.Env = o.Env
	}
	if o.Stdin != nil {
		cmd.Stdin = o.Stdin
	}
	return cmd, bound, named, cancel
}

// Result is the two streams of one finished git.
type Result struct {
	Stdout, Stderr []byte
}

// Run runs git and returns its stdout and stderr apart. A git killed at its deadline
// returns a *subproc.TimeoutError.
func Run(ctx context.Context, o Options, args ...string) (Result, error) {
	cmd, bound, named, cancel := build(ctx, o, args)
	defer cancel()
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	return Result{Stdout: out.Bytes(), Stderr: errb.Bytes()}, wrap(bound, args, named, err)
}

// Combined runs git and returns its stdout and stderr interleaved, as one stream.
func Combined(ctx context.Context, o Options, args ...string) ([]byte, error) {
	cmd, bound, named, cancel := build(ctx, o, args)
	defer cancel()
	out, err := cmd.CombinedOutput()
	return out, wrap(bound, args, named, err)
}

func wrap(bound context.Context, args []string, budget time.Duration, err error) error {
	if err == nil || !subproc.Expired(bound) {
		return err
	}
	return &subproc.TimeoutError{What: "git " + strings.Join(args, " "), Budget: budget, Err: err}
}
