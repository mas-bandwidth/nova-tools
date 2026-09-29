package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// The machine: start and stop set its state; run is the process that ticks
// once a second while it is RUNNING; tick is one tick by hand.

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
		if _, hb, err := st.Machine(ctx); err == nil && a.now().Sub(hb.At) > store.MachineSilence {
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
	if c.actor == "coordinator" {
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

// printTick prints what one tick did: each part's moves, its refusals and a
// summary line; a tick of a STOPPED machine says the machine is STOPPED.
func (a *app) printTick(res store.TickResult, err error, max int, stdout, stderr io.Writer) {
	if res.State == store.Stopped && err == nil {
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
	fmt.Fprintf(stdout, "RUN ticking every %s while the machine is RUNNING; %s\n", store.TickEvery, st.MachineLine(context.Background()))
	a.runLoop(context.Background(), st, c.max, 0, stdout, stderr)
	return 0
}

// runLoop ticks every TickEvery, n times (0 is for ever). A tick of a
// RUNNING machine that moved something, wrote a notification or failed is
// printed; an error is printed always and the loop goes on, waiting longer
// after each failure in a row, up to TickBackoffCap.
func (a *app) runLoop(ctx context.Context, st *store.Store, max, n int, stdout, stderr io.Writer) {
	failures := 0
	was := ""
	for i := 0; n == 0 || i < n; i++ {
		res, err := st.Tick(ctx)
		if res.State != was && res.State != "" {
			fmt.Fprintf(stdout, "%s machine %s\n", a.now().Format("15:04:05"), res.State)
			was = res.State
		}
		if err != nil || len(res.Parts) > 0 || len(res.Repaired) > 0 || res.Stale != "" {
			fmt.Fprintf(stdout, "%s tick\n", a.now().Format("15:04:05"))
			a.printTick(res, err, max, stdout, stderr)
			if line := sprintLine(ctx, st); line != "" {
				fmt.Fprintln(stdout, line)
			}
		}
		wait := store.TickEvery
		if err != nil {
			failures++
			wait = min(store.TickEvery<<min(failures-1, 8), store.TickBackoffCap)
		} else {
			failures = 0
		}
		a.sleep(wait)
	}
}

// machineWords is the machine's part of the help.
func machineWords() string {
	return strings.TrimSpace(`
The machine: nova-sprint start sets it RUNNING, nova-sprint stop sets it
STOPPED; nova-sprint run ticks once a second while it is RUNNING, and
nova-sprint tick is one tick by hand. Each tick deals ready primaries, asks
readers, resolves waiting primaries whose needs landed, and writes the
judgments that need the coordinator. Every verb works in both states.`) + "\n"
}
