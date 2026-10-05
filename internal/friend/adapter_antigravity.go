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
// answers get-conversation-metadata is the plaintext one). Without a session
// named, the newest root conversation whose workspace is Dir, from the
// harness's conversation_summaries.db (read immutable, through sqlite3).
//
// agentapi exits 0 on an error too (a wrong conversation, a missing token):
// the JSON it prints is the truth, never its exit code. Deliver answers 0
// once the session has read the message; a turn it starts runs on, so a
// second message queues in the mailbox rather than waiting for the turn.
type Antigravity struct {
	Dir, Session string
	Run          Exec
	Out          io.Writer                  // the daemon's record, when set
	Home         string                     // the user's home ($HOME when empty): app data under Home/.gemini/antigravity
	User         string                     // daemon's username (current process user when empty)
	FS           fs.FS                      // rooted at Home (os.DirFS(Home) when nil): the mailbox is read through it
	Wait         func(context.Context) bool // one poll interval; false once ctx has ended (real time when nil)
}

// AntigravityData is the harness's app data directory, under the home.
const AntigravityData = ".gemini/antigravity"

// AntigravityTitle heads every message nova-friend sends.
const AntigravityTitle = "nova-friend"

// AntigravityPoll is how often the mailbox is read while waiting for the
// session to take the message, and AntigravityReadBudget how long: past it
// the message is in the mailbox unread (the session takes it at its next
// turn) and the delivery is not acked.
const (
	AntigravityPoll       = 500 * time.Millisecond
	AntigravityReadBudget = 2 * time.Minute
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

// Deliver: find the server, the conversation and the port; send; then wait
// for the session to read the message. Every refusal is exit 1 with the
// reason; 0 is the message read.
func (a *Antigravity) Deliver(ctx context.Context, text string) (int, error) {
	username := a.User
	if username == "" {
		current, err := user.Current()
		if err != nil {
			return 1, fmt.Errorf("current user: %w", err)
		}
		username = current.Username
	}
	ps, _, err := a.Run(ctx, a.Dir, "ps", []string{"-axo", "user=,pid=,args="}, "")
	if err != nil {
		return 1, fmt.Errorf("ps: %w", err)
	}
	pid, token, err := LanguageServer(ps, username)
	if err != nil {
		if errors.Is(err, ErrLanguageServerNotRunning) {
			return 0, Deferred{Reason: err.Error()}
		}
		return 1, err
	}
	listing, _, err := a.Run(ctx, a.Dir, "lsof", []string{"-nP", "-a", "-p", pid, "-iTCP", "-sTCP:LISTEN", "-Fn"}, "")
	if err != nil {
		return 1, fmt.Errorf("lsof: %w", err)
	}
	ports := ListenPorts(listing)
	if len(ports) == 0 {
		return 1, fmt.Errorf("the antigravity language server (pid %s) listens on no TCP port", pid)
	}
	session := a.Session
	if session == "" {
		db := "file:" + filepath.Join(a.home(), AntigravityData, "conversation_summaries.db") + "?mode=ro&immutable=1"
		rows, _, err := a.Run(ctx, a.Dir, "sqlite3", []string{"-json", db, antigravitySummaries}, "")
		if err != nil {
			return 1, fmt.Errorf("sqlite3: %w", err)
		}
		dirs := []string{a.Dir}
		if real, err := filepath.EvalSymlinks(a.Dir); err == nil && real != a.Dir {
			dirs = append(dirs, real)
		}
		if session, err = NewestConversation(rows, dirs...); err != nil {
			return 1, err
		}
	}
	port := ""
	for _, p := range ports {
		if _, err = a.agentapi(ctx, p, token, "get-conversation-metadata", session); err == nil {
			port = p
			break
		}
	}
	if port == "" {
		return 1, fmt.Errorf("no port of the antigravity language server (%s) answers for conversation %s: %v", strings.Join(ports, ", "), session, err)
	}
	mailbox := path.Join(AntigravityData, "brain", session, ".system_generated", "messages")
	before, err := a.mailbox(mailbox)
	if err != nil {
		return 1, fmt.Errorf("conversation %s has no mailbox: %w", session, err)
	}
	if _, err = a.agentapi(ctx, port, token, "send-message", "--title="+AntigravityTitle, session, text); err != nil {
		return 1, err
	}
	ctx, cancel := context.WithTimeout(ctx, AntigravityReadBudget)
	defer cancel()
	id := ""
	for id == "" {
		after, err := a.mailbox(mailbox)
		if err != nil {
			return 1, err
		}
		id, err = a.newMessage(mailbox, before, after)
		if err != nil {
			return 1, err
		}
		if id == "" && !a.wait(ctx) {
			return 1, fmt.Errorf("agentapi accepted the message for conversation %s but none appeared in its mailbox within %s", session, AntigravityReadBudget)
		}
	}
	for {
		readJSON, err := fs.ReadFile(a.fsys(), path.Join(mailbox, "read.json"))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return 1, err
		}
		if Read(readJSON, id) {
			if a.Out != nil {
				fmt.Fprintf(a.Out, "antigravity: message %s read by conversation %s\n", id, session)
			}
			return 0, nil
		}
		if !a.wait(ctx) {
			return 1, fmt.Errorf("message %s is in the mailbox of conversation %s but was not read within %s; the session takes it at its next turn", id, session, AntigravityReadBudget)
		}
	}
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
		return nil, fmt.Errorf("agentapi: %s", reply.Error)
	}
	return reply.Response, nil
}

