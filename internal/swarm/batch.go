package swarm

// Batch: scatter, wait, gather (SPEC-SWARM.md, "Batch: scatter, wait, gather").
//
// A batch is one id and one deadline held across n cards. Scatter starts one runner
// process per card; wait ends every card or the deadline, whatever comes first, and the
// machinery kills the stragglers rather than waiting for them; gather folds the batch into
// one bounded packet with no report body in it.
//
// A card's result is RESULT.md under <root>/<slot>/jobs/<label>/RESULT.md. Line 1 must be
// the contract line the card was admitted under -- line 1 of the card's own text file --
// and a result carrying it is done whatever the harness exit code was. A card that abstains
// in its own words, a wrong line 1 and a missing result are each an ABSTAIN row, and every
// ABSTAIN row names ONE reason token, so a coordinator never reads a RESULT to learn why
// (issue #461). Line 2 is the card's disposition and is the only finding-adjacent text the
// packet ever carries: one line, capped, one per card.
//
// Admission is per card (issue #529): a card refused at admission is one ABSTAIN row with
// reason=admission and the other cards run. A batch is never lost to one card's shape.
//
// A slot is held by one batch at a time (issue #457): <root>/<slot>/BATCH carries
// id=<batch> pid=<n> at=<stamp> from allocation to slot end, a live lock refuses that slot
// for the card that named it, and a stale lock -- the holder's pid is dead -- is taken over
// once, out loud.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// admitRefusalLine is the one place a card's admission refusal is written: ADMIT REFUSED
// <label> <why>, where <why> is the reason the card alone was refused -- a card shape
// against docs/WORKER-CARDS.md practice 17, or a repository it could not reach.
func admitRefusalLine(label, why string) string {
	return fmt.Sprintf("ADMIT REFUSED %s %s", oneline.Field(label), why)
}

// BatchInput is everything the batch scatter/wait/gather needs, held apart from the
// command-line parsing so a test can drive it with a fake runner script.
type BatchInput struct {
	ID       string        // the batch id, printed on the BATCH line
	Deadline time.Duration // the whole batch's own deadline
	Idle     time.Duration // per-card idle timeout: a card's log not growing this long is killed
	Cards    string        // path to the TSV: label \t slot \t model \t card-path
	Root     string        // the root a card's RESULT.md hangs under
	Runner   string        // the command, one process per card; "" runs `nova-swarm native` in this binary
	// Harness and Auth are the native path's own configuration, read only when Runner is
	// empty: with no runner script a local card runs through this binary's own `native`
	// verb (issue #636), the way a bench row's card already does.
	Harness string
	Auth    string
	// Self is the nova-swarm binary a runnerless batch runs `native` from. Empty resolves
	// this process's own executable, and then "nova-swarm" on PATH: a test driving Batch
	// directly is not nova-swarm, and a batch that ran the test binary ran nothing.
	Self string
	// Slots is the slot range this batch allocates from: "<lo>-<hi>", or "<n>" for 1-<n>.
	// Empty keeps the old behaviour -- allocation from 1 with no ceiling. The cards.tsv
	// slot column is optional in either case; a hand slot outside the range is that card's
	// own admission refusal (issue #618).
	Slots string
	// Worker is the worker description whose class gates admission: when its
	// class is public, a card cloning an unlisted repo is refused with
	// `CARD REFUSED reason=private-source ...` and never starts. The zero
	// Worker is paid and gates nothing, so callers without a description keep
	// today's behaviour byte for byte.
	Worker Worker
	// PullWait and PullPoll bound the pull that brings a remote card's files back: how
	// long to wait for RESULT.md to exist on the bench, and how often to ask. Zero takes
	// the spec's own numbers (30 s, one second), and the tests take short ones.
	PullWait time.Duration
	PullPoll time.Duration
	// SlotsStore and SlotOwner are the bench slot store a card's `nova-swarm native` takes
	// its one lease from, and the owner whose share it counts against (nova-tools#1546).
	// They are REQUIRED of any batch that launches native -- which is every batch without
	// a --runner of its own -- and the refusal is NoSlotsStoreRefusal, said once before a
	// card runs rather than once per card.
	//
	// THE PATH IS ON THE MACHINE THAT RUNS THE CARD. For a local card that is this
	// machine; for a bench row it is the bench, reached over ssh, and the path must exist
	// THERE. One batch spanning local and remote benches therefore wants a store path that
	// means the same thing on each, which today means naming the same absolute path on
	// every bench. Said plainly here because it is the seam a reader will meet.
	SlotsStore string
	SlotOwner  string
	Benches    string // path to the benches table; empty means no table is read
	Bench      string // comma-separated bench names to allocate the cards across; empty means local only
	Then       string // a follow-on command, run with sh -c only when every card is done; "" means none
	// MaxInflight is the per-route in-flight cap of nova-tools#917: at most this many of a
	// batch's cards run against one provider/model/key at a time, and the rest WAIT --
	// held, not refused, holding no process and no spend, with their deadlines not begun.
	// Zero is no cap, which is today's behaviour byte for byte.
	//
	// Measured 2026-09-17: above roughly 30-40 concurrent requests on one Muse key the tail
	// latency goes to infinity, and every card launched past that point burns its whole
	// deadline for nothing -- and is paid for.
	MaxInflight int
	// StallAfter is the FIRST-TOKEN deadline of the same issue: a card that has been running
	// this long and has produced nothing at all -- no byte of log, no turn in its harness
	// store -- is ended `ABSTAIN reason=stalled`.
	//
	// IT IS NOT THE IDLE TIMEOUT. `--idle` is about a card that WAS working and stopped, and
	// every signal it has needs a first sample to compare against; a card that never spoke
	// once has no such sample and is invisible to it. On 2026-09-17 a fresh known-answer
	// card hung for its entire 150 s deadline having produced no token at all, while
	// deepseek-flash on the same bench in the same second answered in 11 s. Zero leaves a
	// card to its deadline, which is today's behaviour.
	StallAfter time.Duration
	// THE ROUTE SEAM (Glenn 2026-09-19, SPEC-DECIDE "nova-decide route"). When
	// it is set, the model a card is dispatched with is the ladder's answer --
	// which MIND does this unit of work -- and not the string the fill script
	// wrote in the TSV. The TSV's model becomes the FALLBACK, which is exactly
	// today's behaviour (rule 5): it stands where there is no key, where the
	// provider refused, where the confidence is under the floor and where the
	// rung is a mind a card cannot be dispatched to. Every card carries one
	// receipt line saying which happened. nil leaves the TSV's model alone.
	Route  *RouteInput
	Stdout io.Writer
	Stderr io.Writer
	// clock is the batch's time source. nil means the real clock; a test injects a
	// manual one so the idle kill and the deadline are events it chooses, never the
	// machine's load (#916).
	clock batchClock
	// snapshot is the batch's process table reader. nil means the real kernel
	// table (newProcSnapshot); a test injects a fake reader so process CPU
	// activity and process tree lifecycle are deterministic events rather than
	// scheduler races.
	snapshot func() activitySnapshot
}

// batchClock is the batch's view of time: the idle window (Now), the whole-batch
// deadline (After) and the idle poll (NewTicker). It is the seam the batch tests
// inject so a kill is asserted on the code, not on how busy the machine is.
type batchClock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
	NewTicker(d time.Duration) (<-chan time.Time, func())
}

// realClock is the batch's time source in production.
type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
func (realClock) Sleep(d time.Duration)                  { time.Sleep(d) }
func (realClock) NewTicker(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
}

// batchCard is one admitted card, in TSV order. slot is zero while the card asked for a
// slot allocation (its slot column was empty or '-'), and a positive number once assignSlots
// has either kept the name the card asked for or allocated the lowest free one.
type batchCard struct {
	label    string
	slot     int
	bench    string // the bench this card runs on; empty names the local machine
	model    string
	cardPath string
	contract string // line 1 of the card's text, the line by which it was admitted
	admitWhy string // non-empty when this card alone was refused at admission; the reason
	// unit is the card's own typed evidence for the ladder, read from the card
	// text by CardUnit; routable is false where the card names no kind the
	// ladder knows, and such a card is never routed.
	unit     decide.Unit
	routable bool
	// receipt is the one ROUTE line this card carries once the route has
	// answered: which rung, at what confidence, on which model, and -- where
	// today's model stands -- why.
	receipt string
}

// maxHoldLines is the HOLD ceiling. A packet is BATCH + n card lines + HOLD lines, and the
// spec bounds the packet at n + 12 lines: one BATCH line and at most eleven HOLD lines.
const maxHoldLines = 11

// idlePollInterval is how often the batch re-reads a card's log size while waiting, in
// search of a card whose log has stopped growing. It is short enough that an idle kill lands
// close to the timeout and long enough that it does not busy-spin over n files.
const idlePollInterval = 100 * time.Millisecond

