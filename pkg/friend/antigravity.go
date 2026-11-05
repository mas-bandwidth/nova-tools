package friend

// The antigravity harness pieces nova-hook reads (cmd/nova-hook antigravity.go):
// the agentapi wrapper path, the language server ports, agentapi's answer and
// the newest conversation of a workspace.

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// AntigravityData is the harness's app data directory, under the home.
const AntigravityData = ".gemini/antigravity"

// AgentAPIRefusal is the error agentapi printed in its JSON, at exit 0: the
// harness said no (a wrong conversation, a missing token).
type AgentAPIRefusal string

func (r AgentAPIRefusal) Error() string { return "agentapi: " + string(r) }

// AntigravityRunner is the agentapi wrapper the harness ships under the home
// (Home/.gemini/antigravity/bin/agentapi). An empty home is the current user's.
func AntigravityRunner(home string) string {
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = h
		}
	}
	return filepath.Join(home, AntigravityData, "bin", "agentapi")
}

// agentapi runs one agentapi command against the server at port, with the

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
