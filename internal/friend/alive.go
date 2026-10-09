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
	"sync"
	"time"
)

// AliveEvery is how often the daemon asks the adapter whether its harness is
// alive (SPEC-FRIEND.md, the harness check): the session's last turn for a
// headless harness, the app in the process table for one with no headless
// route; an advisory fact for the status, never presence.
const AliveEvery = 30 * time.Second

// What the status says of the harness (Status.HarnessSeen): seen running, or
// not seen; HarnessUnknown when the adapter cannot tell. A harness run from
// its command line (dsh headless, codex exec, opencode run, gemini) is read by
// its session's last turn, never by an app: not seen is never down.
const HarnessNotSeen = "not-seen"

// Liveness is an adapter's answer about its harness: Known false when the
// adapter cannot tell (its friend relies on the session check alone), else
// Running; Why says what was read, and Rule which rule read it: RuleSession
// (the session's own last turn), RuleApp (a desktop app in the process
// table), or empty for another signal (a window, a tmux session, a runner on
// the path).
type Liveness struct {
	Known, Running bool
	Why            string
	Rule           string
}

// The rules a Liveness is read by, as the record and the status say them
// (alive=session|app).
const (
	RuleSession = "session"
	RuleApp     = "app"
)

// Aliver is an adapter that can say whether its harness is alive, by the
// cheapest true signal it has: for a harness with a headless program, the
// session's own last turn (SessionTurns); for one with none, its app in the
// process table.
type Aliver interface {
	Alive(ctx context.Context) Liveness
}

func running(why string) Liveness    { return Liveness{Known: true, Running: true, Why: why} }
func notRunning(why string) Liveness { return Liveness{Known: true, Why: why} }
func cannotTell(why string) Liveness { return Liveness{Why: why} }

// AliveWithin is how recent the session's last turn ending exit 0 must be for
// a headless harness to be alive: a quiet session is sent a session check
// every SessionQuiet, which has SessionBound to end, so a live session that
// gets no other turn still ends one within this.
const AliveWithin = SessionQuiet + SessionBound

// SessionTurns is a headless adapter's record of its own last turn into the
// session (a delivery, a session check, a lane's turn): when it ended, into
// which session, and how. A harness with a headless program (dsh headless,
// gemini --resume, opencode run, codex exec) is alive on its session's word:
// the last turn ended exit 0 within AliveWithin; no desktop app is read (the
// finding of 2026-10-06: zhi's deliveries through dsh headless answered in
// 4 s while the DeepSeek Harness app was closed, and the app check said
// "harness not running"). A deferred turn (nothing ran) is not a turn. The
// clock is the daemon's, set by WatchHarness; time.Now until then.
type SessionTurns struct {
	mu      sync.Mutex
	now     func() time.Time
	any     bool
	at      time.Time
	session string
	exit    int
	err     string
	running int       // turns begun and not yet ended
	began   time.Time // when the latest of them began
}

// begin is a turn's process starting; end is it ended, whatever it answered.
// Together they are the adapter's own word on whether a turn runs
// (TurnUnderWay), read by the session check instead of any lock.
func (s *SessionTurns) begin() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	s.running++
	s.began = now()
}

func (s *SessionTurns) end() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running > 0 {
		s.running--
	}
}

// TurnUnderWay is the record's word: a turn has begun and not ended, since the
// latest's start.
func (s *SessionTurns) TurnUnderWay() (bool, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running > 0, s.began
}

// sessionTurner is an adapter that keeps a SessionTurns.
type sessionTurner interface{ sessionTurns() *SessionTurns }

func (s *SessionTurns) clock(now func() time.Time) {
	s.mu.Lock()
	s.now = now
	s.mu.Unlock()
}

// saw records a turn into session that ended with exit and err; a Deferred
// that is no SessionRefused ran nothing and is not recorded.
func (s *SessionTurns) saw(session string, exit int, err error) {
	var refusedTurn SessionRefused
	var deferred Deferred
	if err != nil && !errors.As(err, &refusedTurn) && errors.As(err, &deferred) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	s.any, s.at, s.session, s.exit, s.err = true, now(), session, exit, ""
	if err != nil {
		s.err = oneLine(err.Error(), 200)
	}
}