// Batch runs one batch through scatter, wait and gather and returns the process exit code:
// 0 only when every card was done and none held, 1 otherwise, 2 when the admission could
// not even be read.
func Batch(in BatchInput) int {
	clk := in.clock
	if clk == nil {
		clk = realClock{}
	}
	// The root is absolute AND symlink-resolved from here on: absolute alone left `/var/...`
	// and `/private/var/...` naming one directory two ways on darwin (issue #578).
	absroot, err := AbsResolved(in.Root)
	if err != nil {
		fmt.Fprintf(in.Stderr, "nova-swarm batch: %s\n", oneline.Err(err))
		return 2
	}
	in.Root = absroot
	cards, err := readCards(in.Cards)
	if err != nil {
		fmt.Fprintf(in.Stderr, "nova-swarm batch: %s\n", oneline.Err(err))
		return 2
	}
	if len(cards) == 0 {
		fmt.Fprintf(in.Stderr, "BATCH REFUSED: %s holds no card; a batch of no cards is a typo\n", oneline.Field(in.Cards))
		return 1
	}
	// #636: a local card with no --runner runs through this binary's own `native` verb, so
	// a caller needs a runner script only for a runner of its own. The harness is the one
	// thing native cannot derive: it comes from --harness, else from the benches table's
	// `local` row.
	if in.Runner == "" && in.Bench == "" && !anyCardNamesBench(cards) {
		if in.Harness == "" {
			in.Harness = localHarness(in.Benches, &in.Auth)
		}
		if in.Harness == "" {
			fmt.Fprintln(in.Stderr, "nova-swarm batch: --runner or --harness is required; with --harness <path> each card runs through this binary's own `nova-swarm native`, and a `local` row in --benches names one too; refusing to guess")
			return 2
		}
		if _, err := os.Stat(in.Harness); err != nil {
			fmt.Fprintf(in.Stderr, "nova-swarm batch: --harness %s: %s\n", oneline.Field(in.Harness), oneline.Err(err))
			return 2
		}
	}
	// A LAUNCH WITHOUT A LEASE IS REFUSED (nova-tools#1546). Every path out of this
	// function that starts a card starts it through `nova-swarm native` -- selfNative
	// locally, remoteRun on a bench -- and native now refuses without a slot store. The
	// batch says so ONCE, here, before a single card runs, rather than letting every card
	// fail one at a time with the same sentence. The remedy is the one native prints, word
	// for word: a caller who greps for it finds the same string wherever it came from.
	//
	// A batch with its own --runner launches no native and is not held to this: the runner
	// is somebody else's program and the bench cannot speak for what it takes.
	if in.Runner == "" && (in.SlotsStore == "" || in.SlotOwner == "") {
		fmt.Fprintln(in.Stderr, NoSlotsStoreRefusal)
		return 2
	}
	// Admission is per card: every refusal is said once, by name, and the card is scored
	// ABSTAIN reason=admission on the packet rather than taking the batch down with it.
	// THE PUBLIC-CLASS GATE (CARD-8390) runs here too, after the shape and repo
	// checks readCards already applied: a public-class worker's card cloning an
	// unlisted repo carries a private-source refusal and is said as CARD REFUSED.
	if in.Worker.IsPublic() {
		applyPublicGate(cards, in.Worker, in.Root)
	}
	for _, c := range cards {
		if c.admitWhy != "" {
			if IsPublicRefusal(c.admitWhy) {
				fmt.Fprintln(in.Stderr, "CARD REFUSED "+c.admitWhy)
				continue
			}
			fmt.Fprintln(in.Stderr, admitRefusalLine(c.label, c.admitWhy))
		}
	}
	benches := map[string]Bench{}
	if in.Bench != "" || anyCardNamesBench(cards) {
		var err error
		benches, err = allocateBenches(cards, in.Benches, in.Bench)
		if err != nil {
			fmt.Fprintln(in.Stderr, err)
			return 2
		}
	} else {
		lo, hi, err := ParseSlotRange(in.Slots)
		if err != nil {
			fmt.Fprintf(in.Stderr, "nova-swarm batch: %s\n", oneline.Err(err))
			return 2
		}
		if err := assignSlots(cards, in.Root, lo, hi, in.Stderr); err != nil {
			fmt.Fprintln(in.Stderr, err)
			return 1
		}
	}
	// Issue #457: a batch writes its own lock on every local slot it takes, so a slot already
	// in use is refused -- for the card that named it, per issue #529, never for the batch --
	// and a slot whose previous batch is dead is taken over, not left to collide.
	taken, err := takeSlots(in.Root, in.ID, cards, in.Stderr)
	if err != nil {
		fmt.Fprintf(in.Stderr, "nova-swarm batch: %s\n", oneline.Err(err))
		return 2
	}
	defer releaseSlots(in.Root, taken)

	// THE ROUTE, BEFORE A CARD IS ASSIGNED A MODEL. One decision per admitted
	// card, in process through internal/decide: which mind does this unit of
	// work. The answer names the model; today's model in the TSV is the
	// fallback; the receipt line says which of the two the card runs on.
	routeCards(in, cards)

	// scatter: one runner process per card, in TSV order. The job directory is made before
	// the process starts so a runner can write RESULT.md straight into place.
	type proc struct {
		cmd  *exec.Cmd
		slot int
		// scratch is the directory under the local root this card's files are read from:
		// <bench>-<n> for a remote card, whose files the pull below brings back, and the
		// bare <n> for a local one.
		scratch     string
		bench       string
		label       string
		idleLog     string    // the file the idle monitor watched; set only on an idle kill
		cpu         uint64    // the card's process tree's CPU time at the last sample; guarded by doneMu
		haveCPU     bool      // whether cpu holds a sample to compare against; guarded by doneMu
		done        bool      // guarded by doneMu
		idleKilled  bool      // guarded by doneMu
		deadKilled  bool      // killed at the batch deadline; guarded by doneMu
		stallKilled bool      // killed for never producing a first token (#917); guarded by doneMu
		launched    bool      // the child has been started; a card still held by the route cap has not
		launchedAt  time.Time // when it was started, which is when its stall clock begins
		moved       bool      // it has produced SOMETHING at least once; guarded by doneMu
		route       string    // the provider/model/key queue it was launched against
		rc          int       // the child's exit code; guarded by doneMu
		lastGrow    time.Time
		// pgid and started are the card's OWN process group and the identity retained for
		// it at launch (issue #640). They are read at `cmd.Start()` and never again from
		// the process, because a kill that asks a dying child for its group asks too late.
		pgid    int
		started string
		// killed is how many processes the reap of this card's group ended. It is set once,
		// after the reap, and it is what the ABSTAIN row prints; guarded by doneMu.
		killed int
	}
	var doneMu sync.Mutex
	// reapCardGroups ends the WHOLE PROCESS TREE of every card named and records how many
	// processes each reap took with it (issue #640).
	//
	// THE OLD KILL WAS `cmd.Process.Kill()`, which is SIGKILL to ONE PROCESS. A card is not
	// one process: it is the runner, the wall it opened, the harness inside it and whatever
	// the harness started, and none of those are the pid the batch was holding. So the batch
	// printed its line and left the tree running -- two nova-sandbox wrappers at 12:30
	// elapsed and five test binaries burning CPU, five to twelve minutes after
	// `BATCH TP1 n=6 done=0 abstain=6`, killed by hand.
	//
	// `Reap` is the house's own end-a-group: terminate, wait the grace, kill, and confirm.
	// The TERMINATE FIRST is load-bearing rather than polite -- the child of a runnerless
	// batch is `nova-swarm native`, which answers a TERM by reaping its own tree and folding
	// its usage before it goes (cmd/nova-swarm/native.go, nativeTermCh). A SIGKILL alone
	// would lose that fold.
	//
	// THE CARDS ARE REAPED CONCURRENTLY. Serially, the grace would be paid once per card and
	// a six-card batch would sit through six of them after its deadline had already fired.
	reapCardGroups := func(victims []*proc) {
		if len(victims) == 0 {
			return
		}
		var reapWG sync.WaitGroup
		for _, p := range victims {
			reapWG.Add(1)
			go func(p *proc) {
				defer reapWG.Done()
				n := groupSize(p.pgid, p.started)
				Reap(p.pgid, p.started, TerminateGrace)
				doneMu.Lock()
				p.killed = n
				doneMu.Unlock()
			}(p)
		}
		reapWG.Wait()
	}
	procs := make([]proc, len(cards))
	// THE LAUNCH IS GATED PER ROUTE (nova-tools#917). Every card takes one of its route's
	// slots before its process starts and gives it back when that process ends, so at most
	// `MaxInflight` of this batch's cards are ever in flight against one provider/model/key.
	// With no cap the gate admits everything at once and this is today's loop unchanged.
	//
	// THE WAITER IS STARTED HERE, at the launch, rather than in a second pass afterwards.
	// It has to be: the loop below now BLOCKS on the gate, and the thing that unblocks it is
	// a card ending -- which is what the waiter observes. A second pass after the loop would
	// be a pass that never runs.
	gate := newInflight(in.MaxInflight)
	defer gate.close()
	var wg sync.WaitGroup
	// One count held for the whole launch loop, so `allDone` cannot close on the first card
	// finishing while later cards are still held at the gate.
	wg.Add(1)
	launchDone := false
	defer func() {
		if !launchDone {
			wg.Done()
		}
	}()
	allDone := make(chan struct{})
	stopMonitor := make(chan struct{})
	var monitorWG sync.WaitGroup
	startMonitor := func() {
		if in.Idle <= 0 && in.StallAfter <= 0 {
			return
		}
		monitorWG.Add(1)
		go func() {
			defer monitorWG.Done()
			tickC, stopTicker := clk.NewTicker(idlePollInterval)
			defer stopTicker()
			lastSize := make([]int64, len(procs))
			var lastSample time.Time
			for {
				select {
				case <-stopMonitor:
					return
				case <-allDone:
					return
				case now := <-tickC:
					// The process table is read once per activity poll, outside the lock, and
					// every card is asked of that one snapshot (issue #593).
					var toReap []*proc
					var snap activitySnapshot
					var sampleSpan time.Duration
					if now.Sub(lastSample) >= activityInterval(in.Idle) {
						if in.snapshot != nil {
							snap = in.snapshot()
						} else {
							snap = newProcSnapshot()
						}
						sampleSpan = now.Sub(lastSample)
						lastSample = now
					}
					doneMu.Lock()
					for i := range procs {
						// A CARD STILL HELD AT THE ROUTE GATE HAS NOT STARTED, and nothing
						// about it can be idle or stalled: it has no process, its deadline
						// has not begun, and its clocks do not run. This monitor now starts
						// BEFORE the launch loop -- it has to, because the loop blocks on
						// the gate and the thing that unblocks it is a card ending, which
						// only this monitor can force -- so it sees cards that do not exist
						// yet, and it leaves them alone.
						if !procs[i].launched || procs[i].done || procs[i].idleKilled || procs[i].stallKilled {
							continue
						}
						size := logSize(cardLogPath(in.Root, procs[i].scratch, procs[i].label))
						if size > 0 {
							procs[i].moved = true
						}
						if size != lastSize[i] {
							lastSize[i] = size
							procs[i].moved = true
							procs[i].lastGrow = now
							continue
						}
						// A silent log is not a silent card: a harness inside a `go test` that
						// prints nothing for minutes is working, and its work is CPU its process
						// tree spent -- its children's as much as its own. A card is idle only
						// when NEITHER its log NOR its tree moved for the whole --idle window.
						if snap != nil && procs[i].cmd != nil && procs[i].cmd.Process != nil {
							if cpu, ok := snap.TreeCPU(procs[i].cmd.Process.Pid); ok {
								prev := procs[i].cpu
								grew := procs[i].haveCPU && cpu > prev
								// A tree whose CPU fell LOST a process since the last
								// sample: that process was alive and charged, and its
								// departure is work done, not the still, silent card
								// the idle timeout is for. It is also the card whose
								// runner has exited but not yet been reaped, whose CPU
								// can no longer be read (issue #916).
								shrank := procs[i].haveCPU && cpu < prev
								procs[i].cpu, procs[i].haveCPU = cpu, true
								// CPU growth is activity only when the tree spent a real
								// share of the sample interval working. Darwin counts
								// CPU in nanoseconds, so a process that only slept
								// still shows a few microseconds of runtime
								// bookkeeping between two samples, and under the
								// injected clock two polls can be a real microsecond
								// apart while the virtual window says seconds:
								// any-increment-at-all used to keep a silent card
								// alive on darwin where linux's coarse ticks saw zero
								// (issue #916). A tree that is working spends a large
								// fraction of the interval; one percent separates the
								// two by orders of magnitude on both platforms.
								if grew && cpu-prev >= uint64(sampleSpan)/100 {
									// A tree that is working has MOVED, so the
									// first-token check below never fires for a
									// card that is compiling in silence (#593).
									procs[i].moved = true
									procs[i].lastGrow = now
									continue
								}
								if shrank {
									procs[i].moved = true
									procs[i].lastGrow = now
									continue
								}
							}
						}
						// THE FIRST-TOKEN DEADLINE (nova-tools#917), and it is NOT the idle
						// timeout above. Every signal the idle window has needs a first
						// sample to compare against -- a log that grew, a tree whose CPU
						// advanced -- and a card that has never spoken once provides none
						// of them, so it is invisible to all of them and runs until the
						// batch deadline. On 2026-09-17 a fresh known-answer card did
						// exactly that for its whole 150 s while deepseek-flash on the same
						// bench in the same second answered in 11 s: the tier had queued it
						// and would never answer.
						//
						// So a card that has produced NOTHING AT ALL since it launched is
						// ended at `StallAfter`, and its reason names what happened rather
						// than borrowing the idle token: `ABSTAIN reason=stalled`. A card
						// that produced one byte and then went quiet is the idle window's
						// business, not this one's, and this check never fires for it.
						if in.StallAfter > 0 && !procs[i].moved &&
							now.Sub(procs[i].launchedAt) >= in.StallAfter {
							job := filepath.Join(in.Root, procs[i].scratch, "jobs", procs[i].label)
							if _, ok := FindCardResult(job); ok {
								continue
							}
							procs[i].stallKilled = true
							procs[i].idleLog = cardLogPath(in.Root, procs[i].scratch, procs[i].label)
							toReap = append(toReap, &procs[i])
							continue
						}
						if in.Idle > 0 && now.Sub(procs[i].lastGrow) >= in.Idle {
							// A card that already published a result is finishing, not
							// idle: its file exists and gather will score it by that
							// result, so the kill would only race the write and score
							// a half-written RESULT (issue #916).
							job := filepath.Join(in.Root, procs[i].scratch, "jobs", procs[i].label)
							if _, ok := FindCardResult(job); ok {
								continue
							}
							procs[i].idleKilled = true
							procs[i].idleLog = cardLogPath(in.Root, procs[i].scratch, procs[i].label)
							toReap = append(toReap, &procs[i])
						}
					}
					doneMu.Unlock()
					// THE REAP IS OUTSIDE THE LOCK, and that is not a tidiness: a reap waits
					// for the kernel to agree the group is gone, and the goroutine that
					// would mark this card done takes doneMu to do it. Reaping under the
					// lock would hold it for the whole grace and report every group as a
					// survivor.
					reapCardGroups(toReap)
				}
			}
		}()
	}
	// THE MONITOR RUNS DURING LAUNCHING ONLY WHEN LAUNCHING CAN BLOCK. With a route cap the
	// loop below waits at the gate, and the only thing that can free a slot is a card ending
	// -- which for a hung card is this monitor's doing, so it must already be running or the
	// batch deadlocks until its deadline. With NO cap the loop never waits, and the monitor
	// starts after it exactly as it always has: starting it earlier would change when every
	// existing card's first sample is taken, for no gain.
	if in.MaxInflight > 0 {
		startMonitor()
	}

	go func() { wg.Wait(); close(allDone) }()
	// THE DEADLINE IS ARMED ONCE, HERE, AND TWO THINGS WAIT ON IT. The launch loop below
	// blocks at the route gate, so the batch's own deadline has to be able to fire DURING
	// launching: without this, a batch whose first card hangs and whose cap is one would sit
	// in `acquire` with nothing watching the clock. When it fires, the gate is closed -- every
	// held card is released without launching, and each is scored `deadline` -- and the
	// select below, which waits on the same channel rather than arming a second timer, reaps
	// whatever is still running.
	deadline := make(chan struct{})
	go func() {
		select {
		case <-clk.After(in.Deadline):
			gate.close()
			close(deadline)
		case <-allDone:
		}
	}()
	for i, c := range cards {
		// A card refused at admission never starts: it is already its own ABSTAIN row.
		if c.admitWhy != "" {
			procs[i] = proc{slot: c.slot, label: c.label, done: true}
			continue
		}
		// THE GATE. This blocks while the card's route is full, and a card held here holds
		// nothing else: no process, no bench slot lease, no spend, and its deadline has not
		// begun. It returns false only once the batch has given up -- its own deadline
		// passed -- and then this card is never launched at all and is scored `deadline`.
		route := RouteKey(c.model, in.Auth)
		if !gate.acquire(route) {
			doneMu.Lock()
			procs[i] = proc{slot: c.slot, label: c.label, scratch: scratchName(c), bench: c.bench,
				done: true, deadKilled: true, route: route}
			doneMu.Unlock()
			continue
		}
		// A remote card's job directory sits under <root>/<bench>-<n>/jobs/<label>; a local
		// card's under <root>/<n>/jobs/<label>, as today.
		job := filepath.Join(in.Root, scratchName(c), "jobs", c.label)
		if err := os.MkdirAll(job, 0o755); err != nil {
			fmt.Fprintf(in.Stderr, "nova-swarm batch: %s\n", oneline.Err(err))
			return 2
		}
		// The card's ROUTE line travels with the job rather than living only in the
		// dispatcher's stderr: which rung the ladder answered, on which model, and --
		// where today's model stands -- why.
		writeRouteReceipt(job, c.receipt)
		// A card's log is the runner's own stdout pinned to a regular file under the job, the
		// way the spec records a job: harness.log. Idle means this file stopped growing.
		//
		// IT IS APPENDED TO, NEVER TRUNCATED (issue #608). Another process writes this same
		// file for the same card -- the supervisor the runner starts pins the harness's own
		// output to it -- and each holds its own offset. With O_TRUNC this file descriptor
		// starts at offset 0 and the runner's first line lands on top of whatever the other
		// writer has already put there, so the head of a card's evidence was overwritten by
		// the line announcing the run. O_APPEND makes every write land at the end, whoever
		// wrote last, and the file reads in the order it was written.
		logPath := filepath.Join(job, "harness.log")
		logFile, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
		if err != nil {
			fmt.Fprintf(in.Stderr, "nova-swarm batch: %s\n", oneline.Err(err))
			return 2
		}
		var cmd *exec.Cmd
		if c.bench != "" {
			// On a remote bench the batch builds the native command itself: ssh <host>
			// [taskset -c <core>] <root>/bin/nova-swarm native ..., with the card copied first.
			cmd, err = remoteRun(c, benches[c.bench], in.Root, int(in.Deadline.Seconds()), in.SlotsStore, in.SlotOwner, logFile)
			if err != nil {
				_ = logFile.Close()
				fmt.Fprintln(in.Stderr, err)
				return 2
			}
		} else if in.Runner == "" {
			// #636: no runner script, so this binary runs the card through its own `native`.
			cmd, err = selfNative(c, in, logFile)
			if err != nil {
				_ = logFile.Close()
				fmt.Fprintln(in.Stderr, err)
				return 2
			}
		} else {
			cmd = exec.Command(in.Runner, c.label, strconv.Itoa(c.slot), c.model, c.cardPath, in.Root)
			cmd.Env = append(os.Environ(), "NOVA_SWARM_ROOT="+in.Root, "NOVA_SWARM_JOB="+job)
			cmd.Stdout = logFile
			cmd.Stderr = logFile
		}
		// EVERY CARD'S CHILD IS A GROUP LEADER (issue #640), on all three launch shapes and
		// not just the two that already asked for it. `selfNative` and `remoteRun` set this
		// themselves; a `--runner <cmd>` child did not, so the batch had nothing to signal
		// but one pid -- and one pid is not a card. It is set HERE, at the one place every
		// shape passes through, so the kill below has one invariant to rely on rather than
		// three constructors to trust.
		ownGroup(cmd)
		if err := cmd.Start(); err != nil {
			_ = logFile.Close()
			fmt.Fprintf(in.Stderr, "nova-swarm batch: runner %s could not start for %s: %s\n",
				oneline.Field(in.Runner), oneline.Field(c.label), oneline.Err(err))
			return 2
		}
		_ = logFile.Close()
		// The group and its identity are RETAINED at launch, the way the supervisor retains
		// its harness's (`jobPgid := cmd.Process.Pid`, supervise.go:159) and `native` its
		// child's (native.go:469). The group id is the CHILD'S OWN PID and is never read
		// back with Getpgid: `ownGroup` makes the child the LEADER, so its group id is its
		// pid by construction, and a Getpgid in the parent can win the race against the
		// child's own setpgid and answer THIS PROCESS'S group -- which the reap below would
		// then terminate. Reading it later would also mean asking a process that may already
		// be a corpse, and on a platform that re-issues pids, signalling a stranger.
		pgid, started := 0, ""
		if cmd.Process != nil {
			pgid, started = cmd.Process.Pid, StartStamp(cmd.Process.Pid)
		}
		doneMu.Lock()
		procs[i] = proc{cmd: cmd, slot: c.slot, scratch: scratchName(c), bench: c.bench, label: c.label,
			lastGrow: clk.Now(), pgid: pgid, started: started,
			launched: true, launchedAt: clk.Now(), route: route}
		doneMu.Unlock()
		// THE WAITER, started at the launch. It marks the card done, records its exit code,
		// and GIVES THE ROUTE'S SLOT BACK -- exactly once per acquire, however the card
		// ended. A release that does not happen is a route that never launches again.
		wg.Add(1)
		go func(p *proc, route string) {
			defer wg.Done()
			err := p.cmd.Wait()
			doneMu.Lock()
			p.done = true
			if ee, ok := err.(*exec.ExitError); ok {
				p.rc = ee.ExitCode()
			} else if err == nil {
				p.rc = 0
			} else {
				p.rc = -1
			}
			doneMu.Unlock()
			gate.release(route)
		}(&procs[i], route)
	}
	// The launch loop is over: drop the count that kept `allDone` open across it.
	launchDone = true
	wg.Done()
	// The uncapped case, where the loop never waited and the monitor's first sample belongs
	// here, after every card has started, exactly where it has always been taken.
	if in.MaxInflight <= 0 {
		startMonitor()
	}

	// wait: every card ends, or the deadline. The wait is one select over one "all done"
	// signal and one timer; it never waits for a card past the deadline. Alongside it, when
	// --idle is set, one monitor re-reads each running card's own log -- <slot>/native.log
	// when the child wrote one, else the job's harness.log -- AND its process tree's CPU
	// time, and kills a card only when neither has moved for the idle window: a dead card is
	// removed from the wait, so the batch returns on its slowest still-working card rather
	// than burning the whole deadline, and a card whose harness is busy and silent -- a
	// `go test` that prints nothing for minutes -- is not a dead card (issue #593).

	select {
	case <-allDone:
	case <-deadline:
		var toReap []*proc
		doneMu.Lock()
		for i := range procs {
			if procs[i].done || procs[i].cmd == nil {
				continue
			}
			procs[i].deadKilled = true
			toReap = append(toReap, &procs[i])
		}
		doneMu.Unlock()
		// Outside the lock, for the reason given at the idle kill above.
		reapCardGroups(toReap)
	}
	close(stopMonitor)
	monitorWG.Wait()
	// Reap every child before gather reads their exit codes: a killed card's rc must be
	// settled before the gather decides whether a missing RESULT.md is a clean exit.
	wg.Wait()

	// THE PULL. A remote slot's files are on the bench, and the gather below reads the
	// local root: nothing is scored until what the card wrote has come back. It runs after
	// every child has been reaped, one card at a time, and a bench that could not be
	// reached marks its cards rather than failing the batch -- the other benches' cards are
	// still theirs to score.
	unreachable := make([]bool, len(cards))
	for i, c := range cards {
		// A card refused at admission never reached a bench: there is nothing to pull back.
		if c.bench == "" || c.admitWhy != "" {
			continue
		}
		b := benches[c.bench]
		err := pullFromBench(benchPull{
			host:       b.Host,
			remoteSlot: b.Root + "/" + strconv.Itoa(c.slot),
			remoteJob:  b.Root + "/" + strconv.Itoa(c.slot) + "/jobs/" + c.label,
			localJob:   filepath.Join(in.Root, scratchName(c), "jobs", c.label),
			label:      c.label,
			wait:       in.PullWait,
			poll:       in.PullPoll,
			notes:      in.Stderr,
		})
		if err != nil {
			unreachable[i] = isUnreachable(err)
			fmt.Fprintf(in.Stderr, "BATCH NOTE pull %s from bench %s: %s\n",
				oneline.Field(c.label), oneline.Field(c.bench), oneline.Err(err))
		}
	}

	// gather: fold every card into one bounded packet. A result whose line 1 is the card's
	// own line 1 is done, whatever the harness exit code was; every other card is an ABSTAIN
	// row that names ONE reason token (issue #461), so the packet is the whole read.
	idleSeconds := int(in.Idle.Seconds())
	var (
		done, abstain, idle, stalled, partial int
		holds                                 []string
		totalIn, totalOut                     int
		total                                 float64
	)
	type row struct {
		label    string
		slot     int
		state    string // "done" or "abstain"
		line2    string
		in       int
		out      int
		usd      float64
		hold     bool
		reason   string // the abstain's one reason token, with its own fields
		tail     string // one bounded field after log=<n>: the file watched, or the job directory
		logLines int
		// killed is issue #640's field: how many processes the batch ended in this card's
		// own group, and -1 for a card the batch never killed, whose row says nothing about
		// a kill. A count of 0 is a real answer -- the group was already gone -- and is
		// printed.
		killed int
	}
	rows := make([]row, len(cards))
	for i := range rows {
		// A row says nothing about a kill until the batch has made one (issue #640).
		rows[i].killed = -1
	}
	// THE STORE READS HAPPEN BEFORE THE GATHER WALK, AND IN PARALLEL (Fable's cold read of
	// this change). Each one runs a `sqlite3` under the usage reader's own 20-second timeout
	// plus its 2-second wait delay, and done inline, one card at a time, n reaped cards
	// would add up to 22n seconds to a gather that is otherwise all file reads -- three
	// minutes on an eight-slot batch that timed out. Running them together bounds the whole
	// pass at roughly one card's timeout, and the concurrency is capped because these are
	// processes, not goroutines, and a batch should not fork a sqlite3 per slot at once.
	spends := storeSpends(in.Root, cards, func(i int) bool {
		_, wrote := cardUsageRow(in.Root, scratchName(cards[i]), cards[i].label)
		return !wrote && cards[i].admitWhy == ""
	})
	for i, c := range cards {
		rows[i].label = c.label
		rows[i].slot = c.slot
		if procs[i].idleKilled || procs[i].deadKilled {
			rows[i].killed = procs[i].killed
		}
		// A card refused at admission never ran: no slot to read, and its reason is the
		// refusal itself.
		if c.admitWhy != "" {
			rows[i].state = "abstain"
			rows[i].reason = "admission " + c.admitWhy
			abstain++
			continue
		}
		// A CARD THE BATCH REAPED NEVER WROTE THAT FILE, AND IT IS NOT FREE. `usage.tsv` is
		// composed by `native` at the END of a run, so a card killed for idleness or at the
		// deadline leaves none, `readCardUsage` answers zeroes, and the BATCH line reports a
		// card that ran for minutes as `in=0 out=0 usd=0.0000` (Studio, 2026-09-19 14:58Z).
		// That is a NUMBER where there should be a measurement, and a shift that trusted it
		// would under-report its spend.
		//
		// The provider's own numbers are in the harness's store, which is where `native`
		// reads them from (#1712). When the card wrote no row, the batch reads the same
		// store the same way. THE ROW WINS WHENEVER IT EXISTS: `native` composed it from
		// this store with the run's own window, provider and model, and a second reader
		// summing the same database over the top of it would double-count a card that
		// already reported -- the same false ledger in the other direction.
		if row, ok := cardUsageRow(in.Root, scratchName(c), c.label); ok {
			rows[i].in, _ = row.Int("tokens_in")
			rows[i].out, _ = row.Int("tokens_out")
			rows[i].usd, _ = strconv.ParseFloat(strings.TrimSpace(row["usd"]), 64)
		} else {
			s := spends[i]
			rows[i].in, rows[i].out, rows[i].usd = s.in, s.out, s.usd
			if s.partial {
				partial++
			}
			// A READER THAT STOPPED IS NAMED. A locked database, a query that did not
			// answer, a missing sqlite3 -- each leaves this card at zero, and a zero nobody
			// was told about is the very fault this fallback exists to close.
			if s.reason != "" {
				fmt.Fprintf(in.Stderr, "BATCH NOTE %s store unread: %s\n",
					oneline.Field(c.label), oneline.Field(s.reason))
			}
		}
		totalIn += rows[i].in
		totalOut += rows[i].out
		total += rows[i].usd
		logPath := cardLogPath(in.Root, scratchName(c), c.label)
		rows[i].logLines = logOutputLines(logPath)
		// A card whose bench could not be reached is its own score, and the reason token
		// says which of the two it is: the bench never answered the pull, so nothing about
		// what the card did on it is known here (SPEC-SWARM, "Benches").
		if unreachable[i] {
			rows[i].state = "abstain"
			rows[i].reason = "bench-unreachable"
			abstain++
			continue
		}
		// A result the card wrote inside its clone is the card's result, not a missing one
		// (issue #594): it is copied up to the job root before the card is scored, and the
		// copy is said once on stderr so the packet's own bytes stay bounded by n.
		liftResult(filepath.Join(in.Root, scratchName(c), "jobs", c.label), c.label, in.Stderr)
		state, reason, tail, line2 := scoreCard(in.Root, c, procs[i].idleKilled, procs[i].deadKilled, procs[i].stallKilled, procs[i].rc, idleSeconds, logPath, procs[i].idleLog)
		rows[i].state, rows[i].reason, rows[i].tail, rows[i].line2 = state, reason, tail, line2
		// THE REPORT LINE. A wall death is named once, with the path the wall refused, the
		// step the card reached and -- when its clone holds commits past its base -- the
		// branch and count a harvester can still push. It goes to the notes, never the
		// packet, which stays bounded by n.
		if reason == "wall" {
			if raw, err := readRegular(logPath); err == nil {
				if wr, ok := WallRefused(raw); ok {
					branch, commits, _ := WallCommits(filepath.Join(in.Root, scratchName(c), "jobs", c.label, "repo"))
					fmt.Fprintln(in.Stderr, WallLine(c.label, wr, branch, commits))
				}
			}
		}
		if state == "done" {
			done++
			if strings.Contains(rows[i].line2, "HOLD") {
				rows[i].hold = true
				holds = append(holds, rows[i].line2)
			}
			continue
		}
		abstain++
		if strings.HasPrefix(reason, "idle=") {
			idle++
		}
		// A card that ended -- killed or abstained -- with no output after the wall opened is
		// a prompt or harness defect, not a slow model: it is named stalled by its own
		// log=0, and the BATCH line counts it. A card refused at admission never opened a
		// wall, and a job refused for size named its own class, so neither is a stall.
		if rows[i].logLines == 0 && reason != "input-limit" {
			stalled++
		}
	}

	// A batch whose cards ALL abstained with the SAME reason and NONE ran is a uniform
	// abstain, and the BATCH line says so (issue #618). This is the PIT-STOP shape -- a whole
	// batch lost before any harness opened -- and it is one signal, not n independent faults:
	// the manager policy escalates it at once and never requeues it.
	uniform := ""
	if done == 0 && abstain == len(cards) {
		token, same := "", true
		ran := false
		for _, r := range rows {
			fields := strings.Fields(r.reason)
			if len(fields) == 0 {
				same = false
				break
			}
			if token == "" {
				token = fields[0]
			} else if token != fields[0] {
				same = false
				break
			}
			if cardRan(fields[0]) {
				ran = true
			}
		}
		if same && !ran {
			uniform = token
		}
	}

	// The packet's grammar. The BATCH line first, then one line per card in admission
	// order (label, its resolved slot, then line 2 verbatim, or ABSTAIN with its one reason
	// token), then HOLD lines -- at most maxHoldLines -- so the whole packet never grows
	// past n + 12 lines whatever the batch holds.
	fmt.Fprintf(in.Stdout, "BATCH %s n=%d done=%d abstain=%d in=%d out=%d usd=%s idle=%d stalled=%d",
		oneline.Field(in.ID), len(cards), done, abstain, totalIn, totalOut, formatUSD(total), idle, stalled)
	// partial=<n> IS THE FLOOR SAYING IT IS A FLOOR. Every card whose numbers came from the
	// harness store rather than from its own usage row was killed mid-turn, and the turn in
	// flight carries no tokens object anywhere -- the provider charged for it and no
	// database on this machine holds the figure. The totals above are therefore at LEAST
	// what they say for those cards. The field is printed only when there is one, the way
	// benches= and uniform-abstain= are, so an ordinary batch's line is unchanged.
	if partial > 0 {
		fmt.Fprintf(in.Stdout, " partial=%d", partial)
	}
	if uniform != "" {
		fmt.Fprintf(in.Stdout, " uniform-abstain=%s", oneline.Field(uniform))
	}
	fmt.Fprintln(in.Stdout)
	// WHAT THE CAP DID, one line per route, only when there was a cap (#917). `peak` is the
	// most this launcher ever had in flight on that route and `held-back` is how many
	// launches had to wait for a slot: together they say whether the cap bound anything at
	// all, so a reader chasing a slow batch can rule it in or out without guessing.
	for _, line := range gate.statusLines() {
		fmt.Fprintln(in.Stdout, line)
	}
	for _, r := range rows {
		if r.state == "done" {
			line := fmt.Sprintf("%s slot=%d: %s log=%d", oneline.Field(r.label), r.slot, r.line2, r.logLines)
			if r.tail != "" {
				line += " " + r.tail
			}
			fmt.Fprintln(in.Stdout, line)
			continue
		}
		line := fmt.Sprintf("%s slot=%d: ABSTAIN reason=%s log=%d", oneline.Field(r.label), r.slot, r.reason, r.logLines)
		// HOW MANY PROCESSES THE KILL ENDED (issue #640). It is on the rows of cards the
		// batch killed and on no others, because a field that appears on every row is a
		// field nobody reads -- and the fault this closes is precisely a coordinator
		// believing a BATCH line that was not true of the machine.
		if r.killed >= 0 {
			line += " killed=" + strconv.Itoa(r.killed)
		}
		if r.tail != "" {
			line += " " + r.tail
		}
		fmt.Fprintln(in.Stdout, line)
	}
	for i := 0; i < len(holds) && i < maxHoldLines; i++ {
		fmt.Fprintf(in.Stdout, "HOLD: %s\n", oneline.Escape(oneline.Cap(holds[i], oneline.TailBytes)))
	}

	// --then is the follow-on that runs only when every card is done. One abstain, one
	// stalled card or one idle kill leaves the follow-on unrun: the batch prints one
	// SKIPPED line naming its counts and exits 3, proof the follow-on did not run on a
	// batch that was not all done (lesson 26).
	if in.Then != "" {
		if done == len(cards) && stalled == 0 && idle == 0 {
			cmd := exec.Command("sh", "-c", in.Then)
			cmd.Dir = in.Root
			cmd.Env = append(os.Environ(),
				"BATCH_ID="+in.ID,
				"BATCH_DONE="+strconv.Itoa(done),
				"BATCH_N="+strconv.Itoa(len(cards)))
			rc := 0
			if err := cmd.Run(); err != nil {
				if ee, ok := err.(*exec.ExitError); ok {
					rc = ee.ExitCode()
				} else {
					rc = -1
				}
			}
			fmt.Fprintf(in.Stdout, "BATCH THEN rc=%d\n", rc)
		} else {
			fmt.Fprintf(in.Stdout, "BATCH THEN SKIPPED done=%d n=%d abstain=%d stalled=%d\n",
				done, len(cards), abstain, stalled)
			return 3
		}
	}

	if abstain == 0 && len(holds) == 0 {
		return 0
	}
	return 1
}

