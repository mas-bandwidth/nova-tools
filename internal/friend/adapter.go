package friend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Harnesses are the harness names run and install take, in the order the
// help lists them; OpenCode, Codex, Antigravity, DSH, Gemini and Grok have a
// deliver command, the rest refuse honestly (Stub), the surveyed ones with
// their reason.
var Harnesses = append([]string{"opencode", "codex", "claude", "antigravity", "dsh", "gemini", "grok"}, RefusedHarnesses...)

// Deliverer pushes one text into the friend's running session as a turn
// and normally blocks until the turn ends. Codex queue instead confirms
// enqueue acceptance; the app runs it after the active turn ends. Exit 0
// acks the bus message (SPEC-FRIEND.md, the deliver command and Codex).
type Deliverer interface {
	Deliver(ctx context.Context, text string) (exit int, err error)
}

// Deferred is a Deliverer's answer when the session cannot take a turn now
// and nothing has failed (for example, neither Codex queue nor resume can
// accept it). The daemon keeps the message in hand, tries again
// after RecheckEvery, counts nothing toward MaxDeliveries and acks nothing,
// so a chat open all day loses no message.
type Deferred struct{ Reason string }

func (d Deferred) Error() string { return "deferred: " + d.Reason }

// Exec runs one command for an adapter: the program, its arguments and its
// working directory, with the text on stdin, answering what it printed and
// its exit code. The daemon passes the real one (RealExec); a test its own.
type Exec func(ctx context.Context, dir, name string, args []string, stdin string) (stdout string, exit int, err error)

// OutputKept bounds how much of a turn's output the daemon keeps in its
// record: the head, enough to see what the session did with the message.
const OutputKept = 2048

// outputKey carries, in a delivery's context, what to call when the command
// prints: the daemon's watch on a running turn (WithOutputSeen).
type outputKey struct{}

// WithOutputSeen is ctx carrying seen, called each time the command a
// delivery runs prints to stdout or stderr: a turn that prints is working,
// and only a turn silent past the daemon's SilentStop is stopped.
func WithOutputSeen(ctx context.Context, seen func()) context.Context {
	return context.WithValue(ctx, outputKey{}, seen)
}

// seenWriter is a Builder that says each write to the context's watch.
type seenWriter struct {
	b    strings.Builder
	seen func()
}

func (w *seenWriter) Write(p []byte) (int, error) {
	if len(p) > 0 && w.seen != nil {
		w.seen()
	}
	return w.b.Write(p)
}

// RealExec runs the command through os/exec: the program directly, never a
// shell, so a message's text is never interpolated. No clock bounds it: a
// turn that prints keeps running however long it takes, and the daemon
// stops one silent past its SilentStop by cancelling ctx (the finding of
// 2026-10-04: a fixed ten-minute cap killed real work mid-turn). Every write
// to stdout or stderr is said to the watch in ctx (WithOutputSeen). The
// command is its own session leader (Setsid), and on a cancel the whole
// group is signalled, SIGTERM then SIGKILL after KillDelay: a harness that
// forks (opencode run does) leaves no orphan behind a stop. On a nonzero
// exit the output carries the head of stderr after stdout: a harness says
// why it refused there (dsh does).
func RealExec(ctx context.Context, dir, name string, args []string, stdin string) (string, int, error) {
	return realExec(ctx, KillDelay, dir, name, args, stdin)
}

func realExec(ctx context.Context, killDelay time.Duration, dir, name string, args []string, stdin string) (string, int, error) {
	seen, _ := ctx.Value(outputKey{}).(func())
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	} // else /dev/null: a headless opencode run with stdin left open hangs at init (measured 2026-10-04)
	out, stderr := &seenWriter{seen: seen}, &seenWriter{seen: seen}
	cmd.Stdout = out
	cmd.Stderr = stderr
	ownGroup(cmd)
	cmd.WaitDelay = killDelay // the pipes close this long after the group is signalled
	err := cmd.Run()
	if ctx.Err() != nil && cmd.Process != nil {
		killGroup(cmd.Process.Pid) // the leader is dead by now; what it forked is not
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if ctx.Err() != nil {
			return out.b.String(), exitErr.ExitCode(), errors.New("the delivery was stopped with its process group")
		}
		return out.b.String() + Head(stderr.b.String(), OutputKept), exitErr.ExitCode(), nil
	}
	return out.b.String(), 0, err
}

