package friend

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
)

// BeatEvery is how often the daemon beats to the sprint server while its
// loop runs: the sprint's own number (internal/sprint FriendBeatEvery, one
// second; a friend is down after fifteen without one). It is also the
// loop's read block: one read of the stream per beat.
const BeatEvery = time.Second

// RecheckEvery is how long the daemon waits before trying a deferred
// delivery again (Deferred: the session cannot take a turn now and nothing
// is wrong). The message stays in the daemon's hand meanwhile: it is never
// put back on the bus, never counted toward MaxDeliveries, never acked.
const RecheckEvery = 10 * time.Second

// StatusErrorEvery bounds how often a status file that cannot be written
// is said in the record: the loop goes on beating and delivering without it.
// DeferredSaidEvery bounds how often a deferral still in hand is said.
const (
	StatusErrorEvery  = time.Minute
	DeferredSaidEvery = time.Minute
)

// DefaultSilentStop is how long a turn may print nothing before the daemon
// stops it (--silent-stop): a turn that prints keeps running however long
// it takes (the finding of 2026-10-04: a fixed ten-minute cap killed a
// friend's real work mid-turn).
const DefaultSilentStop = 20 * time.Minute

// DefaultBrokenAfter is how many turns in a row the provider must refuse
// with the same reason before the session is broken (--broken-after).
const DefaultBrokenAfter = 3

// RefusalRepeats is how many times in a row the server must refuse one verb
// before the daemon raises it (a down beat and one word to the coordinator),
// and RefusalDownFor how long her beat says down with it while the server stays
// refused (a server that answers again clears it at once: docs/SPEC-FRIEND.md,
// "A refused verb").
const (
	RefusalRepeats = 3
	RefusalDownFor = 15 * time.Minute
)

// MaxBatch bounds the messages a one-shot lane takes with its card; batch
// delivery reads every pending message before Envelope applies TextLimit.
// BatchBytes bounds how much text: the rest waits for the next turn, oldest first. A turn's text
// travels as one argument to some harnesses (opencode run), under the
// platform's argument limit.
const (
	MaxBatch   = 32
	BatchBytes = 256 << 10
)

// SeatCacheFor is how long the daemon keeps the seat holder it read from the
// sprint server: the authority of a message is never older than this
// (docs/SPEC-FRIEND.md, bus-authority-labels.w3).
const SeatCacheFor = 10 * time.Second

// PongSubject is the subject of a session's pong on the bus (nova-friend pong): the
// answer to a reach step or a delivery check, never liveness.
const PongSubject = "pong"

// The session's state, as the status file says it.
const (
	SessionOK     = "ok"
	SessionBroken = "broken"
)