// resultLiftDepth is how far below the job root gather looks for a result the card wrote
// somewhere else: repo/RESULT.md, and one directory down from there -- repo/<clone>/RESULT.md,
// the cwd of a model that cloned into its clone. Deeper is not searched: a result further
// down than that is a file the card left behind, not the result it published.
const resultLiftDepth = 2

// liftResult copies a card's RESULT.md up to the job root when the card wrote it inside its
// clone instead (issue #594). STEP 1 of a card makes repo/ the model's cwd, so the model
// publishes there; gather read only the job root, scored the card reason=no-result, and the
// work was lost. The job root wins whenever it holds a result of its own -- nothing is ever
// overwritten -- and the copy is said once on stderr, never in the packet, so the packet's
// bytes stay bounded by n. The RESULT contract is untouched: line 1 is still the card's
// contract line, and scoreCard still decides.
func liftResult(job, label string, notes io.Writer) {
	root := filepath.Join(job, "RESULT.md")
	if fileExists(root) {
		return
	}
	from, ok := FindCardResult(job)
	if !ok {
		return
	}
	raw, err := readRegular(from)
	if err != nil {
		return
	}
	if err := os.WriteFile(root, raw, 0o644); err != nil {
		return
	}
	if notes != nil {
		fmt.Fprintf(notes, "BATCH NOTE %s RESULT.md copied up from %s\n", oneline.Field(label), oneline.Field(from))
	}
}