// ProviderRefused is a Deliverer's answer when the turn reached the
// session's model provider and the provider refused the request itself
// (an invalid_request_error, an authentication_error): the session is at
// fault, not the message. The daemon counts it toward nothing a message
// owns; the same refusal on BrokenAfter turns in a row marks the session
// broken (the finding of 2026-10-04: a friend's session refused every turn
// for two hours and nothing said so).
type ProviderRefused struct{ Session, Reason string }

func (p ProviderRefused) Error() string {
	return "the provider refused the turn in session " + p.Session + ": " + p.Reason
}

// providerErrorType is a provider's JSON error, the shape OpenAI- and
// Anthropic-style APIs print and harnesses pass on: "type":"<x>_error".
var (
	providerErrorType    = regexp.MustCompile(`"type"\s*:\s*"([a-z_]+_error)"`)
	providerErrorMessage = regexp.MustCompile(`"message"\s*:\s*"([^"]{0,200})`)
)

// transientProviderErrors are provider errors that pass by themselves: a
// rate limit, an overload, the provider's own fault. They are failures of
// a turn, never a refusal of the session.
var transientProviderErrors = map[string]bool{"rate_limit_error": true, "overloaded_error": true, "api_error": true}

// ProviderRefusal reads a failed turn's output for a provider's refusal:
// the error type and the head of its message, one line; ok is false when
// the output carries none, or only a transient one.
func ProviderRefusal(out string) (reason string, ok bool) {
	m := providerErrorType.FindStringSubmatch(out)
	if m == nil || transientProviderErrors[m[1]] {
		return "", false
	}
	reason = m[1]
	if msg := providerErrorMessage.FindStringSubmatch(out); msg != nil {
		reason += ": " + strings.Join(strings.Fields(msg[1]), " ")
	}
	return reason, true
}

// refused is the answer of an adapter whose turn exited nonzero: the
// provider's refusal when the output carries one, else the exit as it was.
func refused(session string, out string, exit int, err error) (int, error) {
	if exit != 0 && err == nil {
		if reason, ok := ProviderRefusal(out); ok {
			return exit, ProviderRefused{Session: session, Reason: reason}
		}
	}
	return exit, err
}

// KillDelay is how long a signalled group gets to end before SIGKILL.
const KillDelay = 5 * time.Second

// NewDeliverer is the adapter for harness, in the friend's directory, into
// session (empty: the newest session of that directory where the harness
// can name one). An unknown harness is refused with the names there are.
func NewDeliverer(harness, dir, session string, run Exec, out io.Writer) (Deliverer, error) {
	switch harness {
	case "opencode":
		return &OpenCode{Dir: dir, Session: session, Run: run, Out: out, Look: realOpenCodeLook}, nil
	case "codex":
		return &Codex{Dir: dir, Session: session, Run: run, Out: out}, nil
	case "grok":
		return &Grok{Dir: dir, Wake: session, Run: run, Out: out}, nil
	case "antigravity":
		return &Antigravity{Dir: dir, Session: session, Run: run, Out: out}, nil
	case "claude":
		return Stub{Harness: harness}, nil
	case "dsh":
		return &DSH{Dir: dir, Session: session, Run: run, Out: out}, nil
	case "gemini":
		return &Gemini{Dir: dir, Session: session, Run: run, Out: out}, nil
	}
	if reason, ok := Refusals[harness]; ok {
		return Stub{Harness: harness, Reason: reason}, nil
	}
	return nil, fmt.Errorf("%q is no harness; the harnesses are %s", harness, strings.Join(Harnesses, ", "))
}

// OpenCodeBusLine is the blocking read the open session runs while a normal
// TUI holds the directory. It is a command inside the session, not a flag
// at app start (docs/SPEC-FRIEND.md, OpenCode).
const OpenCodeBusLine = "nova-bus wait --as <friend>"

