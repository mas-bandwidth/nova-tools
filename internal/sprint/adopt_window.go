package sprint

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
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
// is what the next exec runs.

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