// Daemon is one friend's loop: the recv loop over the friend's stream with
// the deliver adapter, the beat, and the Machine stepped by what arrives.
// Everything it reaches outside itself is a field, so a test runs it over
// bus's Fake, a fake harness and its own clock.
type Daemon struct {
	Friend, Harness, Dir string
	// Pro holds the friend's pronouns (the row's pronouns setting): every text the daemon
	// writes about the friend says them through d.say. Nil is they/them.
	Pro              *PronounsRef
	Store            bus.Store
	Deliver          Deliverer
	Beat             func(ctx context.Context, active time.Time) error // one beat to the sprint server, carrying the session's last activity (zero: none known)
	StepBeatForTests bool                                              // deterministic fake-clock seam; production has one independent beat caller
	// Activity is the newest write of the session's files, read by the
	// independent beat cadence at most once an ActivityEvery and carried on
	// each beat; nil knows no write.
	Activity func() time.Time
	Now      func() time.Time
	// Pause waits d when the store did not: after a read that answered at
	// once (blocked false: an error, or a store that does not block), and
	// while a delivery runs and the loop only peeks.
	Pause  func(ctx context.Context, d time.Duration)
	Record func(line string) // one line per delivery, to the daemon's log
	// MachineStopped says the machine's word, as the owner last read it off the beat's
	// answer (ParseMachine), is STOPPED: the lanes cancel what runs, owe and send
	// stop-returns, and start nothing (stop.go). nil: never stopped.
	MachineStopped func() bool
	// StopReturn sends one stop-return (StopReturnArgv) to the sprint server; nil sends it
	// through Sprint.
	StopReturn func(ctx context.Context, argv []string) error
	// owed is how many stop-returns the lanes owe now (OwedStopReturns): the beat carries it.
	owed atomic.Int64
	// Limited is the harness's limit now: its kind and reset, and whether there is one
	// (Limits.Limited, Limits.Kind); nil: none. While there is one the status says
	// session=limited with them, and the turns the Gate defers stay pending.
	Limited func() (kind string, until time.Time, limited bool)
	// Status receives the daemon's state whenever it changes, and every
	// StatusEvery (WriteStatus over the state files).
	Status func(Status) error
	// SilentStop is how long a running turn may print nothing before it is
	// stopped (DefaultSilentStop when zero); BrokenAfter how many turns in a
	// row the provider refuses the same way before the session is broken
	// (DefaultBrokenAfter when zero); Coordinator who is told of a broken
	// session when no ping has named the seat.
	SilentStop  time.Duration
	BrokenAfter int
	Coordinator string
	// Row is the friend's nova-config row as the daemon last read it (from
	// its beat): her delivery mode (ModeBatch or ModeOneShot) and width,
	// read every step so a change takes effect without a restart; nil, or
	// empty answers, deliver in batch, and a width of 0 runs no lane: the width
	// is nova-config's alone, answered by the server (row).
	Row func() (mode string, width int)
	// Pacing is the row's pacing as the daemon last read it: the fraction of each
	// subscription window the lanes may spend, read every step; nil, or out of
	// (0, 1], is DefaultPacing (pacing.go). No beat carries the row's pacing yet,
	// so nova-friend leaves it nil.
	Pacing func() float64
	// LaneCaps is the row's wall cap of a lane's card by its tier as the daemon last read
	// it (ParseLaneCaps off its beat), read every step; nil, or a tier it names none for,
	// is DefaultLaneCaps (lane_cap.go).
	LaneCaps func() map[string]time.Duration
	// LoadLanes and SaveLanes keep the one-shot lanes' state (ReadLanes,
	// WriteLanes over the state files); nil keeps it in memory only.
	LoadLanes func() (LaneState, error)
	SaveLanes func(LaneState) error
	// CardDone is the one bus line a lane's session sends when its card is
	// done (nova-bus send by path, as this friend, to the coordinator).
	CardDone func(card, to string) string
	// Progress stamps progress on the cards whose lane turn printed (ProgressArgv to the
	// sprint server); nil stamps none.
	Progress func(ctx context.Context, cards []Card) error
	// Finish sends one finish verb to the sprint server: a lane's card whose run ended with
	// no REPORT.md (FinishArgv, lane_end.go), and every working card on her row whose job's
	// REPORT.md says a verdict, whoever wrote its brief (OutboxFinishArgv, outbox.go). Nil,
	// or a finish not answered, leaves it to friend sync, which reads the same REPORT.md.
	Finish func(ctx context.Context, argv []string) error
	// Take sends one take to the sprint server (LaneTakeArgv, BatchTakeArgv), its refusal an
	// error: a lane starts a card only once the server has it working on her row (lane_take.go).
	// Nil (a test's world) takes nothing, and a lane runs any card on her row as before.
	Take func(ctx context.Context, argv []string) (string, error)
	// batchTakeAt and batchTakeSaid pace and say a batch-mode daemon's take (batchTake).
	batchTakeAt   time.Time
	batchTakeSaid string
	// The lanes' parity with the runner scripts they replace (lane_parity.go); Rules nil
	// turns every one off. Rules is her row's lane rules as her beat last answered them,
	// Load the machine's one-minute load (nil: no load rule), Tokens a session's tokens
	// from opencode's database (nil: no cost and no token cap), Route the store's route row
	// for her model and Model its name, LaneHold the pause marker's line (the lanes held
	// down by a provider failure until a person clears it) and LaneHoldDown writes it: her
	// beat then says her down with its message (PauseBeat).
	Rules        func() LaneRules
	Load         func() float64
	Tokens       func(ctx context.Context, session string) (LaneTokens, error)
	Route        func() RoutePrice
	Model        string
	LaneHold     func() string
	LaneHoldDown func(ctx context.Context, message string) error
	// Fault is the lane's provider-refusal fault report in the state directory
	// (ReadLaneFault), and FaultWrite records or clears it (WriteLaneFault): the
	// loop's poll sees a fault it holds whose file a person cleared and releases
	// the lanes, and a stale file a restart cleared. The friend's main wires it
	// to the state directory; nil (a test's world) leaves the fault in memory
	// only, cleared by a restart (docs/SPEC-FRIEND.md, the lanes).
	Fault      func() string
	FaultWrite func(report string) error
	// FaultExternal is a lane fault the limit detector counted itself (lanes
	// failing alike): the loop adopts it as the same record a provider's refusal
	// writes, then clears it. The friend's main wires it to the detector; nil:
	// no such fault.
	FaultExternal func() string
	// FaultDown marks her row down until until with reason: the same harness fault
	// FaultRepeats times within FaultWithin on her lanes (lane_parity.go); her beat says
	// her down with them until then (friend beat --until --reason). Nil: her lanes are
	// held here alone.
	FaultDown func(until time.Time, reason string)
	// RefusalDown marks her row down until until with reason: a server that refused one
	// of her verbs RefusalRepeats times in a row (inbox.go, "A refused verb"). RefusalUp
	// clears it when the server answers again. Nil: the refusal is said, never beaten down.
	RefusalDown func(until time.Time, reason string)
	RefusalUp   func()
	// Prove runs the harness's cheapest no-tool turn and answers its terminal reason
	// (harness_proof.go): "" when the harness can answer, the reason when it cannot. Nil
	// proves nothing (every harness can answer; a test's world). Until it answers, the
	// daemon takes no card, beats down with the reason, tells the coordinator once, and
	// re-proves every ServedEvery (docs/SPEC-FRIEND.md, "A lane proves its harness").
	Prove func(ctx context.Context) string
	// Held is every card on her row as the sprint server says it (HeldVia: friend cards
	// <friend>, else the worker view), asked once an InboxEvery; her inbox is reconciled with
	// the answer (SyncInbox, inbox.go). Nil leaves her inbox to friend sync alone.
	Held func(ctx context.Context) (Row, error)
	// Sprint sends one verb to the sprint server and answers what it printed: the reader row's
	// queue, begin, verdict and return (read_lanes.go). Nil runs no reads. ReadSlots is the
	// row's read slots, read each step (nil: DefaultReadSlots), and ReadModel the model of a
	// read's tier ("": the harness's own).
	Sprint    func(ctx context.Context, argv []string) (string, error)
	ReadSlots func() int
	ReadModel func(tier string) string
	// Stage stages a held work card's job (Stager.Stage: jobs/<job>/repo and its JOB.md) and
	// answers the commit staged (stage.go); nil stages none, and a lane is handed a card with
	// its brief alone.
	Stage func(ctx context.Context, p Packet) (string, error)
	// Prune removes finished jobs' worktrees past FinishedJobsKept (Stager.Prune), given the
	// jobs that are live (held on her row, run by a lane, being staged), after each inbox
	// cleanup, and answers the jobs it removed; nil prunes none.
	Prune func(ctx context.Context, live map[string]bool) ([]string, error)
	// Tip is origin's tip of a branch of a repository (owner/name), "" when origin has no
	// such branch (Stager.Tip: one git ls-remote): a report's LAND finishes only at that tip,
	// as nova-sprint collect's does (outbox.go). Nil reads none, and a LAND finishes at its
	// Head.
	Tip func(ctx context.Context, repo, branch string) (string, error)
	// Running is the beat's running list as the sprint server last said it, every friend's:
	// a card id or job to the friend whose lane runs it. A lane is never started for a card
	// it names another friend running, and a lane whose card left her row names its friend
	// on the job's lane mark (one_lane.go). Nil says none; the lane marks still hold.
	Running func() map[string]string
	// Holders reads the server's current card-to-holder map (view cards), once
	// per outbox pass that finds a report outside her row. Nil uses Running's
	// known holders; an error refuses with the explicit unknown-holder remedy.
	Holders func(ctx context.Context) (map[string]string, error)
	// Seat is the coordinator seat holder as the sprint server says it: who the
	// daemon tells (coordinator), else Coordinator. An error, an empty name or a
	// nil Seat is the seat unknown.
	Seat func(ctx context.Context) (string, error)

	idleNow     atomic.Bool // the last step found no dealt work and no live session (Idle; docs/SPEC-FRIEND.md, idle)
	status      Status
	written     time.Time
	limitSaid   time.Time // the reset of the limit last said on the record
	written0    Status
	statusErrAt time.Time
	active      time.Time // the last walk's answer
	walked      time.Time // when it was
	inboxAt     time.Time // when the inbox was last reconciled
	heldIDs     []string  // the cards on her row at it
	heldCards   []HeldCard
	heldAt      time.Time            // when the ask that answered heldCards began: a lane's card taken after it is not judged by it (laneCheck)
	epoch       uint64               // the sprint's epoch as her row last named it; zero before the first answer (epoch.go)
	inboxSaid   map[string]bool      // the inbox lines the last reconcile said that are said once while they stand
	turnEnded   func()               // a test's hook: a turn's result is on its channel (nil: none)
	outbox      outboxState          // the outbox jobs finished, tried and noted (outbox.go)
	staging     map[string]bool      // the jobs a stage is under way for
	stageRetry  map[string]time.Time // when a job whose stage failed is staged again
	stageSaid   map[string]bool      // the stage failures said, once while they stand
	stageMu     sync.Mutex
	stageDone   []stageResult // the stages that ended, for the loop
	stageWG     sync.WaitGroup
	pruneSaid   string // the prune failure last said, said once while it stands
}

