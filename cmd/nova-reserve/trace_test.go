package main

import (
	"cmp"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The trace check: seeded walks of processes, each running the real acquire,
// taker.step, release and setCapacity against one directory, with an in-memory
// lock table (which knows each lock's holder, so a killed process's locks go,
// as the kernel's do) and a fake clock. Each process is a goroutine that runs
// only when the walk hands it the turn, before each lock call and at a poll's
// sleep, so a walk is one interleaving of the lock calls, chosen by its seed,
// and a kill lands at any of them. Every lock call, every release, and every
// start, end and kill is a boundary at which the walk records the model's
// action and the state read off the lock table, the capacity file and the
// processes: tla/SlotFilesLog.tla, SlotCountLog.tla and CapacityLog.tla, which
// the <model>Trace.tla modules check are behaviours of the models. A walk's
// processes run one at a time, handing the turn over channels, so its seed
// alone decides it.

// procName is p1, p2, ... as the trace configurations name them.
func procName(i int) string { return "p" + strconv.Itoa(i+1) }

// killed is the panic that unwinds a killed process's goroutine.
type killed struct{}

// A turn's word to a parked process: run on, die (a kill, recorded), or quit
// (the walk is over, no record).
const (
	goOn = iota
	die
	quit
)

type report struct {
	p    *tproc
	what string // "call" (parked before a lock call), "sleep", "held", "ended", "died"
}

type tproc struct {
	name                       string
	phase                      string // the model's pc
	scan, mine, adm, want, ask int
	rounds                     int
	allOK, finishing           bool
	resume                     chan int // goOn, die or quit
	alive                      bool
	waiting                    bool // parked before a lock call or in a poll's sleep
	holding                    bool
}

// twalk is one walk: the model it is recorded for ("slotfiles", "capacity"),
// the processes, the lock table, the clock and the records.
type twalk struct {
	t       *testing.T
	model   string
	dir     string
	n       int // SlotFiles: the stored capacity
	procs   []*tproc
	ex      map[string]string          // lock name -> exclusive holder
	sh      map[string]map[string]bool // lock name -> shared holders
	fault   string                     // "nolock": a slot granted while held; "allover": all exclusive granted over shared
	clock   time.Time
	crashes int
	back    chan report
	recs    []string
}

func newWalk(t *testing.T, model string, procs int) *twalk {
	w := &twalk{t: t, model: model, dir: t.TempDir(), ex: map[string]string{}, sh: map[string]map[string]bool{},
		clock: epoch, back: make(chan report)}
	for i := range procs {
		w.procs = append(w.procs, &tproc{name: procName(i), phase: "idle", scan: 1})
	}
	return w
}

// lockFor is p's tryLock: the turn handed over before the call, the kernel's
// table, and the boundary records.
func (w *twalk) lockFor(p *tproc) tryLock {
	return func(path string, exclusive bool) (func(), bool, error) {
		name := filepath.Base(path)
		w.onCall(p, name, exclusive)
		w.back <- report{p, "call"}
		w.parked(p)
		ok := !(w.ex[name] != "" || (exclusive && len(w.sh[name]) > 0))
		granted := ok
		switch {
		case !ok && w.fault == "nolock" && strings.HasPrefix(name, "slot") && w.ex[name] != p.name:
			ok = true // granted without taking it: the table still names the holder
		case !ok && w.fault == "allover" && name == "all" && exclusive && w.ex[name] == "":
			ok, granted = true, true
		}
		if granted {
			if exclusive {
				w.ex[name] = p.name
			} else {
				if w.sh[name] == nil {
					w.sh[name] = map[string]bool{}
				}
				w.sh[name][p.name] = true
			}
		}
		w.onLock(p, name, exclusive, ok)
		if !ok {
			return nil, false, nil
		}
		return func() {
			if !p.alive {
				return // the kernel let go of it at the kill
			}
			if granted && exclusive && w.ex[name] == p.name {
				delete(w.ex, name)
			} else if granted && !exclusive {
				delete(w.sh[name], p.name)
			}
			w.onUnlock(p, name)
		}, true, nil
	}
}

func slotNum(name string) int {
	k, err := strconv.Atoi(strings.TrimPrefix(name, "slot"))
	if err != nil || !strings.HasPrefix(name, "slot") {
		return 0
	}
	return k
}

func (w *twalk) capFile() int {
	c, _, err := readCapacity(w.dir)
	require.NoError(w.t, err)
	return c.n
}

func (w *twalk) bad(p *tproc, what string) {
	w.t.Fatalf("%s in %s: unexpected %s", p.name, p.phase, what)
}

// onCall records the read of the capacity file that a lock call follows,
// before the call hands the turn over: the file as the code read it.
func (w *twalk) onCall(p *tproc, name string, excl bool) {
	switch {
	case p.phase == "sclaim" && name == "gate" && !excl: // the claim saw the file: back to the gate
		p.phase = "sgate"
		w.record("SClaimSeen", p)
	case p.phase == "sclaim" && name == "gate" && excl: // no file: the claim takes the locks
		p.phase = "sclock"
		w.record("SClaimRead", p)
	case p.phase == "sread" && slotNum(name) > 0:
		p.phase, p.adm, p.scan = "sscan", w.capFile(), 1
		w.record("SRead", p)
	}
}

// onLock maps one lock call to the model's action, if it is one.
func (w *twalk) onLock(p *tproc, name string, excl, ok bool) {
	switch {
	case p.phase == "sgate" && name == "gate" && !excl:
		if ok {
			p.phase = "sread"
			if w.model == "slotfiles" { // SlotFiles has no read: the capacity is fixed
				p.phase, p.scan = "sscan", 1
			}
			w.record("SGate", p)
		}
	case p.phase == "sscan" && slotNum(name) > 0:
		if k := slotNum(name); k != p.scan {
			w.bad(p, "scan of "+name)
		}
		if ok {
			p.mine, p.phase = p.scan, "sall"
		} else {
			p.scan++
		}
		w.record("SScan", p)
	case (p.phase == "sall" || p.phase == "sclaim2" || p.phase == "tall") && name == "all" && excl == (p.phase != "sall"):
		p.allOK = ok
	case p.phase == "sclock" && name == "gate" && excl:
		if ok {
			p.phase = "sclaim2"
			w.record("SClaimOK", p)
		} else {
			p.phase = "sclaim"
			w.record("SClaimBusy", p)
		}
	case p.phase == "xgate" && name == "gate" && excl:
		if ok {
			p.phase = "xall"
			w.record("XGate", p)
		}
	case p.phase == "xall" && name == "all" && excl:
		if ok {
			p.phase = "xrun"
			w.record("XAll", p)
		}
	case p.phase == "tgate" && name == "gate" && excl:
		p.phase = map[bool]string{true: "tall", false: "done"}[ok]
		w.record("TGate", p)
	default:
		w.bad(p, fmt.Sprintf("lock %s exclusive=%v", name, excl))
	}
}

// onUnlock maps one release to the model's action, if it ends one.
func (w *twalk) onUnlock(p *tproc, name string) {
	switch {
	case p.phase == "sread" && name == "gate":
		p.phase = map[bool]string{true: "sclaim", false: "done"}[w.capFile() == 0]
		w.record("SRead", p)
	case p.phase == "sscan" && name == "gate":
		p.phase = "sgate"
		w.record("SScan", p)
	case p.phase == "sall" && slotNum(name) > 0:
		p.mine = 0
	case p.phase == "sall" && name == "gate":
		p.phase = map[bool]string{true: "srun", false: "sgate"}[p.allOK]
		w.record("SAll", p)
	case (p.phase == "sclaim2" || p.phase == "tall") && name == "all":
	case p.phase == "sclaim2" && name == "gate":
		p.phase = map[bool]string{true: "sgate", false: "sclaim"}[p.allOK]
		w.record("SClaim2", p)
	case p.phase == "tall" && name == "gate":
		p.phase = "done"
		w.record("TAll", p)
	case p.phase == "xall" && name == "gate": // the wait ran out: release
		p.phase = "idle"
		w.record("XTimeout", p)
	case (p.phase == "srun" || p.phase == "xrun") && p.finishing:
	default:
		w.bad(p, "release of "+name)
	}
}

// kill is the kernel's part of a process dying: every lock it had goes.
func (w *twalk) kill(p *tproc) {
	p.alive = false
	for name, h := range w.ex {
		if h == p.name {
			delete(w.ex, name)
		}
	}
	for _, s := range w.sh {
		delete(s, p.name)
	}
	w.crashes++
	p.phase, p.mine, p.adm = map[string]string{"slotfiles": "idle", "capacity": "done"}[w.model], 0, 0
	w.record("Crash", p)
}

// start begins p's next take as kind, in a goroutine that runs only on its turn.
func (w *twalk) start(p *tproc, kind string, n int, wait time.Duration) {
	p.alive, p.waiting, p.holding, p.finishing = true, false, false, false
	p.resume = make(chan int)
	switch kind {
	case "shared":
		p.phase, p.want = "sgate", n
	case "exclusive":
		p.phase = "xgate"
	case "set":
		p.phase, p.ask = "tgate", n
	}
	p.rounds++
	w.record("Start", p)
	wd := world{lock: w.lockFor(p), now: func() time.Time { return w.clock }, pid: 100 + len(p.name), cpus: 4,
		running: func(int) bool { return true }, stdout: &strings.Builder{}, stderr: &strings.Builder{}}
	wd.sleep = func(time.Duration) {
		w.back <- report{p, "sleep"}
		w.parked(p)
	}
	o := options{verb: "run", slots: n, exclusive: kind == "exclusive", wait: wait, dir: w.dir, argv: []string{"make"}}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(killed); !ok {
					panic(r)
				}
				w.back <- report{p, "died"}
			}
		}()
		w.parked(p) // the first turn
		if kind == "set" {
			o.verb = "set"
			_ = setCapacity(o, wd) // ignored: a refusal is its TGate or TAll
			w.back <- report{p, "ended"}
			return
		}
		tk, err := acquire(wd, o)
		if err != nil {
			w.back <- report{p, "ended"}
			return
		}
		w.back <- report{p, "held"}
		w.parked(p)
		tk.release()
		w.back <- report{p, "ended"}
	}()
	w.turn(p, goOn)
}

