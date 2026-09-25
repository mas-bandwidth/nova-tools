// Package benchsh is the one way this tree runs a script on a bench (#2932,
// control for #3291): `ssh -o BatchMode=yes -o ConnectTimeout=5 <user@host>
// bash -s -- <shell-quoted args>`, with the script on stdin only and no -n.
//
// The bench's login shell (zsh on the Macs) parses only `bash -s -- '<quoted>'`:
// no `$var`, no `$var:` modifier and no glob ever reaches it, so what zsh did
// to `refs/heads/$x:refs/...` in #3291 cannot happen here. The script itself is
// read by bash from stdin.
//
// A site whose stdin also carries data (a batch, a secret's value, a tar
// stream) passes it as Command's input: stdin is then the one line
// `exec bash -c $'<script>' bash "$@"` followed by the data. bash reads a
// pipe one byte at a time, so it stops at that line's newline and the script
// it execs inherits the rest of stdin untouched (#3350).
//
// There is no other ssh exec site in the tree: TestCIOneBenchRunner fails any
// function outside this package that runs ssh itself (#3350 moved the last
// ones here and removed the allow list).
package benchsh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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
// puts a fake in t.TempDir(). Options are further `-o` values after BatchMode
// and ConnectTimeout (ServerAliveInterval=5, ForwardAgent=no).
type Target struct {
	Host           string
	User           string
	SSH            string
	ConnectTimeout time.Duration
	Options        []string
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
// one remote command word `bash -s -- <quoted args>`. ConnectTimeout is whole
// seconds, rounded up.
func Argv(t Target, args ...string) []string {
	timeout := t.ConnectTimeout
	if timeout <= 0 {
		timeout = DefaultConnectTimeout
	}
	secs := int((timeout + time.Second - 1) / time.Second)
	remote := []string{"bash", "-s", "--"}
	for _, a := range args {
		remote = append(remote, Quote(a))
	}
	argv := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=" + strconv.Itoa(secs)}
	for _, o := range t.Options {
		argv = append(argv, "-o", o)
	}
	return append(argv, t.Dest(), strings.Join(remote, " "))
}

// Line is argv as one command line, its words joined with spaces: exactly the
// line `ssh <host> <argv...>` handed the bench's login shell, now read by bash
// from stdin instead. The words keep their shell meaning (&&, a glob, a
// redirect, a leading ~), so every word must already be one the caller
// checked; Quote a word that has to stay literal.
func Line(argv ...string) string { return strings.Join(argv, " ") }

// InputLine is the stdin line that precedes a site's own data: bash -s reads
// it, then execs bash -c on the script with the args, and that bash inherits
// the rest of stdin. The script is ANSI-C quoted ($'...'), so the line is one
// physical line whatever newlines the script holds.
func InputLine(script string) string {
	return "exec bash -c " + quoteLine(script) + ` bash "$@"` + "\n"
}

// quoteLine is bash's $'...' form of s with every control byte escaped.
func quoteLine(s string) string {
	var b strings.Builder
	b.WriteString("$'")
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\\' || c == '\'':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\t':
			b.WriteString(`\t`)
		case c < 0x20 || c == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('\'')
	return b.String()
}

// Command is the ssh child that runs script on the bench with args as $1..,
// not yet started: the caller sets Stdout, Stderr, Env and any process-group
// handling. With input nil stdin is the script alone; otherwise it is
// InputLine(script) followed by input, so the script reads input as its whole
// stdin. It calls testguard.RefuseHosts before it returns.
func Command(ctx context.Context, t Target, script string, input io.Reader, args ...string) (*exec.Cmd, error) {
	if t.Host == "" {
		return nil, errors.New("benchsh: target has no host")
	}
	prog := Program(t.SSH)
	argv := Argv(t, args...)
	testguard.RefuseHosts(prog, argv...)
	cmd := exec.CommandContext(ctx, prog, argv...)
	if input == nil {
		cmd.Stdin = strings.NewReader(script)
	} else {
		cmd.Stdin = io.MultiReader(strings.NewReader(InputLine(script)), input)
	}
	return cmd, nil
}

// Run runs script on the bench with args as $1.. and returns its output. A
// non-zero exit is an *ExitError carrying the code, so a caller can tell a
// refusal of its own script (a code it chose) from ssh failing (255).
func Run(ctx context.Context, t Target, script string, args ...string) (Result, error) {
	return RunInput(ctx, t, script, nil, args...)
}

// RunInput is Run with input as the script's stdin (see Command).
func RunInput(ctx context.Context, t Target, script string, input io.Reader, args ...string) (Result, error) {
	cmd, err := Command(ctx, t, script, input, args...)
	if err != nil {
		return Result{Exit: -1}, err
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err = cmd.Run()
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

// Code is the remote exit code an error from a Command child or Run carries,
// -1 when ssh never exited with one (not started, killed).
func Code(err error) int {
	var be *ExitError
	if errors.As(err, &be) {
		return be.Code
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

func oneLine(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}
