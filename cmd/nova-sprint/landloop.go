package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// The server lands what the readers passed (run --land): the last of the sprint's
// writers outside the server was a coordinator's `land` run beside it, racing the
// tick's accepts for the merge queue: cards queue up and landings are refused
// round after round while both write. Here one landing runs
// at a time, in the server's own process: land's reads and its report take the server's
// line of control (cmdLand, a.serial), its git runs outside it, and nothing else writes
// the merge queue between them but the server itself.

// LandEvery is how often the loop looks for cards queued to merge.
const LandEvery = 2 * time.Second

// LandDeadline is how long a landing may run before the loop raises one judgment
// naming the stage it is in. The pass also abandons an earlier batch gate at
// this bound so a ready later stream can land, and a batch still waiting for a
// slot, the chain or another stream's gate of its base commit leaves that wait
// at the bound (landpass.go merges, acquireGate); the landing continues. A hand
// land of two cards finished in under eight minutes; each gate run still has
// its own landGoBudget.
const LandDeadline = 10 * time.Minute

// landLaneWho is the lander's name on a bench's Go lane, and the prefix of every holder it
// records there: a stream's gate holds as lander/<stream> (fork, laneWho), the base
// re-check as landLaneBase, so the parallel pass's forks are distinct holders and a sibling
// asking a bench one of them holds is queued, never granted by the other's name, and one
// fork's give never frees another's bench (docs/SPEC-SPRINT.md section 7). A lander that is
// neither (none today) holds as landLaneWho.
const landLaneWho = "lander"

// landLaneBase is the holder the base re-check's gate records on a bench's Go lane.
const landLaneBase = landLaneWho + "/base"

// landLoop runs one land cycle every LandEvery until ctx ends. Each cycle writes
// one line: LAND OK, LAND REFUSED, or LAND IDLE. A landing runs beside the loop,
// so a long gate still leaves a line.
func (a *app) landLoop(ctx context.Context, addr string, stdout io.Writer) {
	fmt.Fprintf(stdout, "LANDING every %s: land runs here for every stream with cards queued to merge, one landing at a time\n", LandEvery)
	var flight *landFlight
	for ctx.Err() == nil {
		flight = a.landCycle(ctx, addr, stdout, flight)
		if ctx.Err() != nil {
			break
		}
		a.sleep(LandEvery)
	}
}

// landOnce is a round's landing: land's exit code, and idle when the merge queue was
// read and held nothing.
func (a *app) landOnce(ctx context.Context, addr string, more []string, stdout io.Writer) (int, bool) {
	a.serial.Lock()
	queued, coordinator, err := a.queuedToMerge(ctx, addr)
	a.serial.Unlock()
	idle := err == nil && !queued
	var lines []string
	code := 0
	switch {
	case err != nil:
		lines, code = []string{"LAND FAILED the merge queue could not be read: " + oneline.Err(err) + "; nothing was landed, and the next round tries again; run: nova-sprint where"}, 2
	case queued && coordinator != "":
		var out, errb bytes.Buffer
		a.landLazy, a.landCtx = true, ctx
		code = a.cmdLand(append([]string{"--redis", addr, "--actor", coordinator}, more...), &out, &errb)
		a.landLazy, a.landCtx = false, nil
		// what landed (stdout's LAND lines, but its summary), and everything land said
		// was wrong (stderr: a refused or failed batch, a refusal before any batch, the
		// remedy)
		for _, line := range strings.Split(out.String(), "\n") {
			if strings.HasPrefix(line, "LAND ") && !strings.HasPrefix(line, "LAND DONE") {
				lines = append(lines, line)
			}
		}
		for _, line := range strings.Split(errb.String(), "\n") {
			if strings.TrimSpace(line) != "" {
				lines = append(lines, line)
			}
		}
	}
	said := ""
	if code != 0 {
		exe := a.executable
		if exe == nil {
			exe = os.Executable
		}
		if target, err := exe(); err == nil {
			if rolled, rerr := sprint.CheckRollbackOnLandFailure(target, fmt.Errorf("land failed: %d", code), a.now()); rolled {
				at := oneline.Field(a.now().Format("15:04:05"))
				fmt.Fprintf(stdout, "%s SERVER ROLLBACK land failed within switch window; rolled back to previous binary %s.prev\n", at, target)
			} else if rerr != nil {
				at := oneline.Field(a.now().Format("15:04:05"))
				fmt.Fprintf(stdout, "%s SERVER ROLLBACK ERROR: %s\n", at, oneline.Escape(rerr.Error()))
			}
		}
		said = strings.Join(lines, "\n")
		if said == a.landFailed {
			return code, idle // said when it began
		}
	}
	a.landFailed = said
	at := oneline.Field(a.now().Format("15:04:05"))
	for _, line := range lines {
		fmt.Fprintf(stdout, "%s %s\n", at, oneline.Escape(line))
	}
	return code, idle
}