// parked waits for p's next turn; a kill unwinds it after the kernel's part.
func (w *twalk) parked(p *tproc) {
	switch <-p.resume {
	case die:
		w.kill(p)
		panic(killed{})
	case quit:
		panic(killed{})
	}
}

// turn gives p the turn (goOn), kills it (die) or ends it (quit), and waits for it to stop.
func (w *twalk) turn(p *tproc, word int) {
	p.resume <- word
	r := <-w.back
	require.Same(w.t, p, r.p)
	p.waiting, p.holding = r.what == "call" || r.what == "sleep", r.what == "held"
	if r.what != "ended" {
		return
	}
	switch {
	case p.finishing && p.phase == "srun":
		p.phase, p.mine, p.adm = map[string]string{"slotfiles": "idle", "capacity": "done"}[w.model], 0, 0
		w.record("SFinish", p)
	case p.finishing && p.phase == "xrun":
		p.phase = map[string]string{"slotfiles": "idle", "capacity": "done"}[w.model]
		w.record("XFinish", p)
	case p.phase == "tgate": // set: the value is already stored, nothing taken
		p.phase = "done"
		w.record("TGate", p)
	}
	// A take that gave up at the gate holds nothing and keeps its pc: the
	// models have no step for it, and the walk never starts it again.
	p.alive = false
}

