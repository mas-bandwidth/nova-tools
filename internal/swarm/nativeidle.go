package swarm

// THE CARD THAT STOPS IS ENDED WHEN IT STOPS, NOT AT ITS DEADLINE.
//
// `nova-swarm batch` has watched its cards for idleness since issue #593: a card is idle
// when NEITHER its log NOR its process tree has moved for --idle, so a `go test` that prints
// nothing for minutes is not mistaken for a dead one. `nova-swarm native` -- the verb every
// card on the fleet actually runs through, on every bench and in every runner -- has never
// had it. Its wait has exactly three ends: the child exits, the deadline fires, or a TERM
// arrives from outside. Nothing looks at the card in between.
//
// THE COST, measured: `js-under-20-bytes` stopped making progress at 14:53:36Z and was
// reaped by its deadline at 15:13:36Z. Eighteen of those twenty minutes were a live process
// producing no output, a bench slot held, and a coordinator with nothing to read. The card
// returned no RESULT.md and $0.0244 bought nothing.
//
// This is batch's monitor for one card, reusing its two readings and its rule unchanged --
// log growth, then the process tree's CPU, and idle only when neither moved for the whole
// window. Nothing new is invented about what "working" means: the definition that kept
// cards 664-670 alive is the definition here.

import (
	"fmt"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// DefaultNativeIdle is the window a native run gives a card that is saying nothing and
// spending no CPU. It is 300 seconds, which is `batch --idle`'s own default: one number for
// the two verbs, so a card does not mean two different things on two paths.
const DefaultNativeIdle = 300 * time.Second

// NoIdleWindow is the window `--idle 0` reaches WatchIdle as: no watch at all, which is the
// behaviour every native run had before this file existed. It is named because a bare zero
// beside a duration reads like an oversight, and this one is a choice a caller can type.
const NoIdleWindow = time.Duration(0)

// nativeBusyShare is the divisor of the sample interval a tree must spend to count as
// WORKING: one tenth of it, measured rather than chosen.
//
// batch asks for one hundredth (issue #916), on the reasoning that "a tree that is working
// spends a large fraction of the interval; one percent separates the two by orders of
// magnitude". Measured on hulk on 2026-09-19 against the harness the cards actually run
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

// nativeIdlePoll is how often the log's size is re-read, batch's idlePollInterval by the
// same reasoning: short enough that the end lands near the window rather than a tick past
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

// IdleWatch is one card's watch: the file its own output lands in, its job directory, the
// leader of its process group, the window, and the reader already in its capture chain.
// The last three fields are the test's seams and are nil in every real run.
type IdleWatch struct {
	Log    string // the card's own capture, <job>/harness-output.log
	Job    string // the job directory: a card that published is finishing, not idle
	Pid    int    // the child's process group leader
	Idle   time.Duration
	Reader *WallReader // may be nil: the watch then reports no refusal

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
		var lastSample time.Time
		var cpu uint64
		var haveCPU bool
		for {
			select {
			case <-stop:
				return
			case at := <-tickC:
				if at.IsZero() {
					at = now()
				}
				if size := logSize(w.Log); size != lastSize {
					lastSize, lastGrow = size, at
					continue
				}
				// A silent log is not a silent card (issue #593). The tree's CPU is read on
				// the coarse poll batch uses, and the same one-percent-of-the-interval share
				// separates a working tree from a sleeping one's bookkeeping (issue #916).
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
				if at.Sub(lastGrow) < w.Idle {
					continue
				}
				// A CARD THAT ALREADY PUBLISHED IS FINISHING, NOT IDLE (issue #916): its
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
