package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
	"github.com/mas-bandwidth/nova-tools/pkg/codexdesktop"
	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

const antigravityCmdTimeout = 10 * time.Second

type antigravityExecFunc func(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error)

func realAntigravityExec(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error) {
	cmd := subproc.Context(ctx, name, args...)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = env
	}
	return cmd.CombinedOutput()
}

type antigravityRoute struct {
	as            string
	workspaceDir  string
	sessionID     string
	eventsDir     string
	homeDir       string
	agentAPI      string
	conversation  string
	preferredPort int
	pid           int
	csrfToken     string
	port          int
	exec          antigravityExecFunc
}

func (w world) antigravityConfig(c *tool.Call) (*codexRoute, error) {
	var since time.Time
	rawSince := c.Str("antigravity-since")
	if rawSince != "" {
		var err error
		since, err = time.Parse(time.RFC3339, rawSince)
		if err != nil || rawSince != since.UTC().Format(time.RFC3339) {
			return nil, errors.New("antigravity cutover wants --antigravity-since as a fixed UTC timestamp, for example 2026-10-14T20:00:00Z; retain it in launch configuration across restarts")
		}
	}
	sessionID := strings.TrimSpace(c.Str("antigravity-session"))
	if sessionID == "" {
		sessionID = strings.TrimSpace(c.Str("session"))
	}
	if sessionID != "" && (strings.ContainsAny(sessionID, "/\\") || sessionID == "." || sessionID == "..") {
		return nil, errors.New("antigravity session ID wants a conversation UUID without path separators")
	}
	workspaceDir := strings.TrimSpace(c.Str("antigravity-dir"))
	if workspaceDir == "" && sessionID == "" {
		return nil, errors.New("antigravity delivery wants --antigravity-dir <workspace> or --antigravity-session <uuid>")
	}
	if workspaceDir != "" && (!filepath.IsAbs(workspaceDir) || filepath.Clean(workspaceDir) != workspaceDir) {
		return nil, errors.New("antigravity workspace directory wants a clean absolute path")
	}
	var preferredPort int
	if serverAddr := strings.TrimSpace(c.Str("antigravity-server")); serverAddr != "" {
		_, portStr, splitErr := net.SplitHostPort(serverAddr)
		if splitErr != nil {
			portStr = serverAddr
		}
		p, parseErr := strconv.Atoi(portStr)
		if parseErr != nil || p <= 0 || p > 65535 {
			return nil, errors.New("antigravity server address wants host:port or port")
		}
		preferredPort = p
	}
	if c.Bool("once") {
		return nil, errors.New("antigravity delivery wants a running hook client; omit --once")
	}
	if w.codexLock == nil || w.codexSave == nil || w.antigravityInstall == nil || w.antigravityDeliver == nil {
		return nil, errors.New("antigravity delivery wants a configured local native receiver")
	}
	homeFn := w.antigravityHome
	if homeFn == nil {
		homeFn = os.UserHomeDir
	}
	home, err := homeFn()
	if err != nil || strings.TrimSpace(home) == "" {
		return nil, errors.New("antigravity delivery could not resolve home directory")
	}
	as := strings.TrimSpace(c.Str("as"))
	if as == "" {
		as = "default"
	}
	eventsDir := strings.TrimSpace(c.Str("antigravity-events-dir"))
	if eventsDir == "" {
		eventsDir = filepath.Join(home, ".gemini", "antigravity", "nova-hook", as)
	}
	if err := codexdesktop.ValidateDirectory(eventsDir); err != nil {
		return nil, err
	}
	execFn := w.antigravityExec
	if execFn == nil {
		execFn = realAntigravityExec
	}
	ag := &antigravityRoute{
		as:            as,
		workspaceDir:  workspaceDir,
		sessionID:     sessionID,
		eventsDir:     eventsDir,
		homeDir:       home,
		preferredPort: preferredPort,
		exec:          execFn,
	}
	r := &codexRoute{
		directory:   eventsDir,
		antigravity: ag,
		since:       since,
	}
	if c.DryRun() {
		if _, err := codexdesktop.SavedPath(r.directory, "route"); err != nil {
			return nil, err
		}
		return r, nil
	}
	if err := w.antigravityInstall(ag); err != nil {
		return nil, err
	}
	r.directory = ag.eventsDir
	return r, nil
}