// drive gives p turns until it holds or ends.
func (w *twalk) drive(p *tproc) {
	for i := 0; p.alive && p.waiting; i++ {
		require.Less(w.t, i, 100, "%s never holds", p.name)
		w.turn(p, goOn)
	}
}

// quit ends every goroutine still parked, without a record.
func (w *twalk) quit() {
	for _, p := range w.procs {
		if p.alive && (p.waiting || p.holding) {
			p.alive = false
			w.turn(p, quit)
		}
	}
}

// record appends the model's action and the state after it.
func (w *twalk) record(action string, p *tproc) {
	name := "none"
	if p != nil {
		name = p.name
	}
	fn := func(f func(*tproc) string) string {
		var s []string
		for _, q := range w.procs {
			s = append(s, q.name+" |-> "+f(q))
		}
		return "[" + strings.Join(s, ", ") + "]"
	}
	num := func(f func(*tproc) int) string { return fn(func(q *tproc) string { return strconv.Itoa(f(q)) }) }
	holder := func(lock string) string { return strconv.Quote(cmp.Or(w.ex[lock], "none")) }
	set := func(lock string) string {
		var s []string
		for _, q := range w.procs {
			if w.sh[lock][q.name] {
				s = append(s, strconv.Quote(q.name))
			}
		}
		return "{" + strings.Join(s, ", ") + "}"
	}
	slots := func(n int) string {
		var s []string
		for k := 1; k <= n; k++ {
			s = append(s, holder("slot"+strconv.Itoa(k)))
		}
		return "<<" + strings.Join(s, ", ") + ">>"
	}
	pc := fn(func(q *tproc) string { return strconv.Quote(q.phase) })
	if w.model == "slotfiles" {
		var cmd []string
		for _, q := range w.procs {
			if q.phase == "srun" || q.phase == "xrun" {
				cmd = append(cmd, strconv.Quote(q.name))
			}
		}
		w.recs = append(w.recs, fmt.Sprintf("S(%q, %q, %s, %s, %s, %s, %s, %s, %s, %s, %s, %d, {%s})", action, name, pc,
			num(func(q *tproc) int { return q.scan }), num(func(q *tproc) int { return q.mine }), slots(w.n),
			holder("gate"), set("gate"), holder("all"), set("all"), num(func(q *tproc) int { return q.rounds }), w.crashes,
			strings.Join(cmd, ", ")))
		return
	}
	w.recs = append(w.recs, fmt.Sprintf("C(%q, %q, %s, %d, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %d)", action, name, pc, w.capFile(),
		slots(capMaxSlots), holder("gate"), set("gate"), holder("all"), set("all"),
		num(func(q *tproc) int { return q.scan }), num(func(q *tproc) int { return q.mine }), num(func(q *tproc) int { return q.adm }),
		num(func(q *tproc) int { return q.want }), num(func(q *tproc) int { return q.ask }), w.crashes))
}