// OpenCode delivers into the open TUI when that TUI has a server, and
// otherwise defers while a TUI holds Dir. A normal TUI (no --port, no
// --hostname, no mDNS) does not listen: its client URL is the in-process
// http://opencode.internal, so `opencode run --attach` has nowhere to go
// (measured opencode 1.18.30, packages/opencode/src/cli/cmd/tui.ts). Look
// reports that, from the process table. A listened address, an `attach <url>`
// argv or a `--port <n>` argv is `opencode run --attach <url> --session <id>`.
// No TUI is the headless `opencode run --session <id> --dir <dir> <text>`,
// the newest session of Dir when none is named, and the record says that run
// is not the open chat. Nil Look skips the probe, so a test that only scripts
// Run keeps the headless command.
type OpenCode struct {
	Dir, Session string
	Run          Exec
	Program      string    // "opencode" when empty
	Out          io.Writer // where the turn's output goes, when set: the daemon's record
	// Look reports whether a TUI holds dir and the HTTP URL it is listening
	// on (empty when it holds the directory and listens on nothing). Nil
	// skips the probe. NewDeliverer sets the process-table probe.
	Look func(ctx context.Context, dir string) (hold bool, url string, err error)
	// Allow is every other path the friend's directory is reached by (a
	// symlink in the home directory): with Dir and its real path, allowed in
	// the project config before a turn (AllowDirs), so a headless run never
	// auto-rejects a tool call there. Nil: the config is left alone.
	Allow []string
}

func (o *OpenCode) program() string {
	if o.Program == "" {
		return "opencode"
	}
	return o.Program
}

// session is one row of `opencode session list --format json`.
type session struct {
	ID        string `json:"id"`
	Directory string `json:"directory"`
	Updated   int64  `json:"updated"`
}

// NewestSession picks the most recently updated session of dir from the
// listing's JSON.
func NewestSession(listing, dir string) (string, error) {
	var rows []session
	if err := json.Unmarshal([]byte(listing), &rows); err != nil {
		return "", fmt.Errorf("opencode session list: not a JSON list: %v", err)
	}
	best := session{}
	for _, r := range rows {
		if r.Directory == dir && r.Updated > best.Updated {
			best = r
		}
	}
	if best.ID == "" {
		return "", fmt.Errorf("no opencode session for %s; start one there, or name one with --session", dir)
	}
	return best.ID, nil
}

func (o *OpenCode) Deliver(ctx context.Context, text string) (int, error) {
	hold, url, err := o.hold(ctx)
	if err != nil {
		return 0, Deferred{Reason: o.deferReason("the process table could not be read: " + err.Error())}
	}
	if hold && url == "" {
		return 0, Deferred{Reason: o.deferReason("")}
	}
	if o.Allow != nil {
		o.allow()
	}
	id := o.Session
	if id == "" {
		listing, exit, err := o.Run(ctx, o.Dir, o.program(), []string{"session", "list", "--format", "json"}, "")
		if err != nil {
			return 0, fmt.Errorf("opencode session list: %w", err)
		}
		if exit != 0 {
			return 0, fmt.Errorf("opencode session list exited %d", exit)
		}
		if id, err = NewestSession(listing, o.Dir); err != nil {
			return 0, err
		}
	}
	args := []string{"run"}
	if url != "" {
		args = append(args, "--attach", url)
	}
	args = append(args, "--session", id, "--dir", o.Dir, text)
	out, exit, err := o.Run(ctx, o.Dir, o.program(), args, "")
	if o.Out != nil {
		switch {
		case url != "":
			fmt.Fprintln(o.Out, "turned: opencode run --attach "+url+" session "+id)
		case o.Look != nil:
			fmt.Fprintln(o.Out, "answered by run, not by the open chat")
		}
		if out != "" {
			fmt.Fprintln(o.Out, strings.TrimRight(Head(out, OutputKept), "\n"))
		}
	}
	return refused(id, out, exit, err)
}

// Route is what the daemon records. attach: a TUI holding Dir has an HTTP
// URL. defer: a TUI holds Dir and listens on nothing (or the table could
// not be read); line is the bus read the session runs. run: no TUI holds
// Dir, so delivery is the headless run. Nil Look is run, never a guessed
// server.
func (o *OpenCode) Route(ctx context.Context) (route, line string, err error) {
	hold, url, err := o.hold(ctx)
	switch {
	case err != nil:
		return "defer", OpenCodeBusLine, err
	case hold && url != "":
		return "attach", url, nil
	case hold:
		return "defer", OpenCodeBusLine, nil
	default:
		return "run", "opencode run --session <id> --dir <dir> <text>", nil
	}
}

