package friend

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	session := c.Session
	if session == "" {
		var err error
		session, err = NewestCodexSession(c.home(), c.Dir)
		if err != nil {
			return 1, err
		}
	}
	queue := []string{"queue", "--thread", session, "--message", text}
	resume := ResumeArgs(session, text)
	routes := [][]string{resume, queue}
	if c.held()(LockPath(c.home(), session)) {
		routes[0], routes[1] = queue, resume
	}
	var failures []string
	for _, args := range routes {
		out, exit, err := c.Run(ctx, c.Dir, c.program(), args, "")
		if exit != 0 || err != nil {
			failures = append(failures, fmt.Sprintf("%s exit=%d error=%v output=%q", args[0], exit, err, strings.TrimSpace(Head(out, OutputKept))))
			continue
		}
		if c.Out != nil {
			if args[0] == "queue" {
				fmt.Fprintf(c.Out, "queued for open chat: codex queue --thread %s --message <text> (accepted, not answered)\n", session)
			} else {
				fmt.Fprintln(c.Out, ResumeLabel+": codex "+strings.Join(ResumeArgs(session, "<text>"), " "))
			}
			if out != "" {
				fmt.Fprintln(c.Out, strings.TrimRight(Head(out, OutputKept), "\n"))
			}
		}
		return 0, nil
	}
	return 0, Deferred{Reason: fmt.Sprintf("thread %s: neither delivery route accepted the message (%s); keep it pending and retry when Codex is available", session, strings.Join(failures, "; "))}
}