// FindCardResult is THE ONE PLACE a card's published result is looked for: the job root
// first (ResultPath, the name the legacy runner's records use), then `repo/` and one
// directory below it, exactly as far as `liftResult` copies from (issue #594). It is
// exported because `native` asks the same question before the batch ever gathers -- whether
// the harness published anything at all (issue #591) -- and a second, shallower lookup there
// would call a card that published under `repo/` silent about a run that worked. One lookup,
// one answer, both sides.
func FindCardResult(job string) (string, bool) {
	if root := ResultPath(job); fileExists(root) {
		return root, true
	}
	return findResultBelow(job, resultLiftDepth)
}

// findResultBelow is the first RESULT.md under dir, breadth first and in name order, no
// deeper than depth directories down: repo/ is looked at before any other name, because
// repo/ is the directory the card's own STEP 1 makes. A symlinked directory is not followed
// -- the wall is not a wall if the thing outside it will fetch (regular.go).
func findResultBelow(dir string, depth int) (string, bool) {
	if depth <= 0 {
		return "", false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			dirs = append(dirs, e.Name())
		}
	}
	// repo/ first, then the rest in the order the directory was read (ReadDir sorts by name).
	for i, name := range dirs {
		if name == "repo" {
			dirs = append([]string{name}, append(dirs[:i:i], dirs[i+1:]...)...)
			break
		}
	}
	for _, name := range dirs {
		if p := filepath.Join(dir, name, "RESULT.md"); fileExists(p) {
			return p, true
		}
	}
	for _, name := range dirs {
		if p, ok := findResultBelow(filepath.Join(dir, name), depth-1); ok {
			return p, true
		}
	}
	return "", false
}

