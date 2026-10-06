package friend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// AliveEvery is how often the daemon asks the adapter whether its harness
// process is in the table (SPEC-FRIEND.md, the harness check). The answer is
// advisory: it never holds the beat and never marks the friend down.
const AliveEvery = 30 * time.Second

// HarnessNotRunning is the old beat error. The check no longer returns it
// and no longer marks the friend down; status says harness=not-seen instead.
const HarnessNotRunning = "harness not running"

// HarnessNotSeen is the advisory word when Alive did not see a harness
// process. It decides nothing: the session's answer does.
const HarnessNotSeen = "not-seen"

// Liveness is an adapter's answer about its harness: Known false when the
// adapter cannot tell (its friend relies on the session check alone), else
// Running; Why says what was read.
type Liveness struct {
	Known, Running bool
	Why            string
}

// Aliver is an adapter that can say whether its harness's app or process is
// running, by the cheapest true signal it has: the process table.
type Aliver interface {
	Alive(ctx context.Context) Liveness
}

func running(why string) Liveness    { return Liveness{Known: true, Running: true, Why: why} }
func notRunning(why string) Liveness { return Liveness{Known: true, Why: why} }
func cannotTell(why string) Liveness { return Liveness{Why: why} }

// PSArgs is the process listing every check reads: user, pid and the whole
// command line, never cut.
var PSArgs = []string{"-axww", "-o", "user=,pid=,args="}

// AppRunning says whether listing (ps with PSArgs) has a process of username
// whose executable is exe: the command line is exe, or exe and its
// arguments. ps keeps no argv boundaries, so the path is matched whole,
// spaces and all.
func AppRunning(listing, username, exe string) bool {
	for line := range strings.SplitSeq(listing, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || f[0] != username {
			continue
		}
		args := strings.TrimSpace(line)
		args = strings.TrimSpace(args[len(f[0]):])
		args = strings.TrimSpace(args[len(f[1]):])
		if args == exe || strings.HasPrefix(args, exe+" ") {
			return true
		}
	}
	return false
}

// The executables of the desktop apps the adapters deliver into, on macOS:
// the app's main process, never a helper.
const (
	CodexApp       = "/Applications/ChatGPT.app/Contents/MacOS/ChatGPT"
	AntigravityApp = "/Applications/Antigravity.app/Contents/MacOS/Antigravity"
	DSHApp         = "/Applications/DeepSeek Harness.app/Contents/MacOS/DeepSeek Harness"
)

// appAlive is whether the desktop app name, at exe, runs as username, from
// one ps through run. A desktop app is a macOS app: elsewhere, and when the
// listing or the user cannot be read, the adapter cannot tell.
func appAlive(ctx context.Context, run Exec, dir, goos, username, name, exe string) Liveness {
	if goos != "darwin" {
		return cannotTell(fmt.Sprintf("the %s app is a macOS app; no app process to read on %s", name, goos))
	}
	if username == "" {
		u, err := user.Current()
		if err != nil {
			return cannotTell("current user: " + err.Error())
		}
		username = u.Username
	}
	if run == nil {
		return cannotTell("ps: no process listing")
	}
	listing, exit, err := run(ctx, dir, "ps", PSArgs, "")
	if err != nil || exit != 0 {
		return cannotTell(fmt.Sprintf("ps: exit=%d error=%v", exit, err))
	}
	if AppRunning(listing, username, exe) {
		return running(fmt.Sprintf("the %s app runs (%s)", name, exe))
	}
	return notRunning(fmt.Sprintf("the %s app is not running: no %s of %s in the process table", name, exe, username))
}

// CLIRunning says whether listing has a process of username whose command is
// program (the executable's base name) with mark as one argument: dsh
// headless, codex exec, claude -p. ps keeps no argv boundaries, so the
// executable is the first field of the command and mark is a later field.
func CLIRunning(listing, username, program, mark string) bool {
	for line := range strings.SplitSeq(listing, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || f[0] != username {
			continue
		}
		if filepath.Base(f[2]) != program {
			continue
		}
		for _, arg := range f[3:] {
			if arg == mark {
				return true
			}
		}
	}
	return false
}

