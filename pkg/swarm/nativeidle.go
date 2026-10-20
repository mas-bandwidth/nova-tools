package swarm

// THE CARD THAT STOPS IS ENDED WHEN IT STOPS, NOT AT ITS DEADLINE.
//
// A card is idle when NEITHER its log NOR its process tree has moved for --idle,
// so a `go test` that prints nothing for minutes is not mistaken for a dead one. Without this
// watch, `nova-swarm native`'s wait has exactly three ends: the child exits, the deadline
// fires, or a TERM arrives from outside, and nothing looks at the card in between.
//
// THE COST, measured: `js-under-20-bytes` stopped making progress at 14:53:36Z and was
// reaped by its deadline at 15:13:36Z. Eighteen of those twenty minutes were a live process
// producing no output, a bench slot held, and a coordinator with nothing to read. The card
// returned no RESULT.md and $0.0244 bought nothing.
//
// The watch has two readings and one rule -- log growth, then the process tree's CPU, and
// idle only when neither moved for the whole window. Nothing new is invented about what "working" means: the definition that kept
// cards 664-670 alive is the definition here.

import (
	"fmt"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// DefaultNativeIdle is the window a native run gives a card that is saying nothing and
// spending no CPU. It is 300 seconds. It is not a
// provider read deadline. A response whose headers arrived and then sent no body bytes
// is UNKNOWN, and that deadline is the provider proxy's body timer, not this window.
// Whole-card silence is not that evidence: a quiet think looks the same, and it stays
// CARD IDLE.
const DefaultNativeIdle = 300 * time.Second

// NoIdleWindow is the window `--idle 0` reaches WatchIdle as: no watch at all. It is named because a bare zero
// beside a duration reads like an oversight, and this one is a choice a caller can type.
const NoIdleWindow = time.Duration(0)

// nativeBusyShare is the divisor of the sample interval a tree must spend to count as
// WORKING: one tenth of it, measured rather than chosen.
//
// batch asks for one hundredth, on the reasoning that a tree that is working spends a large
// fraction of the interval and one percent separates the two by orders of magnitude.
// Measured against the harness the cards actually run
// (`opencode` v1.18.20, deepseek-flash), that is no longer true: a harness SITTING STILL --
// blocked on a tool call that never returns, and by the same token on a model turn that
// never answers -- charged 103 clock ticks in 95 seconds, a steady 1.03% of one core,
// FOREVER. Its own event loop is the floor, and one percent sits exactly on it.
//
// A tree that is really working pins at least one core, which is 100% of the interval. One
// tenth is ten times the measured idle floor and ten times below a single busy core, so it
// separates them with a decade of room on both sides -- which is what the one-percent rule
// claimed and no longer has.
//
// The same floor is under `batch --idle`, which this does NOT touch: that path is not the
// one break 6 ran through, and a constant shared between two verbs is changed with both of
// them measured, not with one.
const nativeBusyShare = 10

// nativeLogDribble is how many bytes a card's own log must gain WITHIN ONE IDLE WINDOW
// before the log alone counts as progress.
//
// The watch does not read any change in the file's size as work: the job directory is
// `--write`, so what lands in harness-output.log is chosen by the card's own harness, and a
// child that appends one character a minute, or that rewrites the file, would reset the
// still-clock forever and hold its slot to the deadline with nothing behind it. A signal
// the watched thing can feed for free is not a signal.
//
// Four kilobytes per window is the same shape of floor as nativeBusyShare: a card that is
// really stepping writes its banners, its model turn and its tool output by the page, which
// is orders of magnitude above this, and a card that cannot manage one page in a whole
// window is not being carried by its log. It is not a silence rule -- a card working in
// silence is held by the tree's CPU, which is read separately below and resets the clock on
// its own reading.
const nativeLogDribble = 4096

// nativeIdlePoll is how often the log's size is re-read: the end lands within a second of
// the window, which is a 300 s one. At 100 ms it was 3,000 stats a window per running card,
// and past the window a listing of the job directory every 100 ms until the child exited
// (FindCardResult; 2026-10-17).
const nativeIdlePoll = time.Second

// nativeSnapshotEvery is how often the watch reads the process table for the tree's CPU:
// batch's coarse poll (activityInterval, at most 5 s), and no oftener than every 15 s where
// the window is long enough to hold four of those. A snapshot is the whole table (on Linux a
// /proc listing and a read of every /proc/N/stat), taken per running card. The busy bar is
// not raised with the span: a tree must spend a tenth of batch's poll since the last reading
// (0.5 s in the default window), as it did when the reading came every 5 s (IdleWatch's
// treeMoved).
func nativeSnapshotEvery(idle time.Duration) time.Duration {
	return max(activityInterval(idle), min(15*time.Second, idle/4))
}

// IdleEnd is one card ended by the watch: how long it had been still, the step it had
// reached, and -- when the card's own output named one it never moved past -- the refusal
// and its one word. A card that simply stopped talking carries no refusal, and saying it
// hit the wall would be the very misattribution this change exists to stop.
type IdleEnd struct {
	Idle    time.Duration
	Step    string
	Kind    string
	Path    string
	Refused bool
	// Cause names why a card with no refusal went still, when the run can tell: today only
	// IdleCauseNeverStarted and IdleCauseTreeStopped. "" is a card that worked and then went
	// still, whose end is the plain idle reaping.
	Cause string
	// StoppedPID and StoppedCmd name the stopped process a tree held when Cause is
	// IdleCauseTreeStopped, so the end says which process the environment stopped.
	StoppedPID int
	StoppedCmd string
}

// IdleCauseNeverStarted is the cause of a card the idle watch ended whose harness never
// started work: no harness store, nothing printed and no tokens spent. It is the harness
// (or the provider behind it) that failed the card, not the card going still after work.
const IdleCauseNeverStarted = "harness-never-started"

// IdleCauseTreeStopped is the cause of a card the idle watch ended while a process in its
// tree had been stopped for the whole window: the environment blocked the child (a stopped
// target waiting for an authorization it will never get), so the card's own work did not
// stall and the run is not the card's.
const IdleCauseTreeStopped = "tree-stopped"

// WallRefusal is this end's refusal as a wall death, and whether it is one: it is one
// only when the refusal NAMES a path (docs/roadmap.sexp, swarm-idle-reap-verdict). A
// refusal that names nothing -- a `SANDBOX REFUSED` line with no path in it -- is no
// cause a reader can act on, and a report that said `path=-` would send its reader
// looking at a wall it cannot find; the end's cause there is the idle reaping itself,
// which the report states. A card that carried no refusal at all is no wall death.
func (e IdleEnd) WallRefusal() (WallRefusal, bool) {
	if !e.Refused || strings.TrimSpace(e.Path) == "" {
		return WallRefusal{}, false
	}
	return WallRefusal{Path: e.Path, Step: e.Step}, true
}

// CardIdleLine is the typed line a card ended for idleness is reported on when NO refusal
// stopped it -- a provider turn that never answered, a tool call that never returned:
//
//	CARD IDLE task=<id> step=<n> idle=<n>s[ cause=<c>]
//
// It is deliberately not a WALL line. `js-under-20-bytes` died in a provider stall and was
// reported `WALL task=... path=/var/db/xcode_select_link`, and a shift went looking at the
// wall for it.
func CardIdleLine(task string, e IdleEnd) string {
	line := fmt.Sprintf("CARD IDLE task=%s step=%s idle=%.0fs",
		oneline.Field(task), oneline.Field(dashOr(e.Step)), e.Idle.Seconds())
	if e.Cause != "" {
		line += " cause=" + oneline.Field(e.Cause)
	}
	if e.Cause == IdleCauseTreeStopped {
		line += fmt.Sprintf(" pid=%d cmd=%s", e.StoppedPID, oneline.Field(dashOr(e.StoppedCmd)))
	}
	return line
}

// IdleWatch is one card's watch: the file its own output lands in, its job directory, the
// leader of its process group, the window, and the reader already in its capture chain.
// The last three fields are the test's seams and are nil in every real run.
type IdleWatch struct {
	Log    string // the card's own capture, <job>/harness-output.log
	Job    string // the job directory: a card that published is finishing, not idle
	Pid    int    // the child's process group leader
	Idle   time.Duration
	Reader *WallReader // may be nil: the watch then reports no refusal
	// Stalled, when set, says the card is waiting on its global rate budget or a provider
	// refusal's backoff (ProviderProxy.Stalling): a card that waits is not idle, and the
	// still-clock does not run while it waits. Nil is no budget.
	Stalled func() bool

	// Spend, when not nil, is the card's own accounting asked at every poll: what the
	// job has spent so far and whether any read of it has answered. The harness caps
	// what a shell tool prints, so a working card's log can stay under the dribble
	// floor while its turns complete, and its tree waits on the model under the CPU
	// bar exactly as a stalled one's does (docs/roadmap.sexp, swarm-idle-reap-verdict);
	// the spend is the one reading that cap cannot silence, and a rising spend is a
	// turn that completed, which is work whatever the log says. A nil Spend reads
	// nothing, and the watch is what it was before this field existed. It is wired to
	// the run's live sampler in cmd/nova-swarm, which reads the harness's own database
	// the budget already reads (docs/SPEC-SWARM.md, native).
	Spend func() (int, bool)

	now      func() time.Time
	ticker   func(time.Duration) (<-chan time.Time, func())
	snapshot func() activitySnapshot
}

// WatchIdle watches one card until it goes idle, until stop is closed, or forever. It sends
// AT MOST ONE IdleEnd and NEVER CLOSES THE CHANNEL, because a closed channel is always ready
// and a caller selecting on it would read a zero end -- and kill a working card -- the
// instant the watch decided the card was fine. An Idle of zero or less watches nothing and
// returns a NIL channel, which blocks forever in a select: the run then behaves exactly as
// it did before this file existed.
func WatchIdle(w IdleWatch, stop <-chan struct{}) <-chan IdleEnd {
	if w.Idle <= 0 {
		return nil
	}
	out := make(chan IdleEnd, 1)
	now := w.now
	if now == nil {
		now = time.Now
	}
	newTicker := w.ticker
	if newTicker == nil {
		newTicker = func(d time.Duration) (<-chan time.Time, func()) {
			t := time.NewTicker(d)
			return t.C, t.Stop
		}
	}
	snap := w.snapshot
	if snap == nil {
		snap = func() activitySnapshot { return newProcSnapshot() }
	}
	go func() {
		tickC, stopTicker := newTicker(nativeIdlePoll)
		defer stopTicker()
		lastGrow := now()
		lastSize := logSize(w.Log)
		// growFrom is the length the log had when the still-clock last moved. Progress is
		// measured from THERE rather than from the previous poll, so a byte at a time can
		// never add up to a reset it did not earn.
		growFrom := lastSize
		var lastSample time.Time
		var cpu uint64
		var haveCPU bool
		// stoppedSince is when the stopped process now in the tree was first read as
		// stopped, bounded by the last reading that saw the tree running; zero is a tree
		// with no stop in hand. The pid and command are the process the end names.
		var stoppedSince time.Time
		var stoppedPID int
		var stoppedCmd string
		// treeMoved reads the process table at at and says whether the tree worked since the
		// last reading: it lost a process, or it spent a tenth (nativeBusyShare) of the span
		// or of batch's poll (activityInterval), whichever is shorter. The bar stays what it
		// was at a 5 s poll (0.5 s in the default window): measured against a 15 s span it
		// was 1.5 s, and a card behind the shell cap inside one tool call that ran
		// `gh pr checks` every 10 s at ~0.6 s of CPU each never showed it, and was reaped
		// while it worked (the cold read of PR 5953).
		treeMoved := func(at time.Time) bool {
			lastAt := lastSample
			span := at.Sub(lastSample)
			lastSample = at
			s := snap()
			if s == nil || w.Pid <= 0 {
				return false
			}
			// A STOPPED PROCESS BURNS NO CPU: the state beside the CPU reading says the
			// environment stopped it. The stop is measured from the last reading that saw
			// the tree running (or from the still-clock's start), so the window the card is
			// held for is the window the stop is watched for.
			if stopped, ok := s.(stoppedSnapshot); ok {
				if pid, cmd, isStopped := stopped.TreeStopped(w.Pid); isStopped {
					if stoppedSince.IsZero() {
						stoppedSince = lastAt
						if stoppedSince.IsZero() {
							stoppedSince = lastGrow
						}
					}
					stoppedPID, stoppedCmd = pid, cmd
				} else {
					stoppedSince, stoppedPID, stoppedCmd = at, 0, ""
				}
			}
			got, ok := s.TreeCPU(w.Pid)
			if !ok {
				return false
			}
			grew := haveCPU && got > cpu
			shrank := haveCPU && got < cpu
			prev := cpu
			cpu, haveCPU = got, true
			// A tree that LOST a process did work: the process was alive, it was charged,
			// and its departure is not stillness.
			return shrank || (grew && got-prev >= uint64(min(span, activityInterval(w.Idle)))/nativeBusyShare)
		}
		// lastSpend is the accounting the watch last saw, with haveSpend saying one has
		// been seen: the base a rise is measured from, never a reset on its own.
		var lastSpend int
		var haveSpend bool
		for {
			select {
			case <-stop:
				return
			case at := <-tickC:
				if at.IsZero() {
					at = now()
				}
				if w.Stalled != nil && w.Stalled() {
					// waiting on its budget: the harness is blocked on a request the proxy
					// holds by design; the card stalls and is never idle
					lastGrow = at
					continue
				}
				switch size := logSize(w.Log); {
				case size < lastSize:
					// A SHORTER LOG IS NOT A BUSIER CARD. A truncation or a rewrite is a
					// change, and a change is not work. The new length
					// becomes the floor the next window's progress is measured from, and
					// the still-clock keeps running through it.
					lastSize, growFrom = size, size
				case size-growFrom >= nativeLogDribble:
					lastSize, growFrom, lastGrow = size, size, at
					continue
				default:
					// Growth too small to be progress. It is not nothing -- it is kept in
					// lastSize so a later truncation is still seen -- but the card must
					// answer the tree's CPU reading below like any other quiet card.
					lastSize = size
				}
				// A silent log is not a silent card. The tree's CPU is read every
				// nativeSnapshotEvery (15 s in the default window), and a tree that spent a
				// tenth of batch's poll (0.5 s in the default window) since the last reading
				// is working (treeMoved), which separates a working tree from a sleeping one's
				// bookkeeping.
				if at.Sub(lastSample) >= nativeSnapshotEvery(w.Idle) && treeMoved(at) {
					lastGrow = at
					continue
				}
				// THE SHELL CAP (docs/roadmap.sexp, swarm-idle-reap-verdict): the harness
				// caps what a shell tool prints, so a working card's log can stay silent
				// while its turns complete, and a turn that waits on the model spends under
				// the CPU bar exactly as a stall does. What the cap cannot silence is the
				// card's own accounting: turns that completed spent tokens. A rising spend
				// is work, whatever the log and the tree say; a spend that has stopped
				// rising keeps the clock running, so this never keeps a stalled card alive.
				if w.Spend != nil {
					if spent, ok := w.Spend(); ok {
						if haveSpend && spent > lastSpend {
							lastGrow = at
						}
						lastSpend, haveSpend = spent, true
					}
				}
				if at.Sub(lastGrow) < w.Idle {
					continue
				}
				// A CARD THAT ALREADY PUBLISHED IS FINISHING, NOT IDLE: its
				// report is on disk and the end would only race the write.
				if _, published := FindCardResult(w.Job); published {
					continue
				}
				// ONE FRESH READING BEFORE THE END (after the published check, which a finishing
				// card answers every poll until it exits): the last may be up to nativeSnapshotEvery
				// old, and a tree still for most of the window that began working since it
				// is not idle.
				if at.After(lastSample) && treeMoved(at) {
					lastGrow = at
					continue
				}
				end := IdleEnd{Idle: at.Sub(lastGrow), Step: w.Reader.Step()}
				// A STOP HELD FOR THE WHOLE WINDOW IS THE ENVIRONMENT'S: the tree spent no
				// CPU because a process in it was stopped, not because the card stalled.
				if !stoppedSince.IsZero() && at.Sub(stoppedSince) >= w.Idle {
					end.Cause, end.StoppedPID, end.StoppedCmd = IdleCauseTreeStopped, stoppedPID, stoppedCmd
				}
				if refusal, kind, ok := w.Reader.Stopped(); ok {
					end.Refused, end.Kind, end.Path, end.Step = true, kind, refusal.Path, refusal.Step
					if end.Step == "" {
						end.Step = w.Reader.Step()
					}
				}
				out <- end
				return
			}
		}
	}()
	return out
}
