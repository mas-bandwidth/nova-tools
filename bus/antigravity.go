package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Antigravity (v1.6.29 cmd/nova-hook/antigravity.go): the candidates are
// this user's language_servers in ps with a --csrf_token, the workspace's
// own (--workspace_id) first; Antigravity 2.29 runs one hub server with no
// --workspace_id. The program is the process's own executable, never its
// argv, and must not be writable by others. Its agentapi subcommand, with
// the port (lsof, or --antigravity-server) and the token in its environment,
// answers get-conversation-metadata: the first candidate that does is taken.
// send-message puts the closed wake text into the conversation as a new
// turn. A failed send finds the server once more (Antigravity restarted)
// and sends again; a second failure ends the hook.
const (
	agentLimit = wakeLimit
	maxError   = 160 // bytes of a harness's error text copied into a receipt
)

type agWake struct {
	bin, session, port, dir, name, token, ws, fixed string
	s                                               sys
}

// realShell runs command with sh -c, stdin on its stdin and vars added to
// the environment, and returns its exit code; -1 when it did not run.
func realShell(ctx context.Context, vars []string, stdin, command string) (int, error) {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.Env = append(os.Environ(), vars...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

func realExec(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	return cmd.CombinedOutput()
}

// realProgram is a process's executable as the kernel has it, never its
// argv: /proc/<pid>/exe where there is one (Linux), else the file lsof lists
// as its text (txt) on macOS.
func realProgram(ctx context.Context, pid string) (string, error) {
	if p, err := os.Readlink("/proc/" + pid + "/exe"); err == nil {
		return p, nil
	}
	out, err := realExec(ctx, nil, "lsof", "-a", "-p", pid, "-d", "txt", "-Fn")
	for _, l := range strings.Split(string(out), "\n") {
		if n, ok := strings.CutPrefix(l, "n"); ok {
			return n, nil
		}
	}
	return "", fmt.Errorf("no executable for process %s: %v", pid, err)
}

// checkProgram holds a program to a regular file of this user's or root's
// that neither group nor others can write.
func checkProgram(path string, uid int) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	owner, _, ok := statOf(fi)
	if !filepath.IsAbs(path) || !fi.Mode().IsRegular() || fi.Mode().Perm()&0o022 != 0 || !ok || (int(owner) != uid && owner != 0) {
		return fmt.Errorf("%s is not a program of this user's or root's, closed to others", path)
	}
	return nil
}

func newAntigravity(ctx context.Context, f map[string]string, dir, name string, s sys) (*agWake, error) {
	ws := f["antigravity-dir"]
	if fi, err := os.Stat(ws); err != nil || !fi.IsDir() || !filepath.IsAbs(ws) {
		return nil, badInput("--antigravity-dir %s is not an absolute workspace directory: name the folder open in Antigravity", ws)
	}
	if !safeID(f["antigravity-session"]) {
		return nil, badInput("--antigravity-session %q is not a conversation id: the folder name under ~/.gemini/antigravity/brain/", f["antigravity-session"])
	}
	port := f["antigravity-server"]
	if port != "" {
		if _, p, err := net.SplitHostPort(port); err == nil {
			port = p
		}
		if n, err := strconv.Atoi(port); err != nil || n <= 0 || n > 65535 {
			return nil, badInput("--antigravity-server %q is not a port or host:port", f["antigravity-server"])
		}
	}
	a := &agWake{session: f["antigravity-session"], dir: dir, name: name, ws: ws, fixed: port, s: s}
	if err := a.find(ctx); err != nil {
		return nil, noAnswer("antigravity: %v", err)
	}
	return a, nil
}

// find takes the first candidate server whose agentapi answers for the
// conversation.
func (a *agWake) find(ctx context.Context) error {
	ps, err := a.run(ctx, nil, "ps", "-axww", "-o", "pid=,uid=,command=")
	if err != nil {
		return fmt.Errorf("ps: %v", err)
	}
	servers := findServers(string(ps), strconv.Itoa(a.s.uid), workspaceIDs(a.ws))
	if len(servers) == 0 {
		return fmt.Errorf("no language_server with a --csrf_token runs for this user: open %s in Antigravity", a.ws)
	}
	var last error
	for _, sv := range servers {
		bin, err := a.s.program(ctx, sv.pid)
		if err == nil {
			err = checkProgram(bin, a.s.uid)
		}
		if err != nil {
			last = fmt.Errorf("language_server %s: %v", sv.pid, err)
			continue
		}
		a.bin, a.token = bin, sv.token
		ports := []string{a.fixed}
		if a.fixed == "" {
			out, err := a.run(ctx, nil, "lsof", "-nP", "-a", "-p", sv.pid, "-iTCP", "-sTCP:LISTEN", "-Fn")
			if err != nil {
				last = fmt.Errorf("lsof for language_server %s: %v", sv.pid, err)
				continue
			}
			ports = ports[:0]
			for _, l := range strings.Split(string(out), "\n") {
				if n, ok := strings.CutPrefix(l, "n"); ok && strings.Contains(n, ":") {
					ports = append(ports, n[strings.LastIndex(n, ":")+1:])
				}
			}
		}
		if len(ports) == 0 {
			last = fmt.Errorf("language_server %s listens on no TCP port", sv.pid)
		}
		for _, p := range ports {
			a.port = p
			if last = a.agentapi(ctx, "get-conversation-metadata", a.session); last == nil {
				return nil
			}
		}
	}
	return fmt.Errorf("conversation %s did not answer on any of %d language_servers of this user: %v", a.session, len(servers), last)
}

func (a *agWake) wake(ctx context.Context, m Msg) error {
	path, err := save(a.dir, a.name, m)
	if err != nil {
		return err
	}
	text := fmt.Sprintf("nova-bus saved event %s in %s. Read that JSON file and follow local_receiver.", m.ID, path)
	err = a.agentapi(ctx, "send-message", "--title=nova-bus", a.session, text)
	if err == nil || ctx.Err() != nil {
		if err != nil { // the send was cut off: it may have arrived
			return unconfirmed{err}
		}
		return nil
	}
	if ferr := a.find(ctx); ferr != nil {
		return fmt.Errorf("%v; found again: %v", err, ferr)
	}
	return a.agentapi(ctx, "send-message", "--title=nova-bus", a.session, text)
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
	if err != nil && len(err.Error()) > maxError {
		err = fmt.Errorf("%s", strings.ToValidUTF8(err.Error()[:maxError], ""))
	}
	return err
}

func (a *agWake) run(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, agentLimit)
	defer cancel()
	return a.s.exec(ctx, env, name, args...)
}