// appOrCLI is the advisory liveness of a harness that is either a desktop
// app or a command-line session (program mark). A process of either is
// running. Seeing neither is not running, which status says as not-seen
// and which never marks the friend down. A listing that cannot be read
// cannot tell.
func appOrCLI(ctx context.Context, run Exec, dir, goos, username, appName, exe, program, mark string) Liveness {
	if username == "" {
		u, err := user.Current()
		if err != nil {
			return cannotTell("current user: " + err.Error())
		}
		username = u.Username
	}
	if run == nil {
		return cannotTell("ps: no process listing")
	}
	listing, exit, err := run(ctx, dir, "ps", PSArgs, "")
	if err != nil || exit != 0 {
		return cannotTell(fmt.Sprintf("ps: exit=%d error=%v", exit, err))
	}
	if goos == "darwin" && AppRunning(listing, username, exe) {
		return running(fmt.Sprintf("the %s app runs (%s)", appName, exe))
	}
	if CLIRunning(listing, username, program, mark) {
		return running(fmt.Sprintf("%s %s runs", program, mark))
	}
	if goos != "darwin" {
		return notRunning(fmt.Sprintf("no %s %s process, and the %s app is a macOS app", program, mark, appName))
	}
	return notRunning(fmt.Sprintf("no %s app and no %s %s process of %s", appName, program, mark, username))
}

// cliSessionAlive is the advisory liveness of a harness that is only a
// command-line session (claude -p). No process listing cannot tell.
func cliSessionAlive(ctx context.Context, run Exec, dir, username, program, mark string) Liveness {
	if username == "" {
		u, err := user.Current()
		if err != nil {
			return cannotTell("current user: " + err.Error())
		}
		username = u.Username
	}
	if run == nil {
		return cannotTell("ps: no process listing")
	}
	listing, exit, err := run(ctx, dir, "ps", PSArgs, "")
	if err != nil || exit != 0 {
		return cannotTell(fmt.Sprintf("ps: exit=%d error=%v", exit, err))
	}
	if CLIRunning(listing, username, program, mark) {
		return running(fmt.Sprintf("%s %s runs", program, mark))
	}
	return notRunning(fmt.Sprintf("no %s %s process of %s", program, mark, username))
}

// runnerAlive is the answer of a harness that runs no standing process, one
// whose every turn is a fresh run of program (opencode run, gemini
// --resume): a program that cannot be found means no turn can run, and the
// harness is not running; one that is found says nothing about the session.
func runnerAlive(look func(string) (string, error), program string) Liveness {
	path, err := look(program)
	if err != nil {
		return notRunning(fmt.Sprintf("the runner %s is not found: %v", program, err))
	}
	return cannotTell(fmt.Sprintf("%s runs no standing process (each turn starts %s); the session check alone", program, path))
}

// Alive: the ChatGPT app, or a `codex exec` process. A command-line session
// is a first-class session: the app is not required.
func (c *Codex) Alive(ctx context.Context) Liveness {
	return appOrCLI(ctx, c.Run, c.Dir, runtime.GOOS, "", "ChatGPT", CodexApp, "codex", "exec")
}

// Alive: the Antigravity app, whose language server Deliver reaches.
func (a *Antigravity) Alive(ctx context.Context) Liveness {
	return appAlive(ctx, a.Run, a.Dir, runtime.GOOS, a.User, "Antigravity", AntigravityApp)
}

// Alive: the DeepSeek Harness app, or a `dsh headless` process. A command-line
// session is a first-class session: the app is not required.
func (d *DSH) Alive(ctx context.Context) Liveness {
	return appOrCLI(ctx, d.Run, d.Dir, runtime.GOOS, "", "DeepSeek Harness", DSHApp, "dsh", "headless")
}

// Alive: a grok window (the TUI process) open in Dir: a pid of the
// harness's active_sessions.json with Dir as its cwd, in the process table.
// A window with no monitor is still running; delivery defers on its own.
func (g *Grok) Alive(ctx context.Context) Liveness {
	home, err := g.home()
	if err != nil {
		return cannotTell("grok home: " + err.Error())
	}
	active, err := os.ReadFile(filepath.Join(home, "active_sessions.json"))
	if errors.Is(err, os.ErrNotExist) {
		return notRunning("no grok window is open: " + err.Error())
	}
	if err != nil {
		return cannotTell("active_sessions.json: " + err.Error())
	}
	if g.Run == nil {
		return cannotTell("ps: no process listing")
	}
	listing, exit, err := g.Run(ctx, g.Dir, "ps", []string{"-axww", "-o", "pid=,ppid=,args="}, "")
	if err != nil || exit != 0 {
		return cannotTell(fmt.Sprintf("ps: exit=%d error=%v", exit, err))
	}
	dirs := []string{g.Dir}
	if real, e := filepath.EvalSymlinks(g.Dir); e == nil && real != g.Dir {
		dirs = append(dirs, real)
	}
	for _, dir := range dirs {
		_, err := WakeOf(string(active), listing, dir, g.Wake)
		var nm noMonitorError
		if err == nil || errors.As(err, &nm) {
			return running("a grok window is open in " + dir)
		}
		if !errors.Is(err, ErrNoSession) {
			return cannotTell(err.Error())
		}
	}
	return notRunning("no grok window is open in " + g.Dir + " (no live pid of active_sessions.json with that cwd)")
}