func installAntigravityRoute(ag *antigravityRoute) error {
	if ag == nil {
		return errors.New("antigravity route is nil")
	}
	if ag.workspaceDir != "" {
		info, err := os.Stat(ag.workspaceDir)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("antigravity workspace directory %s is not accessible", ag.workspaceDir)
		}
	}
	if err := ensureAntigravityEventsDir(ag.eventsDir); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), antigravityCmdTimeout)
	defer cancel()

	pid, token, ports, serverBin, err := discoverAntigravityServer(ctx, ag)
	if err != nil {
		return err
	}
	agentAPI, err := ensureAntigravityAgentAPI(ag.homeDir, serverBin)
	if err != nil {
		return err
	}
	ag.agentAPI = agentAPI

	conversation, err := resolveAntigravityConversation(ctx, ag)
	if err != nil {
		return err
	}
	ag.conversation = conversation

	port, err := probeAntigravityPort(ctx, ag, token, ports)
	if err != nil {
		return err
	}
	ag.pid = pid
	ag.csrfToken = token
	ag.port = port

	msgDir := antigravityMailboxDir(ag.homeDir, ag.conversation)
	if err := os.MkdirAll(msgDir, 0700); err != nil {
		return fmt.Errorf("prepare antigravity conversation mailbox: %w", err)
	}
	return nil
}

func ensureAntigravityEventsDir(dir string) error {
	if err := codexdesktop.ValidateDirectory(dir); err != nil {
		return err
	}
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("create antigravity event directory: %w", err)
		}
		if err := os.Chmod(dir, 0700); err != nil {
			return fmt.Errorf("chmod antigravity event directory: %w", err)
		}
	}
	return privateCodexDirectory(dir)
}

func discoverAntigravityServer(ctx context.Context, ag *antigravityRoute) (int, string, []int, string, error) {
	psOut, err := ag.exec(ctx, "", nil, "ps", "-axo", "pid=,command=")
	if err != nil {
		return 0, "", nil, "", fmt.Errorf("discover Antigravity language_server via ps: %w", err)
	}
	pid, token, serverBin, ok := parseAntigravityServer(string(psOut), ag.workspaceDir)
	if !ok {
		target := ag.workspaceDir
		if target == "" {
			target = "active session"
		}
		return 0, "", nil, "", fmt.Errorf("no Antigravity language_server running for %s", target)
	}
	if token == "" {
		return 0, "", nil, "", fmt.Errorf("Antigravity language_server pid %d has no --csrf_token", pid)
	}
	lsofOut, err := ag.exec(ctx, "", nil, "lsof", "-nP", "-a", "-p", strconv.Itoa(pid), "-iTCP", "-sTCP:LISTEN")
	if err != nil {
		return 0, "", nil, "", fmt.Errorf("discover Antigravity language_server ports via lsof: %w", err)
	}
	ports := parseAntigravityListenPorts(string(lsofOut))
	if ag.preferredPort > 0 && !slices.Contains(ports, ag.preferredPort) {
		ports = append([]int{ag.preferredPort}, ports...)
	}
	if len(ports) == 0 {
		return 0, "", nil, "", fmt.Errorf("Antigravity language_server pid %d has no listening TCP ports", pid)
	}
	return pid, token, ports, serverBin, nil
}

func antigravityWorkspaceIDs(dir string) []string {
	cleaned := strings.TrimPrefix(filepath.Clean(dir), "/")
	if cleaned == "" || cleaned == "." {
		return []string{"file"}
	}
	var hexBuilder strings.Builder
	var simpleBuilder strings.Builder
	hexBuilder.WriteString("file_")
	simpleBuilder.WriteString("file_")
	for _, r := range cleaned {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			hexBuilder.WriteRune(r)
			simpleBuilder.WriteRune(r)
		case r == '/':
			hexBuilder.WriteByte('_')
			simpleBuilder.WriteByte('_')
		default:
			hexBuilder.WriteString(fmt.Sprintf("_%02X_", r))
			simpleBuilder.WriteByte('_')
		}
	}
	h := hexBuilder.String()
	s := simpleBuilder.String()
	if h == s {
		return []string{h}
	}
	return []string{h, s}
}

