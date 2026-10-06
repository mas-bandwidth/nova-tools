package friend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Antigravity delivers through the harness's own agent-message channel:
// `agentapi send-message --title=nova-friend <conversation> <text>`, a client
// of the running language server (the wrapper under
// ~/.gemini/antigravity/bin, which execs the app's language_server). The
// server writes the text into the conversation's mailbox
// (~/.gemini/antigravity/brain/<conversation>/.system_generated/messages/) as
// a high-priority message and its watcher starts a turn on it; the session
// marks the message read in read.json there as it takes it. Measured
// 2026-10-04 08:52 ET: sent at :28, the turn's first step at :32, the
// friend's "got it" on nova-bus2 at :35.
//
// The server's address and CSRF token are read off the language_server
// process each delivery (ps, then lsof for its listening ports; the one that
// answers get-conversation-metadata is the plaintext one). The conversation
// is the live one (Follow), else the one named by Session, else the newest
// root conversation whose workspace is Dir, from the harness's
// conversation_summaries.db (read immutable, through sqlite3).
//
// The mailbox queues: a turn the message starts runs on, and a second
// message waits in the mailbox for it, the harness's own order for its
// agents. So Deliver answers 0 once agentapi has taken the message into the
// mailbox, and never waits for the session to read it (the finding of
// 2026-10-05 and 06: a delivery that waited two minutes for the read held
// every other message and the session check behind it while the friend worked
// through a long turn, and three such waits gave a message up that was
// already in her mailbox). Every delivery is kept in the ledger
// (antigravity_ledger.go): who read it, and what to send again if its
// conversation stops reading. agentapi exits 0 on an error too (a wrong
// conversation, a missing token): the JSON it prints is the truth, never its
// exit code. What the harness refuses (no language server, no token, no
// conversation, no port that answers for it, no mailbox), and a session that
// has read nothing delivered for the check period, is a SessionRefused naming
// why, never a Deferred: the daemon marks the session broken with the reason,
// said once, and keeps every message pending on the bus.
type Antigravity struct {
	Dir, Session string
	Run          Exec
	Out          io.Writer                  // the daemon's record, when set
	Home         string                     // the user's home ($HOME when empty): app data under Home/.gemini/antigravity
	User         string                     // daemon's username (current process user when empty)
	FS           fs.FS                      // rooted at Home (os.DirFS(Home) when nil): the mailbox is read through it
	Wait         func(context.Context) bool // one poll interval; false once ctx has ended (real time when nil)
	Now          func() time.Time           // the daemon's clock, every time in the ledger (time.Now when nil)
	State        string                     // the daemon's state directory: the ledger's file (AntigravityLedgerFile); "" keeps it in memory

	sendMu sync.Mutex // one send at a time: each knows its own message by what is new in the mailbox
	mu     sync.Mutex // the ledger
	ledger *AntigravityLedger
	live   string    // the conversation the last delivery went to
	looked time.Time // when Follow last looked
}

// AntigravityData is the harness's app data directory, under the home.
const AntigravityData = ".gemini/antigravity"

// AntigravityTitle heads every message nova-friend sends.
const AntigravityTitle = "nova-friend"

// AntigravityPoll is how often the mailbox is read while waiting for the sent
// message to appear in it, and AntigravityLandBudget how long: past it the
// message agentapi took is in the ledger as delivered, its id read off the
// mailbox when it lands (Follow).
const (
	AntigravityPoll       = 500 * time.Millisecond
	AntigravityLandBudget = 30 * time.Second
)

// antigravitySummaries is the query for root conversations, newest first;
// the directory is matched in Go, so nothing is ever spliced into SQL.
const antigravitySummaries = "SELECT conversation_id, workspace_uris FROM conversation_summaries WHERE nesting_depth = 0 AND killed = 0 ORDER BY last_modified_time DESC"

func (a *Antigravity) home() string {
	if a.Home != "" {
		return a.Home
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
}

func (a *Antigravity) fsys() fs.FS {
	if a.FS != nil {
		return a.FS
	}
	return os.DirFS(a.home())
}

func (a *Antigravity) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *Antigravity) wait(ctx context.Context) bool {
	if a.Wait != nil {
		return a.Wait(ctx)
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(AntigravityPoll):
		return true
	}
}

func (a *Antigravity) say(format string, args ...any) {
	if a.Out != nil {
		fmt.Fprintf(a.Out, format+"\n", args...)
	}
}