func (o *OpenCode) hold(ctx context.Context) (bool, string, error) {
	if o.Look == nil {
		return false, "", nil
	}
	return o.Look(ctx, o.Dir)
}

func (o *OpenCode) deferReason(extra string) string {
	s := "the opencode TUI in " + o.Dir + " listens on nothing, so there is no server for opencode run --attach " +
		"(a normal TUI talks over in-process http://opencode.internal; it listens only when started with " +
		"--port, --hostname or --mdns, which this route does not require); the message stays pending; " +
		"in that session run: " + OpenCodeBusLine
	if extra != "" {
		return extra + "; " + s
	}
	return s
}

// openCodeNotTUI is the first argv word of an opencode process that is not
// the TUI. attach is a TUI connected to a server, so it is absent here.
var openCodeNotTUI = map[string]bool{
	"acp": true, "agent": true, "auth": true, "completion": true, "db": true,
	"debug": true, "export": true, "github": true, "import": true, "mcp": true,
	"models": true, "plug": true, "plugin": true, "pr": true, "providers": true,
	"run": true, "serve": true, "session": true, "stats": true, "uninstall": true,
	"upgrade": true, "web": true,
}

// openCodeHold is one process against dir. hold is an OpenCode TUI whose
// cwd is dir. url is the HTTP server that process is listening on, else the
// URL in its argv (`attach <url>` or `--port <n>`). A normal TUI holds the
// directory with an empty url.
func openCodeHold(command, cwd, dir string, listen []string) (hold bool, url string) {
	tui, argURL := openCodeTUI(command)
	if !tui || !sameDir(cwd, dir) {
		return false, ""
	}
	if u := urlFromListen(listen); u != "" {
		return true, u
	}
	return true, argURL
}

func openCodeTUI(command string) (tui bool, url string) {
	f := strings.Fields(command)
	if len(f) == 0 || filepath.Base(f[0]) != "opencode" {
		return false, ""
	}
	if len(f) == 1 {
		return true, ""
	}
	if f[1] == "attach" {
		if len(f) >= 3 && strings.Contains(f[2], "://") {
			return true, f[2]
		}
		return true, ""
	}
	if openCodeNotTUI[f[1]] {
		return false, ""
	}
	return true, portURL(f[1:])
}

func portURL(args []string) string {
	port, host := "", "127.0.0.1"
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--port" && i+1 < len(args):
			i++
			port = args[i]
		case strings.HasPrefix(a, "--port="):
			port = strings.TrimPrefix(a, "--port=")
		case a == "--hostname" && i+1 < len(args):
			i++
			host = args[i]
		case strings.HasPrefix(a, "--hostname="):
			host = strings.TrimPrefix(a, "--hostname=")
		}
	}
	n, err := strconv.Atoi(port)
	if err != nil || n <= 0 {
		return ""
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]", "*":
		host = "127.0.0.1"
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return "http://" + host + ":" + strconv.Itoa(n)
}

func urlFromListen(listen []string) string {
	var fallback string
	for _, raw := range listen {
		host, port, ok := splitHostPort(raw)
		if !ok {
			continue
		}
		n, err := strconv.Atoi(port)
		if err != nil || n <= 0 {
			continue
		}
		switch host {
		case "127.0.0.1", "localhost":
			return "http://127.0.0.1:" + port
		case "*", "0.0.0.0", "", "::", "[::]":
			fallback = "http://127.0.0.1:" + port
		case "::1", "[::1]":
			if fallback == "" {
				fallback = "http://[::1]:" + port
			}
		default:
			if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
				host = "[" + host + "]"
			}
			if fallback == "" {
				fallback = "http://" + host + ":" + port
			}
		}
	}
	return fallback
}

func splitHostPort(raw string) (host, port string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	if strings.HasPrefix(raw, "[") {
		host, port, ok = strings.Cut(raw, "]:")
		if !ok {
			return "", "", false
		}
		return host + "]", port, port != ""
	}
	i := strings.LastIndex(raw, ":")
	if i < 0 {
		return "", "", false
	}
	return raw[:i], raw[i+1:], raw[i+1:] != ""
}