// Alive: the runner, opencode, which every turn and every lane starts
// afresh.
func (o *OpenCode) Alive(context.Context) Liveness { return runnerAlive(exec.LookPath, o.program()) }

// Alive: the runner, gemini, which every turn starts afresh.
func (g *Gemini) Alive(context.Context) Liveness {
	program := g.Program
	if program == "" {
		program = "gemini"
	}
	return runnerAlive(exec.LookPath, program)
}

// Alive: a stub delivers nothing. Claude is the exception that can be seen:
// a `claude -p` process is a first-class session. With no runner, or for any
// other stub, the adapter cannot tell.
func (s Stub) Alive(ctx context.Context) Liveness {
	if s.Harness == "claude" && s.Run != nil {
		return cliSessionAlive(ctx, s.Run, s.Dir, "", "claude", "-p")
	}
	return cannotTell(s.Harness + " has no adapter that reads its process; the session check alone")
}

// HarnessWatch is the harness check beside the daemon's beat
// (SPEC-FRIEND.md, the harness check). Every Every (AliveEvery when zero)
// it asks Alive and records the advisory word, harness=running or
// harness=not-seen. It never holds the beat and never marks the friend
// down: a session that answered its check, or sent a bus message, within
// the window is up whatever process the table shows. An adapter that
// cannot tell is not-seen, which decides nothing.
type HarnessWatch struct {
	Alive Aliver
	Every time.Duration
	Now   func() time.Time

	d       *Daemon
	beat    func(ctx context.Context, active time.Time) error
	checked time.Time
	live    Liveness
}

// WatchHarness puts a HarnessWatch over adapter in front of d's beat; call
// it once d is built and before Run. Pass the bare adapter, the one
// NewDeliverer returned: the gates in front of it (SessionCheck.Gate,
// Limits.Gate) are Deliverers that answer no Alive. A nil adapter is d's
// Deliver. Alive is optional by assertion: an adapter that is no Aliver
// cannot tell.
func WatchHarness(d *Daemon, adapter Deliverer) *HarnessWatch {
	w := &HarnessWatch{d: d, beat: d.Beat, Now: d.Now}
	if adapter == nil {
		adapter = d.Deliver
	}
	if a, ok := adapter.(Aliver); ok {
		w.Alive = a
	}
	d.Beat = w.Beat
	return w
}

// Down reports that the watch does not hold the friend down. The second
// result is what Alive last read, for a caller that still asks.
func (w *HarnessWatch) Down() (bool, string) { return false, w.live.Why }

// Beat is the daemon's beat behind the advisory check. The check never
// holds the beat and never marks the friend down.
func (w *HarnessWatch) Beat(ctx context.Context, active time.Time) error {
	now := w.Now()
	every := w.Every
	if every <= 0 {
		every = AliveEvery
	}
	if w.Alive != nil && (w.checked.IsZero() || now.Sub(w.checked) >= every) {
		w.checked = now
		w.live = w.Alive.Alive(ctx)
		seen := HarnessNotSeen
		if w.live.Known && w.live.Running {
			seen = HarnessRunning
		}
		if w.d != nil && w.d.status.Seen != seen {
			w.d.status.Seen = seen
			w.record(now, "harness="+seen+": "+w.live.Why)
		} else if w.d != nil {
			w.d.status.Seen = seen
		}
	}
	return w.beat(ctx, active)
}

func (w *HarnessWatch) record(now time.Time, line string) {
	if w.d.Record != nil {
		w.d.Record(now.UTC().Format(time.RFC3339) + " " + line)
	}
}
