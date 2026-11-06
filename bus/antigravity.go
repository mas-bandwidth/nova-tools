package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Antigravity (v1.6.29 cmd/nova-hook/antigravity.go): the workspace's
// language_server is found in ps by its --workspace_id, which carries its
// --csrf_token; its listening ports come from lsof unless
// --antigravity-server names one. The binary's agentapi subcommand, with the
// port and token in its environment, answers get-conversation-metadata
// (the start-up check) and send-message, which puts the closed wake text into
// the conversation as a new turn whether it was idle or busy.
const agentLimit = 10 * time.Second

type agWake struct {
	bin, session, port, dir, name, token string
	exec                                 func(ctx context.Context, env []string, name string, args ...string) ([]byte, error)
}

func realExec(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	return cmd.CombinedOutput()
}

func newAntigravity(ctx context.Context, f map[string]string, dir, name string, s sys) (*agWake, error) {
	ws := f["antigravity-dir"]
	if fi, err := os.Stat(ws); err != nil || !fi.IsDir() || !filepath.IsAbs(ws) {
		return nil, missingInput("--antigravity-dir %s is not an absolute workspace directory", ws)
	}
	if !safeID(f["antigravity-session"]) {
		return nil, missingInput("--antigravity-session %q is not a conversation id", f["antigravity-session"])
	}
	a := &agWake{session: f["antigravity-session"], dir: dir, name: name, exec: s.exec}
	ps, err := a.run(ctx, nil, "ps", "-axo", "pid=,command=")
	if err != nil {
		return nil, noAnswer("antigravity: ps: %v", err)
	}
	var pid string
	pid, a.bin, a.token = findServer(string(ps), workspaceIDs(ws))
	if pid == "" || a.token == "" || a.bin == "" {
		return nil, noAnswer("antigravity: no language_server with a --csrf_token runs for %s: open the workspace in Antigravity", ws)
	}
	ports := []string{}
	if p := f["antigravity-server"]; p != "" {
		if _, port, err := net.SplitHostPort(p); err == nil {
			p = port
		}
		if n, err := strconv.Atoi(p); err != nil || n <= 0 || n > 65535 {
			return nil, missingInput("--antigravity-server %q is not a port or host:port", f["antigravity-server"])
		}
		ports = append(ports, p)
	} else {
		out, err := a.run(ctx, nil, "lsof", "-nP", "-a", "-p", pid, "-iTCP", "-sTCP:LISTEN", "-Fn")
		if err != nil {
			return nil, noAnswer("antigravity: lsof for language_server %s: %v", pid, err)
		}
		for _, l := range strings.Split(string(out), "\n") {
			if n, ok := strings.CutPrefix(l, "n"); ok && strings.Contains(n, ":") {
				ports = append(ports, n[strings.LastIndex(n, ":")+1:])
			}
		}
	}
	var last error = fmt.Errorf("language_server %s listens on no TCP port", pid)
	for _, p := range ports {
		a.port = p
		if last = a.agentapi(ctx, "get-conversation-metadata", a.session); last == nil {
			return a, nil
		}
	}
	return nil, noAnswer("antigravity: conversation %s did not answer: %v", a.session, last)
}

func (a *agWake) wake(ctx context.Context, m Msg) error {
	path, err := save(a.dir, a.name, m)
	if err != nil {
		return err
	}
	return a.agentapi(ctx, "send-message", "--title=nova-bus", a.session,
		fmt.Sprintf("nova-bus saved event %s in %s. Read that JSON file and follow local_receiver.", m.ID, path))
}

// agentapi runs one agentapi command; agentapi prints {"error": ...} at
// exit 0 for a refusal. The token passes only in the environment, and is
// cut out of any error said.
func (a *agWake) agentapi(ctx context.Context, args ...string) error {
	env := []string{"ANTIGRAVITY_CLI_ALIAS=agy", "ANTIGRAVITY_AGENT_PORT=" + a.port, "ANTIGRAVITY_LS_ADDRESS=localhost:" + a.port,
		"ANTIGRAVITY_CSRF_TOKEN=" + a.token, "GEMINI_CLI_CSRF_TOKEN=" + a.token}
	out, err := a.run(ctx, env, a.bin, append([]string{"agentapi"}, args...)...)
	var reply struct{ Error string }
	if t := strings.TrimSpace(string(out)); strings.HasPrefix(t, "{") && json.Unmarshal([]byte(t), &reply) == nil && reply.Error != "" {
		err = fmt.Errorf("agentapi %s: %s", args[0], reply.Error)
	} else if err != nil {
		err = fmt.Errorf("agentapi %s: %v: %s", args[0], err, t)
	}
	if err != nil && a.token != "" {
		err = fmt.Errorf("%s", strings.ReplaceAll(err.Error(), a.token, "<redacted>"))
	}
	return err
}

func (a *agWake) run(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, agentLimit)
	defer cancel()
	return a.exec(ctx, env, name, args...)
}

// findServer is the language_server in ps output whose --workspace_id is one
// of ids: its pid, its binary (the command up to its first flag) and token.
func findServer(ps string, ids []string) (pid, bin, token string) {
	for _, l := range strings.Split(ps, "\n") {
		f := strings.Fields(l)
		if len(f) < 2 || !strings.Contains(l, "language_server") {
			continue
		}
		vals := map[string]string{}
		for i, arg := range f {
			for _, k := range []string{"--workspace_id", "--csrf_token"} {
				if v, ok := strings.CutPrefix(arg, k+"="); ok {
					vals[k] = v
				} else if arg == k && i+1 < len(f) {
					vals[k] = f[i+1]
				}
			}
		}
		if slices.Contains(ids, vals["--workspace_id"]) {
			cmd := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), f[0]))
			if i := strings.Index(cmd, " --"); i > 0 {
				cmd = cmd[:i]
			}
			return f[0], cmd, vals["--csrf_token"]
		}
	}
	return "", "", ""
}

// workspaceIDs is how Antigravity names a workspace directory, for the path
// and, if it is a symlink, its target: file_ and the path with '/' as '_' and
// any other non-alphanumeric rune as _XX_ (hex) or as '_'.
func workspaceIDs(dir string) []string {
	dirs := []string{dir}
	if real, err := filepath.EvalSymlinks(dir); err == nil && real != dir {
		dirs = append(dirs, real)
	}
	var ids []string
	for _, d := range dirs {
		var hex, simple strings.Builder
		for _, r := range strings.TrimPrefix(filepath.Clean(d), "/") {
			switch {
			case unicode.IsLetter(r) || unicode.IsDigit(r):
				hex.WriteRune(r)
				simple.WriteRune(r)
			case r == '/':
				hex.WriteByte('_')
				simple.WriteByte('_')
			default:
				fmt.Fprintf(&hex, "_%02X_", r)
				simple.WriteByte('_')
			}
		}
		ids = append(ids, "file_"+hex.String(), "file_"+simple.String())
	}
	return ids
}