// scoreCard decides one card's state and, when it abstains, its ONE reason token
// (issue #461): line1-mismatch, no-result, harness-silent, rc=<n>, idle=<s>, deadline,
// result-after-deadline, card-abstain, admission -- plus input-limit, the provider's own
// structured class (issue #163). A result whose line 1 is the card's own line 1 is done
// WHATEVER the harness exit code was (issue #577): the contract decides, never the child's
// rc, which is recorded on the card's own NATIVE OK line either way. One exception to
// "whatever the harness exit code": a result that only landed because the deadline fired is
// `result-after-deadline`, not done -- the deadline is the one boundary the batch itself
// holds, and a late result is named for its lateness so a coordinator reads the token
// instead of the RESULT.
//
// THE ORDER, once the result has been looked for: a matching RESULT is the card's contract
// and wins over everything the machinery watched, because a card that published its line 1
// did its work however its process ended -- including an idle kill that raced the process's
// own exit (issue #916). Only when no result was published do the kill classes apply: the
// idle kill the monitor watched, then input-limit. Then the rest of the no-result ladder:
// fence, then deadline, then harness-silent, and only then the pair rc=<n> (ended non-zero)
// and no-result (ended clean), which are one slot split by the exit code. rc is never first:
// an exit code from a harness that never ran the card is nothing to go and read (issue #591).
// deadline is decided only once the result is known: no result means the card never finished,
// a matching result means it finished late. The tail is one bounded field the remedy needs --
// the log the idle monitor watched, or the job directory that holds no result -- printed
// after log=<n>, never in place of the token.
func scoreCard(root string, c batchCard, idleKilled, deadKilled, stallKilled bool, rc, idleSeconds int, logPath, idleLog string) (state, reason, tail, line2 string) {
	// A remote card's job came back under <root>/<bench>-<n>/jobs/<label>; a local card's
	// sits under <root>/<n>/jobs/<label>.
	job := filepath.Join(root, scratchName(c), "jobs", c.label)
	raw, err := readFileSteady(filepath.Join(job, "RESULT.md"))
	if err != nil {
		// A RESULT.md that is a symlink or a FIFO is refused by name, in the one line the
		// probe asserts (issue #233): it is not a result at all, and the refusal names the
		// path and the kind rather than being folded into the missing-result class.
		if errors.Is(err, errNotRegular) {
			return "abstain", "refused", err.Error(), ""
		}
		// A card that never produced a first token is `stalled`, and it is said BEFORE
		// the idle token because it is a different fact: `idle=<s>` is a card that was
		// working and stopped, and this one never started answering at all (#917).
		if stallKilled {
			watched := idleLog
			if watched == "" {
				watched = logPath
			}
			return "abstain", "stalled", "watched=" + watched, ""
		}
		if idleKilled {
			watched := idleLog
			if watched == "" {
				watched = logPath
			}
			return "abstain", fmt.Sprintf("idle=%d", idleSeconds), "watched=" + watched, ""
		}
		if cardEndsInputLimit(logPath) {
			return "abstain", "input-limit", "", ""
		}
		// A harness that wrote nothing at all did not run this card, and that is the first
		// thing to say about it: `harness-silent` comes before BOTH `no-result` and `rc=<n>`
		// (issue #591). `no-result` is a harness that ran and published nothing, which is the
		// model's own doing; `rc=<n>` is a harness that ran and ended badly; a silent harness
		// is neither, and its exit code -- 0 in the fault that wrote this rule -- says nothing
		// worth going to read. The exit code is still on the card's own NATIVE OK line.
		// A WALL DEATH BEFORE THE PLAIN FENCE (issue #918). When the run's own capture
		// holds the harness's raw `auto-rejecting` line, the death is `wall`: it names
		// the rejected path AND the commits ./repo kept, so the harvester can push them.
		if report, ok := WallDeath(job, c.label); ok {
			return "abstain", "wall", report, ""
		}
		// THE FENCE BEFORE EVERYTHING ELSE THE HARNESS DID (issue #644). When the harness's
		// own permission fence auto-rejected a path -- the card's `../scratch`, a read-only
		// /sys path on a bench with no wall -- the model was stopped by the MACHINERY, not
		// by its own judgement, and neither `no-result` (the model published nothing) nor
		// `harness-silent` (the harness never ran) is true of it. The token is read off the
		// card's own NATIVE OK line, never recomputed here, and stays its own token for a
		// runner that reported the rejection without the raw line the wall death reads.
		if p, ok := cardFenceRejected(job); ok {
			return "abstain", "fence", "path=" + p, ""
		}
		// AND THE WALL ITSELF (issue #644's follow-up): the harness's permission auto-reject
		// line, or the sandbox's own refusal, read straight out of the card's log. `native`
		// carries the fence on its NATIVE OK line, but the legacy supervisor does not, and a
		// wall death there was scored `no-result` -- the model blamed for machinery. The
		// result was already read above: a matching RESULT.md is done, and only the absence
		// of one reaches this line.
		if raw, err := readRegular(logPath); err == nil {
			if wr, ok := WallRefused(raw); ok {
				return "abstain", "wall", wallTail(wr), ""
			}
		}
		if deadKilled {
			return "abstain", "deadline", "", ""
		}
		if cardHarnessSilent(job) {
			return "abstain", "harness-silent", "job=" + job, ""
		}
		// A RUNNER THAT EXITED BEFORE THE HARNESS STARTED (issue #618). No `NATIVE` line in
		// the runner's own stdout and no harness capture is a run that never happened: the
		// non-zero exit code is the RUNNER's, and rc=<n> is reserved for the harness. The
		// runner's own last line is carried, bounded, because that is the only evidence of
		// why it refused -- the fault this rule closes was five batches of rc=2 with no log
		// and no coordinator looking until a person asked.
		if !cardHarnessStarted(job) && rc != 0 {
			last := runnerLastLine(filepath.Join(job, "harness.log"))
			tail := ""
			if last != "" {
				tail = "last=" + oneline.Escape(oneline.Cap(last, oneline.TailBytes))
			}
			return "abstain", "runner-refused", tail, ""
		}
		// A card that ran to a clean exit and published nothing named no result; a card that
		// ended non-zero names the code it ended with, which is the thing to go and read.
		if rc != 0 {
			return "abstain", fmt.Sprintf("rc=%d", rc), "job=" + job, ""
		}
		return "abstain", "no-result", "job=" + job, ""
	}
	lines := strings.Split(string(raw), "\n")
	one := strings.TrimSpace(first(lines))
	two := strings.TrimRight(second(lines), "\r\n")
	if strings.HasPrefix(one, "ABSTAIN") {
		return "abstain", "card-abstain", "", ""
	}
	// A card whose line 1 is a prefix of the RESULT line 1 is still this card: the card
	// generator truncates the issue title, so the worker's fuller first line begins with the
	// card's own contract line. That is done, with the extra chars named as tail=<n> on the
	// card line so a coordinator reads how much longer the worker's line ran. A first line
	// that differs before the end of the contract line is a different card and stays
	// line1-mismatch.
	cardLine := strings.TrimSpace(c.contract)
	if len(one) < len(cardLine) || !strings.EqualFold(one[:len(cardLine)], cardLine) {
		return "abstain", "line1-mismatch", "", ""
	}
	if strings.HasPrefix(strings.TrimSpace(two), "ABSTAIN") {
		return "abstain", "card-abstain", "", ""
	}
	// A matching result is done whatever the harness exit code was -- but one that only
	// arrived because the deadline fired is named for that timing, not scored done and not
	// scored a plain deadline (issue #461): the result-after-deadline card DID publish, and
	// the coordinator reads the token rather than opening the RESULT to learn it was late.
	if deadKilled {
		return "abstain", "result-after-deadline", "", ""
	}
	if extra := len(one) - len(cardLine); extra > 0 {
		return "done", "", "tail=" + strconv.Itoa(extra), two
	}
	return "done", "", "", two
}

// applyPublicGate refuses every card a public-class worker must never see: a
// card whose clone URLs are not all listed in <root>/public-repos.txt carries
// a private-source refusal and never starts. Cards already refused keep their
// first refusal; the gate never rewrites one.
func applyPublicGate(cards []batchCard, w Worker, root string) {
	for i := range cards {
		if cards[i].admitWhy != "" {
			continue
		}
		raw, err := os.ReadFile(cards[i].cardPath)
		if err != nil {
			continue
		}
		if repo, refused := CheckPublicCard(w, string(raw), root); refused {
			cards[i].admitWhy = PublicRefusalWhy(repo, w.Name)
		}
	}
}

