package deal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// ErrSessionPerCard is the refusal of a launcher that asks for a second ssh
// session to one bench in one pass. The Studio's sshd was wedged by exactly
// that shape (#2743); the pass refuses it before the second child starts.
var ErrSessionPerCard = errors.New("refused: one ssh session per bench per pass; this launcher asked for a second")

// DefaultRemote is the bench-side verb the one session runs (#2756 4.3 `card
// launch --stdin`, #2931): it reads `<S> <label> <attempt> <token>` lines,
// starts each card wrapper detached in its own session and exits, so the
// session never outlives the batch. bash, never the login shell's dialect.
const DefaultRemote = "exec bash -lc 'exec nova-sprint card launch --stdin'"

// DefaultConnectTimeout bounds how long a wedged sshd can hold the pass: the
// row reads refused or timeout inside it, well within the 10 s of control 12.
const DefaultConnectTimeout = 5 * time.Second

// DefaultRunTimeout bounds the whole session. The launch verb returns within
// 5 s (#2931); a session still open after this is an error, not a card.
const DefaultRunTimeout = 30 * time.Second

// Session is one ssh session to a bench carrying one batch on stdin.
type Session interface {
	Run(ctx context.Context, stdin []byte) error
}

// Dialer opens a session to a bench. It does not connect until Run.
type Dialer interface {
	Dial(b Bench) Session
}

// Opener is the pass's single-use handle to a bench: the first call returns
// the bench's one session, any later call in the same pass returns
// ErrSessionPerCard.
type Opener func(ctx context.Context) (Session, error)

// Launcher carries a bench's reservations to the bench through open.
type Launcher interface {
	Launch(ctx context.Context, open Opener, b Bench, res []Reservation) error
}

// BatchLauncher opens the one session and writes every reservation to it.
type BatchLauncher struct{}

// Launch implements Launcher.
func (BatchLauncher) Launch(ctx context.Context, open Opener, _ Bench, res []Reservation) error {
	s, err := open(ctx)
	if err != nil {
		return err
	}
	return s.Run(ctx, Lines(res))
}

// Lines renders reservations as `card launch --stdin` input: one
// `<S> <label> <attempt> <token>` line each (#2756 4.3).
func Lines(res []Reservation) []byte {
	var b bytes.Buffer
	for _, r := range res {
		fmt.Fprintf(&b, "%s %s %d %s\n", r.Card.Sprint, r.Card.Label, r.Attempt, r.Token)
	}
	return b.Bytes()
}

// oneSession hands out the bench's session once per pass.
type oneSession struct {
	dialer Dialer
	bench  Bench
	mu     sync.Mutex
	opened int
}

func (o *oneSession) Open(context.Context) (Session, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.opened > 0 {
		return nil, fmt.Errorf("%w (bench %s)", ErrSessionPerCard, o.bench.Name)
	}
	o.opened++
	return o.dialer.Dial(o.bench), nil
}

func (o *oneSession) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.opened
}

// SessionError is a session that did not complete. State is SSHRefused or
// SSHTimeout only when the failure happened before the remote verb could
// start (connect, banner or key exchange), so the pass may return the batch
// to the pool; any other failure is SSHError and the batch stays dealt for
// the reconciler's start-ack rule (#2756 3.2).
type SessionError struct {
	Bench  string
	State  string
	Exit   int
	Stderr string
}

func (e *SessionError) Error() string {
	return fmt.Sprintf("ssh: %s: bench %s exit %d: %s", e.State, e.Bench, e.Exit, firstLine(e.Stderr))
}

// Remote is the production Dialer: the system ssh (or Program), BatchMode, a
// connect timeout, one remote command.
type Remote struct {
	Program        string // default "ssh"; a test's fake lives in t.TempDir()
	Command        string // default DefaultRemote
	ConnectTimeout time.Duration
	RunTimeout     time.Duration
}

// Dial implements Dialer.
func (r Remote) Dial(b Bench) Session { return &remoteSession{r: r, bench: b} }

type remoteSession struct {
	r     Remote
	bench Bench
}

// Run starts the one ssh child for the batch. It is the package's only host
// seam and calls testguard.RefuseHosts before the child starts.
func (s *remoteSession) Run(ctx context.Context, stdin []byte) error {
	program := s.r.Program
	if program == "" {
		program = "ssh"
	}
	command := s.r.Command
	if command == "" {
		command = DefaultRemote
	}
	connect := s.r.ConnectTimeout
	if connect <= 0 {
		connect = DefaultConnectTimeout
	}
	run := s.r.RunTimeout
	if run <= 0 {
		run = DefaultRunTimeout
	}
	secs := int((connect + time.Second - 1) / time.Second)
	args := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=" + strconv.Itoa(secs),
		"-o", "ServerAliveInterval=5",
		"-o", "ServerAliveCountMax=2",
		s.bench.Target(), command,
	}
	testguard.RefuseHosts(program, args...)
	ctx, cancel := context.WithTimeout(ctx, run)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return nil
	}
	exit := -1
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		exit = ee.ExitCode()
	}
	if ctx.Err() != nil {
		return &SessionError{Bench: s.bench.Name, State: SSHError, Exit: exit, Stderr: "session exceeded " + run.String() + "; " + stderr.String()}
	}
	return &SessionError{Bench: s.bench.Name, State: Classify(exit, stderr.String()), Exit: exit, Stderr: stderr.String()}
}

// preExec are OpenSSH client messages that mean the session never reached the
// remote command: the TCP connect, the banner or the key exchange failed.
var preExecRefused = []string{
	"connection refused",
	"kex_exchange_identification",
	"no route to host",
	"host is down",
	"network is unreachable",
}

var preExecTimeout = []string{
	"connection timed out",
	"timed out during banner exchange",
}

// connectPhaseOnly are OpenSSH diagnostics that identify the connect phase
// (and so mean the remote command never started) only when they carry the
// client's own "ssh: connect to host ..." prefix. Bare, any of these messages
// can also be printed once the remote command is already running: a
// mid-command network drop prints "Connection closed by <host> port 22" or
// "Connection reset by <host> port 22" with no kex_exchange_identification
// prefix, and some platforms print "Operation timed out" for a lost
// keepalive as well as for a failed connect. Treating those bare messages as
// pre-exec would return the reservations to the pool while the launch may
// already be under way, dealing the same cards twice (#3061 hold 7); they
// classify as SSHError instead, so the reconciler's start-ack rule (3.2)
// keeps the batch dealt.
var connectPhaseOnly = map[string]string{
	"connection closed by": SSHRefused,
	"connection reset by":  SSHRefused,
	"operation timed out":  SSHTimeout,
}

// Classify reads an ssh exit and its stderr. Exit 255 with a pre-exec message
// is refused or timeout (nothing ran on the bench); every other failure is
// error.
func Classify(exit int, stderr string) string {
	if exit == 0 {
		return SSHOK
	}
	if exit != 255 {
		return SSHError
	}
	low := strings.ToLower(stderr)
	for _, m := range preExecTimeout {
		if strings.Contains(low, m) {
			return SSHTimeout
		}
	}
	for _, m := range preExecRefused {
		if strings.Contains(low, m) {
			return SSHRefused
		}
	}
	if strings.Contains(low, "connect to host") {
		for m, state := range connectPhaseOnly {
			if strings.Contains(low, m) {
				return state
			}
		}
	}
	return SSHError
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