// The walks' bounds, which the trace configurations state as constants.
const (
	sfProcs, sfN, sfRounds, sfCrashes = 3, 2, 3, 3
	capProcs, capMaxSlots, capCrashes = 5, 2, 2
	walkSteps                         = 80
)

// slotFilesWalk: three processes, shared or exclusive (an exclusive one may
// give up after 2 s), under a stored capacity of two.
func slotFilesWalk(t *testing.T, seed uint64) *twalk {
	w := newWalk(t, "slotfiles", sfProcs)
	w.n = sfN
	require.NoError(t, writeCapacity(w.dir, sfN, 7, epoch))
	w.record("Init", nil)
	r := rand.New(rand.NewPCG(seed, 0))
	for i := 0; i < 5000 && len(w.recs) < walkSteps; i++ {
		p := w.procs[r.IntN(len(w.procs))]
		switch c := r.IntN(20); {
		case c == 0 && w.crashes < sfCrashes && p.alive && (p.waiting || p.holding):
			w.turn(p, die)
		case c < 4:
			w.clock = w.clock.Add(time.Second)
		case !p.alive && p.phase == "idle" && p.rounds < sfRounds:
			if r.IntN(3) == 0 {
				w.start(p, "exclusive", 0, time.Duration(r.IntN(2))*2*time.Second)
			} else {
				w.start(p, "shared", 0, 0)
			}
		case p.alive && p.waiting:
			w.turn(p, goOn)
		case p.alive && p.holding && r.IntN(2) == 0:
			p.finishing = true
			w.turn(p, goOn)
		}
	}
	w.quit()
	return w
}