// AgentAPIRefusal is the error agentapi printed in its JSON, at exit 0: the
// harness said no (a wrong conversation, a missing token).
type AgentAPIRefusal string

func (r AgentAPIRefusal) Error() string { return "agentapi: " + string(r) }

// agentapi runs one agentapi command against the server at port, with the
// token, answering the JSON's response (or its error).
func (a *Antigravity) agentapi(ctx context.Context, port, token string, args ...string) (json.RawMessage, error) {
	program := filepath.Join(a.home(), AntigravityData, "bin", "agentapi")
	argv := append([]string{"ANTIGRAVITY_LS_ADDRESS=localhost:" + port, "ANTIGRAVITY_CSRF_TOKEN=" + token, program}, args...)
	out, _, err := a.Run(ctx, a.Dir, "/usr/bin/env", argv, "")
	if err != nil {
		return nil, fmt.Errorf("agentapi %s: %w", args[0], err)
	}
	return AgentAPI(out)
}

// antigravityRefused is the harness's refusal: a SessionRefused to the daemon
// (the session broken with the reason, every message pending), and never a
// Deferred, so no deferral is counted for it anywhere (a reader of Deferred,
// the session check's record, sees a failed delivery with its reason).
type antigravityRefused struct{ r SessionRefused }

func (e antigravityRefused) Error() string { return e.r.Error() }

func (e antigravityRefused) As(target any) bool {
	p, ok := target.(*SessionRefused)
	if ok {
		*p = e.r
	}
	return ok
}

// AntigravityOpen is what a friend whose Antigravity refuses does.
const AntigravityOpen = "open Antigravity with a conversation in the friend's directory; every message stays pending and goes into the mailbox once the harness takes it"

func (a *Antigravity) refuse(session, reason string) (int, error) {
	return 1, antigravityRefused{SessionRefused{Session: session, Reason: oneLine(reason, 300), Detail: AntigravityOpen}}
}

// conversation is where the next delivery goes: the conversation Follow moved
// delivery to, else the one named by Session, else the newest root
// conversation of the workspace. refused is set when the harness names none.
func (a *Antigravity) conversation(ctx context.Context) (session string, refused bool, err error) {
	session = a.following()
	if session == "" {
		session = a.Session
	}
	if session != "" {
		return session, false, nil
	}
	rows, err := a.summaries(ctx)
	if err != nil {
		return "", false, err
	}
	if session, err = NewestConversation(rows, a.dirs()...); err != nil {
		return "", true, err
	}
	return session, false, nil
}

// dirs is the friend's directory and its real path.
func (a *Antigravity) dirs() []string {
	dirs := []string{a.Dir}
	if real, err := filepath.EvalSymlinks(a.Dir); err == nil && real != a.Dir {
		dirs = append(dirs, real)
	}
	return dirs
}

// summaries is the root conversations, newest first, as sqlite3 -json prints them.
func (a *Antigravity) summaries(ctx context.Context) (string, error) {
	db := "file:" + filepath.Join(a.home(), AntigravityData, "conversation_summaries.db") + "?mode=ro&immutable=1"
	rows, _, err := a.Run(ctx, a.Dir, "sqlite3", []string{"-json", db, antigravitySummaries}, "")
	if err != nil {
		return "", fmt.Errorf("sqlite3: %w", err)
	}
	return rows, nil
}

// Deliver: find the server and the conversation; refuse while the
// conversation has read nothing delivered for the check period (the session
// is down: the message stays pending on the bus); send; and answer 0 once
// agentapi has taken the message into the mailbox, kept in the ledger.
func (a *Antigravity) Deliver(ctx context.Context, text string) (int, error) {
	srv, exit, err := a.server(ctx)
	if err != nil {
		return exit, err
	}
	session, refused, err := a.conversation(ctx)
	switch {
	case refused:
		return a.refuse("", err.Error())
	case err != nil:
		return 1, err
	}
	now := a.now()
	a.observe(now)
	if why := a.down(session, now); why != "" {
		return a.refuse(session, why)
	}
	return a.send(ctx, srv, session, text)
}

// antigravityServer is the language server a send goes to: its token and ports.
type antigravityServer struct {
	pid, token string
	ports      []string
}

