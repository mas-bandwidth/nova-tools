package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The adopt window verb (docs/SPEC-SPRINT.md, "The adopt window"): the seat
// play calls it after it booted out the agents an adoption stops, with those
// agents' launchd labels and pids, so the window waits on them alone. A verb
// process of the binary (the live dashboard's one-second `where --json` poll,
// a seat's `inbox --wait`) is listed as OTHER and never refuses: the install
// replaces the binary by rename, so a running process keeps its own inode.
func init() {
	notServed = append(notServed, "adopt window")
	verbClasses["adopt window"] = classRead
	verbExit["adopt window"] = "exit codes: 0 every stopped agent is gone (WINDOW OK), 1 one still runs at the bound (WINDOW REFUSED, naming each by pid and label) or the process table does not read, 2 usage"
	verbEffect["adopt window"] = "inspection: reads the host's process table (ps -A -o pid=,args=) and waits, writing nothing; it never touches the store"
}

// windowProcs is ps's every process with the executable its first argument
// resolves to (a bare name found on PATH, links followed), this one aside:
// the adopt window's process table (internal/sprint/adopt_window.go). The
// dashboard's nova-sprint-int2 resolves to the installed nova-sprint, so it is
// seen as the binary it runs, and listed as an OTHER, never waited on.
func windowProcs(run adoptRunner) func(ctx context.Context) ([]sprint.WindowProc, error) {
	return func(ctx context.Context) ([]sprint.WindowProc, error) {
		text, err := run(ctx, "ps", "-A", "-o", "pid=,args=")
		if err != nil {
			return nil, err
		}
		return windowProcsOf(text, os.Getpid()), nil
	}
}

// windowProcsOf is ps's text as the window's table, the pid self aside.
func windowProcsOf(text string, self int) []sprint.WindowProc {
	var out []sprint.WindowProc
	for _, l := range strings.Split(text, "\n") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		pid, err := strconv.Atoi(f[0])
		if err != nil || pid == self {
			continue
		}
		out = append(out, sprint.WindowProc{PID: pid, Path: resolvedExe(f[1]), Args: strings.Join(f[1:], " ")})
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
func windowAgents(flags []string) ([]sprint.WindowAgent, error) {
	var out []sprint.WindowAgent
	for _, v := range flags {
		label, pid, ok := strings.Cut(v, "=")
		n, err := strconv.Atoi(pid)
		if !ok || strings.TrimSpace(label) == "" || err != nil || n < 0 {
			return nil, fmt.Errorf("--stopped wants <label>=<pid>, found %q", v)
		}
		out = append(out, sprint.WindowAgent{Label: label, PID: n})
	}
	return out, nil
}

// windowProcsFor is a test's process table for one app (*app to
// func(context.Context) ([]sprint.WindowProc, error)).
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
	bound := fs.Duration("window", sprint.DefaultAdoptWindow, "how long the stopped agents are given to exit")
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
	w := sprint.AdoptWindow{Stopped: agents, Binary: resolvedExe(*binary), Bound: *bound, Procs: windowProcs(execAdoptRunner), Now: a.now}
	if fake, ok := windowProcsFor.Load(a); ok {
		// a test's clock moves only by the window's own sleeps: no real time
		var slept time.Duration
		w.Procs = fake.(func(context.Context) ([]sprint.WindowProc, error))
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
