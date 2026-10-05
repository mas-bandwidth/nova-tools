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

// AliveEvery is how often the daemon asks the adapter whether its harness is
// running (SPEC-FRIEND.md, the harness check). With the sprint's fifteen
// seconds without a beat, a harness that closes makes its friend down within
// three quarters of a minute.
const AliveEvery = 30 * time.Second

// HarnessNotRunning is the reason a friend is down while its harness is not
// running: the beat's error, so status says it.
const HarnessNotRunning = "harness not running"

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

// Alive: the ChatGPT app, whose Codex chat the friend sits in.
func (c *Codex) Alive(ctx context.Context) Liveness {
	return appAlive(ctx, c.Run, c.Dir, runtime.GOOS, "", "ChatGPT", CodexApp)
}

// Alive: the Antigravity app, whose language server Deliver reaches.
func (a *Antigravity) Alive(ctx context.Context) Liveness {
	return appAlive(ctx, a.Run, a.Dir, runtime.GOOS, a.User, "Antigravity", AntigravityApp)
}

// Alive: the DeepSeek Harness app, the friend's window onto the session.
func (d *DSH) Alive(ctx context.Context) Liveness {
	return appAlive(ctx, d.Run, d.Dir, runtime.GOOS, "", "DeepSeek Harness", DSHApp)
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

// Alive: a stub delivers nothing and watches nothing.
func (s Stub) Alive(context.Context) Liveness {
	return cannotTell(s.Harness + " has no adapter that reads its process; the session check alone")
}

// HarnessWatch is the harness check in front of the daemon's beat
// (SPEC-FRIEND.md, the harness check). Every Every (AliveEvery when zero)
// it asks Alive; a harness not running makes the friend down at once: no
// beat goes to the sprint server, whose friend is down after fifteen seconds
// without one, and the beat's error, so the status, says HarnessNotRunning.
// This is independent of the session check. The friend is up again only
// once the harness runs (or cannot be told) and the session has answered
// the daemon's current nonce, one it had not answered when the harness
// closed. An adapter that cannot tell changes nothing.
type HarnessWatch struct {
	Alive Aliver
	Every time.Duration
	Now   func() time.Time

	d        *Daemon
	beat     func(ctx context.Context, active time.Time) error
	checked  time.Time
	live     Liveness
	down     bool
	before   string // the nonce the pong file held when the harness closed
	saidOnce bool
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

// Down says whether the watch holds the friend down, and why.
func (w *HarnessWatch) Down() (bool, string) { return w.down, w.live.Why }

// Beat is the daemon's beat behind the check.
func (w *HarnessWatch) Beat(ctx context.Context, active time.Time) error {
	now := w.Now()
	every := w.Every
	if every <= 0 {
		every = AliveEvery
	}
	if w.Alive != nil && (w.checked.IsZero() || now.Sub(w.checked) >= every) {
		w.checked = now
		w.live = w.Alive.Alive(ctx)
		switch {
		case w.live.Known && !w.live.Running && !w.down:
			w.down, w.before = true, w.pongNonce()
			w.record(now, "down: "+HarnessNotRunning+": "+w.live.Why+"; up again when the session answers its next nonce")
		case !w.live.Known && !w.saidOnce:
			w.saidOnce = true
			w.record(now, "harness check: cannot tell: "+w.live.Why)
		}
	}
	if w.down && !(w.live.Known && !w.live.Running) {
		if n := w.pongNonce(); n != "" && n != w.before && w.d.m != nil && n == w.d.m.Nonce {
			w.down = false
			w.record(now, "up: the harness runs and the session answered nonce "+n)
		}
	}
	if w.down {
		return errors.New(HarnessNotRunning)
	}
	return w.beat(ctx, active)
}

// pongNonce is the nonce the session last answered, from the pong file.
func (w *HarnessWatch) pongNonce() string {
	if w.d.Pong == nil {
		return ""
	}
	p, found, err := w.d.Pong()
	if err != nil || !found {
		return ""
	}
	return p.Nonce
}

func (w *HarnessWatch) record(now time.Time, line string) {
	if w.d.Record != nil {
		w.d.Record(now.UTC().Format(time.RFC3339) + " " + line)
	}
}
