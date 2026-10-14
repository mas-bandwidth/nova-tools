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

// DefaultNativeStart is how long a native run gives a card to show its first SIGN OF LIFE
// before the watch ends it as never started. It is 60 seconds. It is not a provider read
// deadline and not the idle window: it bounds the launch, the stretch from the child's
// start until the harness first moves, and the idle window above only begins once a sign
// arrives. A harness that never started looks exactly like a quiet think to the idle watch,
// so without this phase a launch that produced neither a byte nor a CPU tick held a lane and
// a bench slot for the whole idle window (docs/SPEC-SWARM.md, native).
const DefaultNativeStart = 60 * time.Second

// NoIdleWindow is the window `--idle 0` reaches WatchIdle as: no watch at all. It is named because a bare zero
// beside a duration reads like an oversight, and this one is a choice a caller can type.
const NoIdleWindow = time.Duration(0)

// noStartKind is the IdleEnd.Kind the start phase gives a card that never showed a sign of
// life: it is not a wall refusal and not the idle reaping, and the member reads it off the
// NATIVE line's `reason=no-start` (cmd/nova-swarm/member.go, EndNoStart).
const noStartKind = "no-start"

// treeForeignReader is the optional other half of the tree reader: a snapshot that can also
// say whether the watched tree holds a process beyond the launch chain (the wrapper,
// nova-sandbox, the launcher script and the harness binary are the programs that bring a
// harness up). The idle watch is handed an activitySnapshot, which reads CPU alone; the start
// phase asks this when the snapshot implements it, so a reader that knows nothing more -- and
// every idle test -- is unchanged.
type treeForeignReader interface {
	ForeignDescendant(pid int) bool
}

// ForeignDescendant reports whether pid's tree holds a process beyond the launch chain: the
// wrapper, nova-sandbox, the launcher script and the harness binary bring a harness up as the
// single path of single-child links from pid, and a node on that path with more than one
// child is the card at work. It is the start phase's third sign of life, and it matches no
// command line: it walks the kernel's own parent links (activity.go's procSnapshot).
func (s *procSnapshot) ForeignDescendant(pid int) bool {
	if s == nil || !s.known || pid <= 0 || !s.live[pid] {
		return false
	}
	seen := map[int]bool{}
	for cur := pid; ; {
		if seen[cur] {
			return false
		}
		seen[cur] = true
		kids := s.children[cur]
		if len(kids) == 0 {
			return false
		}
		if len(kids) > 1 {
			return true
		}
		cur = kids[0]
	}
}

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

// nativeIdlePoll is how often the log's size is re-read: short enough that the end lands near the window rather than a tick past
// it, and cheap because it is one stat of one file.
const nativeIdlePoll = 100 * time.Millisecond

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
}

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
//	CARD IDLE task=<id> step=<n> idle=<n>s
//
// It is deliberately not a WALL line. `js-under-20-bytes` died in a provider stall and was
// reported `WALL task=... path=/var/db/xcode_select_link`, and a shift went looking at the
// wall for it.
func CardIdleLine(task string, e IdleEnd) string {
	return fmt.Sprintf("CARD IDLE task=%s step=%s idle=%.0fs",
		oneline.Field(task), oneline.Field(dashOr(e.Step)), e.Idle.Seconds())
}

// CardNoStartLine is the typed line a card the start phase ended is reported on: the card
// showed none of the three signs of life for the whole start window, so the harness never
// started and the card is ended before the idle window could hide it.
//
//	CARD NOSTART task=<id> step=- start=<n>s
//
// It is not a WALL line and not the CARD IDLE line: the card did not go quiet after working
// and it hit no wall, and the line names the window it never moved in.
func CardNoStartLine(task string, start time.Duration) string {
	return fmt.Sprintf("CARD NOSTART task=%s step=- start=%.0fs", oneline.Field(task), start.Seconds())
}

// NoStart reports whether this end is the start phase's: the card showed none of the three
// signs of life for the whole start window and was ended as never started, not idle and not
// walled.
func (e IdleEnd) NoStart() bool { return e.Kind == noStartKind }

