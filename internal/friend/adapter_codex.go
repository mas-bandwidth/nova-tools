package friend

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Codex uses the writer lock to choose its first delivery route. An open chat
// receives codex queue; a closed chat uses exec resume. A failed route tries
// the other once, then defers without losing the message (SPEC-FRIEND.md,
// Codex). Queue acceptance is not a completed answer: the app may start the
// queued input only after its current turn ends.
// Without a named session, resolve the newest saved thread for Dir first.
//
// A delivery during a turn is queued, never steered: the open chat's turn runs in the Codex
// app's own server, which takes no request from outside (CodexAppServer). So the open chat's
// queue holds at most one request for a pong (a session check, a wake turn, an idle wake;
// PongRequest): before such a delivery is queued, the thread's queue is read, a request the
// new one supersedes (an older nonce, or the same nonce queued an hour or more ago,
// CodexCheckRequeue) is withdrawn, and a request for the same nonce still unread inside the
// hour stands for this one, so nothing is queued twice. A queued message that carries
// anything else (a bus message, a card dealt) is never withdrawn.
type Codex struct {
	Dir, Session string
	Run          Exec
	Program      string                 // "codex" when empty
	Home         string                 // CODEX_HOME; $CODEX_HOME or ~/.codex when empty
	Held         func(lock string) bool // whether the thread's writer lock is held; FlockHeld when nil
	Env          func(string) string    // getenv; os.Getenv when nil
	Out          io.Writer              // where the turn's output goes, when set: the daemon's record
	// App connects to the Codex app-server, through which the thread's queue is read and
	// withdrawn from (dialCodexAppServer under the home when nil); Now is the clock a queued
	// request's age is read by (time.Now when nil).
	App func(ctx context.Context) (CodexAppServer, error)
	Now func() time.Time

	turns SessionTurns // the session's last turns, its liveness (alive.go)

	mu        sync.Mutex
	queued    int  // the thread's queue as the last delivery read it
	queueRead bool // whether one has
	queueSaid string
}

// CodexCheckRequeue is how long a request for a pong stands unread in the queue before the
// same request is queued again in its place.
const CodexCheckRequeue = 60 * time.Minute

// pongNonce finds the nonce of the pong command a delivery asks the session to run.
var pongNonce = regexp.MustCompile(` pong --as \S+ --nonce (\S+)`)

// PongRequest reads a delivery's text for the pong it asks for: the nonce of its pong
// command, and whether it asks for nothing else (a session check, a wake turn, an idle wake,
// which a newer request supersedes). A delivery carrying a bus message or a card dealt is
// never only a request.
func PongRequest(text string) (nonce string, only bool) {
	m := pongNonce.FindStringSubmatch(text)
	if m == nil {
		return "", false
	}
	nonce = m[1]
	switch {
	case strings.Contains(text, "RECV OK id="):
		return nonce, false
	case strings.HasPrefix(text, SessionCheckPrefix):
		return nonce, true
	case strings.HasPrefix(text, "Run this now, exactly as written: ") && strings.Count(strings.TrimSpace(text), "\n") == 0:
		return nonce, true // a wake turn (WakeTurnText)
	}
	_, rest, ok := strings.Cut(text, "\nThen read on.\n\n")
	return nonce, ok && strings.HasPrefix(rest, "nova-friend: you hold ") // an idle wake
}

// Queued is the thread's queue as the last delivery read it; ok is false before one has.
func (c *Codex) Queued() (n int, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.queued, c.queueRead
}

func (c *Codex) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Codex) say(format string, args ...any) {
	if c.Out != nil {
		fmt.Fprintf(c.Out, format+"\n", args...)
	}
}

// tidy reads thread's queue before text is queued into it: it withdraws every pure pong
// request text supersedes, and answers whether one still stands for text (the same request,
// unread, queued inside CodexCheckRequeue), so text is not queued twice. The queue's length
// after it is kept (Queued). A queue that cannot be read is said once while it stands, and
// text is queued as it would be.
func (c *Codex) tidy(ctx context.Context, thread, text string) (stands bool) {
	ctx, cancel := context.WithTimeout(ctx, CodexAppServerBudget)
	defer cancel()
	dial := c.App
	if dial == nil {
		if _, err := os.Stat(CodexControlSocket(c.home())); err != nil {
			return false // no app-server here: nothing to read, the queue's length unknown
		}
		dial = func(ctx context.Context) (CodexAppServer, error) { return dialCodexAppServer(ctx, c.home()) }
	}
	s, err := dial(ctx)
	var items []CodexQueued
	if err == nil {
		defer s.Close() // ignored: a read-and-withdraw connection, nothing pending on it
		items, err = CodexQueue(ctx, s, thread)
	}
	c.mu.Lock()
	if err != nil {
		c.queueRead = false
		said := c.queueSaid == err.Error()
		c.queueSaid = err.Error()
		c.mu.Unlock()
		if !said {
			c.say("codex queue of thread %s not read: %s; queued as it comes", thread, oneLine(err.Error(), 300))
		}
		return false
	}
	c.queueSaid = ""
	c.mu.Unlock()
	nonce, only := PongRequest(text)
	left := len(items)
	for _, q := range items {
		n, pure := PongRequest(q.Text)
		if nonce == "" || n == "" || !pure {
			continue
		}
		at, _ := UUIDv7Time(q.ID)
		young := c.now().Sub(at) < CodexCheckRequeue
		if n == nonce && only && young && !stands {
			stands = true
			c.say("codex: the pong request %s is still unread in the queue of thread %s since %s; not queued twice", nonce, thread, at.UTC().Format(time.RFC3339))
			continue
		}
		why := "superseded by nonce " + nonce
		if n == nonce {
			why = "superseded by the same request queued again"
		}
		deleted, err := CodexDequeue(ctx, s, thread, q.ID)
		switch {
		case err != nil:
			c.say("codex: the queued pong request %s (%s) not withdrawn from thread %s: %s; marked superseded", n, q.ID, thread, oneLine(err.Error(), 300))
		case deleted:
			left--
			c.say("codex: withdrew the queued pong request %s (%s) from thread %s: %s", n, q.ID, thread, why)
		default:
			c.say("codex: the queued pong request %s (%s) was taken before it could be withdrawn: %s", n, q.ID, why)
		}
	}
	if !stands {
		left++
	}
	c.mu.Lock()
	c.queued, c.queueRead = left, true
	c.mu.Unlock()
	return stands
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
		if c.tidy(ctx, session, text) {
			return 0, nil // the same request stands unread in her queue
		}
	}
	var failures []string
	var refusal error // a provider's refusal on either route: the session, not the moment, is at fault
	for _, args := range routes {
		out, exit, err := c.Run(ctx, c.Dir, c.program(), args, "")
		if exit != 0 || err != nil {
			if _, r := refused(session, out, exit, err); refusal == nil {
				if _, ok := r.(ProviderRefused); ok {
					refusal = r
				}
			}
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
		c.turns.saw(session, 0, nil)
		return 0, nil
	}
	if refusal != nil {
		c.turns.saw(session, 1, refusal)
		return 1, refusal
	}
	return 0, Deferred{Reason: fmt.Sprintf("thread %s: neither delivery route accepted the message (%s); keep it pending and retry when Codex is available", session, strings.Join(failures, "; "))}
}