func parseAntigravityServer(psOut, workspaceDir string) (int, string, string, bool) {
	var wantWS []string
	if workspaceDir != "" {
		wantWS = append(wantWS, antigravityWorkspaceIDs(workspaceDir)...)
		if realDir, err := filepath.EvalSymlinks(workspaceDir); err == nil && realDir != workspaceDir {
			wantWS = append(wantWS, antigravityWorkspaceIDs(realDir)...)
		}
	}
	var fallbackPID int
	var fallbackToken, fallbackBin string
	var fallbackFound bool

	lines := strings.Split(psOut, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || !strings.Contains(line, "language_server") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pidIdx := 0
		pid, pidErr := strconv.Atoi(fields[0])
		if pidErr != nil && len(fields) >= 3 {
			pid, pidErr = strconv.Atoi(fields[1])
			pidIdx = 1
		}
		if pidErr != nil || pid <= 0 {
			continue
		}
		var wsID, token string
		isAntigravity := false
		for j := pidIdx + 1; j < len(fields); j++ {
			f := fields[j]
			if strings.Contains(f, "antigravity") || strings.Contains(f, "Antigravity") {
				isAntigravity = true
			}
			switch {
			case f == "--workspace_id" && j+1 < len(fields):
				wsID = fields[j+1]
				isAntigravity = true
				j++
			case strings.HasPrefix(f, "--workspace_id="):
				wsID = strings.TrimPrefix(f, "--workspace_id=")
				isAntigravity = true
			case f == "--csrf_token" && j+1 < len(fields):
				token = fields[j+1]
				j++
			case strings.HasPrefix(f, "--csrf_token="):
				token = strings.TrimPrefix(f, "--csrf_token=")
			}
		}
		bin := extractLanguageServerBin(line, fields[pidIdx])
		if len(wantWS) > 0 && wsID != "" {
			if slices.Contains(wantWS, wsID) {
				return pid, token, bin, true
			}
			continue
		}
		if isAntigravity && !fallbackFound {
			fallbackPID = pid
			fallbackToken = token
			fallbackBin = bin
			fallbackFound = true
		}
	}
	if fallbackFound {
		return fallbackPID, fallbackToken, fallbackBin, true
	}
	return 0, "", "", false
}

func extractLanguageServerBin(line, pidField string) string {
	idx := strings.Index(line, pidField)
	if idx < 0 {
		return ""
	}
	cmd := strings.TrimSpace(line[idx+len(pidField):])
	if flagIdx := strings.Index(cmd, " --"); flagIdx > 0 {
		cmd = strings.TrimSpace(cmd[:flagIdx])
	}
	if filepath.IsAbs(cmd) && !strings.ContainsAny(cmd, "\"\n\r") {
		return cmd
	}
	return ""
}

func parseAntigravityListenPorts(lsofOut string) []int {
	var ports []int
	seen := map[int]bool{}
	for _, pStr := range friend.ListenPorts(lsofOut) {
		if p, err := strconv.Atoi(pStr); err == nil && p > 0 && p <= 65535 && !seen[p] {
			seen[p] = true
			ports = append(ports, p)
		}
	}
	lines := strings.Split(lsofOut, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "COMMAND") {
			continue
		}
		fields := strings.Fields(line)
		for j := 0; j < len(fields); j++ {
			tok := fields[j]
			if tok == "(LISTEN)" || !strings.Contains(tok, ":") {
				continue
			}
			colon := strings.LastIndexByte(tok, ':')
			if colon < 0 || colon+1 >= len(tok) {
				continue
			}
			p, err := strconv.Atoi(tok[colon+1:])
			if err != nil || p <= 0 || p > 65535 || seen[p] {
				continue
			}
			seen[p] = true
			ports = append(ports, p)
		}
	}
	sort.Ints(ports)
	return ports
}