// NewestConversation picks the first conversation of the summaries query
// (newest first, as `sqlite3 -json` printed it) whose workspaces hold one of
// dirs.
func NewestConversation(rows string, dirs ...string) (string, error) {
	var summaries []struct {
		ID         string `json:"conversation_id"`
		Workspaces string `json:"workspace_uris"` // a JSON list of file:// URIs, as a string
	}
	if strings.TrimSpace(rows) != "" {
		if err := json.Unmarshal([]byte(rows), &summaries); err != nil {
			return "", fmt.Errorf("conversation_summaries.db: not a JSON list: %v", err)
		}
	}
	for _, s := range summaries {
		var workspaces []string
		if err := json.Unmarshal([]byte(s.Workspaces), &workspaces); err != nil {
			return "", fmt.Errorf("conversation_summaries.db: workspace_uris is not a JSON list: %w", err)
		}
		for _, workspace := range workspaces {
			u, err := url.Parse(workspace)
			if err != nil || u.Scheme != "file" || (u.Host != "" && u.Host != "localhost") || u.RawQuery != "" || u.Fragment != "" || !filepath.IsAbs(u.Path) {
				continue
			}
			for _, dir := range dirs {
				if filepath.Clean(u.Path) == filepath.Clean(dir) {
					return s.ID, nil
				}
			}
		}
	}
	return "", fmt.Errorf("no antigravity conversation has %s open; open one there, or name one with --session", dirs[0])
}

// newMessage selects a new mailbox entry carrying our exact title. An unrelated
// message appearing during send must not supply the read acknowledgement.
func (a *Antigravity) newMessage(mailbox string, before, after []string) (string, error) {
	for _, id := range after {
		if !slices.Contains(before, id) {
			raw, err := fs.ReadFile(a.fsys(), path.Join(mailbox, id+".json"))
			if err != nil {
				return "", err
			}
			var message struct {
				RenderDetails struct {
					MessageTitle string `json:"messageTitle"`
				} `json:"renderDetails"`
			}
			if json.Unmarshal(raw, &message) == nil && message.RenderDetails.MessageTitle == AntigravityTitle {
				return id, nil
			}
		}
	}
	return "", nil
}

// Read says whether read.json (a map of message id to true) marks id read.
func Read(readJSON []byte, id string) bool {
	var read map[string]bool
	return json.Unmarshal(readJSON, &read) == nil && read[id]
}

// planAntigravity makes the harness data directory a real directory. No model
// or preset is invented (docs/SPEC-FRIEND.md, harness settings). A symlink is
// refused. A missing directory is created by Write and named as drift until then.
func planAntigravity(p *Prepared) error {
	data := filepath.Join(p.settings.Home, filepath.FromSlash(AntigravityData))
	exists, err := classifyDir(data)
	if err != nil {
		return fmt.Errorf("antigravity data: %w", err)
	}
	if !exists {
		p.drifts = append(p.drifts, fmt.Sprintf("antigravity data: %s is not a real directory", data))
		p.Dirs = append(p.Dirs, data)
	}
	return nil
}
