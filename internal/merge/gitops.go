package merge

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Work list 6: every git this tool runs, and THE MUTATION GUARD.
//
// Four of this tool's rules are enforced here rather than by the callers remembering
// them, in the one function that runs a command, BEFORE the command is built:
//
//	--auto               gh pr merge --auto does not queue on this host, it merges at
//	                     once, and #922 went in red that way on 2026-09-11
//	--force, -f          history the lane rewrote is history nobody can reproduce
//	--force-with-lease   bare, or with a ref and no sha, is not a compare-and-swap
//
// The one spelling allowed is --force-with-lease=refs/heads/<base>:<40-char sha>, used
// by the publication step of rule 21 and nowhere else: the remote lands the push only if
// its ref is exactly that sha, and the object pushed has that sha as its first parent, so
// the ref moves forward by one gated commit and no history is rewritten.
//
// The guard is on run(), not on a Mutate() beside a Run(), because "the one function that
// runs a mutating command" is only true if there is no second function that runs anything.
// The prototype's guard lived in a shell function called mut, and every other call site in
// the file ran git or gh directly.

// GuardError is a refusal the guard made. It is exit 1 -- the tool ran and said NO -- and
// never exit 2: the invocation was readable, and what it asked for is forbidden.
type GuardError struct{ Arg, Why string }

func (e *GuardError) Error() string {
	return fmt.Sprintf("refusing to run a command carrying %s: %s", e.Arg, e.Why)
}

// AsGuardError reports whether err is the guard's refusal, for the exit code.
func AsGuardError(err error) (*GuardError, bool) {
	var g *GuardError
	ok := errors.As(err, &g)
	return g, ok
}

// Runner runs one command and returns its combined output. It is an interface so the
// tests can watch every argument this tool would hand a subprocess, and so that the guard
// is provably in front of all of them.
type Runner interface {
	Run(ctx context.Context, dir, name string, args ...string) (string, error)
}

// Exec is the production runner: the command, in a directory, under the context's
// deadline.
type Exec struct{}

// Run runs the command and returns its output, stdout and stderr together, because the
// line a person opened the terminal to read is git's own and git writes it to stderr.
func (Exec) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return string(out), fmt.Errorf("%s took longer than this run's --timeout allows: %w", name, ctx.Err())
	}
	return string(out), err
}

// Git runs git in one directory under one timeout, through the guard.
type Git struct {
	Dir     string
	Timeout time.Duration
	Runner  Runner
	// lease is the ONE --force-with-lease spelling this Git will let past the guard,
	// set for the length of one publication by Publish and cleared after it. A lease
	// nobody set is a lease the guard refuses, so there is no path to a force-push that
	// does not go through rule 21's publication step.
	lease string
}

// NewGit returns a Git on dir. A nil runner is the production one.
func NewGit(dir string, timeout time.Duration, runner Runner) *Git {
	if runner == nil {
		runner = Exec{}
	}
	return &Git{Dir: dir, Timeout: timeout, Runner: runner}
}

// In returns the same git pointed at another directory, sharing the timeout and runner.
func (g *Git) In(dir string) *Git { return NewGit(dir, g.Timeout, g.Runner) }

// Run is the only place in this tool where a command reaches a subprocess, and the guard
// is the first thing in it.
func (g *Git) Run(args ...string) (string, error) {
	if err := guard(args, g.lease); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), g.Timeout)
	defer cancel()
	out, err := g.Runner.Run(ctx, g.Dir, "git", args...)
	if err != nil {
		return out, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, oneLineOf(out))
	}
	return out, nil
}

// Out is Run with the output trimmed, for the many commands whose answer is one token.
func (g *Git) Out(args ...string) (string, error) {
	out, err := g.Run(args...)
	return strings.TrimSpace(out), err
}

// guard refuses the four spellings, in any argument, before the command is built. It is a
// function rather than a method so that a test can call it with the argument list and
// nothing else.
func guard(args []string, lease string) error {
	for _, a := range args {
		switch {
		case a == "--auto" || strings.HasPrefix(a, "--auto="):
			return &GuardError{Arg: a, Why: "gh pr merge --auto does not queue on this host, it merges immediately -- #922 went in red that way (Glenn, 2026-09-11: never --auto)"}
		case a == "--force" || a == "-f" || strings.HasPrefix(a, "--force="):
			return &GuardError{Arg: a, Why: "the lane never force-pushes; history the lane rewrote is history nobody can reproduce (rule 4, 2026-09-11)"}
		case a == "--force-with-lease":
			return &GuardError{Arg: a, Why: "a bare --force-with-lease leases whatever this clone last fetched, which is not a compare-and-swap on the base; the one spelling allowed is --force-with-lease=refs/heads/<base>:<40-char sha> from the publication step (rule 21)"}
		case strings.HasPrefix(a, "--force-with-lease="):
			if a != lease || lease == "" {
				return &GuardError{Arg: a, Why: leaseWhy(a)}
			}
		case a == "--force-if-includes" || strings.HasPrefix(a, "--force-if-includes="):
			return &GuardError{Arg: a, Why: "the lane's one push is a compare-and-swap on the base sha and nothing else (rule 4)"}
		}
	}
	return nil
}