// server finds the running language server of the daemon's user: refused when there is
// none, it has no token or listens on no port.
func (a *Antigravity) server(ctx context.Context) (antigravityServer, int, error) {
	username := a.User
	if username == "" {
		current, err := user.Current()
		if err != nil {
			return antigravityServer{}, 1, fmt.Errorf("current user: %w", err)
		}
		username = current.Username
	}
	ps, _, err := a.Run(ctx, a.Dir, "ps", []string{"-axo", "user=,pid=,args="}, "")
	if err != nil {
		return antigravityServer{}, 1, fmt.Errorf("ps: %w", err)
	}
	pid, token, err := LanguageServer(ps, username)
	if err != nil {
		exit, err := a.refuse(a.Live(), err.Error())
		return antigravityServer{}, exit, err
	}
	listing, _, err := a.Run(ctx, a.Dir, "lsof", []string{"-nP", "-a", "-p", pid, "-iTCP", "-sTCP:LISTEN", "-Fn"}, "")
	if err != nil {
		return antigravityServer{}, 1, fmt.Errorf("lsof: %w", err)
	}
	ports := ListenPorts(listing)
	if len(ports) == 0 {
		exit, err := a.refuse(a.Live(), fmt.Sprintf("the antigravity language server (pid %s) listens on no TCP port", pid))
		return antigravityServer{}, exit, err
	}
	return antigravityServer{pid: pid, token: token, ports: ports}, 0, nil
}

// send puts text into conversation session's mailbox through srv and keeps it in the
// ledger: 0 once agentapi took it, its id once it is there (within AntigravityLandBudget,
// else read off the mailbox later by Follow). One send at a time.
func (a *Antigravity) send(ctx context.Context, srv antigravityServer, session, text string) (int, error) {
	a.sendMu.Lock()
	defer a.sendMu.Unlock()
	port := ""
	var err error
	for _, p := range srv.ports {
		if _, err = a.agentapi(ctx, p, srv.token, "get-conversation-metadata", session); err == nil {
			port = p
			break
		}
	}
	if port == "" {
		return a.refuse(session, fmt.Sprintf("no port of the antigravity language server (%s) answers for conversation %s: %v", strings.Join(srv.ports, ", "), session, err))
	}
	mailbox := antigravityMailbox(session)
	before, err := a.mailbox(mailbox)
	if err != nil {
		return a.refuse(session, fmt.Sprintf("conversation %s has no mailbox: %v", session, err))
	}
	if _, err = a.agentapi(ctx, port, srv.token, "send-message", "--title="+AntigravityTitle, session, text); err != nil {
		var no AgentAPIRefusal
		if errors.As(err, &no) {
			return a.refuse(session, fmt.Sprintf("conversation %s: %s", session, err))
		}
		return 1, err
	}
	// agentapi took it: it is delivered to session from here on, whenever its file lands
	at := a.now()
	lctx, cancel := context.WithTimeout(ctx, AntigravityLandBudget)
	defer cancel()
	id := ""
	for id == "" {
		after, err := a.mailbox(mailbox)
		if err == nil {
			id, err = a.newMessage(mailbox, before, after)
		}
		if err != nil || (id == "" && !a.wait(lctx)) {
			break
		}
	}
	a.keep(AntigravityDelivery{ID: id, Conversation: session, DeliveredAt: at, Text: text})
	if id == "" {
		a.say("antigravity: agentapi took a message for conversation %s and it is not in the mailbox after %s; kept as delivered, its id read when it lands", session, AntigravityLandBudget)
		return 0, nil
	}
	a.say("antigravity: message %s in the mailbox of conversation %s", id, session)
	return 0, nil
}

// mailbox lists the message ids in dir: the .json files but read.json.
func (a *Antigravity) mailbox(dir string) ([]string, error) {
	entries, err := fs.ReadDir(a.fsys(), dir)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if name := e.Name(); !e.IsDir() && name != "read.json" && strings.HasSuffix(name, ".json") {
			ids = append(ids, strings.TrimSuffix(name, ".json"))
		}
	}
	return ids, nil
}

// ErrLanguageServerNotRunning is returned when no Antigravity language server is running.
var ErrLanguageServerNotRunning = errors.New("no antigravity language server is running: is Antigravity open?")

// LanguageServer finds the antigravity language server in `ps -axo
// user=,pid=,args=` belonging to username: its pid and CSRF token.
func LanguageServer(ps, username string) (pid, token string, err error) {
	for line := range strings.SplitSeq(ps, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] != username || !strings.HasSuffix(fields[2], "/language_server") || !slices.Contains(fields, "antigravity") {
			continue
		}
		if i := slices.Index(fields, "--csrf_token"); i > 0 && i+1 < len(fields) {
			return fields[1], fields[i+1], nil
		}
		return "", "", fmt.Errorf("the antigravity language server (pid %s) runs without a --csrf_token", fields[1])
	}
	return "", "", ErrLanguageServerNotRunning
}