func ensureAntigravityAgentAPI(home, serverBin string) (string, error) {
	wrapper := friend.AntigravityRunner(home)
	if _, err := os.Stat(wrapper); errors.Is(err, os.ErrNotExist) && serverBin != "" {
		if err := os.MkdirAll(filepath.Dir(wrapper), 0755); err != nil {
			return "", fmt.Errorf("create agentapi wrapper directory: %w", err)
		}
		script := fmt.Sprintf("#!/bin/sh\nexec %q agentapi \"$@\"\n", serverBin)
		if err := atomicfile.Write(wrapper, []byte(script), 0755); err != nil {
			return "", fmt.Errorf("install agentapi wrapper: %w", err)
		}
	}
	for _, candidate := range []string{
		wrapper,
		filepath.Join(home, ".local", "bin", "agentapi"),
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("Antigravity agentapi helper not found or not executable at %s", wrapper)
}

func resolveAntigravityConversation(ctx context.Context, ag *antigravityRoute) (string, error) {
	if ag.sessionID != "" {
		return ag.sessionID, nil
	}
	dbPath := filepath.Join(ag.homeDir, ".gemini", "antigravity", "conversations", "conversation_summaries.db")
	out, err := ag.exec(ctx, "", nil, "sqlite3", "-json", dbPath, "SELECT key, value FROM ItemTable WHERE key LIKE 'conversation_summaries.%';")
	if err != nil {
		return "", fmt.Errorf("read Antigravity conversations from %s: %w", dbPath, err)
	}
	conv := parseAntigravityNewestConversation(out, ag.workspaceDir)
	if conv == "" {
		return "", fmt.Errorf("no Antigravity conversation found for %s (pass --antigravity-session <uuid>)", ag.workspaceDir)
	}
	return conv, nil
}

func parseAntigravityNewestConversation(raw []byte, workspaceDir string) string {
	dirs := []string{workspaceDir}
	if realDir, err := filepath.EvalSymlinks(workspaceDir); err == nil && realDir != workspaceDir {
		dirs = append(dirs, realDir)
	}
	if id, err := friend.NewestConversation(string(raw), dirs...); err == nil && id != "" {
		return id
	}
	var rows []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return ""
	}
	type candidate struct {
		id    string
		mtime int64
	}
	var best candidate
	for _, row := range rows {
		id := strings.TrimPrefix(row.Key, "conversation_summaries.")
		if id == "" || id == row.Key {
			continue
		}
		if wsURI, mtime, isSub, parentID, ok := decodeProtoSummary(row.Value); ok {
			if isSub || parentID != "" {
				continue
			}
			matched := false
			for _, d := range dirs {
				if strings.TrimPrefix(wsURI, "file://") == filepath.Clean(d) {
					matched = true
				}
			}
			if !matched {
				continue
			}
			if best.id == "" || mtime > best.mtime || (mtime == best.mtime && id > best.id) {
				best = candidate{id: id, mtime: mtime}
			}
			continue
		}
		var meta struct {
			ConversationID string   `json:"conversationId"`
			WorkspacePath  string   `json:"workspacePath"`
			WorkspaceURIs  []string `json:"workspaceUris"`
			UpdatedAt      int64    `json:"updatedAt"`
			LastModified   int64    `json:"lastModifiedTime"`
			ParentID       string   `json:"parentConversationId"`
			IsSubagent     bool     `json:"isSubagent"`
		}
		if json.Unmarshal([]byte(row.Value), &meta) == nil {
			if meta.IsSubagent || meta.ParentID != "" {
				continue
			}
			matches := false
			for _, d := range dirs {
				cleanD := filepath.Clean(d)
				if meta.WorkspacePath != "" && filepath.Clean(meta.WorkspacePath) == cleanD {
					matches = true
				}
				for _, u := range meta.WorkspaceURIs {
					if strings.TrimPrefix(u, "file://") == cleanD {
						matches = true
					}
				}
			}
			if !matches && !strings.Contains(row.Value, workspaceDir) {
				continue
			}
			ts := meta.UpdatedAt
			if meta.LastModified > ts {
				ts = meta.LastModified
			}
			if best.id == "" || ts > best.mtime || (ts == best.mtime && id > best.id) {
				best = candidate{id: id, mtime: ts}
			}
			continue
		}
		if strings.Contains(row.Value, workspaceDir) && best.id == "" {
			best = candidate{id: id, mtime: 1}
		}
	}
	return best.id
}