// Mailbox is a harness whose session queues what is delivered: Follow reads who read
// the deliveries and moves delivery to the conversation that reads, sending again what the
// old one left unread; Live is the conversation deliveries go to.
type Mailbox interface {
	Follow(ctx context.Context, now time.Time)
	Live() string
}

// turn is one lane turn: a card handed to a lane's session, or a per-card run.
// The daemon delivers nothing else into a session: a message to the friend
// reaches her through nova-hook (pkg/harness/notice), never through here.
type turn struct {
	text     string
	started  time.Time
	running  bool // a Deliver is under way (false while a deferral waits)
	cancel   context.CancelFunc
	seen     *atomic.Int64 // outputs the command printed
	seenN    int64
	lastOut  time.Time // when the daemon last saw the turn print, or its start
	stopped  bool      // the daemon stopped it: silent past SilentStop
	capped   bool      // the daemon ended it: its card's wall reached its lane's cap (lane_cap.go)
	held     bool      // the daemon ended it: a provider failure stopped every lane (lane_parity.go)
	byStop   bool      // the daemon ended it: the machine's stop cancelled every lane (stop.go)
	tail     *outputTail
	stamped  time.Time // when the daemon last stamped progress on the turn's card (stampProgress)
	subjects string
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// oneLine is s on one line, at most n bytes: a reason fit for a subject.
func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		s = s[:n]
	}
	return s
}

// loop is one run of the daemon: what Run keeps between steps, shared by the
// batch turn and the one-shot lanes.
type loop struct {
	d            *Daemon
	ctx          context.Context
	b            *bus.Bus
	passive      bool
	silentStop   time.Duration
	brokenAfter  int
	now          time.Time // the step's clock, for a line said beside a verb (OnStampError)
	refusal      string    // the last provider refusal, and how many turns in a row said it
	streak       int
	broken, told bool
	laneFault    LaneFaultWatch       // a lane's provider refusals: the second identical one faults it (docs/SPEC-FRIEND.md, the lanes)
	refusedTold  map[string]bool      // verbs whose refusal has been raised, once each (inbox.go, "A refused verb")
	refusedAt    map[string]time.Time // a verb's last counted refusal: one refusal counted per ServedEvery, never once a beat (inbox.go, "A refused verb")
	proofReason  string               // the harness proof's last terminal reason, "" when it answers
	proofAt      time.Time            // when the proof last ran
	proofTold    bool                 // the harness proof's failure has been told to the coordinator, once
	instantFails int                  // consecutive card runs that ended with no report within InstantFail (lanes.go, "A lane proves its harness")
	outboxRes    chan outboxResult    // the finishes that ended off the loop, applied by the step (outbox.go)
	outboxFly    map[string]bool      // the outbox jobs whose tip check and finish are in flight now
	finishWG     sync.WaitGroup       // the finishes in flight: waited for when Run ends (outbox.go)
	lanes        *laneSet
	reads        *readSet
	mode         string // the mode the daemon delivers in now
	saidNoLanes  bool
	saidNoWidth  bool           // the NOTE that the beat answer names no width has been said (row)
	saidRefusal  string         // the card runner's refusal last recorded, "" when it runs
	tag          string         // this daemon's tag in its lanes' names on a lane mark (laneTag, one_lane.go)
	workWG       sync.WaitGroup // every turn, lane open and read the loop started, waited for when Run ends
	seatHolder   string         // the seat holder as last read; empty while unknown
	seatRead     time.Time      // when it was read; zero before the first read
}

// beatState is the cadence worker's last result. The main loop owns Status and
// reads a snapshot, so a slow inbox or finish cannot hold the native beat or
// race a status-file write (docs/SPEC-FRIEND.md, the loop and presence).
type beatState struct {
	mu      sync.Mutex
	active  time.Time
	last    time.Time
	beats   int
	err     string
	refused string // why the beat was refused (a server's Refused), "" when it was not
}

func (b *beatState) snapshot() (active, last time.Time, beats int, err, refused string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.active, b.last, b.beats, b.err, b.refused
}