// readCards reads the TSV and admits every card or none: one line that does not parse
// queues nothing at all, because a batch is all of its cards or none.
func readCards(path string) ([]batchCard, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("--cards wants a readable TSV of label, slot, model, card-path: %w", err)
	}
	var cards []batchCard
	for i, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 4 {
			return nil, fmt.Errorf("--cards line %d wants label<TAB>slot<TAB>model<TAB>card-path, got %d fields", i+1, len(parts))
		}
		slot := 0
		bench := ""
		if s := strings.TrimSpace(parts[1]); s != "" && s != "-" {
			if b, n, ok := strings.Cut(s, ":"); ok {
				// A bench slot is bench:<n>: slot n on that bench.
				v, err := strconv.Atoi(n)
				if err != nil || v < 1 || b == "" {
					return nil, fmt.Errorf("--cards line %d wants a bench slot bench:<n>, got %q", i+1, parts[1])
				}
				slot = v
				bench = b
			} else {
				n, err := strconv.Atoi(s)
				if err != nil || n < 1 {
					return nil, fmt.Errorf("--cards line %d wants a positive slot number, got %q", i+1, parts[1])
				}
				slot = n
			}
		}
		// THE LABEL IS A NAME (issue #1923). Every consumer of this column joins it into a
		// path: batch makes <root>/<scratch>/jobs/<label>, selfNative passes it to `native`
		// as --label, and native joins it again into the job directory, the temp directory
		// and the wall's write set. A TSV row spelling `../../../OUTSIDE` is a card naming
		// a directory outside the swarm root, so the name is judged here, at the parse,
		// where the line number can be named -- not per card at admission, because a label
		// that is a path is a malformed table rather than a card that abstains.
		if !safepath.NameOK(parts[0]) {
			return nil, fmt.Errorf("--cards line %d wants a label that is a name: letters, digits, dot, dash or underscore, no path separator and no \"..\", got %q", i+1, parts[0])
		}
		cardPath := parts[3]
		cardRaw, err := os.ReadFile(cardPath)
		if err != nil {
			return nil, fmt.Errorf("--cards line %d: %w", i+1, err)
		}
		contract := strings.TrimSpace(first(strings.Split(string(cardRaw), "\n")))
		if contract == "" {
			return nil, fmt.Errorf("--cards line %d: %s is empty; a card admits under line 1 of its text", i+1, cardPath)
		}
		// Admission is PER CARD (issue #529). A card whose shape is refused under practice
		// 17, or whose repositories are not reachable without credentials (lesson 7), carries
		// its own refusal and is scored ABSTAIN reason=admission; the batch's other cards run.
		// A batch of 35 cards once lost 34 of them to one card's quoted word.
		why := ""
		if reason := cardShapeFailure(parts[2], string(cardRaw)); reason != "" {
			why = "card-shape: " + reason
		} else if err := checkRepos(parts[0], string(cardRaw)); err != nil {
			var ar *admitRefusal
			if !errors.As(err, &ar) {
				return nil, err
			}
			why = ar.why
		}
		unit, routable := CardUnit(parts[0], contract, string(cardRaw))
		cards = append(cards, batchCard{
			label:    parts[0],
			slot:     slot,
			bench:    bench,
			model:    parts[2],
			cardPath: cardPath,
			contract: contract,
			admitWhy: why,
			unit:     unit,
			routable: routable,
		})
	}
	return cards, nil
}

// logSize is the byte length of a card's log file, or zero when the file is not there yet.
// Growth is the only signal the idle monitor trusts: a card that has written nothing, or has
// stopped writing, reads the same size twice and is on the clock.
func logSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// cardUsagePath resolves one card's usage.tsv: the job directory beside RESULT.md first,
// then the slot directory fallback (lesson 24: the BATCH line sums in/out/usd from the usage rows).
func cardUsagePath(root, scratch, label string) string {
	jobPath := filepath.Join(root, scratch, "jobs", label, "usage.tsv")
	if _, err := os.Stat(jobPath); err == nil {
		return jobPath
	}
	return filepath.Join(root, scratch, "usage.tsv")
}

// readCardUsage reads one card's usage.tsv -- the native run's header-plus-row -- and
// returns its tokens_in, tokens_out and usd, or zeroes when the file is absent.
func readCardUsage(path string) (in, out int, usd float64) {
	row, err := readUsageFile(path)
	if err != nil {
		return 0, 0, 0
	}
	in, _ = row.Int("tokens_in")
	out, _ = row.Int("tokens_out")
	usd, _ = strconv.ParseFloat(strings.TrimSpace(row["usd"]), 64)
	return in, out, usd
}

// cardEndsInputLimit reports whether a card's own log carries the structured signal the
// supervisor recorded when the job was refused for size -- `INPUT LIMIT class=… value=…
// limit=…` -- the FIELD the batch reads to score the card `reason=input-limit` rather than a
// plain abstain (issue #163). No prose rule is asked to decide it.
func cardEndsInputLimit(logPath string) bool {
	raw, err := os.ReadFile(logPath)
	if err != nil {
		return false
	}
	_, ok := ReadInputLimitSignal(raw)
	return ok
}

// cardHarnessSilent reports whether the runner's own `NATIVE OK` line said the harness left no
// record of itself: `harness=silent`, the token `native` writes when its capture holds no word
// of the child's and no result was found anywhere this gather would look (issue #591). THE
// TOKEN IS READ, NEVER RECOMPUTED: `native` looked at its own capture while the job directory
// held exactly what the child put there, and this side would be guessing from files the batch
// itself creates. The line is read out of the job's `harness.log` -- the file THIS process
// pins the runner's stdout to, never one `native` writes -- and that is where the line lands
// for a local card and a remote one alike (the ssh child's stdout comes back into it). A
// runner that prints no such line -- any runner but `native` -- is not silent here, only
// unsaid, and the card keeps the score it has today.
func cardHarnessSilent(job string) bool {
	raw, err := readRegular(filepath.Join(job, "harness.log"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if !isNativeVerdictLine(line) {
			continue
		}
		for _, tok := range strings.Fields(line) {
			if tok == "harness=silent" {
				return true
			}
		}
	}
	return false
}

// cardFenceRejected reports the path on the runner's own `NATIVE OK` line that the harness's
// fence auto-rejected: `fence=rejected path=<p>`, the field `native` writes when its capture
// holds the harness's own rejection (issue #644). THE TOKEN IS READ, NEVER RECOMPUTED, for
// the same reason harness=silent is: `native` looked at its own capture, and this side would
// be guessing. The line is read out of the job's `harness.log` -- the file THIS process pins
// the runner's stdout to -- so a remote card's line, which comes back through the same pipe,
// is read exactly as a local one is.
func cardFenceRejected(job string) (string, bool) {
	raw, err := readRegular(filepath.Join(job, "harness.log"))
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if !isNativeVerdictLine(line) {
			continue
		}
		fields := strings.Fields(line)
		for i, tok := range fields {
			if tok != "fence=rejected" {
				continue
			}
			if i+1 < len(fields) && strings.HasPrefix(fields[i+1], "path=") {
				return strings.TrimPrefix(fields[i+1], "path="), true
			}
			return "-", true
		}
	}
	return "", false
}

// isNativeVerdictLine reports whether a line is `native`'s own verdict line, in either of
// its two words. `NATIVE OK` used to be the only one; a run that produced nothing now says
// `NATIVE INCOMPLETE` instead (nova-tools #1844), and the tokens this file reads off that
// line -- harness=silent, fence=rejected -- are exactly as true on the incomplete one.
// Reading only "NATIVE OK " would have silently stopped seeing them for failed runs, which
// is the class of bug the rename exists to end.
func isNativeVerdictLine(line string) bool {
	return strings.HasPrefix(line, "NATIVE OK ") || strings.HasPrefix(line, "NATIVE INCOMPLETE ")
}

func formatUSD(n float64) string { return strconv.FormatFloat(n, 'f', 4, 64) }

// cardHarnessStarted reports whether the harness ever began: the runner's own stdout holds a
// `NATIVE` line, or the run left a harness capture, `<job>/harness-output.log` (issue #618).
// A runner that exits before this is runner-refused, whatever its exit code, and rc=<n> is
// left for a harness that ran.
func cardHarnessStarted(job string) bool {
	if fileExists(filepath.Join(job, "harness-output.log")) {
		return true
	}
	raw, err := readRegular(filepath.Join(job, "harness.log"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "NATIVE ") {
			return true
		}
	}
	return false
}

// runnerLastLine is the last non-blank line a runner wrote to its own stdout, the evidence a
// runner-refused card carries. The file is the job's `harness.log`, the file the batch pins
// the runner's stdout to -- never one the harness writes.
func runnerLastLine(path string) string {
	raw, err := readRegular(path)
	if err != nil {
		return ""
	}
	last := ""
	for _, line := range strings.Split(string(raw), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			last = s
		}
	}
	return last
}

// cardRan reports whether a card's harness could have started, from its reason token alone:
// the classes that mean "it never ran" are the pre-run refusals. Every other token means a
// harness opened -- so a batch of those is not the uniform-abstain PIT-STOP shape (issue #618).
func cardRan(token string) bool {
	switch token {
	case "runner-refused", "admission", "bench-unreachable", "harness-silent", "input-limit":
		return false
	}
	return true
}

// cardLogPath is the child's own log the gather counts: <slot>/native.log when the native
// run wrote one, else the job's harness.log (which carries only the runner's own stdout, the
// NATIVE OK line). Run 10's defect was counting a log the child never wrote: a card with a
// valid RESULT.md looked stalled because the gather counted a non-existent native.log under
// the job directory instead of the child's real log under the slot.
func cardLogPath(root, scratch, label string) string {
	native := filepath.Join(root, scratch, "native.log")
	if _, err := os.Stat(native); err == nil {
		return native
	}
	// A remote card's own log comes back from the bench as the job's native.log; a local
	// card that wrote none leaves the runner's harness.log, as today.
	if job := filepath.Join(root, scratch, "jobs", label, "native.log"); fileExists(job) {
		return job
	}
	return filepath.Join(root, scratch, "jobs", label, "harness.log")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// logOutputLines counts a card's own output lines: everything in the run log after the
// sandbox's own header lines, each beginning "SANDBOX ", is what the model wrote once the
// wall opened. A card that ends with none of them is a stall, so a missing log is zero too.
func logOutputLines(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "SANDBOX ") {
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		n++
	}
	return n
}

// slotDirName is the on-disk name of slot <n> under the root.
func slotDirName(n int) string { return strconv.Itoa(n) }

// slotDir is the directory of slot <n> under the root.
func slotDir(root string, n int) string { return filepath.Join(root, slotDirName(n)) }

// slotJobDir is a card's job directory under slot <n>: <root>/<n>/jobs/<label>.
func slotJobDir(root string, n int, label string) string {
	return filepath.Join(slotDir(root, n), "jobs", label)
}

// slotLocked reports whether slot <n> is busy: a <root>/<n>/BATCH lock held by a live
// batch (issue #457), or any lock under <root>/<n>/jobs/<any>/lock that carries a live pid.
func slotLocked(root string, n int) bool {
	if _, pid, ok := readBatchLock(root, n); ok && Alive(pid, "") {
		return true
	}
	entries, err := os.ReadDir(filepath.Join(slotDir(root, n), "jobs"))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(slotDir(root, n), "jobs", e.Name(), "lock"))
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil {
			continue
		}
		if Alive(pid, "") {
			return true
		}
	}
	return false
}

// busySlots collects the slot numbers that are busy on disk under the root.
func busySlots(root string) map[int]bool {
	out := map[int]bool{}
	entries, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		n, err := strconv.Atoi(e.Name())
		if err != nil || n < 1 {
			continue
		}
		if slotLocked(root, n) {
			out[n] = true
		}
	}
	return out
}