func decodeProtoSummary(b64 string) (wsURI string, mtime int64, isSubagent bool, parentID string, ok bool) {
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil || len(data) == 0 {
		return "", 0, false, "", false
	}
	for len(data) > 0 {
		tag, n := readProtoVarint(data)
		if n <= 0 {
			return "", 0, false, "", false
		}
		data = data[n:]
		fieldNum := int(tag >> 3)
		wireType := int(tag & 7)
		switch wireType {
		case 0:
			val, vn := readProtoVarint(data)
			if vn <= 0 {
				return "", 0, false, "", false
			}
			data = data[vn:]
			if fieldNum == 14 && val != 0 {
				isSubagent = true
			}
		case 2:
			length, ln := readProtoVarint(data)
			if ln <= 0 || uint64(len(data)-ln) < length {
				return "", 0, false, "", false
			}
			payload := data[ln : ln+int(length)]
			data = data[ln+int(length):]
			switch fieldNum {
			case 4:
				if subTag, sn := readProtoVarint(payload); sn > 0 && (subTag>>3) == 1 && (subTag&7) == 0 {
					if sec, vn := readProtoVarint(payload[sn:]); vn > 0 {
						mtime = int64(sec)
					}
				}
			case 9:
				wsURI = string(payload)
			case 16:
				parentID = string(payload)
			}
		default:
			return "", 0, false, "", false
		}
	}
	return wsURI, mtime, isSubagent, parentID, wsURI != ""
}

func readProtoVarint(b []byte) (uint64, int) {
	var v uint64
	var shift uint
	for i := 0; i < len(b) && i < 10; i++ {
		 v |= uint64(b[i]&0x7f) << shift
		 if b[i] < 0x80 {
			 return v, i + 1
		 }
		 shift += 7
	}
	return 0, 0
}

func checkAntigravityOutput(out []byte, runErr error) error {
	trimmed := strings.TrimSpace(string(out))
	if strings.HasPrefix(trimmed, "{") {
		if _, apiErr := friend.AgentAPI(trimmed); apiErr != nil {
			return apiErr
		}
	}
	return runErr
}

func probeAntigravityPort(ctx context.Context, ag *antigravityRoute, token string, ports []int) (int, error) {
	var lastErr error
	for _, port := range ports {
		env := antigravityEnv(token, port)
		out, err := ag.exec(ctx, ag.workspaceDir, env, ag.agentAPI, "get-conversation-metadata", ag.conversation)
		err = checkAntigravityOutput(out, err)
		if err == nil {
			return port, nil
		}
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			lastErr = fmt.Errorf("port %d: %w (%s)", port, err, msg)
		} else {
			lastErr = fmt.Errorf("port %d: %w", port, err)
		}
	}
	if lastErr == nil {
		lastErr = errors.New("no candidate ports")
	}
	return 0, fmt.Errorf("Antigravity conversation %s did not answer get-conversation-metadata: %w", ag.conversation, lastErr)
}

func antigravityEnv(token string, port int) []string {
	portStr := strconv.Itoa(port)
	return append(os.Environ(),
		"ANTIGRAVITY_CLI_ALIAS=agy",
		"ANTIGRAVITY_AGENT_PORT="+portStr,
		"ANTIGRAVITY_LS_ADDRESS=localhost:"+portStr,
		"ANTIGRAVITY_CSRF_TOKEN="+token,
		"GEMINI_CLI_CSRF_TOKEN="+token,
	)
}

func antigravityMailboxDir(home, conversation string) string {
	return filepath.Join(home, ".gemini", "antigravity", "brain", conversation, ".system_generated", "messages")
}

func snapshotAntigravityMailbox(msgDir string) map[string]bool {
	before := map[string]bool{}
	entries, err := os.ReadDir(msgDir)
	if err != nil {
		return before
	}
	for _, e := range entries {
		before[e.Name()] = true
	}
	return before
}

func newAntigravityMailboxFile(msgDir string, before map[string]bool) string {
	entries, err := os.ReadDir(msgDir)
	if err != nil {
		return ""
	}
	var added []string
	for _, e := range entries {
		if !before[e.Name()] {
			added = append(added, e.Name())
		}
	}
	if len(added) == 0 {
		return ""
	}
	sort.Strings(added)
	return added[len(added)-1]
}