func sameDir(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b {
		return true
	}
	ar, ea := filepath.EvalSymlinks(a)
	br, eb := filepath.EvalSymlinks(b)
	return ea == nil && eb == nil && filepath.Clean(ar) == filepath.Clean(br)
}

func realOpenCodeLook(ctx context.Context, dir string) (bool, string, error) {
	listing, exit, err := commandResult(ctx, "ps", "-axww", "-o", "pid=,command=")
	if err != nil {
		return false, "", err
	}
	if exit != 0 {
		return false, "", fmt.Errorf("ps exited %d", exit)
	}
	held, url := false, ""
	for _, p := range parsePS(listing) {
		tui, _ := openCodeTUI(p.command)
		if !tui {
			continue
		}
		cwd, err := lsofCwd(ctx, p.pid)
		if err != nil {
			return false, "", err
		}
		listen, err := lsofListen(ctx, p.pid)
		if err != nil {
			return false, "", err
		}
		h, u := openCodeHold(p.command, cwd, dir, listen)
		if !h {
			continue
		}
		held = true
		if u != "" && url == "" {
			url = u
		}
	}
	return held, url, nil
}

type psProc struct {
	pid     int
	command string
}

func parsePS(listing string) []psProc {
	var out []psProc
	for _, line := range strings.Split(listing, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pidS, cmd, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		pid, err := strconv.Atoi(pidS)
		if err != nil {
			continue
		}
		out = append(out, psProc{pid: pid, command: strings.TrimSpace(cmd)})
	}
	return out
}

func lsofCwd(ctx context.Context, pid int) (string, error) {
	out, exit, err := commandResult(ctx, "lsof", "-nP", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-F", "n")
	if err != nil {
		return "", err
	}
	if exit != 0 {
		return "", fmt.Errorf("lsof cwd for %d exited %d", pid, exit)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "n") && len(line) > 1 {
			return line[1:], nil
		}
	}
	return "", fmt.Errorf("lsof cwd for %d named no directory", pid)
}

func lsofListen(ctx context.Context, pid int) ([]string, error) {
	out, exit, err := commandResult(ctx, "lsof", "-nP", "-a", "-p", strconv.Itoa(pid), "-iTCP", "-sTCP:LISTEN", "-F", "n")
	if err != nil {
		return nil, err
	}
	if exit != 0 {
		if strings.TrimSpace(out) == "" {
			return nil, nil // nothing is listening
		}
		return nil, fmt.Errorf("lsof listen for %d exited %d", pid, exit)
	}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "n") && strings.Contains(line, ":") {
			names = append(names, line[1:])
		}
	}
	return names, nil
}

func commandResult(ctx context.Context, name string, args ...string) (string, int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = KillDelay
	var buf strings.Builder
	cmd.Stdout = &buf
	cmd.Stderr = io.Discard
	err := cmd.Run()
	if err == nil {
		return buf.String(), 0, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return buf.String(), ee.ExitCode(), nil
	}
	return "", 0, fmt.Errorf("%s: %w", name, err)
}

// Head is the first n bytes of s, with a note when it was cut.
func Head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("\n[... %d more bytes]", len(s)-n)
}

// Stub is a harness with no deliver command yet: it refuses every delivery
// with the way a session of that harness still reads the bus, so the tool
// is honest. It is Passive: the daemon takes nothing off the stream for it
// (the session's own blocking read does), only peeks, so a ping is still
// answered by the daemon at once and the beat is real.
type Stub struct{ Harness, Reason string }

func (s Stub) Deliver(context.Context, string) (int, error) {
	if s.Reason != "" {
		return 0, fmt.Errorf("no deliver command for %s: %s; run the session's blocking read: nova-bus recv --as <friend>", s.Harness, s.Reason)
	}
	return 0, fmt.Errorf("no deliver command for %s yet; run the session's blocking read: nova-bus recv --as <friend>", s.Harness)
}

// Passive marks a Deliverer that cannot deliver: the daemon reads nothing
// for it.
func (Stub) Passive() {}

// Known says whether harness is one of Harnesses.
func Known(harness string) bool { return slices.Contains(Harnesses, harness) }
