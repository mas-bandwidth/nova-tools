package sprint

import (
	"fmt"
	"strings"
	"time"
)

// The adoption window (docs/SPEC-SPRINT.md, "Adopting a build"): the seat's
// adopt stops the agents it is about to replace and waits for those alone,
// each by the pid launchd held for it before the window, until it has exited.
// A process that merely runs the seat's binary is somebody else's work (the
// dashboard's one-second poll, a person's verb); the binary is replaced by
// rename, so a running process keeps its inode, and the window lists it as
// OTHER and never waits on it. Only an agent the adopt stopped is a reason to
// refuse, named by its pid and label after the bound.
//
// The window is pure over an injected process table, clock and sleep, so a
// test runs it on a fake table with no real time (adopt_window_test.go).

// DefaultAdoptWindow is the bound the adoption window waits: each agent the
// adopt stopped gets this long to exit before the window refuses.
const DefaultAdoptWindow = 60 * time.Second

// DefaultAdoptWindowEvery is how often the window reads the process table
// while it waits.
const DefaultAdoptWindowEvery = time.Second

// AdoptWindowAgent is one agent the adoption stopped: its launchd label and
// the pid its process held when the pre-window manifest read it.
type AdoptWindowAgent struct {
	PID   int    `json:"pid"`
	Label string `json:"label"`
}

// AdoptWindowProcess is one process the window sees of the seat: its pid, its
// arguments, and whether it runs the seat's own nova-sprint binary. Mine
// processes the adopt did not stop are listed as OTHER and ignored; a process
// whose pid is a stopped agent's is that agent, whatever it runs.
type AdoptWindowProcess struct {
	PID  int    `json:"pid"`
	Args string `json:"args"`
	Mine bool   `json:"mine"`
}

// AdoptWindowResult is one window's outcome: the counts it reports and what it
// found still running or beside it.
type AdoptWindowResult struct {
	Stopped int
	Waited  time.Duration
	Others  []AdoptWindowProcess
	Running []AdoptWindowAgent
}

// OK says no stopped agent still runs: the window may open.
func (r AdoptWindowResult) OK() bool { return len(r.Running) == 0 }

// Line is the window's success line: WINDOW OK stopped=<n> waited=<s>s
// others=<n>.
func (r AdoptWindowResult) Line() string {
	return fmt.Sprintf("WINDOW OK stopped=%d waited=%s others=%d", r.Stopped, r.Waited.Round(time.Second), len(r.Others))
}

// OtherLines is one OTHER <pid> <argv> line per process running the binary the
// adopt did not stop, in the order they were first seen.
func (r AdoptWindowResult) OtherLines() []string {
	out := make([]string, 0, len(r.Others))
	for _, p := range r.Others {
		out = append(out, fmt.Sprintf("OTHER %d %s", p.PID, p.Args))
	}
	return out
}

// Refusal names each agent the adopt stopped that still runs after the bound,
// with its pid and label and nothing else.
func (r AdoptWindowResult) Refusal() string {
	parts := make([]string, 0, len(r.Running))
	for _, a := range r.Running {
		parts = append(parts, fmt.Sprintf("%d %s", a.PID, a.Label))
	}
	return fmt.Sprintf("these stopped agents still run after %s, so nothing was migrated: %s",
		r.Waited.Round(time.Second), strings.Join(parts, ", "))
}

// AdoptWindowOptions is one window wait: the agents the adopt stopped, the
// bound, and the injected clock, sleep and process sweep. Sweep is required;
// Now and Sleep default to the real clock and time.Sleep, and a test gives
// fakes so no real time passes.
type AdoptWindowOptions struct {
	Stopped []AdoptWindowAgent
	Bound   time.Duration
	Every   time.Duration
	Now     func() time.Time
	Sleep   func(time.Duration)
	Sweep   func() ([]AdoptWindowProcess, error)
}

// WaitAdoptWindow reads the process table until every stopped agent's process
// has exited or the bound has passed, and reports which stopped agents still
// run and which other processes of the binary stood beside them. It is the one
// decision the live window and the seat play both read; a sweep that does not
// read is returned as an error, never a silent pass.
func WaitAdoptWindow(o AdoptWindowOptions) (AdoptWindowResult, error) {
	bound := o.Bound
	if bound <= 0 {
		bound = DefaultAdoptWindow
	}
	every := o.Every
	if every <= 0 {
		every = DefaultAdoptWindowEvery
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	sleep := o.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	res := AdoptWindowResult{Stopped: len(o.Stopped), Others: []AdoptWindowProcess{}}
	stopped := make(map[int]bool, len(o.Stopped))
	for _, a := range o.Stopped {
		stopped[a.PID] = true
	}
	seen := map[int]bool{}
	start := now()
	for {
		procs, err := o.Sweep()
		if err != nil {
			return res, err
		}
		present := make(map[int]bool, len(procs))
		for _, p := range procs {
			present[p.PID] = true
			if p.Mine && !stopped[p.PID] && !seen[p.PID] {
				seen[p.PID] = true
				res.Others = append(res.Others, p)
			}
		}
		res.Running = res.Running[:0]
		for _, a := range o.Stopped {
			if present[a.PID] {
				res.Running = append(res.Running, a)
			}
		}
		res.Waited = now().Sub(start)
		if len(res.Running) == 0 || res.Waited >= bound {
			return res, nil
		}
		sleep(every)
	}
}