// deliverAntigravitySaved delivers a saved external event file into the target
// Antigravity conversation via `agentapi send-message --title=nova-hook`.
// Only the validated event ID and canonical path enter the notice text.
func deliverAntigravitySaved(ctx context.Context, ag *antigravityRoute, id, directory string) (string, error) {
	if ag == nil {
		return "", fmt.Errorf("%w: antigravity route is nil", codexdesktop.ErrPreSend)
	}
	savedPath, err := codexdesktop.SavedPath(directory, id)
	if err != nil {
		return "", fmt.Errorf("%w: %w", codexdesktop.ErrPreSend, err)
	}
	info, err := os.Lstat(savedPath)
	if err != nil {
		return "", fmt.Errorf("%w: stat saved event: %w", codexdesktop.ErrPreSend, err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxCodexEvent {
		return "", fmt.Errorf("%w: saved event must be a non-empty regular file at most %d bytes", codexdesktop.ErrPreSend, maxCodexEvent)
	}
	notice := fmt.Sprintf("nova-hook saved event %s in %s. Read that JSON file and follow local_receiver.", id, savedPath)
	msgDir := antigravityMailboxDir(ag.homeDir, ag.conversation)
	before := snapshotAntigravityMailbox(msgDir)

	sendOnPort := func(token string, port int) ([]byte, error, bool) {
		sendCtx, cancel := context.WithTimeout(ctx, antigravityCmdTimeout)
		defer cancel()
		out, sendErr := ag.exec(sendCtx, ag.workspaceDir, antigravityEnv(token, port), ag.agentAPI, "send-message", "--title=nova-hook", ag.conversation, notice)
		timedOut := errors.Is(sendErr, context.DeadlineExceeded) || errors.Is(sendErr, context.Canceled) ||
			errors.Is(sendCtx.Err(), context.DeadlineExceeded) || errors.Is(sendCtx.Err(), context.Canceled)
		if !timedOut {
			sendErr = checkAntigravityOutput(out, sendErr)
		}
		return out, sendErr, timedOut
	}

	var out []byte
	var sendErr error
	var timedOut bool
	if ag.port > 0 && ag.csrfToken != "" {
		out, sendErr, timedOut = sendOnPort(ag.csrfToken, ag.port)
		if sendErr == nil {
			turn := newAntigravityMailboxFile(msgDir, before)
			if turn == "" {
				turn = "antigravity-" + id
			}
			return turn, nil
		}
		if turn := newAntigravityMailboxFile(msgDir, before); turn != "" {
			return turn, nil
		}
		if timedOut {
			return "", fmt.Errorf("%w: %v", codexdesktop.ErrOutcomeUnknown, sendErr)
		}
	}

	// Re-discover the running language_server in case it restarted with a new PID, token, or port.
	discCtx, discCancel := context.WithTimeout(ctx, antigravityCmdTimeout)
	pid, token, ports, _, discErr := discoverAntigravityServer(discCtx, ag)
	if discErr == nil {
		var port int
		port, discErr = probeAntigravityPort(discCtx, ag, token, ports)
		if discErr == nil {
			ag.pid = pid
			ag.csrfToken = token
			ag.port = port
		}
	}
	discCancel()
	if discErr != nil {
		if sendErr != nil {
			detail := strings.TrimSpace(string(out))
			if detail != "" {
				return "", fmt.Errorf("%w: %v (%s)", codexdesktop.ErrRejected, sendErr, detail)
			}
			return "", fmt.Errorf("%w: %v", codexdesktop.ErrRejected, sendErr)
		}
		return "", fmt.Errorf("%w: %w", codexdesktop.ErrPreSend, discErr)
	}

	out, sendErr, timedOut = sendOnPort(ag.csrfToken, ag.port)
	if sendErr == nil {
		turn := newAntigravityMailboxFile(msgDir, before)
		if turn == "" {
			turn = "antigravity-" + id
		}
		return turn, nil
	}
	if turn := newAntigravityMailboxFile(msgDir, before); turn != "" {
		return turn, nil
	}
	if timedOut {
		return "", fmt.Errorf("%w: %v", codexdesktop.ErrOutcomeUnknown, sendErr)
	}
	detail := strings.TrimSpace(string(out))
	if detail != "" {
		return "", fmt.Errorf("%w: %v (%s)", codexdesktop.ErrRejected, sendErr, detail)
	}
	return "", fmt.Errorf("%w: %v", codexdesktop.ErrRejected, sendErr)
}
