package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The adopt window (docs/SPEC-SPRINT.md, "The adopt window"). The seat's
// adoption stops its old agents (the server loop, the member, seat-push,
// disk-guard: every agent it booted out) and waits for them to be gone before
// it migrates anything. On 2026-10-07 at 7:42 PM the window waited on every
// process that ran the seat's binary instead, and was refused by the live
// dashboard's one-second `where --json` poll (a link to the same binary): one
// is always in flight, so that window could never close. Earlier the same day
// the seat's own `inbox --wait` held it the same way.
//
// The window now waits only on the pids launchd held for the agents it
// stopped. Any other process of the binary (a verb: where, card, inbox,
// finish) is listed as OTHER and never refuses: the install replaces the
// binary by rename, so a running process keeps its own inode and the new one
// is what the next exec runs. The decision is pure over a process table and a
// clock, so it is tested without a real process or real time.

// The adopt window verb: the seat play calls it after it booted out the
// agents an adoption stops, with those agents' launchd labels and pids, so the
// window waits on them alone. A verb process of the binary (the live
// dashboard's one-second `where --json` poll, a seat's `inbox --wait`) is
// listed as OTHER and never refuses: the install replaces the binary by
// rename, so a running process keeps its own inode.
func init() {
	notServed = append(notServed, "adopt window")
	verbClasses["adopt window"] = classRead
	verbExit["adopt window"] = "exit codes: 0 every stopped agent is gone (WINDOW OK), 1 one still runs at the bound (WINDOW REFUSED, naming each by pid and label) or the process table does not read, 2 usage"
	verbEffect["adopt window"] = "inspection: reads the host's process table (ps -A -o pid=,args=) and waits, writing nothing; it never touches the store"
}

// Window defaults: the bound a stopped agent is given to exit, and how often
// the process table is read while it is waited for.
const (
	DefaultAdoptWindow     = 60 * time.Second
	DefaultAdoptWindowPoll = time.Second
)

// WindowAgent is one agent the adoption stopped: its launchd label and the pid
// launchd held for it when it was booted out (0: none ran, an interval agent
// between runs, so nothing is waited for).
type WindowAgent struct {
	Label string `json:"label"`
	PID   int    `json:"pid"`
}

// WindowProc is one process of the host's table: its pid, the executable its
// first argument resolves to (links followed), and its arguments.
type WindowProc struct {
	PID  int    `json:"pid"`
	Path string `json:"path"`
	Args string `json:"args"`
}

// AdoptWindow is one wait: the agents stopped, the binary whose other
// processes are listed, the bound, and the process table and clock it reads
// (ps and the wall clock in production, fakes in a test).
type AdoptWindow struct {
	Stopped []WindowAgent
	// Binary is the resolved path of the seat's binary: a process running it
	// that is not a stopped agent is an OTHER. "" lists none.
	Binary string
	// Bound is how long the stopped agents are given (DefaultAdoptWindow when
	// zero); Poll is how often the table is read (DefaultAdoptWindowPoll).
	Bound, Poll time.Duration
	Procs       func(ctx context.Context) ([]WindowProc, error)
	Now         func() time.Time
	Sleep       func(ctx context.Context, d time.Duration) error
}

// WindowResult is how a wait ended: OK when every stopped agent is gone,
// Running the stopped agents still running at the bound, Others the other
// processes of the binary on the last read, and Lines what the adoption says.
type WindowResult struct {
	OK      bool
	Waited  time.Duration
	Stopped int
	Running []WindowAgent
	Others  []WindowProc
	Lines   []string
}

func (w AdoptWindow) bounds() (bound, poll time.Duration) {
	bound, poll = w.Bound, w.Poll
	if bound <= 0 {
		bound = DefaultAdoptWindow
	}
	if poll <= 0 {
		poll = DefaultAdoptWindowPoll
	}
	return bound, poll
}

func (w AdoptWindow) now() time.Time {
	if w.Now == nil {
		return time.Now()
	}
	return w.Now()
}