// assignSlots resolves every card's slot before any launch, from the batch's own range
// (issue #618). Every card that NAMED a slot keeps it, and a slot named by two cards is
// refused outright. Every card that asked for allocation (slot zero, an empty or '-' column)
// takes the lowest free slot in [lo,hi] -- free means neither busy on disk (a live lock) nor
// already reserved by another card in this batch. A hand slot outside [lo,hi] is refused AT
// ADMISSION, for that card alone, with `ADMIT REFUSED slot=<n> range=<lo>-<hi> card=<label>`,
// exactly where the spec says, never inside the runner where it costs a whole card's
// deadline. hi 0 means no ceiling: allocation runs on from lo as before.
//
// Hand slots are reserved FIRST, so the order of the TSV cannot decide whether a hand slot
// is honoured: an auto-allocated card never steals a slot another card named below it.
func assignSlots(cards []batchCard, root string, lo, hi int, notes io.Writer) error {
	busy := busySlots(root)
	assigned := map[int]string{}
	// Pass one: the cards that named a slot.
	for i := range cards {
		c := &cards[i]
		if c.slot == 0 {
			continue
		}
		if hi > 0 && (c.slot < lo || c.slot > hi) {
			c.admitWhy = fmt.Sprintf("slot=%d range=%d-%d", c.slot, lo, hi)
			fmt.Fprintf(notes, "ADMIT REFUSED slot=%d range=%d-%d card=%s\n", c.slot, lo, hi, oneline.Field(c.label))
			c.slot = 0
			continue
		}
		if prev, ok := assigned[c.slot]; ok {
			return fmt.Errorf("BATCH REFUSED slot %d named twice (%s, %s)", c.slot, prev, c.label)
		}
		assigned[c.slot] = c.label
	}
	// Pass two: the cards that asked for allocation, each the lowest free slot left.
	for i := range cards {
		c := &cards[i]
		if c.slot != 0 || c.admitWhy != "" {
			continue
		}
		n := lo
		for {
			if hi > 0 && n > hi {
				c.admitWhy = fmt.Sprintf("no-free-slot range=%d-%d", lo, hi)
				fmt.Fprintln(notes, admitRefusalLine(c.label, c.admitWhy))
				n = 0
				break
			}
			if busy[n] {
				n++
				continue
			}
			if _, ok := assigned[n]; ok {
				n++
				continue
			}
			break
		}
		c.slot = n
		if n > 0 {
			assigned[n] = c.label
		}
	}
	return nil
}

// ParseSlotRange parses --slots: "" is no range at all (allocate from 1, no ceiling), "<n>"
// is 1-<n>, and "<lo>-<hi>" is itself. lo is where allocation starts and hi is the last slot
// it may take; hi 0 means no ceiling.
func ParseSlotRange(s string) (lo, hi int, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 1, 0, nil
	}
	if a, b, ok := strings.Cut(s, "-"); ok {
		l, err1 := strconv.Atoi(strings.TrimSpace(a))
		h, err2 := strconv.Atoi(strings.TrimSpace(b))
		if err1 != nil || err2 != nil || l < 1 || h < l {
			return 0, 0, fmt.Errorf("--slots wants <lo>-<hi> with 1 <= lo <= hi, got %q", s)
		}
		return l, h, nil
	}
	n, e := strconv.Atoi(s)
	if e != nil || n < 1 {
		return 0, 0, fmt.Errorf("--slots wants a slot count or a <lo>-<hi> range, got %q", s)
	}
	return 1, n, nil
}

// localHarness reads the benches table's `local` row for the harness (and, when the caller
// named none, the auth) a runnerless batch runs native with. It returns "" when there is no
// table or no local row: the caller's refusal says so.
func localHarness(benchesPath string, auth *string) string {
	if strings.TrimSpace(benchesPath) == "" {
		return ""
	}
	table, err := LoadBenchTable(benchesPath)
	if err != nil {
		return ""
	}
	for _, b := range table {
		if b.Name != "local" {
			continue
		}
		if auth != nil && *auth == "" {
			*auth = b.Auth
		}
		return b.Harness
	}
	return ""
}

// swarmSelf resolves the nova-swarm binary a runnerless batch runs native from: the caller's
// own choice, else this process when it is nova-swarm itself, else nova-swarm on PATH.
func swarmSelf(named string) (string, error) {
	if strings.TrimSpace(named) != "" {
		return named, nil
	}
	if exe, err := os.Executable(); err == nil && strings.HasPrefix(filepath.Base(exe), "nova-swarm") && !strings.HasSuffix(exe, ".test") {
		return exe, nil
	}
	if path, err := exec.LookPath("nova-swarm"); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("nova-swarm batch: no nova-swarm binary to run `native` from; pass --runner <cmd>, or put nova-swarm on PATH")
}

// selfNative builds the command a local card runs when the caller named no runner: this
// binary's own `native` verb, with the same arguments bin/nova-native-runner.sh passed by
// hand (issue #636). The slot argument is the SLOT directory, not the job directory: native
// makes <slot>/jobs/<label> itself.
func selfNative(c batchCard, in BatchInput, logFile *os.File) (*exec.Cmd, error) {
	self, err := swarmSelf(in.Self)
	if err != nil {
		return nil, err
	}
	// The bench slot lease travels with the launch (nova-tools#1546): native refuses
	// without it, and Batch has already refused the whole batch if it is not here, so a
	// missing store at THIS point would be a plumbing bug and not a caller error.
	if in.SlotsStore == "" || in.SlotOwner == "" {
		return nil, fmt.Errorf("%s", NoSlotsStoreRefusal)
	}
	argv := []string{"native",
		"--harness", in.Harness,
		"--model", c.model,
		"--label", c.label,
		"--card", c.cardPath,
		"--slot", filepath.Join(in.Root, strconv.Itoa(c.slot)),
		"--root", in.Root,
		"--deadline", strconv.Itoa(int(in.Deadline.Seconds())) + "s",
		"--slots-store", in.SlotsStore,
		"--owner", in.SlotOwner,
	}
	if in.Auth != "" {
		argv = append(argv, "--auth", in.Auth)
	}
	cmd := exec.Command(self, argv...)
	cmd.Env = append(os.Environ(), "NOVA_SWARM_ROOT="+in.Root)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	ownGroup(cmd)
	return cmd, nil
}

// batchLockPath is the lock a batch writes on a slot it takes: <root>/<slot>/BATCH, one line
// `id=<batch> pid=<n> at=<stamp>`, written at allocation and removed at slot end. The batch
// name travels in the lock so a later batch can say who held the slot it is refusing.
func batchLockPath(root string, slot int) string {
	return filepath.Join(slotDir(root, slot), "BATCH")
}

// readBatchLock reads one batch lock into its id and pid. ok is false when there is no lock.
func readBatchLock(root string, slot int) (id string, pid int, ok bool) {
	raw, err := os.ReadFile(batchLockPath(root, slot))
	if err != nil {
		return "", 0, false
	}
	for _, f := range strings.Fields(string(raw)) {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			continue
		}
		switch k {
		case "id":
			id = v
		case "pid":
			pid, _ = strconv.Atoi(v)
		}
	}
	return id, pid, true
}

// takeSlots admits every local slot the batch was assigned and returns the slots it took.
// A slot whose BATCH lock carries a live pid is refused for THE CARD THAT NAMED IT -- ADMIT
// REFUSED slot=<n> held-by=<id> pid=<n>, that card alone abstaining with reason=admission
// (issue #529) -- and every other card runs; a slot whose lock pid is dead is taken over with
// one BATCH NOTE line. Each slot the batch cleared then carries this batch's own lock until
// slot end. An error is the filesystem refusing, which is the batch's own exit 2.
//
// Slots are distinct within a batch (assignSlots refused any named twice), so each card's
// slot is taken once.
func takeSlots(root, id string, cards []batchCard, note io.Writer) ([]int, error) {
	pid := os.Getpid()
	at := Stamp(time.Now())
	var taken []int
	for i := range cards {
		c := &cards[i]
		if c.admitWhy != "" || c.bench != "" || c.slot <= 0 {
			continue
		}
		heldBy, heldPid, ok := readBatchLock(root, c.slot)
		if ok && Alive(heldPid, "") {
			// The holder is alive: this card abstains and the lock is left exactly as it is.
			c.admitWhy = fmt.Sprintf("slot=%d held-by=%s pid=%d", c.slot, oneline.Field(heldBy), heldPid)
			fmt.Fprintln(note, "ADMIT REFUSED "+c.admitWhy)
			continue
		}
		if ok {
			fmt.Fprintf(note, "BATCH NOTE slot=%d stale-lock id=%s taken\n", c.slot, oneline.Field(heldBy))
		}
		if err := os.MkdirAll(slotDir(root, c.slot), 0o755); err != nil {
			return taken, err
		}
		line := fmt.Sprintf("id=%s pid=%d at=%s\n", oneline.Field(id), pid, at)
		if err := os.WriteFile(batchLockPath(root, c.slot), []byte(line), 0o644); err != nil {
			return taken, err
		}
		taken = append(taken, c.slot)
	}
	return taken, nil
}

// releaseSlots removes the BATCH lock this batch wrote, at slot end, on the slots it took
// and on no others: the live lock of a batch that refused one of our cards is never ours.
func releaseSlots(root string, taken []int) {
	for _, slot := range taken {
		_ = os.Remove(batchLockPath(root, slot))
	}
}

func first(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return lines[0]
}

func second(lines []string) string {
	if len(lines) < 2 {
		return ""
	}
	return lines[1]
}

// anyCardNamesBench reports whether any card's slot column named a bench, so a bench column
// alone takes the batch onto the bench path even with no --bench (which then refuses the
// unnamed bench).
func anyCardNamesBench(cards []batchCard) bool {
	for _, c := range cards {
		if c.bench != "" {
			return true
		}
	}
	return false
}

// splitBenchNames splits --bench's comma-separated list into names in deal order, dropping
// empty entries.
func splitBenchNames(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// allocateBenches resolves every card's bench and slot across the named benches, and checks
// each pinning bench's slot count against its cores, returning the ADMIT REFUSED message when
// a bench is over-subscribed. Local cards keep their bare slot numbers; a card naming a bench
// not in the table, or not in --bench, is refused. Unassigned cards are dealt round robin,
// each bench giving its lowest free slot; the local machine is never a pinning bench.
func allocateBenches(cards []batchCard, benchesPath, benchNames string) (map[string]Bench, error) {
	table, err := ReadBenchTable(benchesPath)
	if err != nil {
		return nil, err
	}
	names := splitBenchNames(benchNames)
	named := map[string]bool{}
	for _, n := range names {
		named[n] = true
	}
	type benchUse struct {
		slots int
		used  map[int]bool
	}
	use := map[string]*benchUse{}
	for _, n := range names {
		use[n] = &benchUse{used: map[int]bool{}}
	}
	localUsed := map[int]bool{}
	var unassigned []*batchCard
	for i := range cards {
		c := &cards[i]
		if c.bench != "" {
			if _, ok := table[c.bench]; !ok || !named[c.bench] {
				return nil, fmt.Errorf("BATCH REFUSED card %s names bench %s not in --bench", oneline.Field(c.label), oneline.Field(c.bench))
			}
			if c.slot != 0 {
				u := use[c.bench]
				if u.used[c.slot] {
					return nil, fmt.Errorf("BATCH REFUSED bench %s slot %d named twice", c.bench, c.slot)
				}
				u.used[c.slot] = true
				u.slots++
				continue
			}
			unassigned = append(unassigned, c)
			continue
		}
		if c.slot != 0 {
			if localUsed[c.slot] {
				return nil, fmt.Errorf("BATCH REFUSED slot %d named twice", c.slot)
			}
			localUsed[c.slot] = true
		} else {
			unassigned = append(unassigned, c)
		}
	}
	idx := 0
	for _, c := range unassigned {
		for tries := 0; tries < len(names); tries++ {
			n := names[idx%len(names)]
			idx++
			slot := 1
			if n == "local" {
				for localUsed[slot] {
					slot++
				}
				localUsed[slot] = true
				c.bench = ""
				c.slot = slot
				break
			}
			u := use[n]
			for u.used[slot] {
				slot++
			}
			u.used[slot] = true
			u.slots++
			c.bench = n
			c.slot = slot
			break
		}
	}
	for _, n := range names {
		if n == "local" {
			continue
		}
		b := table[n]
		count := coreCount(b.Cores)
		if count < 0 {
			continue
		}
		if use[n].slots > count {
			return nil, fmt.Errorf("ADMIT REFUSED bench=%s slots=%d cores=%d", b.Name, use[n].slots, count)
		}
	}
	return table, nil
}

// cardShapeFailure checks a card's shape at admission, and only for a DeepSeek model whose
// provider prefix is opencode/ or deepseek/. Mercury (inception/) cards are not checked.
// It returns the reason if the card is refused, or "" if the card's shape is acceptable.
// The refusal cites docs/WORKER-CARDS.md practice 17: a DeepSeek card wants a working
// directory and the clone as step 1, one command per line, numbered steps, the verdict
// vocabulary inside the step, the RESULT shape last and short, no capitalised contract
// block and no launcher text.
func cardShapeFailure(model, raw string) string {
	if !isDeepSeekModel(model) {
		return ""
	}
	lines := strings.Split(raw, "\n")
	step := "docs/WORKER-CARDS.md practice 17"
	if firstNonEmpty := firstNonEmptyLine(lines); lineIsCapitalsOnly(firstNonEmpty) {
		return "capitalised contract block (" + step + ")"
	}
	if !hasStep1(lines) {
		return "no 'STEP 1' line in the first 15 lines (" + step + ")"
	}
	if mentionsLauncher(lines) {
		return "'launcher' in the contract lines (lines 1-3) (" + step + ")"
	}
	if reason := cardPipelineFailure(raw); reason != "" {
		return reason
	}
	return ""
}

// isDeepSeekModel reports whether a model's provider prefix is opencode/ or deepseek/.
func isDeepSeekModel(model string) bool {
	prefix, _, ok := strings.Cut(model, "/")
	if !ok {
		return false
	}
	return prefix == "opencode" || prefix == "deepseek"
}

// firstNonEmptyLine is the first line whose trimmed form is not empty, or "" when every
// line is empty.
func firstNonEmptyLine(lines []string) string {
	for _, ln := range lines {
		if strings.TrimSpace(ln) != "" {
			return ln
		}
	}
	return ""
}

// lineIsCapitalsOnly reports whether a line holds at least one letter and no lowercase one:
// a capitalised contract block.
func lineIsCapitalsOnly(s string) bool {
	hasLetter := false
	for _, r := range s {
		if unicode.IsLetter(r) {
			hasLetter = true
			if unicode.IsLower(r) {
				return false
			}
		}
	}
	return hasLetter
}

// hasStep1 reports whether any of the first 15 lines begins "STEP 1".
func hasStep1(lines []string) bool {
	n := len(lines)
	if n > 15 {
		n = 15
	}
	for i := 0; i < n; i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "STEP 1") {
			return true
		}
	}
	return false
}