// queuedToMerge says a stream has a card queued to merge, and names the sprint's
// coordinator, whose the landing is ("" when the sprint has none); err when the sprint
// could not be read, which says nothing of what is queued.
func (a *app) queuedToMerge(ctx context.Context, addr string) (queued bool, coordinator string, err error) {
	n, coordinator, _, err := a.mergeQueued(ctx, addr)
	return n > 0, coordinator, err
}

// mergeQueued counts the cards queued to merge, and names the coordinator and the
// first stream that has one. err when the sprint could not be read.
func (a *app) mergeQueued(ctx context.Context, addr string) (n int, coordinator, stream string, err error) {
	st, err := a.storeCtx(ctx, common{verb: "where", redis: addr})
	if err != nil {
		return 0, "", "", err
	}
	if coordinator, err = st.B.Coordinator(ctx); err != nil {
		return 0, "", "", err
	}
	s, err := st.Load(ctx, []string{sprint.Merge}, nil)
	if err != nil {
		return 0, "", "", err
	}
	for _, row := range s.Merge.Rows() {
		c := s.Merge.Count(row, sprint.Queued)
		if c <= 0 {
			continue
		}
		n += c
		if stream == "" {
			stream = row
		}
	}
	return n, coordinator, stream, nil
}

// landFlight is one landing the loop started and has not yet reported.
type landFlight struct {
	mu                 sync.Mutex
	queued             int
	step, proc         string
	stream, coord      string
	began              time.Time
	done, judged, idle bool
	out                []byte
	code               int
}

// landBeat is the loop's bench seam and the landing in flight.
type landBeat struct {
	mu        sync.Mutex
	hostName  func() (string, error)
	gateBench func(ctx context.Context, host, dir string, runs [][]string, withGit bool) (string, int, error)
	landMore  []string
	flight    *landFlight
	// pulse is one slot filled each land-loop cycle. A landing waiting to ask
	// for a Go lane again receives it. The loop's own clock is the wait; the
	// ask adds none.
	pulse chan struct{}
}

// landState is the loop's memory, made once.
func (a *app) landState() *landBeat {
	a.prune.mu.Lock()
	defer a.prune.mu.Unlock()
	if a.prune.beat == nil {
		a.prune.beat = &landBeat{pulse: make(chan struct{}, 1)}
	}
	return a.prune.beat
}