// capacityWalk: four processes, each one shared taker (with --slots 0..2),
// exclusive taker or set (1..2), from a capacity file absent or holding 1 or 2.
func capacityWalk(t *testing.T, seed uint64) *twalk {
	w := newWalk(t, "capacity", capProcs)
	r := rand.New(rand.NewPCG(seed, 1))
	if n := r.IntN(4) - 1; n > 0 {
		require.NoError(t, writeCapacity(w.dir, n, 7, epoch))
	}
	w.record("Init", nil)
	for i := 0; i < 5000 && len(w.recs) < walkSteps && slices.ContainsFunc(w.procs, func(p *tproc) bool { return p.phase != "done" }); i++ {
		p := w.procs[r.IntN(len(w.procs))]
		switch c := r.IntN(25); {
		case c == 0 && w.crashes < capCrashes && p.alive && (p.waiting || p.holding):
			w.turn(p, die)
		case p.phase == "idle":
			switch r.IntN(4) {
			case 0:
				w.start(p, "exclusive", 0, 0)
			case 1:
				w.start(p, "set", 1+r.IntN(capMaxSlots), 0)
			default:
				w.start(p, "shared", r.IntN(capMaxSlots+1), 0)
			}
		case p.alive && p.waiting:
			w.turn(p, goOn)
		case p.alive && p.holding && r.IntN(2) == 0:
			p.finishing = true
			w.turn(p, goOn)
		}
	}
	w.quit()
	return w
}

// brokenNoLock: the lock table grants a held slot (tla/SlotFiles.tla
// "nolock"): three shared takers under a capacity of two all take slot1.
func brokenNoLock(t *testing.T) *twalk {
	w := newWalk(t, "slotfiles", sfProcs)
	w.n, w.fault = sfN, "nolock"
	require.NoError(t, writeCapacity(w.dir, sfN, 7, epoch))
	w.record("Init", nil)
	for _, p := range w.procs {
		w.start(p, "shared", 0, 0)
		w.drive(p)
	}
	w.quit()
	return w
}

// brokenAllOver: the lock table grants all exclusive over its shared holder
// (tla/Capacity.tla "setwhileheld"): a set stores 2 while a holder taken
// under 1 runs.
func brokenAllOver(t *testing.T) *twalk {
	w := newWalk(t, "capacity", capProcs)
	w.fault = "allover"
	require.NoError(t, writeCapacity(w.dir, 1, 7, epoch))
	w.record("Init", nil)
	w.start(w.procs[0], "shared", 0, 0)
	w.drive(w.procs[0])
	w.start(w.procs[1], "set", 2, 0)
	w.drive(w.procs[1])
	w.quit()
	return w
}

// The seeds: together their walks take every action of each model but those
// only a broken design takes (Capacity's SWrite and the prober's).
const sfWalks, capWalks = 16, 16

var (
	sfActions  = []string{"Start", "SGate", "SScan", "SAll", "SFinish", "XGate", "XAll", "XTimeout", "XFinish", "Crash"}
	capActions = []string{"Start", "SGate", "SRead", "SClaimRead", "SClaimSeen", "SClaimOK", "SClaimBusy", "SClaim2", "SScan",
		"SAll", "SFinish", "XGate", "XAll", "XFinish", "TGate", "TAll", "Crash"}
)