// mentionsLauncher reports whether the card's CONTRACT LINES -- lines 1-3: the contract
// line, the role line and STEP 1 -- mention "launcher". The check is the card's own
// instructions to the worker, never the text it quotes further down: a card quoting an
// issue that says "launcher" is a card about a launcher, not a card run by one, and
// refusing it cost a batch 34 cards (issue #529).
func mentionsLauncher(lines []string) bool {
	n := len(lines)
	if n > 3 {
		n = 3
	}
	for i := 0; i < n; i++ {
		if strings.Contains(strings.ToLower(lines[i]), "launcher") {
			return true
		}
	}
	return false
}

// routeCards is the ladder on the fill/launch path: one decision per admitted
// card, before that card is assigned a model.
//
// It is the whole of what Glenn asked for on 2026-09-19 -- "Are we all using
// Jev yet when selecting which model to send work to?" -- and it is written so
// the answer to that question is mechanical rather than a habit: with a route
// input set, NO card on this path gets its model from a person's hand without
// the ladder having been asked and the answer written down.
//
// Every card's receipt line is said once on stderr, in TSV order, and written
// into the card's own job directory as route.txt so it travels with the job.
// A card the ladder cannot type -- one whose text names no kind it knows -- is
// not routed at all, and its line says so rather than inventing evidence.
func routeCards(in BatchInput, cards []batchCard) {
	if in.Route == nil {
		return
	}
	if ok, why := in.Route.Accountable(); !ok {
		fmt.Fprintf(in.Stderr, "BATCH NOTE route: %s\n", why)
	}
	for i := range cards {
		c := &cards[i]
		if c.admitWhy != "" {
			continue // a card refused at admission never runs, so it is never routed
		}
		if !c.routable {
			c.receipt = fmt.Sprintf("ROUTE jev=fallback conf=0.00 rung=- model=%s why=%s",
				oneline.Field(c.model), oneline.Field("card-names-no-kind"))
			fmt.Fprintf(in.Stderr, "ROUTE %s %s\n", oneline.Field(c.label), c.receipt)
			continue
		}
		res := RouteCard(context.Background(), *in.Route, c.unit, c.model)
		c.model = res.Model
		c.receipt = res.Receipt
		fmt.Fprintf(in.Stderr, "ROUTE %s %s\n", oneline.Field(c.label), c.receipt)
	}
}

// writeRouteReceipt puts a card's ROUTE line in its job directory, beside the
// log the run writes, so the decision travels with the job rather than living
// only in the dispatcher's stderr. A receipt that cannot be written is not a
// reason not to run the card: the line has already been said out loud.
func writeRouteReceipt(jobDir, receipt string) {
	if strings.TrimSpace(receipt) == "" {
		return
	}
	_ = os.WriteFile(filepath.Join(jobDir, "route.txt"), []byte(receipt+"\n"), 0o644)
}

// groupSize is how many processes are in a card's group at the instant BEFORE it is ended,
// and it is the `killed=<n>` the ABSTAIN row prints (issue #640). It is read before the reap
// because after one there is, by construction, nothing left to count.
//
// WHAT THE NUMBER IS ON EACH PLATFORM, said here rather than implied. On linux the group is
// enumerated from /proc and the count is exact (proc_linux.go). On darwin and the BSDs it
// cannot be enumerated at all, so a group that is alive counts as ONE -- the leader, which
// is the only member this platform can prove -- and that is a FLOOR and never a guess at a
// tree's size. A group that is already gone counts as none, which is the honest answer for a
// deadline that fired on a card that had just finished.
//
// `self` is excluded by GroupMembers, and the batch is never in a card's group anyway: the
// child is the leader of its own.
func groupSize(pgid int, started string) int {
	if pgid <= 0 {
		return 0
	}
	if n, ok := GroupMembers(pgid, os.Getpid()); ok {
		return n
	}
	if GroupAlive(pgid, started) {
		return 1
	}
	return 0
}

// cardUsageRow is the row the card composed for itself, and whether there is one. It is the
// ONE resolver for that question: `cardUsagePath` above answers "which path would I read",
// and this answers "is there a row there, and is it THIS CARD'S". They were two spellings of
// the same walk and one of them was wrong.
//
// THE SLOT FALLBACK IS CHECKED AGAINST THE LABEL (Fable's cold read of #1739). A slot
// directory is reused across batches, so a `usage.tsv` left at the slot root by an EARLIER
// card is a file that exists, says nothing about this one, and -- before this check -- both
// answered "the card reported" and contributed a previous card's numbers. Every row carries a
// `job` column naming the label that wrote it, so the question has a mechanical answer. The
// job-directory row needs no such check: that directory is this card's.
func cardUsageRow(root, scratch, label string) (UsageRow, bool) {
	jobPath := filepath.Join(root, scratch, "jobs", label, "usage.tsv")
	if row, err := readUsageFile(jobPath); err == nil {
		return row, true
	}
	row, err := readUsageFile(filepath.Join(root, scratch, "usage.tsv"))
	if err != nil {
		return nil, false
	}
	if strings.TrimSpace(row["job"]) != label {
		return nil, false
	}
	return row, true
}

// storeCardSpend is what the provider billed a card that never got to write its own row: the
// same harness store `native` samples (#1712), under the card's own data home. It returns the
// numbers, whether they are a LOWER BOUND, and the reason no number could be had.
//
// THE WINDOW IS THE CARD'S WHOLE LIFE, and that is not a widening. `native` brackets its read
// with the run's own timestamps because one data home can hold more than one ATTEMPT of the
// same job; this data home is `<root>/<scratch>/data`, made for this card in this batch, and
// every turn in it is this card's. A window narrower than the card would drop turns it was
// billed for, which is the very failure this reader exists to close.
//
// WHAT COMES BACK IS A LOWER BOUND, ALWAYS (Fable's cold read of #1739). The card was killed
// mid-turn; the assistant row for the turn in flight carries no `tokens` object, so the
// provider's charge for it is in nobody's database. A reaped card's spend is at least this,
// and the BATCH line says so rather than presenting a floor as a total.
//
// A STORE THAT COULD NOT BE READ IS NAMED, NEVER SILENT. `ReadCardUsage` maps a locked
// database, a timed-out query and a missing sqlite3 all onto a reason, and swallowing it
// would put this change's own fault -- a silent zero for a card that spent -- straight back.
// The reason is returned for the caller to print.
func storeCardSpend(root, scratch string) (in, out int, usd float64, partial bool, reason string) {
	usage, _, _, why := ReadCardUsage(filepath.Join(root, scratch, "data"),
		time.UnixMilli(0), time.Now())
	if why != "" {
		// TWO OF THE FOUR REASONS ARE ABSENCES AND ARE SILENT. `no-store` is a harness that
		// never wrote a database; `no-rows` is one that wrote the schema and was killed
		// before its first answer -- a store READ PERFECTLY that holds nothing. Neither is
		// a reader that stopped, and a note on either would fire on every card reaped early
		// and teach its readers to scroll past the line. Then the note that matters -- a
		// locked database, a query past its timeout -- goes unread with them.
		//
		// `no-sqlite3` and `query-failed` ARE readers that stopped, and those are said out
		// loud. They were one token with `no-rows` until 2026-09-19.
		if why == "no-store" || why == "no-rows" {
			return 0, 0, 0, false, ""
		}
		return 0, 0, 0, false, why
	}
	in = usageInt(usage, "tokens_in")
	out = usageInt(usage, "tokens_out")
	if f, err := strconv.ParseFloat(strings.TrimSpace(usage.Values["usd"]), 64); err == nil {
		usd = f
	}
	return in, out, usd, true, ""
}

// usageInt reads one token column of a store's fold. A dash is an absence and reads as zero:
// the BATCH line's in= and out= are sums and have no spelling for "unreported".
func usageInt(u ProviderUsage, column string) int {
	n, err := strconv.Atoi(strings.TrimSpace(u.Values[column]))
	if err != nil {
		return 0
	}
	return n
}

// cardSpend is one card's accounting as the harness store answered it.
type cardSpend struct {
	in, out int
	usd     float64
	partial bool   // the numbers are a LOWER BOUND: the turn in flight was never recorded
	reason  string // the reader stopped, and this names why; empty when it did not
}

// storeSpendWorkers caps how many `sqlite3` processes a gather forks at once. These are
// processes, not goroutines: a batch with sixteen reaped cards should not put sixteen
// readers on the machine at the same instant, and four is enough to turn a serial 22n-second
// pass into roughly one card's timeout.
const storeSpendWorkers = 4

// storeSpends reads the harness store for every card `want` selects, at most
// storeSpendWorkers at a time, and returns one entry per card. A card `want` did not select
// keeps its zero value and no reader ran for it.
func storeSpends(root string, cards []batchCard, want func(i int) bool) []cardSpend {
	out := make([]cardSpend, len(cards))
	var todo []int
	for i := range cards {
		if want(i) {
			todo = append(todo, i)
		}
	}
	if len(todo) == 0 {
		return out
	}
	work := make(chan int)
	var wg sync.WaitGroup
	workers := storeSpendWorkers
	if len(todo) < workers {
		workers = len(todo)
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				var s cardSpend
				s.in, s.out, s.usd, s.partial, s.reason = storeCardSpend(root, scratchName(cards[i]))
				out[i] = s
			}
		}()
	}
	for _, i := range todo {
		work <- i
	}
	close(work)
	wg.Wait()
	return out
}