func (w AdoptWindow) sleep(ctx context.Context, d time.Duration) error {
	if w.Sleep != nil {
		return w.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// split is one read of the table: the stopped agents whose pid still runs,
// and every other process of the binary, by pid.
func (w AdoptWindow) split(procs []WindowProc) (running []WindowAgent, others []WindowProc) {
	stopped := map[int]bool{}
	for _, a := range w.Stopped {
		if a.PID > 0 {
			stopped[a.PID] = true
		}
	}
	live := map[int]bool{}
	for _, p := range procs {
		live[p.PID] = true
		if !stopped[p.PID] && w.Binary != "" && p.Path == w.Binary {
			others = append(others, p)
		}
	}
	for _, a := range w.Stopped {
		if a.PID > 0 && live[a.PID] {
			running = append(running, a)
		}
	}
	slices.SortFunc(others, func(a, b WindowProc) int { return a.PID - b.PID })
	return running, others
}

// Wait reads the table until every stopped agent's pid is gone, or the bound
// passes with some still running. Neither is an error: the result says which,
// and its last line is WINDOW OK or WINDOW REFUSED. The error is only a table
// that does not read, or the context ending.
func (w AdoptWindow) Wait(ctx context.Context) (WindowResult, error) {
	bound, poll := w.bounds()
	r := WindowResult{Stopped: len(w.Stopped)}
	if w.Procs == nil {
		return r, fmt.Errorf("the window has no process table to read")
	}
	start := w.now()
	for {
		procs, err := w.Procs(ctx)
		if err != nil {
			return r, fmt.Errorf("the process table does not read: %w", err)
		}
		r.Running, r.Others = w.split(procs)
		r.Waited = w.now().Sub(start)
		if len(r.Running) == 0 || r.Waited >= bound {
			r.OK = len(r.Running) == 0
			r.Lines = windowLines(r, bound)
			return r, nil
		}
		if err := w.sleep(ctx, min(poll, bound-r.Waited)); err != nil {
			return r, err
		}
	}
}

// windowLines is the result as the adoption prints it: one OTHER line per
// other process of the binary, then WINDOW OK or the refusal, which names
// each stopped agent still running by pid and label and nothing else.
func windowLines(r WindowResult, bound time.Duration) []string {
	var out []string
	for _, o := range r.Others {
		out = append(out, fmt.Sprintf("OTHER %d %s", o.PID, o.Args))
	}
	if r.OK {
		return append(out, fmt.Sprintf("WINDOW OK stopped=%d waited=%ds others=%d", r.Stopped, wholeSeconds(r.Waited), len(r.Others)))
	}
	still := make([]string, 0, len(r.Running))
	for _, a := range r.Running {
		still = append(still, fmt.Sprintf("%d %s", a.PID, a.Label))
	}
	return append(out, fmt.Sprintf("WINDOW REFUSED after %ds: these stopped agents still run: %s", wholeSeconds(bound), strings.Join(still, ", ")))
}

func wholeSeconds(d time.Duration) int64 { return int64(d.Round(time.Second) / time.Second) }

// windowProcs is ps's every process with the executable its first argument
// resolves to (a bare name found on PATH, links followed), this one aside:
// the adopt window's process table. The dashboard's nova-sprint-int2 resolves
// to the installed nova-sprint, so it is seen as the binary it runs, and
// listed as an OTHER, never waited on.
func windowProcs(run adoptRunner) func(ctx context.Context) ([]WindowProc, error) {
	return func(ctx context.Context) ([]WindowProc, error) {
		text, err := run(ctx, "ps", "-A", "-o", "pid=,args=")
		if err != nil {
			return nil, err
		}
		return windowProcsOf(text, os.Getpid()), nil
	}
}

// windowProcsOf is ps's text as the window's table, the pid self aside.
func windowProcsOf(text string, self int) []WindowProc {
	var out []WindowProc
	for _, l := range strings.Split(text, "\n") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		pid, err := strconv.Atoi(f[0])
		if err != nil || pid == self {
			continue
		}
		out = append(out, WindowProc{PID: pid, Path: resolvedExe(f[1]), Args: strings.Join(f[1:], " ")})
	}
	return out
}

// resolvedExe is the file a first argument runs: a bare name looked up on
// PATH, then every link followed; as it was when neither resolves.
func resolvedExe(arg0 string) string {
	path := arg0
	if !strings.Contains(path, "/") {
		if found, err := exec.LookPath(path); err == nil {
			path = found
		}
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return path
}

// windowAgents reads --stopped <label>=<pid>, each agent the adoption booted
// out with the pid launchd held for it (0: none ran).
func windowAgents(flags []string) ([]WindowAgent, error) {
	var out []WindowAgent
	for _, v := range flags {
		label, pid, ok := strings.Cut(v, "=")
		n, err := strconv.Atoi(pid)
		if !ok || strings.TrimSpace(label) == "" || err != nil || n < 0 {
			return nil, fmt.Errorf("--stopped wants <label>=<pid>, found %q", v)
		}
		out = append(out, WindowAgent{Label: label, PID: n})
	}
	return out, nil
}

// windowProcsFor is a test's process table for one app (*app to
// func(context.Context) ([]WindowProc, error)).
var windowProcsFor sync.Map

// cmdAdoptWindow is the adopt window (docs/SPEC-SPRINT.md, "The adopt
// window"): it waits until every agent the adoption stopped, named by its
// launchd label and pid, has exited, for at most --window. Any other process
// of the binary (the dashboard's poll, a seat's inbox --wait, a where, card or
// finish) is one OTHER line and never refuses: the install replaces the binary
// by rename, and a running process keeps its inode. Exit 0 with WINDOW OK,
// 1 with WINDOW REFUSED naming each stopped agent still running by pid and
// label, 2 usage.
func (a *app) cmdAdoptWindow(args []string, stdout, stderr io.Writer) int {
	const name = "adopt window"
	fs, _ := a.verbSetup(name)
	var stopped stringList
	fs.Var(&stopped, "stopped", "an agent the adoption stopped, <label>=<pid> with the pid launchd held for it (repeatable)")
	binary := fs.String("binary", filepath.Join(a.getenv("HOME"), ".local", "bin", "nova-sprint"), "the seat's binary: its other processes are listed as OTHER and never waited on")
	bound := fs.Duration("window", DefaultAdoptWindow, "how long the stopped agents are given to exit")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, name, argErr("takes no words ", err, pos...))
	}
	if *bound <= 0 {
		return refuse(stderr, name, fmt.Sprintf("--window wants a positive duration, found %s", *bound))
	}
	agents, err := windowAgents(stopped)
	if err != nil {
		return refuse(stderr, name, oneline.Err(err))
	}
	w := AdoptWindow{Stopped: agents, Binary: resolvedExe(*binary), Bound: *bound, Procs: windowProcs(execAdoptRunner), Now: a.now}
	if fake, ok := windowProcsFor.Load(a); ok {
		// a test's clock moves only by the window's own sleeps: no real time
		var slept time.Duration
		w.Procs = fake.(func(context.Context) ([]WindowProc, error))
		w.Now = func() time.Time { return a.now().Add(slept) }
		w.Sleep = func(_ context.Context, d time.Duration) error { slept += d; return nil }
	}
	r, err := w.Wait(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: %s; run: nova-sprint live\n", prog, name, oneline.Err(err))
		return 1
	}
	for _, l := range r.Lines {
		fmt.Fprintln(stdout, oneline.Escape(l))
	}
	if !r.OK {
		return 1
	}
	return 0
}