// requireEvery fails unless each action is taken somewhere in walks.
func requireEvery(t *testing.T, model string, walks [][]string, actions []string) {
	t.Helper()
	for _, a := range actions {
		require.True(t, slices.ContainsFunc(walks, func(recs []string) bool {
			return slices.ContainsFunc(recs, func(r string) bool { return strings.HasPrefix(r[2:], strconv.Quote(a)) })
		}), "no walk takes %s's action %s: choose other seeds", model, a)
	}
}

// TestTracesAreBehavioursOfTheModels renders the walks as the committed
// tla/*Log.tla modules and requires them to be the same; the <model>Trace.tla
// modules check every record is a step of the model (tests/tla/check.sh).
// RESERVE_UPDATE_LOG=1 rewrites them.
func TestTracesAreBehavioursOfTheModels(t *testing.T) {
	t.Parallel()
	var sf, sc, capw [][]string
	for s := range uint64(sfWalks) {
		w := slotFilesWalk(t, s+1)
		sf, sc = append(sf, w.recs), append(sc, slotCountOf(w.recs))
	}
	for s := range uint64(capWalks) {
		capw = append(capw, capacityWalk(t, s+1).recs)
	}
	requireEvery(t, "SlotFiles", sf, sfActions)
	requireEvery(t, "Capacity", capw, capActions)
	nl := brokenNoLock(t)
	ao := brokenAllOver(t)
	files := map[string]string{
		"tla/SlotFilesLog.tla": traceLog("SlotFilesLog", slotFilesHead, map[string][][]string{"Walks": sf, "BrokenNoLock": {nl.recs}}),
		"tla/SlotCountLog.tla": traceLog("SlotCountLog", slotCountHead, map[string][][]string{"Walks": sc, "BrokenNoLock": {slotCountOf(nl.recs)}}),
		"tla/CapacityLog.tla":  traceLog("CapacityLog", capacityHead, map[string][][]string{"Walks": capw, "BrokenAllOver": {ao.recs}}),
	}
	for _, file := range slices.Sorted(func(yield func(string) bool) {
		for f := range files {
			if !yield(f) {
				return
			}
		}
	}) {
		got := files[file]
		if os.Getenv("RESERVE_UPDATE_LOG") == "1" {
			require.NoError(t, os.WriteFile(file, []byte(got), 0o644))
			continue
		}
		want, err := os.ReadFile(file)
		require.NoError(t, err)
		if string(want) != got {
			t.Errorf("the steps the code took are not the ones %s holds: the code and the model have drifted. "+
				"Change both together, then rewrite the log and run TLC on it; run: RESERVE_UPDATE_LOG=1 go test -run TestTracesAreBehavioursOfTheModels ./cmd/nova-reserve && tests/tla/check.sh <jar> cmd/nova-reserve/tla", file)
		}
	}
}

// slotCountOf projects SlotFiles records onto SlotCount.tla: shared is every
// process in srun, excl every one in xrun; a record that changes neither is
// no step of that model.
func slotCountOf(recs []string) []string {
	out := []string{`K("Init", "none", {}, {})`}
	prevS, prevX := []string{}, []string{}
	for _, r := range recs[1:] {
		var s, x []string
		for i := range sfProcs {
			n := procName(i)
			switch {
			case strings.Contains(pcOf(r), n+` |-> "srun"`):
				s = append(s, strconv.Quote(n))
			case strings.Contains(pcOf(r), n+` |-> "xrun"`):
				x = append(x, strconv.Quote(n))
			}
		}
		action, who := "", ""
		switch {
		case len(s) > len(prevS):
			action, who = "AcquireShared", added(s, prevS)
		case len(x) > len(prevX):
			action, who = "AcquireExclusive", added(x, prevX)
		case len(s) < len(prevS):
			action, who = "Release", added(prevS, s)
		case len(x) < len(prevX):
			action, who = "Release", added(prevX, x)
		}
		if action != "" {
			out = append(out, fmt.Sprintf("K(%q, %s, {%s}, {%s})", action, who, strings.Join(s, ", "), strings.Join(x, ", ")))
		}
		prevS, prevX = s, x
	}
	return out
}

