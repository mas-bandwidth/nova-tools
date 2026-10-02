package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/pprof"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/binstamp"
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
		fmt.Fprintf(stderr, "%s %s: %s\n", prog, name, oneline.WithRemedy(err.Error(), prog+" "+name+" -h"))
		return 2
	}
	what := "changed"
	if !changed {
		what = "unchanged: the machine is " + after.StateWord() + " already"
	}
	fmt.Fprintf(stdout, "%s OK before=%s after=%s %s\n", token(name), before.StateWord(), after.StateWord(), what)
	if running {
		if _, hb, err := st.Machine(ctx); err == nil && a.now().Sub(hb.Alive()) > store.MachineSilence {
			if a.twinOpen(c.redis) {
				fmt.Fprintf(stdout, "nothing is ticking between commands in a twin: tick by hand: nova-sprint tick\n")
			} else {
				fmt.Fprintf(stdout, "nothing is ticking: run: nova-sprint run\n")
			}
		}
	}
	if line != "" {
		fmt.Fprintln(stdout, line)
	}
	return 0
}

// machineVerb is the store of tick and run, acting as the machine unless
// --actor names another.
func (a *app) machineVerb(name string, args []string, stderr io.Writer, extra ...func(flagSet)) (*store.Store, *common, int) {
	fs, c := a.verbSetup(name)
	for _, x := range extra {
		x(fs)
	}
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
	for _, n := range res.Said {
		fmt.Fprintf(stdout, "NOTE %s\n", oneline.Escape(n))
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
	var profile, listen string
	var profileTicks int
	var land bool
	st, c, code := a.machineVerb("run", args, stderr, func(fs flagSet) {
		fs.StringVar(&listen, "listen", "", "also be the sprint's server: the workers' verbs on this `address:port` (this machine's address on the fleet's private network; 0.0.0.0 and other every-network addresses are refused), where nova-swarm member --server <address>:<port> sends them, and the coordinator's verbs on 127.0.0.1 at the same port, where NOVA_SPRINT_SERVER=127.0.0.1:<port> sends them")
		fs.BoolVar(&land, "land", false, "also land what the readers passed, every "+LandEvery.String()+", one landing at a time, as the coordinator (land's defaults: each card's REPO: and BASE: lines); land is then not run by hand")
		fs.StringVar(&profile, "cpuprofile", "", "write a CPU profile of the loop's first ticks to this file (see --profile-ticks)")
		fs.IntVar(&profileTicks, "profile-ticks", 10, "the ticks --cpuprofile covers; the profile is written after the last of them")
	})
	if st == nil {
		return code
	}
	if a.twinOpen(c.redis) {
		return refuse(stderr, "run", twinMachine)
	}
	if profile != "" {
		stop, err := startProfile(profile)
		if err != nil {
			return refuse(stderr, "run", "--cpuprofile: "+err.Error())
		}
		stopped := func() {
			if err := stop(); err != nil {
				fmt.Fprintf(stderr, "PROFILE not written whole to %s: %v; run again with --cpuprofile\n", profile, err)
			}
		}
		defer stopped()
		// a run stopped before its last profiled tick (a signal) still
		// writes the profile of the ticks it made
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
		go func() {
			<-sigs
			stopped()
			os.Exit(0)
		}()
		a.profiled = func(n int) {
			if n == profileTicks {
				if err := stop(); err != nil {
					fmt.Fprintf(stderr, "PROFILE not written whole to %s: %v; run again with --cpuprofile\n", profile, err)
					return
				}
				fmt.Fprintf(stdout, "PROFILE %d ticks written to %s; run: go tool pprof -top %s\n", n, profile, profile)
			}
		}
	}
	if listen != "" {
		if err := a.listen(listen, c.redis, stdout); err != nil {
			return refuse(stderr, "run", err.Error())
		}
	}
	if land {
		go a.landLoop(context.Background(), c.redis, stdout)
	}
	fmt.Fprintf(stdout, "RUN ticking on every line of the log (at most every %s) and every %s while it is quiet; %s\n", store.TickFloor, store.TickEvery, st.MachineLine(context.Background()))
	if a.runLoop(context.Background(), st, c.max, 0, stdout, stderr) {
		return exitReplaced
	}
	return 0
}

// exitReplaced is run's exit when its binary was replaced under it: not 0, so
// a supervisor that restarts only a failed loop restarts it too.
const exitReplaced = 3

// binaryStamp is the binary file this process was started from, as it is on disk
// now: its path, size and modification time; "" when it cannot be read.
func (a *app) binaryStamp() string {
	exe := a.executable
	if exe == nil {
		exe = os.Executable
	}
	path, err := exe()
	if err != nil {
		return ""
	}
	return binstamp.Of(path)
}

// startProfile begins a CPU profile to the file; its stop, which may be
// called more than once, ends it and closes the file, and says (once) if the
// file was not closed whole.
func startProfile(path string) (func() error, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		_ = f.Close() // ignored: nothing was written to it, and the start's error is the one returned
		return nil, err
	}
	var once sync.Once
	return func() error {
		var err error
		once.Do(func() {
			pprof.StopCPUProfile()
			err = f.Close()
		})
		return err
	}, nil
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
//
// A loop runs the code it was started with for as long as it runs: a binary
// installed under it (a release, a fix) would leave the store ticked by the
// code before it (the owner's store, 2026-10-01: a loop started before the
// routes dealt a fresh card with none while the verbs drew them). So before
// each tick it reads its binary's file, and when that changed since it began it
// stops, saying so, and returns true: its supervisor starts the new binary.
func (a *app) runLoop(ctx context.Context, st *store.Store, max, n int, stdout, stderr io.Writer) (replaced bool) {
	failures := 0
	was := ""
	began0 := a.binaryStamp()
	// every line before the loop is seen: the first tick reads the state whole
	cursor, _ := st.LogTail(ctx)
	why := tickStart
	for i := 0; (n == 0 || i < n) && ctx.Err() == nil; i++ {
		if now := a.binaryStamp(); began0 != "" && now != began0 {
			fmt.Fprintf(stdout, "RUN STOP the binary this loop runs was replaced on disk since it began (%s, now %s): exiting so its supervisor starts the new one; a loop that is not supervised: run nova-sprint run again\n", began0, orDashStr(now, "unreadable"))
			return true
		}
		began := a.now()
		// one tick, or one worker's batch, at a time (serve.go)
		a.serial.Lock()
		res, err := st.Tick(ctx)
		a.serial.Unlock()
		if a.ticked != nil {
			a.ticked(i+1, began, why)
		}
		if a.profiled != nil {
			a.profiled(i + 1)
		}
		if res.State != was && res.State != "" {
			fmt.Fprintf(stdout, "%s machine %s\n", a.now().Format("15:04:05"), res.State)
			was = res.State
		}
		if err != nil || res.State == store.Running || len(res.Parts) > 0 || len(res.Repaired) > 0 || res.Stale != "" || res.Halted != "" {
			fmt.Fprintf(stdout, "%s tick\n", a.now().Format("15:04:05"))
			a.printTick(res, err, max, stdout, stderr)
			if res.State == store.Running || err != nil {
				// what the tick cost, part by part: its time, round trips,
				// whole-table reads and records read (store/stats.go)
				fmt.Fprintln(stdout, res.TimesLine())
			}
			if line := sprintLine(ctx, st); line != "" {
				fmt.Fprintln(stdout, line)
			}
		}
		if n != 0 && i == n-1 {
			return false
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
	return false
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
start. Every verb works in both states. run stops (exit 3) when its own binary
is replaced on disk, so its supervisor starts the new build.`) + "\n"
}