type server struct{ pid, bin, token string }

// findServers is every language_server of this user (uid) in ps output that
// carries a --csrf_token: its pid, its binary (the command up to its first
// flag) and token. Those whose --workspace_id is one of ids come first; the
// rest follow, because a server may name the workspace another way (its
// symlink's other side) or not at all, and get-conversation-metadata picks
// the one that has the conversation (v1.6.29 fell back the same way).
func findServers(ps, uid string, ids []string) []server {
	var match, rest []server
	for _, l := range strings.Split(ps, "\n") {
		f := strings.Fields(l)
		if len(f) < 3 || f[1] != uid || !strings.Contains(l, "language_server") {
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
		cmd := strings.TrimSpace(l)
		for _, lead := range f[:2] { // the command is what follows the pid and uid fields
			cmd = strings.TrimSpace(strings.TrimPrefix(cmd, lead))
		}
		if i := strings.Index(cmd, " --"); i > 0 {
			cmd = cmd[:i]
		}
		if vals["--csrf_token"] == "" || cmd == "" {
			continue
		}
		if sv := (server{f[0], cmd, vals["--csrf_token"]}); slices.Contains(ids, vals["--workspace_id"]) {
			match = append(match, sv)
		} else {
			rest = append(rest, sv)
		}
	}
	return append(match, rest...)
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