// ListenPorts reads the ports out of `lsof -Fn` (one n<host>:<port> line per
// socket), in the listing's order.
func ListenPorts(lsof string) []string {
	var ports []string
	for line := range strings.SplitSeq(lsof, "\n") {
		if name, ok := strings.CutPrefix(line, "n"); ok {
			if i := strings.LastIndex(name, ":"); i >= 0 {
				ports = append(ports, name[i+1:])
			}
		}
	}
	return ports
}

// AgentAPI reads what agentapi printed: its response, or its error (printed
// at exit 0 like a response).
func AgentAPI(out string) (json.RawMessage, error) {
	var reply struct {
		Response json.RawMessage `json:"response"`
		Error    string          `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &reply); err != nil {
		return nil, fmt.Errorf("agentapi printed no JSON: %s", strings.TrimSpace(Head(out, 200)))
	}
	if reply.Error != "" {
		return nil, AgentAPIRefusal(reply.Error)
	}
	return reply.Response, nil
}

// NewestConversation picks the first conversation of the summaries query
// (newest first, as `sqlite3 -json` printed it) whose workspaces hold one of
// dirs.
func NewestConversation(rows string, dirs ...string) (string, error) {
	ids, err := WorkspaceConversations(rows, dirs...)
	if err != nil {
		return "", err
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("no antigravity conversation has %s open; open one there, or name one with --session", dirs[0])
	}
	return ids[0], nil
}

// WorkspaceConversations is every conversation of the summaries query, in its
// order (newest first), whose workspaces hold one of dirs.
func WorkspaceConversations(rows string, dirs ...string) ([]string, error) {
	var summaries []struct {
		ID         string `json:"conversation_id"`
		Workspaces string `json:"workspace_uris"` // a JSON list of file:// URIs, as a string
	}
	if strings.TrimSpace(rows) != "" {
		if err := json.Unmarshal([]byte(rows), &summaries); err != nil {
			return nil, fmt.Errorf("conversation_summaries.db: not a JSON list: %v", err)
		}
	}
	var ids []string
	for _, s := range summaries {
		var workspaces []string
		if err := json.Unmarshal([]byte(s.Workspaces), &workspaces); err != nil {
			return nil, fmt.Errorf("conversation_summaries.db: workspace_uris is not a JSON list: %w", err)
		}
		if slices.ContainsFunc(workspaces, func(w string) bool { return workspaceIs(w, dirs) }) {
			ids = append(ids, s.ID)
		}
	}
	return ids, nil
}

// workspaceIs says whether the workspace URI is a local file:// URI of one of dirs.
func workspaceIs(workspace string, dirs []string) bool {
	u, err := url.Parse(workspace)
	if err != nil || u.Scheme != "file" || (u.Host != "" && u.Host != "localhost") || u.RawQuery != "" || u.Fragment != "" || !filepath.IsAbs(u.Path) {
		return false
	}
	for _, dir := range dirs {
		if filepath.Clean(u.Path) == filepath.Clean(dir) {
			return true
		}
	}
	return false
}

// newMessage selects a new mailbox entry carrying our exact title, never one the ledger
// already holds (a message that landed late). An unrelated message appearing during send
// must not stand for ours.
func (a *Antigravity) newMessage(mailbox string, before, after []string) (string, error) {
	for _, id := range after {
		if slices.Contains(before, id) || a.known(id) {
			continue
		}
		mine, err := a.titled(mailbox, id)
		if err != nil {
			return "", err
		}
		if mine {
			return id, nil
		}
	}
	return "", nil
}

// titled says whether message id in mailbox carries our exact title.
func (a *Antigravity) titled(mailbox, id string) (bool, error) {
	raw, err := fs.ReadFile(a.fsys(), path.Join(mailbox, id+".json"))
	if err != nil {
		return false, err
	}
	var message struct {
		RenderDetails struct {
			MessageTitle string `json:"messageTitle"`
		} `json:"renderDetails"`
	}
	return json.Unmarshal(raw, &message) == nil && message.RenderDetails.MessageTitle == AntigravityTitle, nil
}

// Read says whether read.json (a map of message id to true) marks id read.
func Read(readJSON []byte, id string) bool {
	var read map[string]bool
	return json.Unmarshal(readJSON, &read) == nil && read[id]
}
