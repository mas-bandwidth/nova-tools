package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/pprof"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/binstamp"
	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
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
	// Reason and Until are a stop by hand's (docs/SPEC-SPRINT.md section 14).
	Reason string `json:"reason,omitempty"`
	Until  string `json:"until,omitempty"`
}

func (a *app) cmdMachineStart(args []string, stdout, stderr io.Writer) int {
	return a.setMachine("start", true, args, stdout, stderr)
}

func (a *app) cmdMachineStop(args []string, stdout, stderr io.Writer) int {
	return a.setMachine("stop", false, args, stdout, stderr)
}

// setMachine is start and stop: the state before and after, whether it
// changed, and the sprint line. Setting the state the machine has changes
// nothing and says so. A stop wants --reason and --until (docs/SPEC-SPRINT.md
// section 14): the machine line says who stopped it, why and when it is back,
// and only an explicit start resumes it; a stop of a STOPPED machine replaces the
// two.
func (a *app) setMachine(name string, running bool, args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup(name)
	var reason, untilArg string
	if !running {
		fs.StringVar(&reason, "reason", "", "why the machine stops, shown with it: the machine line of where, inbox and the dashboard says \"STOPPED by <actor>: <reason>, back by <time>\" (required)")
		fs.StringVar(&untilArg, "until", "", "when the machine starts itself again: a `time or duration`, a duration from now (90m), a clock time (2:04 PM or 14:04, today's or tomorrow's) or an RFC 3339 time; the tick starts it then unless it is stopped again (required)")
	}
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if len(pos) > 0 {
		return refuse(stderr, name, "takes no words, found "+pos[0])
	}
	var until time.Time
	if !running {
		if until, err = sprint.StopArgs(reason, untilArg, a.now()); err != nil {
			return refuse(stderr, name, err.Error())
		}
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	ctx := context.Background()
	if running {
		// every provider out of credit: the machine stays STOPPED until a provider is paid
		// (nova-tools#5199; the owner, 2026-10-03: "if all providers are out, then you stop
		// the sprint.")
		why, err := st.OutOfCredit(ctx)
		if err != nil {
			return a.readFailed(name, err, stderr)
		}
		if why != "" {
			fmt.Fprintf(stderr, "%s %s: %s\n", prog, name, oneline.WithRemedy(why, prog+" where --json (its providers), then "+prog+" funded <provider> --reason <the payment> once one is paid"))
			return 1
		}
	}
	var before, after store.Machine
	var res store.Result
	if running {
		before, after, res, err = st.SetMachine(ctx, true)
	} else {
		before, after, res, err = st.StopUntil(ctx, reason, until)
	}
	changed := before.Running() != after.Running()
	line := sprintLine(ctx, st)
	if c.json {
		o := machineOut{Before: before.StateWord(), After: after.StateWord(), Changed: changed, Notes: res.Notes, Sprint: line, Reason: after.Reason}
		if !after.Until.IsZero() {
			o.Until = after.Until.UTC().Format(time.RFC3339)
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
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: %s\n", prog, name, oneline.WithRemedy(err.Error(), prog+" "+name+" -h"))
		return 2
	}
	what := "changed"
	switch {
	case !changed && !running:
		what = "unchanged: the machine is STOPPED already; its reason and back-by time are this stop's"
	case !changed:
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
	c, code := a.machineFlags(name, args, stderr, extra...)
	if c == nil {
		return nil, nil, code
	}
	st, err := a.store(*c)
	if err != nil {
		return nil, nil, refuse(stderr, name, err.Error())
	}
	return st, c, 0
}

// machineFlags is machineVerb's words alone, the store not yet opened: a shadow
// tick opens its own, read-only (shadow.go).
func (a *app) machineFlags(name string, args []string, stderr io.Writer, extra ...func(flagSet)) (*common, int) {
	fs, c := a.verbSetup(name)
	for _, x := range extra {
		x(fs)
	}
	pos, err := parse(fs, args)
	if err != nil {
		return nil, refuse(stderr, name, err.Error())
	}
	if len(pos) > 0 {
		return nil, refuse(stderr, name, "takes no words, found "+pos[0])
	}
	if c.actor == "" {
		c.actor = sprint.MachineActor
	}
	return c, 0
}

func (a *app) cmdTick(args []string, stdout, stderr io.Writer) int {
	var rules, idle, shadow bool
	c, code := a.machineFlags("tick", args, stderr, answerRulesFlag(&rules, false), idleAlarmFlag(&idle, false), shadowFlag(&shadow))
	if c == nil {
		return code
	}
	if shadow {
		return a.shadowTick(*c, rules, idle, stdout, stderr)
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "tick", err.Error())
	}
	st.AnswerRules, st.IdleAlarm = rules, idle
	st.WakeFriend = a.stallWaker(st, stderr)
	ctx := context.Background()
	res, err := st.Tick(ctx)
	err = noSprintYet(err)
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
		// every table, every tick: the rows the tick
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
		// the sprint is done: the machine stopped itself
		fmt.Fprintf(stdout, "HAPPENED %s: %s; the machine is STOPPED; %s\n", sprint.NSprintDone, oneline.Escape(res.Done), oneline.Escape(res.Hint))
	}
	status := "OK"
	if err != nil {
		status = "FAILED"
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
	var profile, listen, decideDir, keyNames string
	var profileTicks int
	var land bool
	landParallel := landParallelDefault
	var rules, idle bool
	st, c, code := a.machineVerb("run", args, stderr, answerRulesFlag(&rules, true), idleAlarmFlag(&idle, true), func(fs flagSet) {
		fs.StringVar(&listen, "listen", "", "also be the sprint's server: the workers' verbs on this `address:port` (this machine's address on the fleet's private network; a name, a public address, a link-local address, and an every-network address are refused), where nova-swarm member --server <address>:<port> sends them, and the coordinator's verbs on 127.0.0.1 at the same port, where NOVA_SPRINT_SERVER=127.0.0.1:<port> sends them")
		fs.StringVar(&keyNames, "keys", "", "the `NAME,...` of secrets this process reads from the seat login's nova-secrets seat (also recorded as keys.json beside the login): the decision key and each provider key. A name that cannot be read refuses at start. The unit's environment carries no key value")
		fs.StringVar(&decideDir, "decide", "", "also keep the record of the sprint's attempt and grade decisions in this `dir` (nova-decide's layer 2: attempt.jsonl, grade.jsonl): the finishes' attempt decisions recorded, every card graded before its first deal with JEV_API_KEY from this environment or read in this process when --keys or keys.json names it, and each decision's outcome attached when its card lands or is dropped, every "+DecideEvery.String())
		fs.BoolVar(&land, "land", false, "also land what the readers passed, every "+LandEvery.String()+", one landing at a time, as the coordinator (land's defaults: each card's REPO: and BASE: lines); every cycle prints one line, and a landing still running after "+LandDeadline.String()+" raises one judgment naming the stage; land is then not run by hand")
		fs.IntVar(&landParallel, "land-parallel", landParallelDefault, "with --land, how many streams each landing merges at once before it lands them one at a time (land --land-parallel)")
		fs.StringVar(&profile, "cpuprofile", "", "write a CPU profile of the loop's first ticks to this file (see --profile-ticks)")
		fs.IntVar(&profileTicks, "profile-ticks", 10, "the ticks --cpuprofile covers; the profile is written after the last of them")
		fs.DurationVar(&a.tickDeadline, "tick-deadline", TickDeadline, "the least time a tick may take before it is given up (stretched to 3 x the median wall of the last 20 ticks, at most "+TickDeadlineCap.String()+"): past it the stacks are printed, the tick's plan is given up and the loop goes on; three wedged ticks in a row (given up and not stopped within a further deadline) exit 4 so the supervisor starts the loop again (0: wait for ever)")
	})
	if st == nil {
		return code
	}
	st.AnswerRules, st.IdleAlarm = rules, idle
	st.WakeFriend = a.stallWaker(st, stderr)
	if a.twinOpen(c.redis) {
		return refuse(stderr, "run", twinMachine)
	}
	// named secrets are read here, before a port is bound, so a name that cannot
	// be read refuses without listening (keys.go)
	if err := a.holdNamedUnitKeys(keyNames); err != nil {
		return refuse(stderr, "run", err.Error())
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
		if landParallel != landParallelDefault {
			b := a.landState()
			b.mu.Lock()
			b.landMore = append(b.landMore, "--land-parallel", strconv.Itoa(landParallel))
			b.mu.Unlock()
		}
		go a.landLoop(context.Background(), c.redis, stdout)
	}
	// the providers' balances, read outside every tick (balance.go)
	go a.balanceLoop(context.Background(), st, stdout)
	// the store round trip, timed every 10 s for where (store-latency-row-r.w2)
	go a.storeRTTLoop(context.Background(), st)
	if decideDir != "" {
		var b decide.Backend
		if key := a.getenv(decide.JevSecret); key != "" {
			b = decide.JevHTTP(key, decide.JevTimeout)
		}
		if a.decide == nil { // a test's lane, with its backend, is kept
			a.decide = newDecideLane(decideDir, b, a.now, GradeWait)
		}
		if a.decide.diffOf == nil {
			a.decide.diffOf = a.cardDiff
		}
		go a.decideLoop(context.Background(), c.redis, stdout)
	}
	// on server start, keep every in-flight read whose lease is live and only
	// take back reads whose lease has lapsed (tla/ServerLanes.tla, Restart)
	// ignored: a best-effort read cleanup on startup; tick takes care of any subsequent lapses
	_ = a.serverStart(context.Background(), st)

	fmt.Fprintf(stdout, "RUN ticking on every line of the log (at most every %s) and every %s while it is quiet; %s\n", store.TickFloor, store.TickEvery, st.MachineLine(context.Background()))
	if a.runLoop(context.Background(), st, c.max, 0, stdout, stderr) {
		return exitReplaced
	}
	return 0
}

// exitReplaced is run's exit when its binary was replaced under it: not 0, so
// a supervisor that restarts only a failed loop restarts it too.
const exitReplaced = 3

// TickDeadline is the least time run waits for one tick before it gives the
// tick's plan up (--tick-deadline). A tick takes tens of milliseconds on a quiet
// store; on 2026-10-02 one whose plan never ended held serial, the server's one
// line of control, for half an hour while its heap grew to 244 GB, and no verb
// was answered (nova-tools#5122). The deadline stretches with the store
// (tickDeadlineOf): on 2026-10-06, under load, ticks took 7 to 8 s, a fixed 10 s
// fired every few minutes and each exit left the workers refused for 15 to 30 s.
const TickDeadline = 10 * time.Second

// TickDeadlineCap is the most the deadline stretches to (unless --tick-deadline
// asks for more), and tickWalls the ticks whose walls it is stretched by.
const (
	TickDeadlineCap = 60 * time.Second
	tickWalls       = 20
)

// TickWedgedToExit is how many wedged ticks in a row mean the process is wedged:
// run then exits exitTickDeadline. A tick is wedged when, given up past its
// deadline and cancelled, it has not stopped within a further deadline; each
// further deadline it has not stopped in counts one more. A tick given up that
// stops when cancelled is no wedge: the store was slow, the process is not stuck
// (a step change in load, 100 ms ticks become 12 s ticks, pays no exit).
const TickWedgedToExit = 3

// exitTickDeadline is run's exit when TickWedgedToExit wedged ticks came in a
// row: not 0, so its supervisor starts it again.
const exitTickDeadline = 4

// tickDeadlineOf is the deadline of the next tick: three times the median wall
// of the ticks in walls (the last tickWalls; with an even count, the upper of
// the two middle walls), never less than least and never more than
// TickDeadlineCap, or least when that is more; 0 (no deadline) when least is 0.
// A slow store stretches the deadline instead of killing the server.
func tickDeadlineOf(least time.Duration, walls []time.Duration) time.Duration {
	if least <= 0 {
		return 0
	}
	d := least
	if len(walls) > 0 {
		sorted := slices.Clone(walls)
		slices.Sort(sorted)
		d = max(d, 3*sorted[len(sorted)/2])
	}
	return min(d, max(TickDeadlineCap, least))
}

// liftAfterOverrun is the least deadline after a tick given up at deadline d:
// three times d, at most TickDeadlineCap (or least when that is more). The walls'
// median follows a step change in load only after ten slow ticks; the lift holds
// the deadline up from the first one, until a tick ends within least.
func liftAfterOverrun(least, d time.Duration) time.Duration {
	return min(3*d, max(TickDeadlineCap, least))
}

// keepWall adds a tick's wall to the last tickWalls.
func keepWall(walls []time.Duration, wall time.Duration) []time.Duration {
	walls = append(walls, wall)
	if len(walls) > tickWalls {
		walls = slices.Delete(walls, 0, len(walls)-tickWalls)
	}
	return walls
}

// tickOf is one tick of the loop's store: a test's tick when it gives one.
func (a *app) tickOf(ctx context.Context, st *store.Store) (store.TickResult, error) {
	if a.tickFn != nil {
		return a.tickFn(ctx, st)
	}
	return st.Tick(ctx)
}

// tickWithin runs one tick, begun at began, on a context of its own, and waits
// for it at most d (0 waits for ever, reading no clock). Past d it says so on
// stdout, writes every goroutine's stack to stderr (the stack names the planner
// the tick is in), cancels the tick's context, so its store calls end and a
// write not yet sent is never sent, and returns over: the tick's plan is given up
// and never written (a write already in flight is the store's fence's to finish
// or repair). ended is closed when the tick's goroutine has returned: the caller
// waits for it before the next tick (awaitGivenUp), for the goroutine shares the
// loop's store and its twin.
func (a *app) tickWithin(ctx context.Context, tick func(context.Context) (store.TickResult, error), d time.Duration, began time.Time, stdout, stderr io.Writer) (res store.TickResult, over bool, ended <-chan struct{}, err error) {
	end := make(chan struct{})
	if d <= 0 {
		res, err = tick(ctx)
		close(end)
		return res, false, end, err
	}
	type result struct {
		res store.TickResult
		err error
	}
	tctx, cancel := context.WithCancel(ctx)
	done := make(chan result, 1)
	go func() {
		defer close(end)
		r, e := tick(tctx)
		done <- result{r, e}
	}()
	select {
	case r := <-done:
		cancel()
		return r.res, false, end, r.err
	case <-a.after(d):
		fmt.Fprintf(stdout, "%s TICK DEADLINE the tick begun at %s did not end within %s: its plan is given up and the loop goes on to the next tick once it has stopped; the stacks follow on stderr\n",
			a.now().Format("15:04:05"), began.Format("15:04:05"), d)
		// ignored: the stacks are a diagnosis; the line above says what happened
		_ = pprof.Lookup("goroutine").WriteTo(stderr, 2)
		cancel()
		return store.TickResult{}, true, end, nil
	}
}

// awaitGivenUp waits for the tick given up (its context cancelled) to end, so the
// next tick never runs beside it, and returns true once it has. A tick that has
// not stopped within a further deadline d is wedged: each further deadline it has
// not stopped in counts one more on wedged, the wedged ticks in a row; a tick that
// stops within the first further deadline is no wedge and starts the count again.
// At TickWedgedToExit the process is wedged: it says so, writes the stacks again,
// and returns false, and the caller exits exitTickDeadline. A cancel ends no store
// call in flight at once (a read already sent runs to its ReadTimeout, 5 s), so a
// tick that stops is given a whole further deadline to do it.
func (a *app) awaitGivenUp(ended <-chan struct{}, d time.Duration, began time.Time, wedged *int, stdout, stderr io.Writer) bool {
	select {
	case <-ended:
		*wedged = 0
		return true
	case <-a.after(d):
	}
	for {
		*wedged++
		fmt.Fprintf(stdout, "%s TICK DEADLINE the tick begun at %s, given up, has not stopped within a further %s: a wedged tick, %d in a row\n",
			a.now().Format("15:04:05"), began.Format("15:04:05"), d, *wedged)
		if *wedged >= TickWedgedToExit {
			fmt.Fprintf(stdout, "%s TICK WEDGED %d given-up ticks in a row did not stop within a further deadline (%s) of being cancelled, the last begun at %s: the process is wedged and run exits %d so its supervisor starts it again; the stacks follow on stderr\n",
				a.now().Format("15:04:05"), *wedged, d, began.Format("15:04:05"), exitTickDeadline)
			// ignored: the process is about to exit, and the line above says why
			_ = pprof.Lookup("goroutine").WriteTo(stderr, 2)
			return false
		}
		select {
		case <-ended:
			return true
		case <-a.after(d):
		}
	}
}

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
// (store/waitlog.go): after each tick it blocks on the
// epoch's log from the last line it has seen, and a line wakes it, so a step
// that frees room or makes cards ready (a finish, a merge, a drop, a release,
// fleet up, start) is ticked on, and its room dealt, at most TickFloor after
// the tick before began; a quiet log ticks it TickEvery after the tick before
// began (the sweep, the presence, the lateness). A wake costs the blocked
// read alone. Every tick of a RUNNING machine is printed, naming every table
// and the rows it changed in each, and every tick that
// failed; an error is printed always and the loop goes on, waiting longer
// after each failure in a row, up to TickBackoffCap. After each tick of a
// RUNNING machine every friend is reconciled (reconcileFriendsTick).
//
// A loop runs the code it was started with for as long as it runs: a binary
// installed under it (a release, a fix) would leave the store ticked by the
// code before it: a loop started before the routes, say, deals a fresh card
// with none while the verbs draw them. So before
// each tick it reads its binary's file, and when that changed since it began it
// stops, saying so, and returns true: its supervisor starts the new binary.
func (a *app) runLoop(ctx context.Context, st *store.Store, max, n int, stdout, stderr io.Writer) (replaced bool) {
	failures := 0
	was := ""
	began0 := a.binaryStamp()
	// every line before the loop is seen: the first tick reads the state whole
	cursor, _ := st.LogTail(ctx)
	why := tickStart
	friends := newFriendTick()
	// the server's record, the actor this loop runs as, which seat and handover
	// show beside the seat's holder: written before the first tick and every
	// store.ServerEvery (seat-key-follows-record.w2)
	var said time.Time
	// the walls of the last ticks, which stretch the deadline (tickDeadlineOf),
	// the least deadline an overrun lifted it to (liftAfterOverrun), and the
	// wedged ticks in a row (awaitGivenUp)
	var walls []time.Duration
	var lift time.Duration
	wedged := 0
	for i := 0; (n == 0 || i < n) && ctx.Err() == nil; i++ {
		if now := a.binaryStamp(); began0 != "" && now != began0 {
			fmt.Fprintf(stdout, "RUN STOP the binary this loop runs was replaced on disk since it began (%s, now %s): exiting so its supervisor starts the new one; a loop that is not supervised: run nova-sprint run again\n", began0, orDashStr(now, "unreadable"))
			return true
		}
		began := a.now()
		said = a.sayServer(ctx, st, said, stderr)
		// one tick, or one worker's batch, at a time (serve.go); the tick takes the line at
		// its turn, after the batch in flight, not behind every batch waiting
		// (sprint.ControlLine; docs/SPEC-SPRINT.md section 14, The server, "The tick's turn")
		if waited := a.serial.TickLock(); waited > store.TickEvery {
			fmt.Fprintf(stdout, "%s LINE the tick waited %s for the server's line of control (a batch or a lane held it)\n", a.now().Format("15:04:05"), waited.Round(time.Millisecond))
		}
		deadline := tickDeadlineOf(a.tickDeadline, walls)
		if deadline > 0 && lift > deadline {
			deadline = lift
		}
		start := a.now()
		res, over, ended, err := a.tickWithin(ctx, func(c context.Context) (store.TickResult, error) { return a.tickOf(c, st) }, deadline, began, stdout, stderr)
		if over {
			// a tick past its deadline gives up its plan, never the process: the
			// line is held until the tick given up has stopped, then the loop goes on
			// (docs/SPEC-SPRINT.md section 14, The server, "The tick's deadline")
			walls = keepWall(walls, deadline)
			lift = liftAfterOverrun(a.tickDeadline, deadline)
			if !a.awaitGivenUp(ended, deadline, began, &wedged, stdout, stderr) {
				// serial stays held: the tick's goroutine is still in its plan
				a.exit(exitTickDeadline)
				return false
			}
			a.serial.Unlock()
			if count, cerr := st.CountTickOverrun(ctx); cerr != nil {
				fmt.Fprintf(stderr, "%s run: the tick's overrun was not counted on the heartbeat: %s\n", prog, oneline.Escape(cerr.Error()))
			} else {
				fmt.Fprintf(stdout, "%s TICK OVERRUN the tick begun at %s was given up and has stopped; tick_overrun=%d; the next deadline is at least %s; the loop goes on\n", a.now().Format("15:04:05"), began.Format("15:04:05"), count, lift)
			}
			if n != 0 && i == n-1 {
				return false
			}
			why = tickRetry
			continue
		}
		wedged = 0
		wall := a.now().Sub(start)
		walls = keepWall(walls, wall)
		if wall <= a.tickDeadline {
			lift = 0 // the store is as fast as --tick-deadline again
		}
		if a.ticked != nil {
			a.ticked(i+1, began, why)
		}
		if a.profiled != nil {
			a.profiled(i + 1)
		}
		if res.BusyRetries > 0 {
			fmt.Fprintf(stdout, "%s TICK BUSY other operations kept the fence moving under the tick: its parts ran again %d times within it\n", a.now().Format("15:04:05"), res.BusyRetries)
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
				fmt.Fprintf(stdout, "%s deadline=%s\n", res.TimesLine(), deadline)
			}
			if line := sprintLine(ctx, st); line != "" {
				fmt.Fprintln(stdout, line)
			}
		}
		if err == nil && res.State == store.Running {
			// every friend reconciled after the tick's deal, with friend reconcile's plan
			// (friendreconcile_tick.go; docs/SPEC-SPRINT.md section 1,
			// friend-reconcile-every-tick-r.w1), in the tick's own turn of the line: on
			// 2026-10-10 each friend's pass queued behind every waiting batch, and the
			// passes cost 12 s a tick at the median (docs/SPEC-SPRINT.md section 14,
			// store-trips-pipelinedb-bb)
			a.reconcileFriendsTick(ctx, st, friends, stdout)
		}
		a.serial.Unlock()
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

// sayServer writes the server's record, the actor the loop runs as, when
// store.ServerEvery has passed since said, its last write, and returns the
// time of the last write; a failed write is said and tried again on the next
// tick. It writes nothing else: never the coordinator key, which init and the
// seat's steps write from the seat's record.
func (a *app) sayServer(ctx context.Context, st *store.Store, said time.Time, stderr io.Writer) time.Time {
	now := a.now()
	if !said.IsZero() && now.Sub(said) < store.ServerEvery {
		return said
	}
	if err := st.SetServerActor(ctx, st.Actor); err != nil {
		fmt.Fprintf(stderr, "%s run: the server's record was not written: %s\n", prog, oneline.Escape(err.Error()))
		return said
	}
	return now

}

// storeRTTLoop times one store round trip every store.StoreRTTEvery, waiting on
// a.after between them, until ctx is done; where shows the p50 and p99 of the last
// minute (store.MeasureStoreRTT, docs/SPEC-SPRINT.md section 14,
// store-latency-row-r.w2). A failed round trip is not a sample, and the next is
// timed as usual.
func (a *app) storeRTTLoop(ctx context.Context, st *store.Store) {
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
			return
		case <-a.after(store.StoreRTTEvery):
			// ignored: a failed round trip records nothing; the next one is timed in 10 s
			_, _ = st.MeasureStoreRTT(ctx)
		}
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
The machine: nova-sprint start sets it RUNNING, nova-sprint stop --reason
<text> --until <time or duration> sets it STOPPED: where, inbox and the
dashboard say "STOPPED by <actor>: <reason>, back by 2:04 PM". That time is
display metadata; only an explicit start resumes after every owned working
job has been cancelled and returned with stop-return. nova-sprint run ticks
as soon as a line comes on the log (a verb's
step: a finish, a merge, a start), at most every 100ms, and once a second
while the log is quiet; nova-sprint tick is one tick by hand. Each tick deals
ready primaries, asks readers, resolves waiting primaries whose needs landed,
and writes the judgments that need the coordinator; a judgment open past its
due time is marked overdue, once. A stop halts the tick before its next part.
When nothing is left open (every card landed or dropped) the tick says "the
sprint is done" to the coordinator and stops the machine itself: DONE, in
where and the view; work added after leaves it STOPPED until nova-sprint
start. run reads each provider's balance every 10 minutes through the seat's
key (OPENROUTER_API_KEY, read in this process when --keys or keys.json names
it; a BALANCE line) with its spend over the last hour from the sprint's cost
records. A balance rests nothing: one not over an hour of its spend is the
coordinator's judgment (a provider is low on funds), its routes serving until
nova-sprint routes rest says otherwise. A provider that refuses a take for
credit is rested until a payment is seen (a balance read higher than the read
before it, or than at the refusal), nova-sprint funded <provider> says it was
paid, or nova-sprint routes wake <provider> ends it. When every provider is
OUT the tick stops the machine (STOPPED, every provider is out of credit) and
start is refused until one is paid. Low on funds never stops it. Every
other verbs can inspect and repair stopped state. run stops (exit 3) when its own binary is replaced
on disk, so its supervisor starts the new build.`) + "\n"
}

// answerRulesFlag is run's and tick's --answer-rules: the tick answers the mechanical
// judgments by rule (docs/SPEC-SPRINT.md section 8, answered by rule). The run loop answers
// by default; a tick by hand only when asked, so a twin's or a test's tick is the machine's
// moves alone unless it says so.
func answerRulesFlag(on *bool, byDefault bool) func(flagSet) {
	return func(fs flagSet) {
		fs.BoolVar(on, "answer-rules", byDefault, "answer the mechanical judgments by rule, recorded \"answered by rule <name>\" (work came back failed: a harness fault or a HOLD with findings reworked on its tier with the failure as its fix, any other failure redealt, then a tier up; a card at its bound: a tier up, heavy to a friend; a late card: a wait once with progress, else returned and redealt; a conflict in a file no ledger owns: returned, redone on the tip, resumed; the same finding twice: marked a brief defect); nova-config's sprint row answer_rules_off turns single rules off; --answer-rules=false leaves every judgment to the coordinator (run answers by default, a tick by hand only with --answer-rules); nova-sprint rules prints what they would answer now")
	}
}

// idleAlarmFlag is run's and tick's --idle-alarm: the tick watches for an idle fleet
// (docs/SPEC-SPRINT.md section 14, the fleet is idle); on in the run loop, off in a tick by
// hand unless asked.
func idleAlarmFlag(on *bool, byDefault bool) func(flagSet) {
	return func(fs flagSet) {
		fs.BoolVar(on, "idle-alarm", byDefault, "when the fleet works under half its width for "+sprint.IdleWindow.String()+" while cards wait, push the coordinator one note (the inbox, and inbox --push) naming the roots the waiting cards are behind, the most cards first, once an episode, and one more when it recovers (run: on by default; a tick by hand only with --idle-alarm)")
	}
}

// serverStart runs the server startup steps before the first tick:
// on server start, keep every in-flight read whose lease is live and only
// take back reads whose lease has lapsed (tla/ServerLanes.tla, Restart;
// docs/SPEC-SPRINT.md section 6).
func (a *app) serverStart(ctx context.Context, st *store.Store) error {
	_, err := st.Run(ctx, store.ServerRestartStep())
	return err
}

// cardDiff reads the unified diff of a card in review as the workers' head left it,
// against its base from the repository clone under landRoot.
func (a *app) cardDiff(ctx context.Context, c *sprint.Card) (string, error) {
	if c == nil {
		return "", errors.New("no card")
	}
	if diff := c.F("diff"); diff != "" {
		return diff, nil
	}
	head := c.F("head")
	if head == "" {
		return "", fmt.Errorf("%s has no head", c.ID)
	}
	base := swarm.ReadCardBase([]byte(c.F("brief")))
	if base.Repo == "" {
		return "", fmt.Errorf("%s brief names no repository", c.ID)
	}
	root, err := a.landRoot()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, repoDirName(base.Repo))
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return "", fmt.Errorf("no clone at %s", dir)
	}
	git := func(args ...string) (string, error) {
		res, err := gitrun.Run(ctx, gitrun.Options{C: dir, Env: a.gitEnv, OwnRepo: true}, args...)
		return strings.TrimSpace(string(res.Stdout)), err
	}
	branch := c.F("branch")
	if _, err := git("cat-file", "-e", head+"^{commit}"); err != nil && branch != "" {
		// the head is not in the clone, so the diff cannot run without this fetch: its
		// failure is the one that explains the card, said with its remedy
		res, err := gitrun.Run(ctx, gitrun.Options{C: dir, Env: a.gitEnv, OwnRepo: true}, "fetch", "-q", "--no-tags", "origin", branch)
		if err != nil {
			why := firstLine("", err)
			if stderr := strings.TrimSpace(string(res.Stderr)); stderr != "" {
				why += ": " + firstLine(stderr, nil)
			}
			return "", fmt.Errorf("%s: head %s is not in the clone at %s and fetching branch %s from origin failed (%s); check the branch was pushed and origin is reachable, then run git -C %s fetch origin %s", c.ID, head, dir, branch, why, dir, branch)
		}
	}
	at := base.Sha
	if at == "" {
		ref := cmp.Or(base.Ref, c.F("base"), "main")
		if _, err := git("rev-parse", "--verify", "-q", "refs/remotes/origin/"+ref+"^{commit}"); err == nil {
			at = "origin/" + ref
		} else {
			at = ref
		}
	}
	res, err := gitrun.Run(ctx, gitrun.Options{C: dir, Env: a.gitEnv, OwnRepo: true}, "diff", "-M", "--no-color", "--end-of-options", at+"..."+head)
	if err != nil {
		res, err = gitrun.Run(ctx, gitrun.Options{C: dir, Env: a.gitEnv, OwnRepo: true}, "diff", "-M", "--no-color", "--end-of-options", at, head)
	}
	if err != nil {
		return "", err
	}
	return string(res.Stdout), nil
}
