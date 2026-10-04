package friend

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Codex delivers through `codex exec resume --skip-git-repo-check <thread>
// <text>`, which resumes the saved thread headlessly in a new codex process,
// appends the text as a turn, and blocks until the model has answered. This
// is the thread's model with the friend's whole context, but it is not the
// open chat: the Codex desktop app (ChatGPT.app) runs its own app-server on
// a stdio pair it owns and exposes no socket, and while a thread is open
// there the app holds the thread's writer lock
// (~/.codex/thread-writer-locks/<thread>.lock), so a resume is refused with
// "thread <id> already has an active writer" (measured 2026-10-04, exit 1).
// So a message is answered only while the chat is closed in the app, and
// the record says on every delivery that it was answered by resume, not by
// the open chat. The open chat is reachable only through the app's own
// app-server, which the app connects to the shared local daemon
// (`codex app-server daemon start`, then `codex queue --thread <id>
// --message <text>`) only when launched with
// CODEX_APP_SERVER_USE_LOCAL_DAEMON=1 (docs/SPEC-FRIEND.md, Codex).
//
// Without a thread named, `--last` resumes the newest recorded thread whose
// directory is Dir (codex filters by the working directory), so a friend who
// starts a fresh thread there is still reached.
type Codex struct {
	Dir, Session string
	Run          Exec
	Program      string                 // "codex" when empty
	Home         string                 // CODEX_HOME; $CODEX_HOME or ~/.codex when empty
	Held         func(lock string) bool // whether the thread's writer lock is held; FlockHeld when nil
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
	if h := os.Getenv("CODEX_HOME"); h != "" {
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
	if c.Session != "" && c.held()(LockPath(c.home(), c.Session)) {
		return 1, fmt.Errorf("thread %s is open in the Codex app, which holds its writer lock; a resume cannot reach an open chat, so the message stays pending until the chat is closed", c.Session)
	}
	out, exit, err := c.Run(ctx, c.Dir, c.program(), ResumeArgs(c.Session, text), "")
	if c.Out != nil {
		if exit == 0 && err == nil {
			fmt.Fprintln(c.Out, ResumeLabel+": codex "+strings.Join(ResumeArgs(c.Session, "<text>"), " "))
		} else {
			fmt.Fprintf(c.Out, "not answered: codex exec resume exited %d (a thread open in the Codex app refuses a resume; or no such thread)\n", exit)
		}
		if out != "" {
			fmt.Fprintln(c.Out, strings.TrimRight(Head(out, OutputKept), "\n"))
		}
	}
	return exit, err
}