// alive is the session rule over the record, read at its clock: the last
// turn ended exit 0 within AliveWithin is alive; one that failed, or ended
// longer ago, is not; no turn yet cannot tell.
func (s *SessionTurns) alive(harness string) Liveness {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	var l Liveness
	age := now().Sub(s.at)
	switch {
	case s.running > 0:
		l = running(fmt.Sprintf("a turn is running in the %s session since %s", harness, s.began.UTC().Format(time.RFC3339)))
	case !s.any:
		l = cannotTell("no turn into the " + harness + " session has ended yet; the session check alone")
	case s.exit != 0 || s.err != "":
		why := fmt.Sprintf("the last turn into the %s session %s ended exit=%d", harness, s.session, s.exit)
		if s.err != "" {
			why += ": " + s.err
		}
		l = notRunning(why + " (" + Ago(age) + " ago)")
	case age > AliveWithin:
		// a one-shot harness is seen only by its turns: a quiet one is no harness gone,
		// and its next delivery is the check (the finding of 2026-10-06)
		l = cannotTell(fmt.Sprintf("quiet: the last turn into the %s session %s ended exit 0 %s ago; a one-shot harness is seen by its next turn, and delivering is the check", harness, s.session, Ago(age)))
	default:
		l = running(fmt.Sprintf("the last turn into the %s session %s ended exit 0 %s ago; no app is read", harness, s.session, Ago(age)))
	}
	l.Rule = RuleSession
	return l
}

// PSArgs is the process listing every check reads: user, pid and the whole
// command line, never cut.
var PSArgs = []string{"-axww", "-o", "user=,pid=,args="}

// App is a desktop app as the process table shows it: its bundle and the
// name of its main executable (Contents/MacOS/<Name>).
type App struct{ Bundle, Name string }

// AppRunning says whether listing (ps with PSArgs) has a process of username
// that is app's main executable: its command line starts inside the bundle
// and names an executable called app.Name whose path, cleaned, is the
// bundle's Contents/MacOS/<Name>, so a launch through another spelling of the
// path (Contents/Resources/../MacOS/<Name>, the finding of 2026-10-06) is the
// app, and a helper or a longer name is not. ps keeps no argv boundaries, so
// every place the name ends (a blank, or the line's end) is tried.
func AppRunning(listing, username string, app App) bool {
	want := filepath.Join(app.Bundle, "Contents", "MacOS", app.Name)
	for line := range strings.SplitSeq(listing, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || f[0] != username {
			continue
		}
		args := strings.TrimSpace(line)
		args = strings.TrimSpace(args[len(f[0]):])
		args = strings.TrimSpace(args[len(f[1]):])
		if !strings.HasPrefix(args, app.Bundle+"/") {
			continue
		}
		for i := 0; i < len(args); {
			j := strings.Index(args[i:], "/"+app.Name)
			if j < 0 {
				break
			}
			end := i + j + 1 + len(app.Name)
			if (end == len(args) || args[end] == ' ') && filepath.Clean(args[:end]) == want {
				return true
			}
			i += j + 1
		}
	}
	return false
}

// AntigravityApp is the desktop app the Antigravity adapter delivers into on
// macOS: the one harness with no headless route, so the one app check.
var AntigravityApp = App{Bundle: "/Applications/Antigravity.app", Name: "Antigravity"}

// appAlive is whether the desktop app runs as username, from one ps through
// run. A desktop app is a macOS app: elsewhere, and when the listing or the
// user cannot be read, the adapter cannot tell.
func appAlive(ctx context.Context, run Exec, dir, goos, username string, app App) Liveness {
	l := appLook(ctx, run, dir, goos, username, app)
	l.Rule = RuleApp
	return l
}