// landPulse fills the cycle's one slot. A slot already full is a pulse the
// landing has not taken; this cycle leaves it.
func (a *app) landPulse() {
	b := a.landState()
	b.mu.Lock()
	ch := b.pulse
	b.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

// gate is the test seam that runs a gate command in place of the bench.
func (b *landBeat) gate() func(context.Context, string, string, [][]string, bool) (string, int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.gateBench
}

// landStage records the stage the in-flight landing has reached.
func (a *app) landStage(step, proc string) {
	b := a.landState()
	b.mu.Lock()
	f := b.flight
	b.mu.Unlock()
	if f == nil {
		return
	}
	f.mu.Lock()
	f.step, f.proc = step, proc
	f.mu.Unlock()
}

// machineName is this machine's short host name, lower case, the fleet member
// the gate does not send work to. A test's hostName seam stands in for the OS.
func (a *app) machineName() string {
	b := a.landState()
	b.mu.Lock()
	fn := b.hostName
	b.mu.Unlock()
	var host string
	var err error
	if fn != nil {
		host, err = fn()
	} else {
		host, err = os.Hostname()
	}
	if err != nil || host == "" {
		return ""
	}
	host = strings.ToLower(host)
	if i := strings.IndexByte(host, '.'); i >= 0 {
		host = host[:i]
	}
	return host
}

// landCycle is one beat of the loop. A landing still running writes LAND IDLE.
// A landing that finished writes the lines land kept (LAND OK or LAND REFUSED),
// not also an IDLE line. Nothing queued writes LAND IDLE queued=0.
func (a *app) landCycle(ctx context.Context, addr string, stdout io.Writer, flight *landFlight) *landFlight {
	a.landPulse()
	if flight != nil {
		flight.mu.Lock()
		done, idle := flight.done, flight.idle
		flight.mu.Unlock()
		if !done {
			a.writeBeat(stdout, flight)
			a.raiseIfStuck(ctx, addr, flight)
			return flight
		}
		a.writeFlight(stdout, flight)
		a.clearFlight(flight)
		a.landAfter(ctx, idle, stdout)
		return nil
	}
	a.serial.Lock()
	n, coord, stream, err := a.mergeQueued(ctx, addr)
	a.serial.Unlock()
	if err != nil {
		// said once, by landOnce; an unreadable queue is not an idle one
		a.landOnce(ctx, addr, nil, stdout)
		a.landAfter(ctx, false, stdout)
		return nil
	}
	if n == 0 || coord == "" {
		a.writeIdle(stdout, n, "-", 0)
		a.landAfter(ctx, n == 0, stdout)
		return nil
	}
	f := &landFlight{queued: n, step: "land", proc: "nova-sprint land", stream: stream, coord: coord, began: a.now()}
	b := a.landState()
	b.mu.Lock()
	b.flight = f
	more := append([]string(nil), b.landMore...)
	b.mu.Unlock()
	go a.runFlight(ctx, addr, f, more)
	a.writeBeat(stdout, f)
	return f
}

// runFlight is the landing beside the loop. Its lines stay in the flight until
// the cycle that sees it done.
func (a *app) runFlight(ctx context.Context, addr string, f *landFlight, more []string) {
	var buf bytes.Buffer
	a.landLazy, a.landCtx = true, ctx
	code, idle := a.landOnce(ctx, addr, more, &buf)
	a.landLazy, a.landCtx = false, nil
	f.mu.Lock()
	f.done, f.code, f.idle, f.out = true, code, idle, append([]byte(nil), buf.Bytes()...)
	f.mu.Unlock()
}

// clearFlight forgets f once the loop has reported it.
func (a *app) clearFlight(f *landFlight) {
	b := a.landState()
	b.mu.Lock()
	if b.flight == f {
		b.flight = nil
	}
	b.mu.Unlock()
}

// writeBeat is the in-flight cycle's one line.
func (a *app) writeBeat(w io.Writer, f *landFlight) {
	f.mu.Lock()
	queued, step, began := f.queued, f.step, f.began
	f.mu.Unlock()
	a.writeIdle(w, queued, step, a.now().Sub(began))
}

// writeFlight is the cycle that reports a finished landing.
func (a *app) writeFlight(w io.Writer, f *landFlight) {
	f.mu.Lock()
	out := append([]byte(nil), f.out...)
	queued, step, began := f.queued, f.step, f.began
	f.mu.Unlock()
	if len(bytes.TrimSpace(out)) == 0 {
		a.writeIdle(w, queued, step, a.now().Sub(began))
		return
	}
	_, _ = w.Write(out) // ignored: the cycle's line is already formed; a short write is the pipe closing
}

// writeIdle is one LAND IDLE line, the cycle's beat when nothing has finished.
func (a *app) writeIdle(w io.Writer, queued int, step string, age time.Duration) {
	if step == "" {
		step = "-"
	}
	if age < 0 {
		age = 0
	}
	a.writeCycle(w, fmt.Sprintf("LAND IDLE queued=%d step=%s since=%s", queued, step, age.Round(time.Second)))
}

// writeCycle prefixes one cycle line the way the loop prefixes land's own lines.
func (a *app) writeCycle(w io.Writer, line string) {
	at := oneline.Field(a.now().Format("15:04:05"))
	fmt.Fprintf(w, "%s %s\n", at, oneline.Escape(line))
}

// raiseIfStuck writes one judgment when a landing with cards queued has run past
// LandDeadline. The line is tried, not waited on, so the beat is never stuck
// behind the landing. A write that does not land is tried again next cycle.
func (a *app) raiseIfStuck(ctx context.Context, addr string, f *landFlight) {
	f.mu.Lock()
	if f.judged || f.queued == 0 || a.now().Sub(f.began) < LandDeadline {
		f.mu.Unlock()
		return
	}
	step, proc, stream, coord := f.step, f.proc, f.stream, f.coord
	age := a.now().Sub(f.began).Round(time.Second)
	f.mu.Unlock()
	if proc == "" {
		proc = "nothing named"
	}
	if !a.serial.TryLock() {
		return
	}
	defer a.serial.Unlock()
	f.mu.Lock()
	if f.judged {
		f.mu.Unlock()
		return
	}
	f.mu.Unlock()
	st, err := a.storeCtx(ctx, common{verb: "land", redis: addr, actor: coord})
	if err != nil { // ignored: the judgment is tried again next cycle when the store cannot be opened
		return
	}
	n := sprint.Note{
		Kind:        sprint.Judgment,
		Type:        sprint.NOpStuck,
		Stream:      stream,
		StreamLevel: true,
		Who:         coord,
		Decisions:   append([]string(nil), sprint.Decisions[sprint.NOpStuck]...),
		What:        fmt.Sprintf("landing stuck at step=%s waiting on %s for %s", step, proc, age),
	}
	res, err := st.Run(ctx, store.NoteStep("land", n))
	if err != nil || len(res.Refused) > 0 {
		return
	}
	f.mu.Lock()
	f.judged = true
	f.mu.Unlock()
}

// landAfter is the cleanup and the promote step, never during a landing. The cleanup
// (landprune.go) runs when it is due: a round with nothing queued to merge, or PruneEvery
// branches waiting, and no failed cleanup waiting out PruneRetry; its PRUNE lines are
// printed as land's. A stop of the loop flushes nothing: what is still queued then stays
// on origin. Then it runs the promote step when one is armed (promote.go); nil arms
// nothing, so run --land does not open a pull request.
func (a *app) landAfter(ctx context.Context, idle bool, stdout io.Writer) {
	if a.prune.due(idle, a.now()) {
		at := oneline.Field(a.now().Format("15:04:05"))
		for _, r := range a.flushPrune(ctx, false) {
			fmt.Fprintf(stdout, "%s %s\n", at, oneline.Escape(r.line(false)))
		}
	}
	a.promoteOnTick(ctx, stdout)
}
