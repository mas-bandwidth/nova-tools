package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Seeded deterministic walks of the real acquire, release, and setCapacity
// functions over an in-memory flock table and on-disk JSON records, bound to
// tla/Reserve.tla via tla/ReserveTrace.tla and tla/ReserveLog.tla.

const (
	trProcs     = 4
	trMaxCap    = 2
	trMaxStarts = 16
	trMaxCrash  = 3
	trMaxSet    = 2
	trWalks     = 16
)

func procName(i int) string { return "p" + strconv.Itoa(i+1) }

type killed struct{}

const (
	stepGo = iota
	stepDie
	stepQuit
)

type tproc struct {
	name        string
	pc          string // "idle", "wait", "paused", "run", "between", "done"
	kind        string // "work" or "ci"
	prio        int    // 1 ("normal") or 2 ("high")
	cpu, mem    int
	tag         bool
	stepsLeft   int
	wasPaused   bool
	parked      bool
	clockOffset time.Duration
	holding     *activeHold
	resume      chan int
	yield       chan string // "parked", "held", "refused", "died"
}

type twalk struct {
	t       *testing.T
	dir     string
	procs   []*tproc
	mu      sync.Mutex
	ex      map[string]string
	sh      map[string]map[string]bool
	clock   time.Time
	starts  int
	crashes int
	sets    int
	recs    []string
}

type traceWatcher struct {
	w *twalk
	p *tproc
}

func (tw traceWatcher) drain() {}
func (tw traceWatcher) close() {}
func (tw traceWatcher) wait(_ []int, _ time.Duration) {
	tw.w.yieldProc(tw.p, "parked")
}

func newWalk(t *testing.T) *twalk {
	w := &twalk{
		t:     t,
		dir:   t.TempDir(),
		ex:    map[string]string{},
		sh:    map[string]map[string]bool{},
		clock: epoch,
	}
	for i := range trProcs {
		w.procs = append(w.procs, &tproc{
			name: procName(i), pc: "idle", kind: "work", prio: 1, cpu: 1, mem: 1,
		})
	}
	require.NoError(t, ensureLayout(w.dir, "--dir", "nova-reserve"))
	require.NoError(t, writeCapacity(w.dir, capRecord{
		Tags:        []string{"amd64", "linux"},
		UsableCores: 2,
		Resources:   map[string]int{"cpu": trMaxCap, "memory": trMaxCap},
		SetByPID:    1,
		SetAt:       epoch.Format(time.RFC3339),
	}, 1))
	return w
}

func (w *twalk) lockFor(owner string) tryLock {
	return func(path string, exclusive bool) (func(), bool, error) {
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) && !exclusive {
			return nil, false, os.ErrNotExist
		}
		if exclusive {
			if f, err := openIn(path, os.O_RDWR|os.O_CREATE); err == nil {
				_ = f.Close()
			}
		}
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.ex[path] != "" || (exclusive && len(w.sh[path]) > 0) {
			return nil, false, nil
		}
		if exclusive {
			w.ex[path] = owner
			var once sync.Once
			return func() {
				once.Do(func() {
					w.mu.Lock()
					if w.ex[path] == owner {
						delete(w.ex, path)
					}
					w.mu.Unlock()
				})
			}, true, nil
		}
		if w.sh[path] == nil {
			w.sh[path] = map[string]bool{}
		}
		w.sh[path][owner] = true
		var once sync.Once
		return func() {
			once.Do(func() {
				w.mu.Lock()
				delete(w.sh[path], owner)
				w.mu.Unlock()
			})
		}, true, nil
	}
}

func (w *twalk) worldFor(p *tproc, pid int) world {
	return world{
		env:     func(k string) string { return map[string]string{"PATH": "/usr/bin:/bin"}[k] },
		environ: func() []string { return nil },
		lock:    w.lockFor(p.name),
		watch:   func(string) watcher { return traceWatcher{w: w, p: p} },
		now:     func() time.Time { return w.clock.Add(p.clockOffset) },
		sleep:   func(time.Duration) {},
		resolve: func(name string) (string, error) { return "/bin/" + name, nil },
		exec:    func(string, []string, []string, bool) int { return 0 },
		waitEOF: func(io.Reader) {},
		running: func(int) bool { return true },
		bench:   func(int, bool) (int, int, error) { return 2, 2, nil },
		pid:     pid,
		cpus:    2,
		goos:    "linux",
		goarch:  "amd64",
		usable:  func() ([]int, error) { return []int{0, 1}, nil },
		groups:  func([]int) [][]int { return [][]int{{0}, {1}} },
		pin:     func([]int) error { return nil },
		stdin:   strings.NewReader(""),
		stdout:  &bytes.Buffer{},
		stderr:  &bytes.Buffer{},
	}
}

