// Package benchsh is the one way this tree runs a script on a bench (#2932,
// control for #3291): `ssh -o BatchMode=yes -o ConnectTimeout=5 <user@host>
// bash -s -- <shell-quoted args>`, with the script on stdin only and no -n.
//
// The bench's login shell (zsh on the Macs) parses only `bash -s -- '<quoted>'`:
// no `$var`, no `$var:` modifier and no glob ever reaches it, so what zsh did
// to `refs/heads/$x:refs/...` in #3291 cannot happen here. The script itself is
// read by bash from stdin.
//
// Every other ssh exec site in the tree is listed in
// internal/ci/testdata/bench-runners.allow with its retiring issue;
// TestCIOneBenchRunner refuses a new one.
package benchsh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// DefaultConnectTimeout is ssh's ConnectTimeout when the target names none.
const DefaultConnectTimeout = 5 * time.Second

// Target is the bench: host and user come from the bench's own beat
// (bench:<b>:beat), never assumed. SSH is the program (default "ssh"); a test
// puts a fake in t.TempDir().
type Target struct {
	Host           string
	User           string
	SSH            string
	ConnectTimeout time.Duration
}

// Result is one run: the combined output and the remote exit code (-1 when
// ssh never ran the script).
type Result struct {
	Output string
	Exit   int
}

// ExitError is a script (or ssh) that exited non-zero.
type ExitError struct {
	Target string
	Code   int
	Output string
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("ssh %s: exit %d: %s", e.Target, e.Code, oneLine(e.Output))
}

// Program is the ssh program a target runs: its SSH, or "ssh".
func Program(ssh string) string {
	if ssh == "" {
		return "ssh"
	}
	return ssh
}

// Quote is POSIX single quoting: the one form both bash and zsh read as a
// single literal word.
func Quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Dest is user@host, or host alone.
func (t Target) Dest() string {
	if t.User != "" {
		return t.User + "@" + t.Host
	}
	return t.Host
}

// Argv is the ssh argv after the program: the options, the target, and the
// one remote command word `bash -s -- <quoted args>`.
func Argv(t Target, args ...string) []string {
	timeout := t.ConnectTimeout
	if timeout <= 0 {
		timeout = DefaultConnectTimeout
	}
	remote := []string{"bash", "-s", "--"}
	for _, a := range args {
		remote = append(remote, Quote(a))
	}
	return []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=" + strconv.Itoa(int(timeout/time.Second)),
		t.Dest(), strings.Join(remote, " ")}
}

// Run runs script on the bench with args as $1.. and returns its output. A
// non-zero exit is an *ExitError carrying the code, so a caller can tell a
// refusal of its own script (a code it chose) from ssh failing (255).
func Run(ctx context.Context, t Target, script string, args ...string) (Result, error) {
	if t.Host == "" {
		return Result{Exit: -1}, errors.New("benchsh: target has no host")
	}
	prog := Program(t.SSH)
	argv := Argv(t, args...)
	testguard.RefuseHosts(prog, argv...)
	cmd := exec.CommandContext(ctx, prog, argv...)
	cmd.Stdin = strings.NewReader(script)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	res := Result{Output: out.String(), Exit: 0}
	if err == nil {
		return res, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ctx.Err() == nil {
		res.Exit = ee.ExitCode()
		return res, &ExitError{Target: t.Dest(), Code: res.Exit, Output: res.Output}
	}
	res.Exit = -1
	return res, fmt.Errorf("ssh %s: %w: %s", t.Dest(), err, oneLine(res.Output))
}

func oneLine(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}
