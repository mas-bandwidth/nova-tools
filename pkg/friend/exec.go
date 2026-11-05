package friend

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// OutputKept bounds how much of a command's stderr RealExec keeps after its
// stdout on a nonzero exit.
const OutputKept = 2048

// KillDelay is how long a signalled group gets to end before SIGKILL.
const KillDelay = 5 * time.Second

// RealExec runs the command through os/exec: the program directly, never a
// shell, so a text is never interpolated. No clock bounds it; a cancel of ctx
// stops it. The command is its own session leader (Setsid), and on a cancel
// the whole group is signalled, SIGTERM then SIGKILL after KillDelay: a
// command that forks leaves no orphan behind a stop. On a nonzero exit the
// output carries the head of stderr after stdout: a command says why it
// refused there.
func RealExec(ctx context.Context, dir, name string, args []string, stdin string) (string, int, error) {
	return realExec(ctx, KillDelay, dir, name, args, stdin)
}

func realExec(ctx context.Context, killDelay time.Duration, dir, name string, args []string, stdin string) (string, int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	} // else /dev/null: a headless opencode run with stdin left open hangs at init (measured 2026-10-04)
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
			return out.String(), exitErr.ExitCode(), errors.New("the command was stopped with its process group")
		}
		return out.String() + Head(stderr.String(), OutputKept), exitErr.ExitCode(), nil
	}
	return out.String(), 0, err
}

// Head is the first n bytes of s, with a note when it was cut.
func Head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("\n[... %d more bytes]", len(s)-n)
}