// pcOf is the pc field of a rendered S(...) record: its first [...].
func pcOf(r string) string {
	i := strings.Index(r, "[")
	return r[i : i+strings.Index(r[i:], "]")+1]
}

func added(more, less []string) string {
	for _, m := range more {
		if !slices.Contains(less, m) {
			return m
		}
	}
	return `"none"`
}

const slotFilesHead = `(* The walks of cmd/nova-reserve/trace_test.go (TestTracesAreBehavioursOfTheModels), as SlotFiles.tla's     *)
(* state after each action: the action and its process, then pc, scan, mine, slotEx, gateEx, gateSh, allEx,    *)
(* allSh, rounds, crashes and cmd, read off the lock table and the takers. Written by that test               *)
(* (RESERVE_UPDATE_LOG=1); SlotFilesTrace.tla checks each walk is a behaviour of SlotFiles.tla. BrokenNoLock is  *)
(* the same code over a lock table that grants a held slot, which it must refuse.                            *)
S(a, p, pc, scan, mine, slotEx, gateEx, gateSh, allEx, allSh, rounds, crashes, cmd) ==
    [a |-> a, p |-> p, pc |-> pc, scan |-> scan, mine |-> mine, slotEx |-> slotEx, gateEx |-> gateEx,
     gateSh |-> gateSh, allEx |-> allEx, allSh |-> allSh, rounds |-> rounds, crashes |-> crashes, cmd |-> cmd]`

const slotCountHead = `(* The walks of SlotFilesLog.tla projected onto SlotCount.tla: shared is every process holding a slot, excl *)
(* every one holding all exclusive; each record is the change and the sets after it. Written by              *)
(* cmd/nova-reserve/trace_test.go (RESERVE_UPDATE_LOG=1); SlotCountTrace.tla checks each is a behaviour of      *)
(* SlotCount.tla, and BrokenNoLock, three holders under a capacity of two, must be refused.                    *)
K(a, p, shared, excl) == [a |-> a, p |-> p, shared |-> shared, excl |-> excl]`

const capacityHead = `(* The walks of cmd/nova-reserve/trace_test.go (TestTracesAreBehavioursOfTheModels), as Capacity.tla's      *)
(* state after each action: the action and its process, then pc, capFile, slotEx, gateEx, gateSh, allEx,     *)
(* allSh, scan, mine, adm, want, ask and crashes, read off the lock table, the capacity file and the takers.   *)
(* Written by that test (RESERVE_UPDATE_LOG=1); CapacityTrace.tla checks each walk is a behaviour of           *)
(* Capacity.tla. BrokenAllOver is the same code over a lock table that grants all exclusive over a shared      *)
(* holder, so a set stores a new capacity under a running holder, which it must refuse.                        *)
C(a, p, pc, capFile, slotEx, gateEx, gateSh, allEx, allSh, scan, mine, adm, want, ask, crashes) ==
    [a |-> a, p |-> p, pc |-> pc, capFile |-> capFile, slotEx |-> slotEx, gateEx |-> gateEx, gateSh |-> gateSh,
     allEx |-> allEx, allSh |-> allSh, scan |-> scan, mine |-> mine, adm |-> adm, want |-> want, ask |-> ask,
     crashes |-> crashes]`

// traceLog renders a module defining each named list of walks.
func traceLog(module, head string, defs map[string][][]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s MODULE %s %s\n%s\n", strings.Repeat("-", 30), module, strings.Repeat("-", 30), head)
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for n := range defs {
			if !yield(n) {
				return
			}
		}
	}) {
		var walks []string
		for _, recs := range defs[name] {
			walks = append(walks, "<<\n        "+strings.Join(recs, ",\n        ")+"\n    >>")
		}
		fmt.Fprintf(&b, "%s == <<\n    %s\n>>\n", name, strings.Join(walks, ",\n    "))
	}
	b.WriteString(strings.Repeat("=", 77) + "\n")
	return b.String()
}