func appLook(ctx context.Context, run Exec, dir, goos, username string, app App) Liveness {
	if goos != "darwin" {
		return cannotTell(fmt.Sprintf("the %s app is a macOS app; no app process to read on %s", app.Name, goos))
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
	if AppRunning(listing, username, app) {
		return running(fmt.Sprintf("the %s app runs (%s)", app.Name, app.Bundle))
	}
	return notRunning(fmt.Sprintf("the %s app is not running: no %s of %s in %s in the process table", app.Name, app.Name, username, app.Bundle))
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

// headlessAlive is the session rule, and before the first turn the runner's
// answer when there is a runner to look up.
func headlessAlive(t *SessionTurns, harness string, look func(string) (string, error), program string) Liveness {
	l := t.alive(harness)
	if l.Known || look == nil {
		return l
	}
	if r := runnerAlive(look, program); r.Known {
		return r
	}
	return l
}

func (c *Codex) sessionTurns() *SessionTurns  { return &c.turns }
func (d *DSH) sessionTurns() *SessionTurns    { return &d.turns }
func (g *Gemini) sessionTurns() *SessionTurns { return &g.turns }

// The headless adapters' own turn records (TurnRecord): each turn a one-shot
// process into the session.
func (d *DSH) TurnUnderWay() (bool, time.Time)    { return d.turns.TurnUnderWay() }
func (g *Gemini) TurnUnderWay() (bool, time.Time) { return g.turns.TurnUnderWay() }
func (o *OpenCode) sessionTurns() *SessionTurns   { return &o.turns }

// Alive: the session's last turn (codex exec resume, codex queue); the
// ChatGPT app is not read.
func (c *Codex) Alive(context.Context) Liveness { return c.turns.alive("codex") }

// Alive: the Antigravity app, whose language server Deliver reaches: no
// headless route, so the app check, by its bundle and its name.
func (a *Antigravity) Alive(ctx context.Context) Liveness {
	return appAlive(ctx, a.Run, a.Dir, runtime.GOOS, a.User, AntigravityApp)
}

// Alive: the session's last turn (dsh headless --session-id); the DeepSeek
// Harness app is not read: a headless friend needs no window.
func (d *DSH) Alive(context.Context) Liveness { return d.turns.alive("dsh") }

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

// Alive: a claude session is reached only through its own wait on the wake
// file, and no process says which window runs it.
func (c *ClaudeWake) Alive(context.Context) Liveness {
	return cannotTell("claude is reached by the session's own wait on " + ClaudeWakePath(c.Dir, c.Name) + "; the session check alone")
}

// Alive: the session's last turn (opencode run, a batch's or a lane's);
// before the first, a runner that cannot be found is not running.
func (o *OpenCode) Alive(context.Context) Liveness {
	return headlessAlive(&o.turns, "opencode", exec.LookPath, o.program())
}

// Alive: the session's last turn (gemini --resume); before the first, a
// runner that cannot be found is not running.
func (g *Gemini) Alive(context.Context) Liveness {
	program := g.Program
	if program == "" {
		program = "gemini"
	}
	return headlessAlive(&g.turns, "gemini", exec.LookPath, program)
}

// Alive: a stub delivers nothing and watches nothing.
func (s Stub) Alive(context.Context) Liveness {
	return cannotTell(s.Harness + " has no adapter that reads its process; the session check alone")
}

// HarnessWatch is the harness check beside the daemon's beat (SPEC-FRIEND.md,
// the harness check). Every Every (AliveEvery when zero) it asks Alive and
// keeps the answer on the daemon's status (Status.HarnessSeen), saying each
// change on the record. It is advisory: it never holds the beat back and never
// makes the friend down. Presence is the session's (SessionCheck): the finding
// of 2026-10-05 was a friend run from the dsh command line, answering every
// session check for three hours, held down because no app was in the process
// table. An adapter that cannot tell changes nothing. The model is app in
// tla/FriendPresence.tla, read by no rule (SessionShownUp; the witness
// appholds is this finding).
type HarnessWatch struct {
	Alive Aliver
	Every time.Duration
	Now   func() time.Time

	mu      sync.Mutex
	d       *Daemon
	beat    func(ctx context.Context, active time.Time) error
	checked time.Time
	live    Liveness
	said    string // the seen state and rule the record last said: each change is said once
	told    bool
}

// WatchHarness puts a HarnessWatch over adapter beside d's beat; call it
// once d is built and before Run. Pass the bare adapter, the one
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
	if t, ok := adapter.(sessionTurner); ok {
		t.sessionTurns().clock(func() time.Time { return w.Now() }) // the session's turns on the daemon's clock
	}
	d.Beat = w.Beat
	d.HarnessStatus = w.Status
	return w
}

// Status returns the last advisory observation without sharing the daemon's
// mutable Status with the independent beat worker.
func (w *HarnessWatch) Status() (string, string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return seen(w.live), w.live.Rule
}

func seen(l Liveness) string {
	switch {
	case !l.Known:
		return HarnessUnknown
	case l.Running:
		return HarnessRunning
	}
	return HarnessNotSeen
}

// Beat is the daemon's beat, with the harness check beside it: it runs the
// check when one is due, then beats, whatever the check read.
func (w *HarnessWatch) Beat(ctx context.Context, active time.Time) error {
	now := w.Now()
	every := w.Every
	if every <= 0 {
		every = AliveEvery
	}
	if w.Alive != nil && (w.checked.IsZero() || now.Sub(w.checked) >= every) {
		w.checked = now
		live := w.Alive.Alive(ctx)
		w.mu.Lock()
		w.live = live
		w.mu.Unlock()
		if said := seen(live) + " " + live.Rule; !w.told || said != w.said {
			w.told, w.said = true, said
			w.record(now, seenLine(live))
		}
	}
	return w.beat(ctx, active)
}

// seenLine is the record's line for what the check read, with the rule that
// read it (alive=session|app) when there is one.
func seenLine(l Liveness) string {
	rule := ""
	if l.Rule != "" {
		rule = "alive=" + l.Rule + " "
	}
	switch seen(l) {
	case HarnessRunning:
		return "harness: running: " + rule + l.Why
	case HarnessNotSeen:
		return "harness: not seen: " + rule + l.Why + "; advisory: presence is the session's answer"
	}
	return "harness check: cannot tell: " + rule + l.Why
}

func (w *HarnessWatch) record(now time.Time, line string) {
	if w.d.Record != nil {
		w.d.Record(now.UTC().Format(time.RFC3339) + " " + line)
	}
}
