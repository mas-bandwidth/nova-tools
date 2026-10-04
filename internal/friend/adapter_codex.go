package friend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Codex uses the writer lock to choose its first delivery route. An open chat
// receives codex queue; a closed chat uses exec resume. A failed route tries
// the other once, then defers without losing the message (SPEC-FRIEND.md,
// Codex). Queue acceptance is not a completed answer: the app may start the
// queued input only after its current turn ends.
// Without a named session, resolve the newest saved thread for Dir first.
type Codex struct {
	Dir, Session string
	Run          Exec
	Program      string                 // "codex" when empty
	Home         string                 // CODEX_HOME; $CODEX_HOME or ~/.codex when empty
	Held         func(lock string) bool // whether the thread's writer lock is held; FlockHeld when nil
	Env          func(string) string    // getenv; os.Getenv when nil
	Out          io.Writer              // where the turn's output goes, when set: the daemon's record
	Resolve      func(home, dir, session string) (id, rollout string, err error)
	Receipt      func(path, session, text string, from int64) (found bool, next int64, err error)
	Pause        func(context.Context, time.Duration)
}

const CodexReceiptEvery = 100 * time.Millisecond

func (c *Codex) resolve() func(string, string, string) (string, string, error) {
	if c.Resolve != nil {
		return c.Resolve
	}
	return ResolveCodexSession
}

func (c *Codex) receipt() func(string, string, string, int64) (bool, int64, error) {
	if c.Receipt != nil {
		return c.Receipt
	}
	return CodexReceipt
}

func (c *Codex) pause(ctx context.Context, d time.Duration) {
	if c.Pause != nil {
		c.Pause(ctx, d)
		return
	}
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func (c *Codex) program() string {
	if c.Program == "" {
		return "codex"
	}
	return c.Program
}

func (c *Codex) home() string {
	if c.Home != "" {
		return c.Home
	}
	getenv := c.Env
	if getenv == nil {
		getenv = os.Getenv
	}
	if h := getenv("CODEX_HOME"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".codex")
}

func (c *Codex) held() func(string) bool {
	if c.Held == nil {
		return FlockHeld
	}
	return c.Held
}

// ResumeArgs is the codex command line that resumes session (empty: the
// newest thread of the working directory) with text as its next turn.
func ResumeArgs(session, text string) []string {
	if session == "" {
		return []string{"exec", "resume", "--skip-git-repo-check", "--last", text}
	}
	return []string{"exec", "resume", "--skip-git-repo-check", session, text}
}

// LockPath is the writer lock codex holds on thread while a process has it
// open for writing, under home (CODEX_HOME).
func LockPath(home, thread string) string {
	return filepath.Join(home, "thread-writer-locks", thread+".lock")
}

// ResumeLabel is the line the record carries for a turn answered by resume.
const ResumeLabel = "answered by resume, not by the open chat"

func (c *Codex) Deliver(ctx context.Context, text string) (int, error) {
	session, rollout, err := c.resolve()(c.home(), c.Dir, c.Session)
	if err != nil {
		return 1, err
	}
	queue := []string{"queue", "--thread", session, "--message", text}
	resume := ResumeArgs(session, text)
	if !c.held()(LockPath(c.home(), session)) {
		out, exit, runErr := c.Run(ctx, c.Dir, c.program(), resume, "")
		if exit == 0 && runErr == nil {
			c.recordRoute(ResumeLabel+": codex "+strings.Join(ResumeArgs(session, "<text>"), " "), out)
			return 0, nil
		}
		classifiedExit, classifiedErr := refused(session, out, exit, runErr)
		outcome, queueErr := c.queue(ctx, rollout, session, text, queue, []string{routeFailure(resume, out, exit, runErr)})
		if outcome != queueRefused {
			return 0, queueErr
		}
		var provider ProviderRefused
		if errors.As(classifiedErr, &provider) {
			return classifiedExit, classifiedErr
		}
		return 0, Deferred{Reason: queueErr.Error() + "; keep it pending and retry when Codex is available"}
	}
	outcome, queueErr := c.queue(ctx, rollout, session, text, queue, nil)
	if outcome != queueRefused {
		return 0, queueErr
	}
	out, resumeExit, resumeErr := c.Run(ctx, c.Dir, c.program(), resume, "")
	if resumeExit == 0 && resumeErr == nil {
		c.recordRoute(ResumeLabel+": codex "+strings.Join(ResumeArgs(session, "<text>"), " "), out)
		return 0, nil
	}
	classifiedExit, classifiedErr := refused(session, out, resumeExit, resumeErr)
	var provider ProviderRefused
	if errors.As(classifiedErr, &provider) {
		return classifiedExit, classifiedErr
	}
	return 0, Deferred{Reason: queueErr.Error() + "; " + routeFailure(resume, out, resumeExit, resumeErr) + "; keep it pending and retry when Codex is available"}
}

type queueOutcome uint8

const (
	queueConfirmed queueOutcome = iota
	queueRefused
	queueUnknown
)

func (c *Codex) queue(ctx context.Context, rollout, session, text string, args []string, failures []string) (queueOutcome, error) {
	_, boundary, err := c.receipt()(rollout, session, text, 0)
	if err != nil {
		return queueUnknown, Deferred{Reason: fmt.Sprintf("thread %s receipt cannot be read before queue: %v; keep it pending", session, err)}
	}
	out, exit, runErr := c.Run(ctx, c.Dir, c.program(), args, "")
	if runErr != nil {
		return queueUnknown, Deferred{Reason: fmt.Sprintf("thread %s queue may have been accepted but the command result is unknown: %v; no alternate route was tried", session, runErr)}
	}
	if ctx.Err() != nil {
		return queueUnknown, Deferred{Reason: fmt.Sprintf("thread %s queue may have been accepted before cancellation; no alternate route was tried", session)}
	}
	if exit != 0 {
		failures = append(failures, routeFailure(args, out, exit, runErr))
		return queueRefused, Deferred{Reason: fmt.Sprintf("thread %s: queue command refused (%s)", session, strings.Join(failures, "; "))}
	}
	for ctx.Err() == nil {
		found, next, receiptErr := c.receipt()(rollout, session, text, boundary)
		if receiptErr != nil {
			return queueUnknown, Deferred{Reason: fmt.Sprintf("thread %s queue was accepted but its receipt cannot be confirmed: %v; no alternate route was tried", session, receiptErr)}
		}
		boundary = next
		if found {
			c.recordRoute(fmt.Sprintf("received by open chat: codex queue --thread %s --message <text>", session), out)
			return queueConfirmed, nil
		}
		c.pause(ctx, CodexReceiptEvery)
	}
	return queueUnknown, Deferred{Reason: fmt.Sprintf("thread %s queue was accepted but no exact user receipt was confirmed before cancellation; no alternate route was tried", session)}
}

func routeFailure(args []string, out string, exit int, err error) string {
	return fmt.Sprintf("%s exit=%d error=%v output=%q", args[0], exit, err, strings.TrimSpace(Head(out, OutputKept)))
}

func (c *Codex) recordRoute(line, out string) {
	if c.Out == nil {
		return
	}
	fmt.Fprintln(c.Out, line)
	if out != "" {
		fmt.Fprintln(c.Out, strings.TrimRight(Head(out, OutputKept), "\n"))
	}
}