func (w *twalk) yieldProc(p *tproc, status string) {
	p.yield <- status
	switch <-p.resume {
	case stepDie:
		panic(killed{})
	case stepQuit:
		panic(killed{})
	}
}

// launchAcquire starts p running acquire in a parked goroutine and runs its first step.
func (w *twalk) launchAcquire(p *tproc, o options) string {
	p.resume = make(chan int)
	p.yield = make(chan string)
	wd := w.worldFor(p, 100+len(p.name))
	go func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(killed); !ok {
					panic(r)
				}
				p.yield <- "died"
			}
		}()
		<-p.resume
		ah, err := acquire(wd, o)
		if err != nil {
			p.holding = nil
			p.yield <- "refused"
			return
		}
		p.holding = ah
		p.yield <- "held"
	}()
	p.resume <- stepGo
	res := <-p.yield
	p.parked = (res == "parked")
	return res
}

func (w *twalk) stepParked(p *tproc) string {
	p.resume <- stepGo
	res := <-p.yield
	p.parked = (res == "parked")
	return res
}

func (w *twalk) quitAll() {
	for _, p := range w.procs {
		if p.parked {
			p.parked = false
			p.resume <- stepQuit
			<-p.yield
		}
	}
}

// syncFromDisk updates process views off the actual files in w.dir and the lock table,
// and drains any continuing process whose hold was unpaused on disk by reconcileLocked.
func (w *twalk) syncFromDisk() {
	holdMap := map[string]holdRecord{}
	for _, seq := range seqNumbersIn(filepath.Join(w.dir, "holds")) {
		_, jp := seqFiles(w.dir, "holds", seq)
		if line, ok, err := readRecord(jp); err == nil && ok {
			var h holdRecord
			if json.Unmarshal(line, &h) == nil {
				holdMap[h.Why] = h
			}
		}
	}
	lineMap := map[string]waiterRecord{}
	for _, seq := range seqNumbersIn(filepath.Join(w.dir, "line")) {
		_, jp := seqFiles(w.dir, "line", seq)
		if line, ok, err := readRecord(jp); err == nil && ok {
			var wr waiterRecord
			if json.Unmarshal(line, &wr) == nil {
				lineMap[wr.Why] = wr
			}
		}
	}

	// If reconcileLocked unpaused a parked continuing consumer on disk, step its goroutine
	// so it picks up its activeHold handle cleanly without changing disk state.
	for _, p := range w.procs {
		if p.parked {
			if h, ok := holdMap[p.name]; ok && !h.Paused && h.BetweenUntil == 0 && w.hasLockFor(p.name) {
				res := w.stepParked(p)
				require.Equal(w.t, "held", res)
			}
		}
	}

	for _, p := range w.procs {
		if p.pc == "idle" || p.pc == "done" {
			p.kind, p.prio, p.cpu, p.mem, p.tag, p.stepsLeft, p.wasPaused, p.clockOffset = "work", 1, 1, 1, false, 0, false, 0
			continue
		}
		if h, ok := holdMap[p.name]; ok {
			if h.BetweenUntil > 0 {
				p.pc = "between"
			} else if w.hasLockFor(p.name) {
				if h.Paused {
					p.pc = "paused"
				} else {
					p.pc = "run"
					p.wasPaused = false
				}
			}
			continue
		}
		if _, ok := lineMap[p.name]; ok && w.hasLockFor(p.name) {
			p.pc = "wait"
		}
	}
}

func (w *twalk) hasLockFor(owner string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for path, holder := range w.ex {
		if holder == owner && filepath.Base(path) != "mutex" {
			return true
		}
	}
	return false
}