// IdleWatch is one card's watch: the file its own output lands in, its job directory, the
// leader of its process group, the window, and the reader already in its capture chain.
// The last three fields are the test's seams and are nil in every real run.
type IdleWatch struct {
	Log  string // the card's own capture, <job>/harness-output.log
	Job  string // the job directory: a card that published is finishing, not idle
	Pid  int    // the child's process group leader
	Idle time.Duration
	// Start, when positive, is the start window: the watch starts in phase "starting" and
	// ends the card as never started when no sign of life arrives within it. Zero is no
	// start watch, the test seam default, and leaves the watch exactly what it was.
	Start  time.Duration
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
		// lastSpend is the accounting the watch last saw, with haveSpend saying one has
		// been seen: the base a rise is measured from, never a reset on its own.
		var lastSpend int
		var haveSpend bool
		// THE START PHASE. started says the first sign of life has arrived and the idle
		// phase (the rules below, unchanged) owns the card from then on; phaseStart is
		// when the launch began, and startInterval is the coarse cadence the tree is read
		// on while starting, the same shape as the idle phase's activityInterval.
		started := w.Start <= 0
		phaseStart := lastGrow
		startInterval := activityInterval(w.Start)
		for {
			select {
			case <-stop:
				return
			case at := <-tickC:
				if at.IsZero() {
					at = now()
				}
				if !started {
					// SIGN 1: THE LOG GREW. One byte is enough here where the idle phase
					// needs a page: a harness that prints at all has begun, and the dribble
					// floor still governs the idle window it moves into.
					sign := logSize(w.Log) > lastSize
					// SIGN 2 and SIGN 3, read off the tree on the coarse cadence: CPU that
					// reached the busy share in the interval, or a descendant beyond the
					// launch chain (the wrapper, nova-sandbox, the launcher script, the
					// harness binary). A snapshot that knows no descendants reads CPU alone.
					if !sign && at.Sub(lastSample) >= startInterval {
						span := at.Sub(lastSample)
						lastSample = at
						if s := snap(); s != nil && w.Pid > 0 {
							if fr, ok := s.(treeForeignReader); ok && fr.ForeignDescendant(w.Pid) {
								sign = true
							}
							if !sign {
								if got, ok := s.TreeCPU(w.Pid); ok {
									if haveCPU && got >= cpu+uint64(span)/nativeBusyShare {
										sign = true
									}
									cpu, haveCPU = got, true
								}
							}
						}
					}
					if sign {
						started = true
						lastGrow, lastSample, haveCPU = at, at, false
						lastSize, growFrom = logSize(w.Log), logSize(w.Log)
						continue
					}
					// NO SIGN IN THE WHOLE START WINDOW: the watch ends the card as never
					// started, with the typed no-start end, rather than let a launch that
					// never ran hold a lane and a bench for the idle window.
					if at.Sub(phaseStart) >= w.Start {
						out <- IdleEnd{Idle: at.Sub(phaseStart), Kind: noStartKind, Step: "-"}
						return
					}
					continue
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
				// A silent log is not a silent card. The tree's CPU is read on
				// the coarse poll batch uses, and the same one-percent-of-the-interval share
				// separates a working tree from a sleeping one's bookkeeping.
				if at.Sub(lastSample) >= activityInterval(w.Idle) {
					span := at.Sub(lastSample)
					lastSample = at
					if s := snap(); s != nil && w.Pid > 0 {
						if got, ok := s.TreeCPU(w.Pid); ok {
							grew := haveCPU && got > cpu
							shrank := haveCPU && got < cpu
							prev := cpu
							cpu, haveCPU = got, true
							// A tree that LOST a process did work: the process was alive,
							// it was charged, and its departure is not stillness.
							if shrank || (grew && got-prev >= uint64(span)/nativeBusyShare) {
								lastGrow = at
								continue
							}
						}
					}
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
				end := IdleEnd{Idle: at.Sub(lastGrow), Step: w.Reader.Step()}
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
