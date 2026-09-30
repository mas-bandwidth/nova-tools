package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// The machine: start and stop set its state; run is the process that ticks
// on every line of the log and once a second while the log is quiet; tick is
// one tick by hand.

// machineOut is start's, stop's and tick's report for a program.
type machineOut struct {
	Before  string             `json:"before,omitempty"`
	After   string             `json:"after,omitempty"`
	Changed bool               `json:"changed"`
	Tick    *store.TickResult  `json:"tick,omitempty"`
	Notes   int                `json:"notes"`
	Error   string             `json:"error,omitempty"`
	Sprint  string             `json:"sprint,omitempty"`
	Refused []sprint.Refusal   `json:"refused,omitempty"`
	Moved   []string           `json:"moved,omitempty"`
	Parts   []store.PartResult `json:"parts,omitempty"`
}

func (a *app) cmdMachineStart(args []string, stdout, stderr io.Writer) int {
	return a.setMachine("start", true, args, stdout, stderr)
}

func (a *app) cmdMachineStop(args []string, stdout, stderr io.Writer) int {
	return a.setMachine("stop", false, args, stdout, stderr)
}

// setMachine is start and stop: the state before and after, whether it
// changed, and the sprint line. Setting the state the machine has changes
// nothing and says so.
func (a *app) setMachine(name string, running bool, args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup(name)
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if len(pos) > 0 {
		return refuse(stderr, name, "takes no words, found "+pos[0])
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	ctx := context.Background()
	before, after, res, err := st.SetMachine(ctx, running)
	changed := before.Running() != after.Running()
	line := sprintLine(ctx, st)
	if c.json {
		o := machineOut{Before: before.StateWord(), After: after.StateWord(), Changed: changed, Notes: res.Notes, Sprint: line}
		if err != nil {
			o.Error = err.Error()
		}
		b, _ := json.Marshal(o)
		fmt.Fprintln(stdout, string(b))
		if err != nil {
			return 2
		}
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: %s\n", prog, name, oneline.Escape(err.Error()))
		return 2
	}
	what := "changed"
	if !changed {
		what = "unchanged: the machine is " + after.StateWord() + " already"
	}
	fmt.Fprintf(stdout, "%s OK before=%s after=%s %s\n", token(name), before.StateWord(), after.StateWord(), what)
	if running {
		if _, hb, err := st.Machine(ctx); err == nil && a.now().Sub(hb.Alive()) > store.MachineSilence {
			fmt.Fprintf(stdout, "nothing is ticking: run: nova-sprint run\n")
		}
	}
	if line != "" {
		fmt.Fprintln(stdout, line)
	}
	return 0
}

// machineVerb is the store of tick and run, acting as the machine unless
// --actor names another.
func (a *app) machineVerb(name string, args []string, stderr io.Writer) (*store.Store, *common, int) {
	fs, c := a.verbSetup(name)
	pos, err := parse(fs, args)
	if err != nil {
		return nil, nil, refuse(stderr, name, err.Error())
	}
	if len(pos) > 0 {
		return nil, nil, refuse(stderr, name, "takes no words, found "+pos[0])
	}
	if c.actor == "" {
		c.actor = sprint.MachineActor
	}
	st, err := a.store(*c)
	if err != nil {
		return nil, nil, refuse(stderr, name, err.Error())
	}
	return st, c, 0
}

func (a *app) cmdTick(args []string, stdout, stderr io.Writer) int {
	st, c, code := a.machineVerb("tick", args, stderr)
	if st == nil {
		return code
	}
	ctx := context.Background()
	res, err := st.Tick(ctx)
	line := sprintLine(ctx, st)
	if c.json {
		o := machineOut{Tick: &res, Notes: res.Notes(), Sprint: line, Moved: res.Moved()}
		if o.Moved == nil {
			o.Moved = []string{}
		}
		if err != nil {
			o.Error = err.Error()
		}
		b, _ := json.Marshal(o)
		fmt.Fprintln(stdout, string(b))
		if err != nil {
			return 2
		}
		return 0
	}
	a.printTick(res, err, c.max, stdout, stderr)
	if line != "" {
		fmt.Fprintln(stdout, line)
	}
	if err != nil {
		return 2
	}
	return 0
}

// printTick prints what one tick did: each part's moves, its refusals, a
// halt by a stop, the moves left due past its bounds, and a summary line; a
// tick of a STOPPED machine says the machine is STOPPED.
func (a *app) printTick(res store.TickResult, err error, max int, stdout, stderr io.Writer) {
	if res.State == store.Stopped && err == nil && res.Halted == "" && res.Done == "" {
		fmt.Fprintf(stdout, "TICK OK state=STOPPED nothing done; run: nova-sprint start\n")
		return
	}
	for _, r := range res.Repaired {
		fmt.Fprintf(stdout, "REPAIRED %s %s %s\n", oneline.Escape(r.Op), r.Done, oneline.Escape(r.Detail))
	}
	var moved, refused []string
	for _, p := range res.Parts {
		for _, m := range p.Moved {
			moved = append(moved, p.Name+": "+m)
		}
		for _, r := range p.Refused {
			refused = append(refused, p.Name+": "+r.Key+": "+r.Why)
		}
	}
	listed(stdout, "MOVED", moved, max, "tick")
	listed(stderr, "REFUSED", refused, max, "tick")
	if res.Stale != "" {
		fmt.Fprintf(stdout, "STALE %s\n", oneline.Escape(res.Stale))
	}
	if res.Halted != "" {
		fmt.Fprintf(stdout, "HALTED %s\n", oneline.Escape(res.Halted))
	}
	if len(res.Tables) > 0 {
		// every table, every tick (errata 3 amendment 10): the rows the tick
		// changed in each, none when it had nothing to do
		words := make([]string, len(res.Tables))
		for i, tb := range res.Tables {
			words[i] = fmt.Sprintf("%s=%d", tb.Table, len(tb.Rows))
		}
		fmt.Fprintf(stdout, "TABLES rows changed: %s\n", strings.Join(words, " "))
	}
	if res.Due > 0 {
		fmt.Fprintf(stdout, "DUE %d moves past the tick's bounds: the next ticks catch up\n", res.Due)
	}
	if res.Done != "" {
		// the sprint is done: the machine stopped itself (errata 3 amendment 6)
		fmt.Fprintf(stdout, "HAPPENED %s: %s; the machine is STOPPED; %s\n", sprint.NSprintDone, oneline.Escape(res.Done), oneline.Escape(res.Hint))
	}
	status := "OK"
	if err != nil {
		status = "FAIL"
	}
	idle := "no"
	if res.Idle {
		idle = "yes"
	}
	fmt.Fprintf(stdout, "TICK %s state=%s idle=%s moved=%d notes=%d\n", status, res.State, idle, len(moved), res.Notes())
	if err != nil {
		fmt.Fprintf(stderr, "%s tick: %s\n", prog, oneline.Escape(err.Error()))
	}
}

func (a *app) cmdRun(args []string, stdout, stderr io.Writer) int {
	st, c, code := a.machineVerb("run", args, stderr)
	if st == nil {
		return code
	}
	fmt.Fprintf(stdout, "RUN ticking on every line of the log (at most every %s) and every %s while it is quiet; %s\n", store.TickFloor, store.TickEvery, st.MachineLine(context.Background()))
	a.runLoop(context.Background(), st, c.max, 0, stdout, stderr)
	return 0
}

// Why a tick of run began: the loop's start, a line on the log, the clock of
// a quiet log, or the retry after a failed tick.
const (
	tickStart = "start"
	tickLog   = "log"
	tickClock = "clock"
	tickRetry = "retry"
)

// runLoop ticks n times (0 is for ever), waking on the log, not the clock
// (the owner's finding of 2026-09-30; store/waitlog.go): after each tick it blocks on the
// epoch's log from the last line it has seen, and a line wakes it, so a step
// that frees room or makes cards ready (a finish, a merge, a drop, a release,
// fleet up, start) is ticked on, and its room dealt, at most TickFloor after
// the tick before began; a quiet log ticks it TickEvery after the tick before
// began (the sweep, the presence, the lateness). A wake costs the blocked
// read alone. Every tick of a RUNNING machine is printed, naming every table
// and the rows it changed in each (errata 3 amendment 10), and every tick that
// failed; an error is printed always and the loop goes on, waiting longer
// after each failure in a row, up to TickBackoffCap.
func (a *app) runLoop(ctx context.Context, st *store.Store, max, n int, stdout, stderr io.Writer) {
	failures := 0
	was := ""
	// every line before the loop is seen: the first tick reads the state whole
	cursor, _ := st.LogTail(ctx)
	why := tickStart
	for i := 0; (n == 0 || i < n) && ctx.Err() == nil; i++ {
		began := a.now()
		res, err := st.Tick(ctx)
		if a.ticked != nil {
			a.ticked(i+1, began, why)
		}
		if res.State != was && res.State != "" {
			fmt.Fprintf(stdout, "%s machine %s\n", a.now().Format("15:04:05"), res.State)
			was = res.State
		}
		if err != nil || res.State == store.Running || len(res.Parts) > 0 || len(res.Repaired) > 0 || res.Stale != "" || res.Halted != "" {
			fmt.Fprintf(stdout, "%s tick\n", a.now().Format("15:04:05"))
			a.printTick(res, err, max, stdout, stderr)
			if line := sprintLine(ctx, st); line != "" {
				fmt.Fprintln(stdout, line)
			}
		}
		if n != 0 && i == n-1 {
			return
		}
		if err != nil {
			failures++
			a.sleep(min(store.TickEvery<<min(failures-1, 8), store.TickBackoffCap))
			why = tickRetry
			continue
		}
		failures = 0
		cursor, why = a.pace(ctx, st, res.Epoch, cursor, began)
	}
}

// pace is the wait between two ticks of run: it blocks on the log of the
// epoch the tick ran at from cursor, until a line comes or TickEvery after
// the tick began (never less than TickFloor); a line wakes it, and the next
// tick begins TickFloor after the last began at the soonest. It returns the
// cursor for the next wait and why the next tick begins. A wait the store
// refuses falls back to the clock.
func (a *app) pace(ctx context.Context, st *store.Store, epoch uint64, cursor string, began time.Time) (string, string) {
	d := max(store.TickEvery-a.now().Sub(began), store.TickFloor)
	tail, woke, err := st.WaitLog(ctx, epoch, cursor, d)
	if err != nil {
		a.sleep(max(store.TickEvery-a.now().Sub(began), 0))
		return cursor, tickClock
	}
	if !woke {
		return tail, tickClock
	}
	if left := store.TickFloor - a.now().Sub(began); left > 0 {
		a.sleep(left)
	}
	return tail, tickLog
}

// machineWords is the machine's part of the help.
func machineWords() string {
	return strings.TrimSpace(`
The machine: nova-sprint start sets it RUNNING, nova-sprint stop sets it
STOPPED; nova-sprint run ticks as soon as a line comes on the log (a verb's
step: a finish, a merge, a start), at most every 100ms, and once a second
while the log is quiet; nova-sprint tick is one tick by hand. Each tick deals
ready primaries, asks readers, resolves waiting primaries whose needs landed,
and writes the judgments that need the coordinator; a judgment open past its
due time is marked overdue, once. A stop halts the tick before its next part.
When nothing is left open (every card landed or dropped) the tick says "the
sprint is done" to the coordinator and stops the machine itself: DONE, in
where and the view; work added after leaves it STOPPED until nova-sprint
start. Every verb works in both states.`) + "\n"
}