func (w *twalk) snapshot(action, who string) string {
	w.syncFromDisk()

	c, have, err := readCapacity(w.dir)
	require.NoError(w.t, err)
	require.True(w.t, have)

	var seqVal uint64
	if line, ok, err := readRecord(filepath.Join(w.dir, "seq")); err == nil && ok {
		seqVal, _ = strconv.ParseUint(strings.TrimSpace(string(line)), 10, 64)
	}

	holdSeqByProc := map[string]uint64{}
	holdInFlight := map[string]bool{}
	for _, seq := range seqNumbersIn(filepath.Join(w.dir, "holds")) {
		_, jp := seqFiles(w.dir, "holds", seq)
		if line, ok, err := readRecord(jp); err == nil && ok {
			var h holdRecord
			if json.Unmarshal(line, &h) == nil {
				holdSeqByProc[h.Why] = h.Seq
				if h.BetweenUntil == 0 && !h.WaitingStart && w.hasLockFor(h.Why) {
					holdInFlight[h.Why] = true
				}
			}
		}
	}
	lineSeqByProc := map[string]uint64{}
	for _, seq := range seqNumbersIn(filepath.Join(w.dir, "line")) {
		_, jp := seqFiles(w.dir, "line", seq)
		if line, ok, err := readRecord(jp); err == nil && ok {
			var wr waiterRecord
			if json.Unmarshal(line, &wr) == nil {
				lineSeqByProc[wr.Why] = wr.Seq
			}
		}
	}

	fn := func(f func(*tproc) string) string {
		var parts []string
		for _, p := range w.procs {
			parts = append(parts, p.name+" |-> "+f(p))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	tlaBool := func(b bool) string {
		if b {
			return "TRUE"
		}
		return "FALSE"
	}

	var locked, wp []string
	for _, p := range w.procs {
		if w.hasLockFor(p.name) {
			locked = append(locked, strconv.Quote(p.name))
		}
		if p.wasPaused {
			wp = append(wp, strconv.Quote(p.name))
		}
	}

	return fmt.Sprintf("R(%q, %q, %s, %s, %s, %s, %s, %s, %s, %d, %d, %s, %s, {%s}, [cpu |-> %d, memory |-> %d], %d, %d, {%s})",
		action,
		who,
		fn(func(p *tproc) string { return strconv.Quote(p.pc) }),
		fn(func(p *tproc) string { return strconv.Quote(p.kind) }),
		fn(func(p *tproc) string { return strconv.Itoa(p.prio) }),
		fn(func(p *tproc) string { return fmt.Sprintf("[cpu |-> %d, memory |-> %d]", p.cpu, p.mem) }),
		fn(func(p *tproc) string { return tlaBool(p.tag) }),
		fn(func(p *tproc) string { return strconv.Itoa(p.stepsLeft) }),
		fn(func(p *tproc) string { return tlaBool(holdInFlight[p.name]) }),
		w.starts,
		seqVal,
		fn(func(p *tproc) string { return strconv.FormatUint(holdSeqByProc[p.name], 10) }),
		fn(func(p *tproc) string { return strconv.FormatUint(lineSeqByProc[p.name], 10) }),
		strings.Join(locked, ", "),
		c.Resources["cpu"],
		c.Resources["memory"],
		w.crashes,
		w.sets,
		strings.Join(wp, ", "),
	)
}

func (w *twalk) record(action, who string) {
	w.recs = append(w.recs, w.snapshot(action, who))
}

func (w *twalk) doAcquireWork(p *tproc, prio, cpu, mem int, tag, nowait bool, steps int) {
	w.starts++
	p.kind, p.prio, p.cpu, p.mem, p.tag, p.stepsLeft = "work", prio, cpu, mem, tag, steps
	prioName := map[int]string{1: "normal", 2: "high"}[prio]
	spec := fmt.Sprintf("cpu %d, memory %d", cpu, mem)
	if tag {
		spec += ", metal"
	}
	args := []string{"hold", "--dir", w.dir, "--holder", "bench", "--consumer", p.name, "--priority", prioName, "--why", p.name, "--needs", spec}
	if nowait {
		args = append(args, "--nowait")
	} else {
		args = append(args, "--wait", "5s")
	}
	o, err := parse(args)
	require.NoError(w.t, err)
	res := w.launchAcquire(p, o)
	switch res {
	case "held":
		p.pc = "run"
	case "parked":
		p.pc = "wait"
	case "refused":
		p.pc = "done"
	}
	w.record("AcquireWork", p.name)
}

func (w *twalk) ciUsed() (int, int) {
	var cu, mu int
	for _, p := range w.procs {
		if p.pc == "run" && p.kind == "ci" && w.hasLockFor(p.name) {
			cu += p.cpu
			mu += p.mem
		}
	}
	return cu, mu
}

func (w *twalk) doAcquireCI(p *tproc, cpu, mem int, tag bool) bool {
	c, _, _ := readCapacity(w.dir)
	cu, mu := w.ciUsed()
	fits := !tag && cpu <= c.Resources["cpu"] && mem <= c.Resources["memory"]
	if fits && (cu+cpu > c.Resources["cpu"] || mu+mem > c.Resources["memory"]) {
		return false // R39a: concurrent CI jobs on a machine fit in its capacity
	}
	w.starts++
	p.kind, p.prio, p.cpu, p.mem, p.tag, p.stepsLeft = "ci", 2, cpu, mem, tag, 0
	spec := fmt.Sprintf("cpu %d, memory %d", cpu, mem)
	if tag {
		spec += ", metal"
	}
	o, err := parse([]string{"hold", "--dir", w.dir, "--holder", "ci", "--why", p.name, "--needs", spec})
	require.NoError(w.t, err)
	res := w.launchAcquire(p, o)
	if res == "held" {
		p.pc = "run"
	} else {
		p.pc = "done"
	}
	w.record("AcquireCI", p.name)
	return true
}

func (w *twalk) doFinish(p *tproc) {
	require.NotNil(w.t, p.holding)
	w.syncFromDisk()
	wasPaused := (p.pc == "paused")
	st := p.stepsLeft
	ah := p.holding
	p.holding = nil
	ah.release(st == 1)
	p.stepsLeft = 0
	switch {
	case st == 1 && wasPaused:
		p.pc = "between"
		p.wasPaused = true
	case st == 1:
		p.pc = "idle"
	default:
		p.pc = "done"
	}
	w.record("FinishStep", p.name)
}

func (w *twalk) doContinue(p *tproc) {
	prioName := map[int]string{1: "normal", 2: "high"}[p.prio]
	spec := fmt.Sprintf("cpu %d, memory %d", p.cpu, p.mem)
	o, err := parse([]string{"hold", "--dir", w.dir, "--holder", "bench", "--consumer", p.name, "--priority", prioName, "--why", p.name, "--wait", "5s", "--needs", spec})
	require.NoError(w.t, err)
	res := w.launchAcquire(p, o)
	switch res {
	case "held":
		p.pc = "run"
		p.wasPaused = false
	case "parked":
		p.pc = "paused"
	case "refused":
		p.pc = "done"
		p.wasPaused = false
	}
	w.record("ContinueStep", p.name)
}

func (w *twalk) doWakeWaiter(p *tproc) {
	before := w.recs[len(w.recs)-1]
	res := w.stepParked(p)
	switch res {
	case "held":
		p.pc = "run"
		w.record("WakeWaiter", p.name)
	case "refused":
		p.pc = "done"
		w.record("WakeWaiter", p.name)
	case "parked":
		// Check whether reaping/reconciling mutated disk state without promoting p.
		after := w.snapshot("Reap", "none")
		if stripAct(after) != stripAct(before) {
			w.recs = append(w.recs, after)
		}
	}
}

func stripAct(rec string) string {
	// Skip `R("Action", "who", ` prefix to compare state tuple.
	idx := strings.Index(rec, "[")
	if idx < 0 {
		return rec
	}
	return rec[idx:]
}

func (w *twalk) doTimeout(p *tproc) {
	p.clockOffset += 10 * time.Second
	res := w.stepParked(p)
	require.Equal(w.t, "refused", res)
	p.pc = "done"
	p.wasPaused = false
	w.record("Timeout", p.name)
}

func (w *twalk) doCrash(p *tproc) {
	if p.parked {
		p.parked = false
		p.resume <- stepDie
		<-p.yield
	}
	p.holding = nil
	w.mu.Lock()
	for path, holder := range w.ex {
		if holder == p.name {
			delete(w.ex, path)
		}
	}
	w.mu.Unlock()
	w.crashes++
	p.pc = "done"
	p.wasPaused = false
	w.record("Crash", p.name)
}

func (w *twalk) doSetCap(cpu, mem int) bool {
	c, _, _ := readCapacity(w.dir)
	if c.Resources["cpu"] == cpu && c.Resources["memory"] == mem {
		return false
	}
	w.sets++
	wd := w.worldFor(w.procs[0], 999)
	code := run([]string{"set", "--dir", w.dir, "--cpu", strconv.Itoa(cpu), "--memory", strconv.Itoa(mem)}, wd)
	require.Equal(w.t, 0, code)
	w.record("SetCap", "none")
	return true
}

func runSeededWalk(t *testing.T, seed uint64) *twalk {
	w := newWalk(t)
	w.record("Init", "none")
	r := rand.New(rand.NewPCG(seed, 7))

	// Scripted scenario on seed 1 guarantees ContinueStep, Reap, Timeout, and SetCap coverage.
	if seed == 1 {
		w.doAcquireWork(w.procs[0], 2, 2, 2, false, false, 1) // p1 runs step 1 (stepsLeft=1)
		w.doAcquireWork(w.procs[1], 1, 1, 1, false, false, 0) // p2 queues in line/
		w.doAcquireCI(w.procs[2], 2, 2, false)                // p3 CI pauses p1
		w.doFinish(w.procs[0])                                // p1 finishes step 1 paused -> between!
		w.doContinue(w.procs[0])                              // p1 attaches step 2 -> paused!
		w.doTimeout(w.procs[1])                               // p2 times out in line/
		w.doFinish(w.procs[2])                                // p3 CI finishes -> unpauses p1!
		w.doAcquireWork(w.procs[3], 1, 1, 1, false, false, 0) // p4 queues behind p1
		w.doCrash(w.procs[0])                                 // p1 crashes holding lock!
		w.doWakeWaiter(w.procs[3])                            // p4 wakes, reaps p1, and runs!
		w.doSetCap(1, 2)                                      // live set while p4 runs!
		w.doFinish(w.procs[3])
		w.quitAll()
		return w
	}

	for step := 0; step < 80; step++ {
		p := w.procs[r.IntN(len(w.procs))]
		switch act := r.IntN(12); {
		case act == 0 && w.crashes < trMaxCrash && (p.holding != nil || p.parked) && w.hasLockFor(p.name):
			w.doCrash(p)
		case act == 1 && w.sets < trMaxSet:
			w.doSetCap(1+r.IntN(trMaxCap), 1+r.IntN(trMaxCap))
		case p.pc == "between":
			w.doContinue(p)
		case p.pc == "idle" && w.starts < trMaxStarts:
			if r.IntN(4) == 0 {
				w.doAcquireCI(p, 1+r.IntN(trMaxCap+1), 1+r.IntN(trMaxCap), r.IntN(6) == 0)
			} else {
				w.doAcquireWork(p, 1+r.IntN(2), 1+r.IntN(trMaxCap+1), 1+r.IntN(trMaxCap), r.IntN(6) == 0, r.IntN(4) == 0, r.IntN(2))
			}
		case (p.pc == "run" || (p.pc == "paused" && p.holding != nil)) && r.IntN(2) == 0:
			w.doFinish(p)
		case p.pc == "wait" && p.parked:
			if r.IntN(5) == 0 {
				w.doTimeout(p)
			} else {
				w.doWakeWaiter(p)
			}
		}
	}
	w.quitAll()
	return w
}

// brokenOverCap constructs a witness walk where CI acquires without displacing p1,
// violating NeverOver / TraceAccepted.
func brokenOverCap(t *testing.T) []string {
	w := newWalk(t)
	w.record("Init", "none")
	w.doAcquireWork(w.procs[0], 1, 2, 2, false, false, 0)
	// Faulted CI record: p2 enters "run" while p1 stays "run" (total cpu 4 > cap 2)!
	bad := strings.Replace(
		w.recs[len(w.recs)-1],
		`R("AcquireWork", "p1", [p1 |-> "run", p2 |-> "idle"`,
		`R("AcquireCI", "p2", [p1 |-> "run", p2 |-> "run"`,
		1,
	)
	bad = strings.Replace(bad, `p2 |-> "work"`, `p2 |-> "ci"`, 1)
	bad = strings.Replace(bad, `p2 |-> 1, p3`, `p2 |-> 2, p3`, 1)
	bad = strings.Replace(bad, `p2 |-> FALSE, p3`, `p2 |-> TRUE, p3`, 2)
	bad = strings.Replace(bad, `, 1, 1, [p1 |-> 1, p2 |-> 0`, `, 2, 2, [p1 |-> 1, p2 |-> 2`, 1)
	bad = strings.Replace(bad, `{"p1"}`, `{"p1", "p2"}`, 1)
	w.quitAll()
	return append(slices.Clone(w.recs), bad)
}

// brokenCutPause constructs a witness walk where queued waiter p2 cuts into "run"
// while p1 is still paused, violating UnpauseFirst / TraceAccepted.
func brokenCutPause(t *testing.T) []string {
	w := newWalk(t)
	w.record("Init", "none")
	w.doAcquireWork(w.procs[0], 2, 2, 2, false, false, 0) // p1 holds cpu 2
	w.doAcquireWork(w.procs[1], 1, 1, 1, false, false, 0) // p2 waits in line/ for cpu 1
	w.doAcquireCI(w.procs[2], 1, 1, false)                // p3 CI holds cpu 1, pauses p1 (1 spare cpu!)
	// Faulted WakeWaiter: p2 grabs the 1 spare CPU while p1 is still paused!
	last := w.recs[len(w.recs)-1]
	bad := strings.Replace(last, `R("AcquireCI", "p3"`, `R("WakeWaiter", "p2"`, 1)
	bad = strings.Replace(bad, `p2 |-> "wait"`, `p2 |-> "run"`, 1)
	bad = strings.Replace(bad, `p2 |-> FALSE, p3 |-> TRUE`, `p2 |-> TRUE, p3 |-> TRUE`, 1)
	bad = strings.Replace(bad, `, 3, 3, [p1 |-> 1, p2 |-> 0, p3 |-> 3, p4 |-> 0], [p1 |-> 0, p2 |-> 2`, `, 3, 4, [p1 |-> 1, p2 |-> 4, p3 |-> 3, p4 |-> 0], [p1 |-> 0, p2 |-> 0`, 1)
	w.quitAll()
	return append(slices.Clone(w.recs), bad)
}

var traceActions = []string{
	"AcquireWork", "AcquireCI", "WakeWaiter", "FinishStep", "ContinueStep", "Timeout", "Crash", "SetCap",
}

const reserveLogHead = `(* Execution walks of cmd/nova-reserve/trace_test.go (TestTracesAreBehavioursOfTheModels), *)
(* read off the real capacity.json, holds/*.json, line/*.json, and lock table.             *)
(* ReserveTrace.tla verifies every walk is a valid behaviour of Reserve.tla, and rejects   *)
(* BrokenOverCap and BrokenCutPause. Rewrite with RESERVE_UPDATE_LOG=1.                    *)
R(a, p, pc, kind, prio, reqNeeds, reqTag, stepsLeft, inFlight, starts, seq, holdSeq, lineSeq, lockHeld, cap, crashes, sets, wasPausedStep) ==
    [a |-> a, p |-> p, pc |-> pc, kind |-> kind, prio |-> prio, reqNeeds |-> reqNeeds, reqTag |-> reqTag,
     stepsLeft |-> stepsLeft, inFlight |-> inFlight, starts |-> starts, seq |-> seq, holdSeq |-> holdSeq,
     lineSeq |-> lineSeq, lockHeld |-> lockHeld, cap |-> cap, crashes |-> crashes, sets |-> sets,
     wasPausedStep |-> wasPausedStep]`

func renderTraceModule(module, head string, defs map[string][][]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s MODULE %s %s\n%s\n", strings.Repeat("-", 30), module, strings.Repeat("-", 30), head)
	var keys []string
	for k := range defs {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, name := range keys {
		var walks []string
		for _, recs := range defs[name] {
			walks = append(walks, "<<\n        "+strings.Join(recs, ",\n        ")+"\n    >>")
		}
		fmt.Fprintf(&b, "%s == <<\n    %s\n>>\n", name, strings.Join(walks, ",\n    "))
	}
	b.WriteString(strings.Repeat("=", 77) + "\n")
	return b.String()
}

func TestTracesAreBehavioursOfTheModels(t *testing.T) {
	t.Parallel()
	var walks [][]string
	for s := range uint64(trWalks) {
		walks = append(walks, runSeededWalk(t, s+1).recs)
	}
	for _, act := range traceActions {
		found := slices.ContainsFunc(walks, func(recs []string) bool {
			return slices.ContainsFunc(recs, func(r string) bool {
				return strings.HasPrefix(r, fmt.Sprintf("R(%q,", act))
			})
		})
		require.True(t, found, "no walk exercised action %s", act)
	}

	got := renderTraceModule("ReserveLog", reserveLogHead, map[string][][]string{
		"Walks":          walks,
		"BrokenOverCap":  {brokenOverCap(t)},
		"BrokenCutPause": {brokenCutPause(t)},
	})
	const file = "tla/ReserveLog.tla"
	if os.Getenv("RESERVE_UPDATE_LOG") == "1" {
		require.NoError(t, os.WriteFile(file, []byte(got), 0o644))
		return
	}
	want, err := os.ReadFile(file)
	require.NoError(t, err)
	if string(want) != got {
		t.Errorf("%s drifted from the code; rewrite and check with TLC: RESERVE_UPDATE_LOG=1 go test -run TestTracesAreBehavioursOfTheModels ./cmd/nova-reserve && tests/tla/check.sh <jar> cmd/nova-reserve/tla", file)
	}
}