// beatLoop is this daemon's sole production beat caller. A tick waits for the
// previous beat, never sending overlapping proof words or duplicate beat verbs.
func (d *Daemon) beatLoop(ctx context.Context, b *beatState) {
	ticker := time.NewTicker(BeatEvery)
	defer ticker.Stop()
	var active, walked time.Time
	for {
		now := d.Now()
		if d.Activity != nil && (walked.IsZero() || now.Sub(walked) >= ActivityEvery) {
			active, walked = d.Activity(), now
		}
		err := d.Beat(ctx, active)
		b.mu.Lock()
		b.active = active
		if err != nil {
			b.err = err.Error()
			var refused *Refused
			if errors.As(err, &refused) {
				b.refused = refused.Why
			} else {
				b.refused = ""
			}
		} else {
			b.err, b.refused, b.beats, b.last = "", "", b.beats+1, now
		}
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Run is the loop until ctx ends. Each step: the clock; the friend's row
// (Row: her mode and width); each running lane turn's output watched, and a
// turn silent past SilentStop stopped; the ended lane turns, reads and
// finishes applied; her inbox reconciled with her row; then, in one-shot mode,
// each free lane handed its next card (lanes.go) and the reader row served
// (read_lanes.go); an independent beat; the status. The daemon reads nothing
// off her bus stream and delivers nothing into her session: a message to her
// reaches her through nova-hook (pkg/harness/notice), and whether she is there
// is the nova-hook record's alone.
func (d *Daemon) Run(ctx context.Context) error {
	l := &loop{d: d, ctx: ctx, b: &bus.Bus{Store: d.Store}, silentStop: d.SilentStop, brokenAfter: d.BrokenAfter,
		outboxRes: make(chan outboxResult, 64), outboxFly: map[string]bool{},
		lanes: &laneSet{results: make(chan laneResult, 64), refused: map[string]string{}}, reads: newReadSet(), mode: ModeBatch, tag: laneTag()}
	_, l.passive = d.Deliver.(interface{ Passive() })
	l.b.OnStampError = func(err error) { d.Record(l.now.UTC().Format(time.RFC3339) + " " + oneLine(err.Error(), 300)) }
	if l.silentStop <= 0 {
		l.silentStop = DefaultSilentStop
	}
	if l.brokenAfter <= 0 {
		l.brokenAfter = DefaultBrokenAfter
	}
	if d.staging == nil {
		d.staging, d.stageRetry, d.stageSaid = map[string]bool{}, map[string]time.Time{}, map[string]bool{}
	}
	defer d.stageWG.Wait()                                                    // a stage under way ends with ctx (its git is killed) and its result is kept for the next Run
	defer l.workWG.Wait()                                                     // a turn under way ends with ctx (its process group is signalled): Run never returns while one runs
	d.status = Status{Friend: d.Friend, Harness: d.Harness, Started: d.Now()} // the width is the beat answer's, set by row each step
	if !l.passive {
		d.status.Session = SessionOK
	}
	var beats beatState
	if !d.StepBeatForTests {
		beatCtx, stopBeat := context.WithCancel(ctx)
		var beatWG sync.WaitGroup
		beatWG.Add(1)
		go func() { defer beatWG.Done(); d.beatLoop(beatCtx, &beats) }()
		defer func() { stopBeat(); beatWG.Wait() }()
	}
	for ctx.Err() == nil {
		now := d.Now()
		l.now = now
		mode, width := l.row(now)
		l.harnessProof(now)
		d.status.Seat = l.seat(now)
		d.Pause(ctx, BeatEvery) // one step a beat: the lanes' turns run while the loop waits
		if ctx.Err() != nil {
			return nil
		}
		for _, t := range l.turns() {
			l.watch(t, now)
		}
		l.capWatch(now)
		l.stampProgress(now)
		l.drainResults(now)
		// a change of mode waits for the other mode's turns to end
		if mode != l.mode && !l.lanes.running() && len(l.reads.running) == 0 {
			d.Record(fmt.Sprintf("%s mode: %s, from %s (the friend row)", now.UTC().Format(time.RFC3339), mode, l.mode))
			l.mode = mode
		}
		d.status.Mode = l.mode
		l.inboxStep(now) // before the lanes: a card written this step is handed this step
		// the fault file is the lane fault's truth: a fault the daemon holds
		// whose file a person cleared is released, and a stale file a restart
		// cleared is removed (docs/SPEC-FRIEND.md, the lanes).
		if d.Fault != nil {
			// a lane fault the limit detector counted itself (lanes failing alike)
			// is adopted as the same record a provider's refusal writes, then its
			// pending is cleared (docs/SPEC-FRIEND.md, the lanes).
			if d.FaultExternal != nil {
				if fault := d.FaultExternal(); fault != "" {
					l.laneFault.Set(fault)
					if d.FaultWrite != nil {
						if err := d.FaultWrite(fault); err != nil {
							d.Record(now.UTC().Format(time.RFC3339) + " lane fault: the report cannot be written to the state directory: " + oneLine(err.Error(), 300))
						}
					}
					d.Record(now.UTC().Format(time.RFC3339) + " lane fault: " + fault + "; the lanes take no card until a restart")
				}
			}
			switch file := d.Fault(); {
			case file == "" && l.laneFault.Faulted():
				l.laneFault.Clear()
				d.Record(now.UTC().Format(time.RFC3339) + " lane fault cleared by a person; the lanes take cards again")
			case file != "" && !l.laneFault.Faulted():
				if d.FaultWrite != nil {
					if err := d.FaultWrite(""); err != nil {
						d.Record(now.UTC().Format(time.RFC3339) + " lane fault: the clear cannot be written to the state directory: " + oneLine(err.Error(), 300))
					}
				}
				d.Record(now.UTC().Format(time.RFC3339) + " lane fault cleared by a restart")
			}
		}
		switch {
		case l.broken:
		case l.laneFault.Faulted(): // a faulted lane takes no card until a restart or a clear (docs/SPEC-FRIEND.md, the lanes)
		case l.mode == ModeOneShot && l.proofReason != "": // the harness proof failed: no card is taken until the harness answers
		case l.mode == ModeOneShot:
			l.laneStep(now, width)
			l.readStep(now)
			d.status.Lanes = l.lanes.said(width)
		}
		// the idle signal the spend cap reads (Idle): no lane or read running, so a
		// turn is launched only when there is one to launch. It is stored after the
		// step's own lane work, so the just-dealt card the lanes hand this step is
		// never seen as idle (docs/SPEC-FRIEND.md, idle).
		d.idleNow.Store(!l.lanes.running() && len(l.reads.running) == 0)
		if l.broken && !l.told {
			l.told = d.tellBroken(ctx, l.b, l.coordinator(), fmt.Sprintf("friend %s: session %s broken: %s", d.Friend, d.status.SessionID, d.status.SessionReason), fmt.Sprintf("The provider refused %d lane turns in a row the same way. The lanes take no card until the daemon restarts. Renew the session, then restart the daemon (nova-friend install again, or launchctl kickstart -k gui/<uid>/com.nova.friend-%s).", l.brokenAfter, d.Friend))
		}
		if d.StepBeatForTests {
			if d.Activity != nil && (d.walked.IsZero() || now.Sub(d.walked) >= ActivityEvery) {
				d.active, d.walked = d.Activity(), now
			}
			if err := d.Beat(ctx, d.active); err != nil {
				d.status.BeatError = err.Error()
				var refused *Refused
				if errors.As(err, &refused) {
					l.refused(FriendBeatVerb, refused.Why, now)
				}
			} else {
				d.status.BeatError, d.status.Beats, d.status.LastBeat = "", d.status.Beats+1, now
				l.recovered(FriendBeatVerb, now)
			}
		} else {
			var beatRefused string
			d.active, d.status.LastBeat, d.status.Beats, d.status.BeatError, beatRefused = beats.snapshot()
			if beatRefused != "" {
				l.refused(FriendBeatVerb, beatRefused, now)
			} else if d.status.BeatError == "" {
				l.recovered(FriendBeatVerb, now)
			}
		}
		d.flush(now)
	}
	// a finish in flight when the loop stops is waited for, up to FinishWait, and
	// its answer applied here, so a restart does not send an answered finish again;
	// a finish that never answered is not marked finished and is sent again next run
	l.finishWG.Wait()
	for {
		select {
		case r := <-l.outboxRes:
			l.applyOutboxResult(r, l.now)
		default:
			return nil
		}
	}
}

// maxStepResults bounds the ended results one step applies: one select is not
// enough while several lanes end together, so the step drains every channel
// until none has a value, and the cap keeps one step bounded with the rest
// seen the next step.
const maxStepResults = 64

// drainResults applies every ended result already waiting: the lanes, the
// reads and the outbox finishes (outbox.go), in the loop goroutine,
// so the daemon's state stays single-writer. It stops at maxStepResults, when
// the three channels and the finishes are empty, or at that cap.
func (l *loop) drainResults(now time.Time) {
	for n := 0; n < maxStepResults; n++ {
		select {
		case r := <-l.lanes.results:
			// a provider's refusal on a lane's open is terminal too, never a
			// retry each minute: the second identical one faults the lane, and
			// the faulted lane opens no more session (docs/SPEC-FRIEND.md, the
			// lanes). The open's own handling (laneDone) still runs and records
			// the failure; the loop steps no lane while the fault stands.
			if r.open {
				if fault := l.laneFaulted(r.err, now); fault != "" {
					l.d.Record(now.UTC().Format(time.RFC3339) + fmt.Sprintf(" lane %d: ", r.ln.n) + fault + "; the lane takes no card until a restart")
				}
			}
			l.laneDone(r, now)
		case r := <-l.reads.results:
			l.readDone(r, now)
		case r := <-l.outboxRes:
			l.applyOutboxResult(r, now)
		default:
			return
		}
	}
}

// Idle reports whether the last step found the friend with no dealt work and
// no live session: nothing in hand, no dealt brief, no turn, no wake or
// present owed, no lane or read running, and no live mailbox conversation
// (docs/SPEC-FRIEND.md, idle). cmd/nova-friend wires it to the spend cap's
// idle seam, so a daemon with nothing dealt launches no turn at all.
func (d *Daemon) Idle() bool { return d.idleNow.Load() }

// row is the mode and width the daemon delivers by: the friend's row when
// Row says it (one-shot needs a harness that opens sessions, LaneHarness,
// else the daemon delivers in batch and says why once; or a CardRunner that
// can run a card, else its refusal is recorded once and nothing runs), else
// batch. The width is the beat answer's row_width alone, read every step: the
// server reads it from nova-config, the one definition of a width, and the
// daemon has none of its own and no floor. No answer, or 0, is no lane, said
// once with its remedy (docs/SPEC-SWARM.md, the width; tla/FriendLane.tla
// RowWidth, LanesStartNeverPastWidth: a floor of 1 opens a lane at width 0).
func (l *loop) row(now time.Time) (mode string, width int) {
	d := l.d
	mode = ModeBatch
	if d.Row != nil {
		m, w := d.Row()
		if m != "" {
			mode = m
		}
		width = max(w, 0)
	}
	switch {
	case mode != ModeOneShot: // one batch turn: no lane reads the width
	case width == 0 && !l.saidNoWidth:
		l.saidNoWidth = true
		d.Record(now.UTC().Format(time.RFC3339) + " NOTE width 0: the beat answer names no width for this friend, so no lane takes a card; nova-config is the one definition of a width: nova-config friend set " + d.Friend + " --width <n>")
	case width > 0 && l.saidNoWidth:
		l.saidNoWidth = false
		d.Record(fmt.Sprintf("%s width %d: the beat answer names it; the lanes take cards again", now.UTC().Format(time.RFC3339), width))
	}
	d.status.Width = width
	if runner, ok := d.Deliver.(CardRunner); ok && mode == ModeOneShot {
		// a lane per card process: refused, with its remedy, until it can run one
		why := runner.Refusal()
		if why != "" && why != l.saidRefusal {
			d.Record(now.UTC().Format(time.RFC3339) + " mode: one-shot REFUSED: " + why + "; no lane runs")
		}
		if l.saidRefusal = why; why != "" {
			mode = ModeBatch
		}
		return mode, width
	}
	if mode == ModeOneShot {
		if _, ok := d.Deliver.(LaneHarness); !ok || l.passive {
			if !l.saidNoLanes {
				l.saidNoLanes = true
				d.Record(now.UTC().Format(time.RFC3339) + " mode: the row says one-shot, and " + d.Harness + " cannot open a session per lane; no lane runs")
			}
			mode = ModeBatch
		}
	}
	return mode, width
}

// turns is every lane turn running now.
func (l *loop) turns() []*turn {
	var out []*turn
	for _, ln := range l.lanes.lanes {
		if ln.t != nil && ln.t.running {
			out = append(out, ln.t)
		}
	}
	return out
}

// coordinator is who the daemon tells: the seat holder as the sprint server
// says it (Seat), else the Coordinator flag.
func (l *loop) coordinator() string {
	if seat := l.seat(l.now); seat != "" {
		return seat
	}
	return l.d.Coordinator
}

// tell sends the coordinator one message, said on the record when there is
// no one to tell or the send fails.
func (l *loop) tell(subject, body string, now time.Time) {
	l.tellKind("", subject, body, now)
}

// tellKind is tell with the message's kind ("" is status).
func (l *loop) tellKind(kind, subject, body string, now time.Time) {
	to := l.coordinator()
	if to == "" {
		l.d.Record(now.UTC().Format(time.RFC3339) + " no coordinator to tell: " + subject)
		return
	}
	if _, err := l.d.send(l.ctx, l.b, bus.Message{From: l.d.Friend, To: []string{to}, Kind: kind, Subject: subject, Body: subject + "\n" + body}); err != nil {
		l.d.Record(now.UTC().Format(time.RFC3339) + " telling " + to + " failed: " + err.Error() + ": " + subject)
	}
}

// seat is the seat holder for a delivery at now: read from the sprint server
// at most every SeatCacheFor on the daemon's clock, and empty (unknown) when
// Seat is nil, errors or answers nothing (docs/SPEC-FRIEND.md, bus-authority-labels.w3).
func (l *loop) seat(now time.Time) string {
	if l.d.Seat == nil {
		return ""
	}
	if !l.seatRead.IsZero() && now.Sub(l.seatRead) < SeatCacheFor {
		return l.seatHolder
	}
	holder, err := l.d.Seat(l.ctx)
	if err != nil {
		holder = ""
	}
	l.seatHolder, l.seatRead = strings.TrimSpace(holder), now
	return l.seatHolder
}

// send is a message the daemon sends as the friend: a word to the coordinator,
// never one into her session.
func (d *Daemon) send(ctx context.Context, b *bus.Bus, m bus.Message) (bus.Message, error) {
	return b.Send(ctx, m)
}

// startTurn runs deliver for t in its own goroutine, its context carrying the
// watch on its output; the result goes to the lanes' channel.
func (l *loop) startTurn(t *turn, now time.Time, deliver any) {
	tctx, cancel := context.WithCancel(l.ctx)
	seen := &atomic.Int64{}
	tctx = WithOutputSeen(tctx, func() { seen.Add(1) })
	tail := &outputTail{}
	tctx = WithOutputTail(tctx, tail.add)
	t.started, t.running, t.cancel, t.seen, t.seenN, t.lastOut, t.stopped, t.capped, t.tail = now, true, cancel, seen, 0, now, false, false, tail
	switch f := deliver.(type) {
	case func(context.Context) laneResult:
		l.work(func() { r := f(tctx); cancel(); queue(l.ctx, l.lanes.results, r); l.turnEnded() })
	}
}

// work runs f in a goroutine Run waits for before it returns, so nothing the loop started
// (a turn, a lane open, a read) runs on, or touches the harness, after Run has ended.
func (l *loop) work(f func()) {
	l.workWG.Add(1)
	go func() {
		defer l.workWG.Done()
		f()
	}()
}

// queue hands v to the loop on ch, or drops it once ctx has ended: the loop reads no result
// after its ctx ends.
func queue[T any](ctx context.Context, ch chan<- T, v T) {
	select {
	case ch <- v:
	case <-ctx.Done():
		select {
		case ch <- v: // room still: kept as before, never dropped for nothing
		default:
		}
	}
}

// turnEnded calls the daemon's test hook, if any, once a turn's result is queued.
func (l *loop) turnEnded() {
	if l.d.turnEnded != nil {
		l.d.turnEnded()
	}
}

// watch is the silence watch on a running turn: a turn that prints is
// working; one silent past SilentStop is stopped, said on the record.
func (l *loop) watch(t *turn, now time.Time) {
	if n := t.seen.Load(); n != t.seenN {
		t.seenN, t.lastOut = n, now
	}
	if !t.stopped && !t.capped && now.Sub(t.lastOut) >= l.silentStop {
		t.stopped = true
		t.cancel()
		l.d.Record(fmt.Sprintf("%s subject=%s stopping: no output for %s (silent since %s); its process group is signalled",
			now.UTC().Format(time.RFC3339), t.subjects, l.silentStop, t.lastOut.UTC().Format(time.RFC3339)))
	}
}

// stampProgress stamps progress on the cards whose lane turn printed since the turn's last
// stamp, each at most every ProgressEvery, in one Progress call (docs/SPEC-SPRINT.md
// section 8, the rules table's row late; tla/SprintRules.tla, Stamp). A turn that has
// printed nothing stamps nothing, and the late rule reads that silence; a batch turn carries
// messages, not a card, and stamps none. A stamp that fails is said and waits ProgressEvery
// like any other.
func (l *loop) stampProgress(now time.Time) {
	if l.d.Progress == nil {
		return
	}
	var cards []Card
	var ts []*turn
	for _, ln := range l.lanes.lanes {
		t := ln.t
		if t == nil || !t.running || ln.card == nil || now.Sub(t.stamped) < ProgressEvery {
			continue
		}
		if ln.card.Lane > 0 {
			// a lane the server gave its card: its stamp is the lane heard from, sent while the
			// run is under way, printing or not, and a refusal drops the job (lane_take.go)
			t.stamped = now
			if err := l.d.Progress(l.ctx, []Card{*ln.card}); err != nil {
				var refused *Refused
				if errors.As(err, &refused) {
					l.laneDrop(ln, "the server refused its progress: "+refused.Why, now)
					continue
				}
				l.d.Record(fmt.Sprintf("%s lane %d: progress on card %s not stamped: %s; tried again in %s", now.UTC().Format(time.RFC3339), ln.n, ln.card.ID, oneLine(err.Error(), 300), ProgressEvery))
			}
			continue
		}
		if t.seenN == 0 || !t.lastOut.After(t.stamped) {
			continue
		}
		cards, ts = append(cards, *ln.card), append(ts, t)
	}
	if len(cards) == 0 {
		return
	}
	for _, t := range ts {
		t.stamped = now
	}
	if err := l.d.Progress(l.ctx, cards); err != nil {
		l.d.Record(fmt.Sprintf("%s progress: not stamped on %d cards: %s; tried again in %s", now.UTC().Format(time.RFC3339), len(cards), oneLine(err.Error(), 300), ProgressEvery))
	}
}

// LaneFaultAlike is how many identical provider refusals in a row mark the
// lane faulted: a guard's denial is terminal, never a retry each minute, and
// the second identical one is the judgment (docs/SPEC-FRIEND.md, the lanes).
const LaneFaultAlike = 2

// LaneFaultReport is the one line a faulted lane carries on the beat: lane-fault=<n>
// refusal="<first line>", where n is how many identical refusals faulted it.
func LaneFaultReport(n int, line string) string {
	return fmt.Sprintf("lane-fault=%d refusal=%q", n, line)
}

// LaneFaultParts is LaneFaultReport's inverse, so a reader (nova-friend
// status, nova-friend lanes) renders the report's count and refusal line as
// the two fields the card names without re-parsing the wording in two places.
// ok is false when report is not a LaneFaultReport (docs/SPEC-FRIEND.md, the
// lanes).
func LaneFaultParts(report string) (n int, line string, ok bool) {
	rest, found := strings.CutPrefix(report, "lane-fault=")
	if !found {
		return 0, "", false
	}
	num, quoted, found := strings.Cut(rest, " refusal=")
	if !found {
		return 0, "", false
	}
	n, err := strconv.Atoi(num)
	if err != nil {
		return 0, "", false
	}
	line, err = strconv.Unquote(quoted)
	if err != nil {
		return 0, "", false
	}
	return n, line, true
}

// LaneFaultText is the one judgment the coordinator is told when a lane faults
// on a provider refusal: the subject and body naming the fault, the hold verb
// that returns the friend's cards, and the harness check (docs/SPEC-FRIEND.md,
// the lanes).
func LaneFaultText(friend string, p Pronouns, fault string) (subject, body string) {
	subject = fmt.Sprintf("friend %s: a lane faulted on a provider refusal", friend)
	body = p.Sprintf("%s: %s\nA lane's turns were refused by the provider and the denial is terminal: the lane holds its card and runs nothing more until {their} daemon restarts. Hold {them} and return {their} cards: nova-sprint hold %s --return; run the harness check: nova-friend check --as %s\n",
		subject, fault, friend, friend)
	return subject, body
}

// LaneFaultWatch counts one lane's provider refusals: the LaneFaultAlike-th of
// the same refusal is the fault, with the first refusal line; a different
// line starts the count again. The zero value is ready.
type LaneFaultWatch struct {
	line   string
	count  int
	said   bool
	report string
}

// Observe adds one provider-refusal line and answers the lane fault on the
// LaneFaultAlike-th identical one, once per run of alike refusals.
func (f *LaneFaultWatch) Observe(line string) (string, bool) {
	if line == "" {
		return "", false
	}
	if line != f.line {
		f.line, f.count, f.said = line, 0, false
	}
	f.count++
	if f.count == LaneFaultAlike && !f.said {
		f.said = true
		f.report = LaneFaultReport(f.count, line)
		return f.report, true
	}
	return "", false
}

// Faulted answers whether the lane is faulted: after the LaneFaultAlike-th
// identical provider refusal the lane holds its card; it is released only by
// a restart or a clear (docs/SPEC-FRIEND.md, the lanes).
func (f *LaneFaultWatch) Faulted() bool { return f.said }

// Report is the lane-fault report (lane-fault=<n> refusal="<first line>") the
// lane last faulted on, for the daemon's beat; "" when the lane is not faulted
// (docs/SPEC-FRIEND.md, the lanes).
func (f *LaneFaultWatch) Report() string { return f.report }

// Clear clears the lane fault, the release: the lane may take cards again and
// a fresh run of alike refusals is counted anew. The loop calls it on a
// restart and when the fault file a person removed is gone (docs/SPEC-FRIEND.md,
// the lanes).
func (f *LaneFaultWatch) Clear() {
	f.line, f.count, f.said, f.report = "", 0, false, ""
}

// Set marks the lane faulted with report as given, for a detector that counted
// its own bound (the limit detector's lanes failing alike): the same record a
// provider refusal's Observe writes, never counted again.
func (f *LaneFaultWatch) Set(report string) {
	f.line, f.count, f.said, f.report = "", 0, true, report
}

// laneFaulted reads a lane's turn's end for a provider's refusal and, on the
// LaneFaultAlike-th identical one, marks the lane faulted: the report on the
// record (and, when the friend's main wires the fault file, the status's
// lane_fault), and the one judgment to the coordinator with the hold verb and
// the harness check (LaneFaultText). It answers the fault report when the lane
// just faulted, "" when it did not. The batch session keeps its own broken
// streak, never this (docs/SPEC-FRIEND.md, the lanes).
func (l *loop) laneFaulted(err error, now time.Time) string {
	var refused ProviderRefused
	if !errors.As(err, &refused) || l.mode != ModeOneShot {
		return ""
	}
	fault, yes := l.laneFault.Observe(refused.Reason)
	if !yes {
		return ""
	}
	if l.d.FaultWrite != nil {
		if err := l.d.FaultWrite(fault); err != nil {
			l.d.Record(now.UTC().Format(time.RFC3339) + " lane fault: the report cannot be written to the state directory: " + oneLine(err.Error(), 300))
		}
	}
	subject, body := LaneFaultText(l.d.Friend, l.d.Pro.Get(), fault)
	l.tell(subject, body, now)
	return fault
}

// settle is what a lane turn's end does to the session's refusal streak; it
// answers the record's words for it. A provider's refusal counts, and the same
// refusal BrokenAfter turns in a row breaks the session; anything else clears
// the streak.
func (l *loop) settle(t *turn, ok bool, err error, now time.Time) string {
	d := l.d
	line := ""
	var refused ProviderRefused
	switch {
	case !ok && errors.As(err, &refused) && !t.stopped:
		if refused.Reason == l.refusal {
			l.streak++
		} else {
			l.refusal, l.streak = refused.Reason, 1
		}
		line += fmt.Sprintf(" refused=%d/%d", l.streak, l.brokenAfter)
		if l.streak >= l.brokenAfter && !l.broken {
			l.broken = true
			d.status.Session, d.status.SessionID, d.status.SessionReason, d.status.BrokenAt = SessionBroken, refused.Session, oneLine(refused.Reason, 200), now
			line += " session=broken"
		}
		// a guard's denial is terminal, never a retry each minute: the second
		// identical refusal faults the lane, the one judgment to the coordinator
		// with the hold verb and the harness check (docs/SPEC-FRIEND.md, the lanes).
		if fault := l.laneFaulted(err, now); fault != "" {
			line += " " + fault
		}
	default:
		l.streak, l.refusal = 0, ""
	}
	if l.broken && !l.told {
		line += fmt.Sprintf("\n%s session %s broken: %s (%d turns in a row); the lanes take no card until the daemon restarts",
			now.UTC().Format(time.RFC3339), d.status.SessionID, d.status.SessionReason, l.brokenAfter)
	}
	return line
}

// tellBroken sends to (the coordinator) one message that the daemon is broken —
// a broken session, or a server that refused a verb. subject names what is
// broken; why says how to read it. It answers whether the word went out, or
// there was no one to tell (said on the record); a send that fails is tried
// again the next step.
func (d *Daemon) tellBroken(ctx context.Context, b *bus.Bus, to, subject, why string) bool {
	if to == "" {
		d.Record("broken, and no coordinator to tell: the sprint server names no seat and --coordinator is not set")
		return true
	}
	body := subject + "\n" + why + "\n"
	if _, err := d.send(ctx, b, bus.Message{From: d.Friend, To: []string{to}, Subject: subject, Body: body}); err != nil {
		d.status.StoreError = "telling " + to + " it is broken: " + err.Error()
		return false
	}
	d.status.StoreError = ""
	return true
}

// refused observes one refusal of a server verb at now: counted per verb on
// the status, one per ServedEvery (never once a beat), and the
// RefusalRepeats-th consecutive one is raised once — a down beat naming the
// reason (RefusalDown) and one word to the coordinator (tellBroken) — never
// only logged (docs/SPEC-FRIEND.md, "A refused verb").
func (l *loop) refused(verb, why string, now time.Time) {
	d := l.d
	if l.refusedAt == nil {
		l.refusedAt = map[string]time.Time{}
	}
	if last, ok := l.refusedAt[verb]; ok && now.Sub(last) < ServedEvery {
		return // one refusal counted per ServedEvery, never once a beat
	}
	l.refusedAt[verb] = now
	if d.status.Refused == nil {
		d.status.Refused = map[string]int{}
	}
	n := d.status.Refused[verb] + 1
	d.status.Refused[verb] = n
	d.status.LastRefusal, d.status.LastRefusalAt = verb+": "+oneLine(why, 300), now
	if n < RefusalRepeats || l.refusedTold[verb] {
		return
	}
	if l.refusedTold == nil {
		l.refusedTold = map[string]bool{}
	}
	l.refusedTold[verb] = true
	until := now.Add(RefusalDownFor)
	d.Record(d.sayf("%s refused: %s refused %d times in a row: %s; {their} beat says down with it until it answers", now.UTC().Format(time.RFC3339), verb, n, oneLine(why, 300)))
	if d.RefusalDown != nil {
		d.RefusalDown(until, why)
	}
	d.tellBroken(l.ctx, l.b, l.coordinator(), fmt.Sprintf("friend %s: the server refused %s", d.Friend, verb), oneLine(why, 400)+d.say("\n{Their} beat says down with it until the server answers."))
}

// recovered clears one verb's refusals at now: said once, and the down a
// refusal set is lifted (RefusalUp; docs/SPEC-FRIEND.md, "A refused verb").
func (l *loop) recovered(verb string, now time.Time) {
	d := l.d
	if d.status.Refused == nil || d.status.Refused[verb] == 0 {
		return
	}
	delete(d.status.Refused, verb)
	delete(l.refusedTold, verb)
	delete(l.refusedAt, verb)
	d.Record(fmt.Sprintf("%s recovered: %s answers again; the down is lifted", now.UTC().Format(time.RFC3339), verb))
	if d.RefusalUp != nil {
		d.RefusalUp()
	}
}

// harnessProof runs the harness's cheapest no-tool turn at now, once a
// ServedEvery or after the second instant failure, and answers whether the
// daemon may take a card: false while the harness cannot answer, with the
// reason on the status, a down beat naming it, and one word to the
// coordinator, tried again every ServedEvery until it answers
// (docs/SPEC-FRIEND.md, "A lane proves its harness").
func (l *loop) harnessProof(now time.Time) bool {
	d := l.d
	if d.Prove == nil {
		return true
	}
	if l.proofReason == "" && !l.proofAt.IsZero() && now.Sub(l.proofAt) < ServedEvery {
		return true // a pass holds for one ServedEvery: re-proved to catch a harness that lost its login
	}
	if l.proofReason != "" && !l.proofAt.IsZero() && now.Sub(l.proofAt) < ServedEvery {
		return false // re-proved once a ServedEvery, never every loop
	}
	reason := d.Prove(l.ctx) // "" when it answers, else the full "harness <h> cannot answer: ..." reason
	l.proofAt = now
	if reason == "" {
		l.proofReason = ""
		d.status.Proof, d.status.ProofAt = "ok", now
		d.Record(fmt.Sprintf("%s harness proof: %s answers; cards taken again", now.UTC().Format(time.RFC3339), d.Harness))
		l.proofTold = false
		return true
	}
	l.proofReason = reason
	d.status.Proof, d.status.ProofAt = reason, now
	d.Record(fmt.Sprintf("%s harness proof: %s; no card is taken, re-proved every %s", now.UTC().Format(time.RFC3339), oneLine(reason, 300), ServedEvery))
	if d.RefusalDown != nil {
		d.RefusalDown(now.Add(RefusalDownFor), reason)
	}
	if !l.proofTold {
		l.proofTold = true
		to := l.coordinator()
		if to == "" {
			d.Record(now.UTC().Format(time.RFC3339) + " harness proof: no coordinator to tell: " + oneLine(reason, 300))
		} else {
			subject := fmt.Sprintf("friend %s: %s", d.Friend, oneLine(reason, 200))
			if _, err := d.send(l.ctx, l.b, bus.Message{From: d.Friend, To: []string{to}, Kind: bus.KindBlocker, Subject: subject, Body: subject + "\n"}); err != nil {
				d.Record(now.UTC().Format(time.RFC3339) + " harness proof: telling " + to + " failed: " + err.Error())
			}
		}
	}
	return false
}

// statusSame says two statuses are the same, field by field: the refusal map
// (which makes Status otherwise incomparable) compared entry by entry.
func statusSame(a, b Status) bool { return reflect.DeepEqual(a, b) }

// flush writes the status when it changed, and every StatusEvery anyway,
// so a reader tells a live daemon from a dead one by the file's age.
func (d *Daemon) flush(now time.Time) {
	s := d.status
	if d.Limited != nil && s.Session != SessionBroken {
		if kind, until, limited := d.Limited(); limited {
			s.Session, s.LimitKind, s.LimitUntil = SessionLimited, kind, until
			if d.limitSaid != until {
				d.limitSaid = until
				d.Record(fmt.Sprintf("%s session=limited kind=%s until=%s: the lanes take no card until the reset", now.UTC().Format(time.RFC3339), kind, until.UTC().Format(time.RFC3339)))
			}
		} else if !d.limitSaid.IsZero() {
			d.limitSaid = time.Time{}
			d.Record(now.UTC().Format(time.RFC3339) + " session=ok: the harness answered after its reset")
		}
	}
	s.At, s.LastBeat, s.Beats = time.Time{}, time.Time{}, 0 // what every beat changes is not a change
	if statusSame(s, d.written0) && now.Sub(d.written) < StatusEvery {
		return
	}
	d.written0, d.written = s, now
	s.At, s.LastBeat, s.Beats = now, d.status.LastBeat, d.status.Beats
	if err := d.Status(s); err != nil {
		if now.Sub(d.statusErrAt) >= StatusErrorEvery {
			d.statusErrAt = now
			d.Record(now.UTC().Format(time.RFC3339) + " status: " + err.Error() + " (said once per " + StatusErrorEvery.String() + "; the beat goes on)")
		}
	}
}