// leaseWhy says which half of the one allowed spelling is missing, so a caller can fix
// what is wrong rather than read the rule again.
func leaseWhy(arg string) string {
	value := strings.TrimPrefix(arg, "--force-with-lease=")
	ref, sha, ok := strings.Cut(value, ":")
	switch {
	case !ok || sha == "":
		return fmt.Sprintf("--force-with-lease=%s names a ref and no sha, so the remote is asked to compare with whatever it last told this clone; the one spelling allowed is --force-with-lease=%s:<40-char sha> (rule 4)", value, ref)
	case !IsSHA(sha):
		return fmt.Sprintf("--force-with-lease wants the full 40-character sha of the base this gate was taken against, got %q; a truncated sha might match the wrong commit (rules 4 and 21)", sha)
	default:
		return "this lease was not built by the publication step of rule 21, which is the one call site allowed to build one"
	}
}

// Publish is rule 21, and it is the ONE call site that builds a lease.
//
// It pushes the gated object itself to the base ref with a compare-and-swap: the remote
// lands it only if the base is exactly expected, and then the base IS the gated commit,
// whose first parent is that sha, so the ref moves forward by one tested commit. Nothing
// is rebuilt or re-merged on the way here: the object pushed is the object the gate
// record names, by sha.
//
// A rejected lease is RacedError -- nothing was published -- and every other refusal is
// the remote's own, which the caller prints as MERGE BLOCKED missing=atomic_publication.
func (g *Git) Publish(remote, baseBranch, expectedBase, mergeSHA string) (string, error) {
	if !IsSHA(expectedBase) || !IsSHA(mergeSHA) {
		return "", fmt.Errorf("publication wants two full shas, got base %q and merge %q", expectedBase, mergeSHA)
	}
	ref := "refs/heads/" + baseBranch
	lease := "--force-with-lease=" + ref + ":" + expectedBase
	g.lease = lease
	defer func() { g.lease = "" }()
	out, err := g.Run("push", remote, mergeSHA+":"+ref, lease)
	if err != nil {
		if isLeaseRejection(out) {
			return out, &RacedError{Expected: expectedBase, Output: oneLineOf(out)}
		}
		return out, &PublishRefusedError{Output: oneLineOf(out)}
	}
	return out, nil
}

// RacedError is a lease the remote rejected: the base moved between the gate and the
// push, nothing was published, and the pass stops.
type RacedError struct {
	Expected string
	Output   string
}

func (e *RacedError) Error() string {
	return fmt.Sprintf("the base moved after the gate; the lease on %s was rejected and nothing was published: %s", Short(e.Expected), e.Output)
}

// PublishRefusedError is a push the remote refused for any reason OTHER than the lease --
// a protected base that admits no direct push, a missing permission, a remote that does
// not report the ref it compared. The tool never falls back to a plain push or to a merge
// with no base precondition, so this is where an unsupported atomic publication stops.
type PublishRefusedError struct{ Output string }

func (e *PublishRefusedError) Error() string { return e.Output }

// isLeaseRejection reads the remote's answer for the one word that means the lease was
// what failed. git spells a rejected lease "stale info"; the ref-status line carries
// "[rejected]" for every refusal, so the lease word is what tells the two apart.
func isLeaseRejection(out string) bool {
	return strings.Contains(out, "stale info") || strings.Contains(out, "fetch first") ||
		strings.Contains(out, "non-fast-forward") || strings.Contains(out, "cannot lock ref")
}

// oneLineOf reduces a subprocess's output to the first line that says something, capped,
// for the tail of an event line. The whole output is the caller's to print raw where a
// person needs to read it; a line of the grammar carries one line of it.
func oneLineOf(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			return s
		}
	}
	return "(no output)"
}

// contextWithTimeout is the one place a deadline is attached, so that every subprocess
// this tool starts is held to the run's --timeout and a hung fetch is a tool that says so
// rather than a tool that has stopped saying anything.
func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
