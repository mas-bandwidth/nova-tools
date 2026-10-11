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
// queue holds at most one request for a pong of each kind (PongRequest): a session check
// (the presence nonce) and a wake (a wake turn or an idle wake: the coordinator's challenge
// nonce), each its own series, so one kind never withdraws the other. Before such a delivery
// is queued the thread's queue is read: a request of the same kind for the same nonce still
// unread inside CodexCheckRequeue stands for this one, and nothing is queued twice; else the
// new request is queued first, and only once it is in is every request of its kind it
// supersedes withdrawn (an older nonce, or the same nonce queued CodexCheckRequeue ago or
// more), so a queue that fails leaves the old request standing. A queued message that
// carries anything else (a bus message) is never withdrawn. A message is
// known by its text's shape: a person who types the exact shape of a pong request into the
// chat has it treated as one.
type Codex struct {
	Dir, Session string
	// QueueOnly keeps notification delivery in the existing app, never a competing exec resume (SPEC-FRIEND.md, notifications).
	QueueOnly bool
	Run       Exec
	Program   string                 // "codex" when empty
	Home      string                 // CODEX_HOME; $CODEX_HOME or ~/.codex when empty
	Held      func(lock string) bool // whether the thread's writer lock is held; FlockHeld when nil
	Env       func(string) string    // getenv; os.Getenv when nil
	Out       io.Writer              // where the turn's output goes, when set: the daemon's record
	// App connects to the Codex app-server, through which the thread's queue is read and
	// withdrawn from (DialCodexAppServer under the home when nil); Now is the clock a queued
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
// same request is queued again in its place: the session check's re-ask (ReaskAfter) less one
// recheck, so the re-ask at the hour always finds the old one past its age and the session
// check is never held two hours.
const CodexCheckRequeue = ReaskAfter - RecheckEvery

// The kinds of pong request (PongRequest): each its own series of nonces.
const (
	PongCheck = "session check" // the presence's nonce (SessionCheckText)
	PongWake  = "wake"          // the coordinator's challenge nonce (a wake turn, an idle wake, a turn's head)
)

// pongNonce finds the nonce of the pong command a delivery asks the session to run.
var pongNonce = regexp.MustCompile(` pong --as \S+ --nonce (\S+)`)

// PongRequest reads a delivery's text for the pong it asks for: its kind (PongCheck for a
// session check, else PongWake), the nonce of its pong command, and whether it asks for
// nothing else (a session check, a wake turn, an idle wake, which a newer request of its kind
// supersedes). A delivery carrying a bus message is never only a request.
func PongRequest(text string) (kind, nonce string, only bool) {
	m := pongNonce.FindStringSubmatch(text)
	if m == nil {
		return "", "", false
	}
	nonce = m[1]
	switch {
	case strings.HasPrefix(text, SessionCheckPrefix):
		return PongCheck, nonce, !strings.Contains(text, "RECV OK id=")
	case strings.Contains(text, "RECV OK id="):
		return PongWake, nonce, false
	case strings.HasPrefix(text, "Run this now, exactly as written: ") && strings.Count(strings.TrimSpace(text), "\n") == 0:
		return PongWake, nonce, true // a wake turn (WakeTurnText)
	}
	_, rest, ok := strings.Cut(text, "\nThen read on.\n\n")
	return PongWake, nonce, ok && strings.HasPrefix(rest, "nova-friend: you hold ") // an idle wake
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

// app dials the Codex app-server; there is false, and nothing is said, when no socket is there.
func (c *Codex) app(ctx context.Context) (s CodexAppServer, there bool, err error) {
	if c.App != nil {
		s, err = c.App(ctx)
		return s, true, err
	}
	if _, err := os.Stat(CodexControlSocket(c.home())); err != nil {
		return nil, false, nil
	}
	s, err = DialCodexAppServer(ctx, c.home())
	return s, true, err
}

// readQueue is thread's queue; ok is false when it cannot be read (no app-server socket, or
// one that does not answer, said once while it stands), and the queue's length is then not
// known (Queued).
func (c *Codex) readQueue(ctx context.Context, thread string) (items []CodexQueued, ok bool) {
	ctx, cancel := context.WithTimeout(ctx, CodexAppServerBudget)
	defer cancel()
	s, there, err := c.app(ctx)
	if there && err == nil {
		defer s.Close() // ignored: a read connection, nothing pending on it
		items, err = CodexQueue(ctx, s, thread)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case !there:
		c.queueRead = false
		return nil, false
	case err != nil:
		c.queueRead = false
		if c.queueSaid != err.Error() {
			c.queueSaid = err.Error()
			c.say("codex queue of thread %s not read: %s; queued as it comes", thread, oneLine(err.Error(), 300))
		}
		return nil, false
	}
	c.queueSaid = ""
	return items, true
}

// superseded is a queued request a delivery replaces once it is in.
type superseded struct {
	q          CodexQueued
	nonce, why string
}

// withdraw takes each superseded request off thread's queue, one line each, and answers how
// many it took off.
func (c *Codex) withdraw(ctx context.Context, thread string, old []superseded) int {
	if len(old) == 0 {
		return 0
	}
	ctx, cancel := context.WithTimeout(ctx, CodexAppServerBudget)
	defer cancel()
	s, there, err := c.app(ctx)
	if !there && err == nil {
		err = fmt.Errorf("no codex app-server at %s", CodexControlSocket(c.home()))
	}
	if err == nil {
		defer s.Close() // ignored: a withdraw connection, nothing pending on it
	}
	gone := err // the server not reached: every withdrawal is said with it
	took := 0
	for _, o := range old {
		deleted, err := false, gone
		if err == nil {
			deleted, err = CodexDequeue(ctx, s, thread, o.q.ID)
		}
		switch {
		case err != nil:
			c.say("codex: the queued pong request %s (%s) not withdrawn from thread %s: %s; marked superseded", o.nonce, o.q.ID, thread, oneLine(err.Error(), 300))
		case deleted:
			took++
			c.say("codex: withdrew the queued pong request %s (%s) from thread %s: %s", o.nonce, o.q.ID, thread, o.why)
		default:
			c.say("codex: the queued pong request %s (%s) was taken before it could be withdrawn: %s", o.nonce, o.q.ID, o.why)
		}
	}
	return took
}

// queueing delivers text into the open chat (routes, its queue first): its queue read before,
// a request of text's kind that stands for it answered at once, and the requests of its kind
// it supersedes withdrawn only once it is in. The queue's length after is kept (Queued).
func (c *Codex) queueing(ctx context.Context, thread, text string, routes [][]string) (int, error) {
	items, ok := c.readQueue(ctx, thread)
	key := NotificationKey(text)
	if key != "" && !ok {
		return 0, Deferred{Reason: "the Codex queue cannot be read; notification stays pending until its bounded retry"}
	}
	if key != "" {
		for _, q := range items {
			if NotificationKey(q.Text) == key {
				c.mu.Lock()
				c.queued, c.queueRead = len(items), true
				c.mu.Unlock()
				c.say("codex: notification %s already accepted and unread; not processed, not queued twice", key)
				return 0, nil
			}
		}
		for _, q := range items {
			if NotificationCategory(q.Text) == NotificationCategory(text) {
				return 0, Deferred{Reason: "one unread " + NotificationCategory(text) + " notification already stands; full input stays pending until capacity opens"}
			}
		}
	}
	kind, nonce, only := PongRequest(text)
	var old []superseded
	for _, q := range items {
		k, n, pure := PongRequest(q.Text)
		if nonce == "" || !pure || k != kind {
			continue
		}
		at, _ := UUIDv7Time(q.ID)
		if n == nonce && only && c.now().Sub(at) < CodexCheckRequeue {
			c.mu.Lock()
			c.queued, c.queueRead = len(items), true
			c.mu.Unlock()
			c.say("codex: the %s %s is still unread in the queue of thread %s since %s; not queued twice", kind, nonce, thread, at.UTC().Format(time.RFC3339))
			return 0, nil
		}
		why := "superseded by the " + kind + " " + nonce
		if n == nonce {
			why = "superseded by the same " + kind + " queued again"
		}
		old = append(old, superseded{q: q, nonce: n, why: why})
	}
	exit, used, err := c.routes(ctx, thread, routes)
	left := len(items)
	if err == nil && exit == 0 {
		if used == "queue" {
			left++
		}
		left -= c.withdraw(ctx, thread, old) // the new one is in: the old ones go
	}
	if ok {
		c.mu.Lock()
		c.queued, c.queueRead = left, true
		c.mu.Unlock()
	}
	return exit, err
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
	if c.QueueOnly {
		return c.queueing(ctx, session, text, [][]string{queue})
	}
	if c.held()(LockPath(c.home(), session)) {
		return c.queueing(ctx, session, text, [][]string{queue, resume})
	}
	exit, _, err := c.routes(ctx, session, [][]string{resume, queue})
	return exit, err
}

// routes runs each route in order until one takes the text; used is the route that did.
func (c *Codex) routes(ctx context.Context, session string, routes [][]string) (exit int, used string, err error) {
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
		return 0, args[0], nil
	}
	if refusal != nil {
		c.turns.saw(session, 1, refusal)
		return 1, "", refusal
	}
	return 0, "", Deferred{Reason: fmt.Sprintf("thread %s: neither delivery route accepted the message (%s); keep it pending and retry when Codex is available", session, strings.Join(failures, "; "))}
}
