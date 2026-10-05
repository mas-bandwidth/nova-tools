package friend

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// BeatEvery is how often the daemon beats to the sprint server while its
// loop runs: the sprint's own number (internal/sprint FriendBeatEvery, one
// second; a friend is down after fifteen without one). It is also the
// loop's read block: one read of the stream per beat.
const BeatEvery = time.Second

// MaxDeliveries is how many times a message is handed into the session
// before the daemon gives up on it: a turn that fails leaves the message
// pending and the bus hands it in again once its claim opens (bus.ClaimAfter);
// the last failure acks it, with the failure on the record, so a message the
// session cannot take never comes back for ever. A turn the provider refused
// (ProviderRefused) counts toward nothing here: the session is at fault, not
// the message, and BrokenAfter says what happens instead.
const MaxDeliveries = 3

// RecheckEvery is how long the daemon waits before trying a deferred
// delivery again (Deferred: the session cannot take a turn now and nothing
// is wrong). The message stays in the daemon's hand meanwhile: it is never
// put back on the bus, never counted toward MaxDeliveries, never acked.
const RecheckEvery = 10 * time.Second

// DaemonConsumer is the bus consumer the daemon reads as, apart from an
// interactive receiver's (bus.Consumer): what is pending under it is the
// daemon's own held work, recovered at start.
const DaemonConsumer = "nova-friend-daemon"

// DaemonReadBatch bounds one read of fresh entries while the daemon holds
// work back (asleep, or waiting for a wake barrier) and one page of its own
// pending entries at start. At the bus's 1 MiB body ceiling one read retains
// at most 128 MiB of payloads transiently; held work keeps entry ids, not
// another copy of the bodies.
const DaemonReadBatch = 128

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

// MaxBatch bounds how many messages go into one turn, and BatchBytes how
// much text: the rest waits for the next turn, oldest first. A turn's text
// travels as one argument to some harnesses (opencode run), under the
// platform's argument limit.
const (
	MaxBatch   = 32
	BatchBytes = 256 << 10
)

// The subjects of the daemon's own messages on the bus.
const (
	DaemonPongSubject = "daemon-pong"
	PongSubject       = "pong"
	PingPrefix        = "PING "
)

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
	// StateDir is the daemon's state directory: with it the daemon takes the
	// singleton lock and keeps the sleep/wake choice (SessionState) there;
	// empty keeps no state, always awake.
	StateDir string
	// Keepalive is the daemon-only keepalive lane (internal/friend/keepalive),
	// started after the singleton and the session state are validated, and
	// stopped with the daemon; nil runs none.
	Keepalive func(context.Context) error
	Width     int
	Store     bus.Store
	Deliver   Deliverer
	Beat      func(ctx context.Context, asleep bool) error // one beat to the sprint server
	Now       func() time.Time
	// Pause waits d when the store did not: after a read that answered at
	// once (blocked false: an error, or a store that does not block), and
	// while a delivery runs and the loop only peeks.
	Pause  func(ctx context.Context, d time.Duration)
	Record func(line string) // one line per delivery, to the daemon's log
	// Pong is the session's recorded answer, read each step while a
	// challenge is open (ReadPong over the state files).
	Pong func() (Pong, bool, error)
	// Status receives the daemon's state whenever it changes, and every
	// StatusEvery (WriteStatus over the state files).
	Status func(Status) error
	// PongCommand is the exact pong line for this friend and nonce (the
	// binary by path, --as, --dir, --redis), put at the head of a turn while
	// a challenge is open, so a small model has one line to run and nothing
	// to fill in.
	PongCommand func(nonce string) string
	// SilentStop is how long a running turn may print nothing before it is
	// stopped (DefaultSilentStop when zero); BrokenAfter how many turns in a
	// row the provider refuses the same way before the session is broken
	// (DefaultBrokenAfter when zero); Coordinator who is told of a broken
	// session when no ping has named the seat, and the one bus sender whose
	// message wakes a sleeping session (saved in the session state).
	SilentStop  time.Duration
	BrokenAfter int
	Coordinator string
	// Row is the friend's nova-config row as the daemon last read it (from
	// its beat): her delivery mode (ModeBatch or ModeOneShot) and width,
	// read every step so a change takes effect without a restart; nil, or
	// empty answers, deliver in batch at Width.
	Row func() (mode string, width int)
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

	m           *Machine
	status      Status
	written     time.Time
	written0    Status
	statusErrAt time.Time
}

// turn is one delivery into the session: the messages it carries (acked
// together at exit 0) and the daemon's word about the coordinator, if any.
type turn struct {
	entries  []string // the stream entries to ack
	msgs     []bus.Message
	notice   *Push
	text     string
	started  time.Time
	running  bool // a Deliver is under way (false while a deferral waits)
	cancel   context.CancelFunc
	seen     *atomic.Int64 // outputs the command printed
	seenN    int64
	lastOut  time.Time // when the daemon last saw the turn print, or its start
	stopped  bool      // the daemon stopped it: silent past SilentStop
	reserved bool      // its messages were taken from the hand under the session-state lock
	stamped  time.Time // when the daemon last stamped progress on the turn's card (stampProgress)
	subjects string
}

type result struct {
	t    *turn
	exit int
	err  error
}

// Text is a message as the session reads it, the shape nova-bus recv
// prints: the header line, a blank line, the body ending in a newline.
func Text(m bus.Message) string {
	body := m.Body
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return fmt.Sprintf("RECV OK id=%s from=%s to=%s cc=%s re=%s at=%s subject=%q\n\n%s",
		m.ID, m.From, dash(strings.Join(m.To, ",")), dash(strings.Join(m.CC, ",")), dash(m.Re), m.At.Format(time.RFC3339), m.Subject, body)
}

// Batch is one turn's text: the pong line to run first while a challenge
// is open, the daemon's word about the coordinator, then every message,
// oldest first, each as nova-bus recv prints it under a numbered rule. A
// single message with nothing else is its Text alone.
func Batch(msgs []bus.Message, notice, pongCommand string) string {
	if len(msgs) == 1 && notice == "" && pongCommand == "" {
		return Text(msgs[0])
	}
	var b strings.Builder
	if pongCommand != "" {
		b.WriteString("Run this now, first, exactly as written: " + pongCommand + "\nThen read on.\n\n")
	}
	if notice != "" {
		b.WriteString("nova-friend: " + notice + "\n\n")
	}
	fmt.Fprintf(&b, "nova-friend: %d message(s) for you, oldest first, in one turn; take each in order.\n", len(msgs))
	for i, m := range msgs {
		fmt.Fprintf(&b, "\n=== message %d of %d: id=%s from=%s subject=%q ===\n", i+1, len(msgs), m.ID, m.From, m.Subject)
		b.WriteString(Text(m))
	}
	return b.String()
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
	state        SessionState    // the sleep/wake choice as of this step
	held         []string        // pending entries kept by id, not in hand: held back asleep, recovered at start; oldest first
	observed     map[string]bool // passive harness: entries seen, for the wake they did
	fatal        error           // an error that ends the run, set by a step that cannot return it
	answered     map[string]bool // entries whose ping the daemon has ponged
	failed       map[string]int  // entries whose turn failed, and how often
	hand         []bus.Entry     // messages read and not yet in a turn, oldest first
	inHand       map[string]bool // entries read and not yet acked or failed: in hand or in a turn
	notice       *Push           // the latest word about the coordinator the session is owed
	noticeTaken  *Push           // the word the last head() put in a turn
	saidSilent   bool            // what the session last heard: the coordinator silent
	busy         *turn           // the batch turn under way, or deferred in hand
	retry        time.Time       // when the deferred turn in hand is tried again; zero while none is
	deferrals    int
	deferSaid    time.Time
	refusal      string // the last provider refusal, and how many turns in a row said it
	streak       int
	broken, told bool
	results      chan result
	lanes        *laneSet
	mode         string // the mode the daemon delivers in now
	saidNoLanes  bool
}

// Run is the loop until ctx ends. At start: the singleton lock on the state
// directory, the coordinator saved, the session state read, the keepalive
// lane started, and every page of the daemon's own pending entries recovered
// (recover). Each step: the session state (asleep or awake); the clock; the
// friend's row (Row: her delivery mode and width); every pending message read
// off the stream when nothing waits on it (a ping is answered by the daemon
// at once and acked, never pushed in), else one peek, so a ping arriving
// during a long turn is still answered at once; each running turn's output
// watched, and a turn silent past SilentStop stopped; the turns' results
// (exit 0 acks every message a turn carried); then, awake, in batch mode, one
// turn with every waiting message when the session is free, and in one-shot
// mode, each free lane handed its next card with the waiting messages riding
// along (lanes.go); a beat when the store answered, saying asleep; the
// session's pong; the status. Asleep, the daemon starts no turn: it answers
// pings, keeps the messages by id, and wakes when the coordinator sends one
// (SPEC-FRIEND.md, asleep behaviour). The daemon's own words about the
// coordinator collapse to the latest and ride in a turn that carries messages
// or a card, never alone.
func (d *Daemon) Run(ctx context.Context) (runErr error) {
	if d.StateDir != "" {
		lock, err := TakeDaemonLock(d.StateDir, d.Friend)
		if err != nil {
			return err
		}
		defer func() { runErr = errors.Join(runErr, lock.Unlock()) }()
		if d.Coordinator != "" {
			if _, err := UpdateSessionState(d.StateDir, func(s *SessionState) error { s.Coordinator = d.Coordinator; return nil }); err != nil {
				return err
			}
		}
	}
	state, err := d.sessionState()
	if err != nil {
		return err
	}
	if state.Asleep && state.Coordinator == "" {
		return errors.New("asleep friend needs a configured coordinator or local wake")
	}
	if d.Keepalive != nil {
		child, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() {
			err := d.Keepalive(child)
			if err == nil && child.Err() == nil {
				err = errors.New("keepalive loop stopped before daemon shutdown")
			}
			done <- err
			cancel()
		}()
		defer func() { cancel(); runErr = errors.Join(runErr, <-done) }()
		ctx = child
	}
	l := &loop{d: d, ctx: ctx, b: &bus.Bus{Store: d.Store}, silentStop: d.SilentStop, brokenAfter: d.BrokenAfter, state: state,
		answered: map[string]bool{}, failed: map[string]int{}, inHand: map[string]bool{}, observed: map[string]bool{}, results: make(chan result, 1),
		lanes: &laneSet{results: make(chan laneResult, 64)}, mode: ModeBatch}
	_, l.passive = d.Deliver.(interface{ Passive() })
	if l.silentStop <= 0 {
		l.silentStop = DefaultSilentStop
	}
	if l.brokenAfter <= 0 {
		l.brokenAfter = DefaultBrokenAfter
	}
	d.m = Start(d.Now())
	d.status = Status{Friend: d.Friend, Harness: d.Harness, Started: d.m.LastPing, Width: d.Width}
	if !l.passive {
		d.status.Session = SessionOK
		if err := l.recover(); err != nil || ctx.Err() != nil {
			return err
		}
		if b := l.state.WakeBarrier; b != "" && !l.isHeld(b) {
			return fmt.Errorf("wake barrier %s is missing from daemon-owned pending deliveries; reconcile missing or foreign-owned entry before restarting", b)
		}
	}
	for ctx.Err() == nil {
		now := d.Now()
		if l.state, err = d.sessionState(); err != nil {
			return err
		}
		d.status.Asleep = l.state.Asleep
		l.parkDeferred()
		for _, p := range d.m.TickWhen(now, l.state.Asleep) {
			l.say(p)
		}
		if l.passive && l.notice != nil {
			// nothing can be pushed in: the session hears of it from its own read
			d.Record(now.UTC().Format(time.RFC3339) + " not delivered: " + d.Harness + " has no deliver command: " + l.notice.Subject)
			l.notice = nil
		}
		mode, width := l.row(now)
		storeOK := l.read(now)
		if l.fatal != nil {
			return l.fatal
		}
		if ctx.Err() != nil {
			return nil
		}
		for _, t := range l.turns() {
			l.watch(t, now)
		}
		l.stampProgress(now)
		select {
		case r := <-l.results:
			l.batchDone(r, now)
		case r := <-l.lanes.results:
			l.laneDone(r, now)
		default:
		}
		if l.fatal != nil {
			return l.fatal
		}
		// a change of mode waits for the other mode's turns to end
		if mode != l.mode && l.busy == nil && !l.lanes.running() {
			d.Record(fmt.Sprintf("%s mode: %s, from %s (the friend row)", now.UTC().Format(time.RFC3339), mode, l.mode))
			l.mode = mode
		}
		d.status.Mode = l.mode
		if !l.broken && !l.state.Asleep && !l.passive {
			if err := l.fill(); err != nil {
				return err
			}
		}
		switch {
		case l.broken, l.state.Asleep:
		case l.mode == ModeOneShot:
			l.laneStep(now, width)
			d.status.Lanes = l.lanes.said(width)
		case l.busy == nil && len(l.hand) > 0:
			l.startBatch(now)
		case l.busy != nil && !l.busy.running && !l.retry.IsZero() && !now.Before(l.retry):
			l.retry = time.Time{}
			l.startTurn(l.busy, now, l.deliverBatch(l.busy))
		}
		if l.fatal != nil {
			return l.fatal
		}
		if l.broken && !l.told {
			l.told = d.tellBroken(ctx, l.b, l.brokenAfter)
		}
		if storeOK {
			if err := d.Beat(ctx, l.state.Asleep); err != nil {
				d.status.BeatError = err.Error()
			} else {
				d.status.BeatError, d.status.Beats, d.status.LastBeat = "", d.status.Beats+1, now
			}
		}
		if d.m.Challenge != Quiet { // the nonce says which challenge a pong answers; its at is the store's clock, never compared with ours
			if p, found, err := d.Pong(); err == nil && found {
				d.m.Pong(p.At, p.Nonce)
			}
		}
		d.flush(now)
	}
	return nil
}

func (d *Daemon) sessionState() (SessionState, error) {
	if d.StateDir == "" {
		return SessionState{Coordinator: d.Coordinator}, nil
	}
	return ReadSessionState(d.StateDir)
}

// entryBefore orders two stream entry ids numerically (ms, then seq).
func entryBefore(a, b string) bool {
	am, as, _ := strings.Cut(a, "-")
	bm, bs, _ := strings.Cut(b, "-")
	x, _ := strconv.ParseUint(am, 10, 64)
	y, _ := strconv.ParseUint(bm, 10, 64)
	if x != y {
		return x < y
	}
	x, _ = strconv.ParseUint(as, 10, 64)
	y, _ = strconv.ParseUint(bs, 10, 64)
	return x < y
}

// recover takes in every page of the daemon's own pending entries, so work a
// dead daemon held is dispatched before anything new, and never another
// consumer's. An error is retried, dispatching nothing meanwhile; it answers
// only a state error that ends the run. The empty recovery also makes the
// group.
func (l *loop) recover() error {
	d := l.d
	for cursor := ""; l.ctx.Err() == nil; {
		entries, next, err := l.b.PendingPage(l.ctx, d.Friend, DaemonConsumer, cursor, DaemonReadBatch)
		if l.ctx.Err() != nil {
			return nil
		}
		if err != nil {
			d.status.StoreError = err.Error()
			d.flush(d.Now())
			d.Pause(l.ctx, BeatEvery)
			continue
		}
		d.status.StoreError = ""
		for _, e := range entries {
			if err := l.observe(e, d.Now()); err != nil {
				return err
			}
		}
		if next == "" {
			break
		}
		cursor = next
	}
	return nil
}

// isHeld says whether the entry is kept by id or in the hand.
func (l *loop) isHeld(entry string) bool {
	return slices.Contains(l.held, entry) || l.inHand[entry]
}

// hold keeps a pending entry by id, oldest first, unless the daemon has it.
func (l *loop) hold(entry string) {
	if l.isHeld(entry) {
		return
	}
	l.held = append(l.held, entry)
	sort.SliceStable(l.held, func(i, j int) bool { return entryBefore(l.held[i], l.held[j]) })
}

// observe is a pending entry the daemon did not read to deliver at once
// (recovered at start, or read fresh while asleep): a message from the
// configured coordinator wakes the session, and, if it is no ping, its entry
// becomes the wake barrier (the first delivery once awake); a ping is
// answered, acked and never held; anything else is kept by id.
func (l *loop) observe(e bus.Entry, now time.Time) error {
	d := l.d
	msg := e.Message()
	nonce, seat, since, isPing := ParsePing(msg.Body)
	if err := l.wake(e, msg, isPing); err != nil {
		return err
	}
	if isPing {
		l.ping(e, msg, nonce, seat, since, now)
		if _, err := l.b.AckEntry(l.ctx, d.Friend, e.Entry); err != nil {
			d.status.StoreError = err.Error()
			return nil
		}
		delete(l.answered, e.Entry)
		return nil
	}
	l.hold(e.Entry)
	return nil
}

// wake wakes the session when the entry is from the configured coordinator,
// whatever its subject or body: awake state is committed first, under the
// session-state lock, so a sleep that landed after the daemon last read the
// state is seen. A message that is no ping becomes the wake barrier, the first
// delivery; a ping is answered and acked, so it is none. From is routing
// metadata, not authentication (SPEC-FRIEND.md, asleep behaviour).
func (l *loop) wake(e bus.Entry, msg bus.Message, isPing bool) error {
	d := l.d
	if d.StateDir == "" || l.state.Coordinator == "" || msg.From != l.state.Coordinator || e.Entry == l.state.WakeBarrier {
		return nil
	}
	s, err := UpdateSessionState(d.StateDir, func(s *SessionState) error {
		if s.Asleep && s.Coordinator != "" && s.Coordinator == msg.From && e.Entry != s.WakeBarrier {
			s.Asleep = false
			if !isPing {
				s.WakeBarrier = e.Entry
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	l.state = s
	return nil
}

// parkDeferred puts a deferred batch back to held when the session fell
// asleep, or a wake barrier it does not carry came first: the held turn is
// not retried while the wake takes priority.
func (l *loop) parkDeferred() {
	t := l.busy
	if t == nil || t.running || l.retry.IsZero() {
		return
	}
	b := l.state.WakeBarrier
	if !l.state.Asleep && (b == "" || slices.Contains(t.entries, b)) {
		return
	}
	for _, e := range t.entries {
		delete(l.inHand, e)
		l.hold(e)
	}
	l.busy, l.retry, l.deferrals = nil, time.Time{}, 0
}

// fill moves held entries into the hand, barrier first, then oldest first,
// fetching their bodies only now. An entry the stream no longer holds ends the
// run: it was this daemon's pending work, and something removed it.
func (l *loop) fill() error {
	for len(l.held) > 0 && len(l.hand) < MaxBatch {
		n := min(MaxBatch-len(l.hand), len(l.held))
		ids := append([]string(nil), l.held[:n]...)
		if b := l.state.WakeBarrier; b != "" && !slices.Contains(ids, b) && slices.Contains(l.held, b) {
			ids[0] = b
		}
		entries, err := l.d.Store.Get(l.ctx, bus.StreamOf(l.d.Friend), ids)
		if l.ctx.Err() != nil {
			return nil
		}
		if err != nil {
			l.d.status.StoreError = err.Error()
			return nil
		}
		if len(entries) != len(ids) {
			return fmt.Errorf("pending delivery %s is missing; reconcile the stream before restarting", strings.Join(ids, ","))
		}
		l.held = slices.DeleteFunc(l.held, func(h string) bool { return slices.Contains(ids, h) })
		for _, e := range entries {
			l.hand = append(l.hand, e)
			l.inHand[e.Entry] = true
		}
	}
	return nil
}

// row is the mode and width the daemon delivers by: the friend's row when
// Row says it (one-shot needs a harness that opens sessions, LaneHarness,
// else the daemon delivers in batch and says why once), else batch at Width.
func (l *loop) row(now time.Time) (mode string, width int) {
	d := l.d
	mode, width = ModeBatch, d.Width
	if d.Row != nil {
		m, w := d.Row()
		if m != "" {
			mode = m
		}
		if w > 0 {
			width = w
		}
	}
	if width < 1 {
		width = 1
	}
	d.status.Width = width
	if mode == ModeOneShot {
		if _, ok := d.Deliver.(LaneHarness); !ok || l.passive {
			if !l.saidNoLanes {
				l.saidNoLanes = true
				d.Record(now.UTC().Format(time.RFC3339) + " mode: the row says one-shot, and " + d.Harness + " cannot open a session per lane; delivering in batch")
			}
			mode = ModeBatch
		}
	}
	return mode, width
}

// turns is every turn running now: the batch turn and the lanes'.
func (l *loop) turns() []*turn {
	var out []*turn
	if l.busy != nil && l.busy.running {
		out = append(out, l.busy)
	}
	for _, ln := range l.lanes.lanes {
		if ln.t != nil && ln.t.running {
			out = append(out, ln.t)
		}
	}
	return out
}

func (l *loop) say(p Push) {
	if p.Subject == "coordinator back" && !l.saidSilent {
		l.notice = nil // the session never heard otherwise: nothing to say
		return
	}
	l.notice = &p
}

// head is what heads the next turn: the word about the coordinator, taken
// (noticeTaken keeps it, owed again if the turn fails), and the pong line
// while a challenge is open.
func (l *loop) head() (notice, pong string) {
	l.noticeTaken = l.notice
	if l.notice != nil {
		notice = l.notice.Text
		l.saidSilent = l.notice.Subject == "coordinator silent"
		l.notice = nil
	}
	if l.d.m.Challenge != Quiet && l.d.PongCommand != nil {
		pong = l.d.PongCommand(l.d.m.Nonce)
	}
	return notice, pong
}

// coordinator is who the daemon tells: the seat the last ping named, else
// the Coordinator flag.
func (l *loop) coordinator() string {
	if l.d.m.Seat != "" {
		return l.d.m.Seat
	}
	return l.d.Coordinator
}

// tell sends the coordinator one message, said on the record when there is
// no one to tell or the send fails.
func (l *loop) tell(subject, body string, now time.Time) {
	to := l.coordinator()
	if to == "" {
		l.d.Record(now.UTC().Format(time.RFC3339) + " no coordinator to tell: " + subject)
		return
	}
	if _, err := l.b.Send(l.ctx, bus.Message{From: l.d.Friend, To: []string{to}, Subject: subject, Body: subject + "\n" + body}); err != nil {
		l.d.Record(now.UTC().Format(time.RFC3339) + " telling " + to + " failed: " + err.Error() + ": " + subject)
	}
}

func (l *loop) ping(e bus.Entry, msg bus.Message, nonce, seat string, since, now time.Time) {
	if l.answered[e.Entry] {
		return // the machine saw it when the daemon first did
	}
	l.d.daemonPong(l.ctx, l.b, msg, nonce, l.state.Asleep)
	l.answered[e.Entry] = true
	if l.state.Asleep {
		return // a sleeping session is not challenged: the machine does not step
	}
	var pushes []Push
	if l.state.Coordinator == "" {
		pushes = l.d.m.Ping(now, seatOf(seat, msg), since, nonce)
	} else {
		pushes = l.d.m.ReceivePing(now, msg.From, l.state.Coordinator, seatOf(seat, msg), since, nonce)
	}
	for _, p := range pushes {
		l.say(p)
	}
}

// read is the step's one look at the stream. Held back (asleep, a wake barrier
// not yet seen, or entries already held by id, which come first): fresh entries
// are read and kept by id (readHeld). Otherwise every pending message is taken
// into the hand when the session can take them (no batch turn running, the
// session not broken, the hand not full), else a peek that answers pings. It
// answers whether the store answered.
func (l *loop) read(now time.Time) bool {
	d, b := l.d, l.b
	switch {
	case l.passive:
	case l.broken:
	case l.state.Asleep || len(l.held) > 0 || (l.state.WakeBarrier != "" && !l.isHeld(l.state.WakeBarrier)):
		return l.readHeld(now)
	case l.busy == nil && len(l.hand) < MaxBatch:
		// in one-shot mode the lanes' turns run while the loop reads: it reads
		// at once and pauses after, so a lane's result is never a block behind
		block := BeatEvery
		if l.mode == ModeOneShot {
			block = 0
			defer d.Pause(l.ctx, BeatEvery)
		}
		got, err := b.RecvBatch(l.ctx, d.Friend, DaemonConsumer, block, MaxBatch-len(l.hand))
		for err == nil && len(got) > 0 {
			for _, e := range got {
				msg := e.Message()
				nonce, seat, since, isPing := ParsePing(msg.Body)
				if werr := l.wake(e, msg, isPing); werr != nil {
					l.fatal = werr
					return false
				}
				if isPing {
					// answered by the daemon, never pushed in: the transport is proved, and a ping is no turn
					l.ping(e, msg, nonce, seat, since, now)
					if _, aerr := b.AckEntry(l.ctx, d.Friend, e.Entry); aerr != nil {
						err = aerr
						break
					}
					delete(l.answered, e.Entry)
				} else if !l.inHand[e.Entry] {
					l.hand = append(l.hand, e)
					l.inHand[e.Entry] = true
				}
			}
			if err != nil || len(l.hand) >= MaxBatch {
				break
			}
			got, err = b.RecvBatch(l.ctx, d.Friend, DaemonConsumer, 0, MaxBatch-len(l.hand)) // the rest of what is pending, at once
		}
		if err != nil && l.ctx.Err() == nil {
			d.status.StoreError = err.Error()
			if block != 0 {
				d.Pause(l.ctx, BeatEvery)
			}
			return false
		}
		d.status.StoreError = ""
		return true
	}
	// a turn is running, the session is broken, the hand is full, or the harness is passive: peek, take nothing
	_, fresh, err := b.Peek(l.ctx, d.Friend)
	if l.ctx.Err() != nil {
		return false
	}
	ok := err == nil
	if err != nil {
		d.status.StoreError = err.Error()
	} else {
		d.status.StoreError = ""
		for _, e := range fresh {
			msg := e.Message()
			if l.passive && !l.observed[e.Entry] {
				// a passive harness takes nothing off the stream, so a message from the
				// coordinator wakes the session here; the entry is remembered in memory only
				l.observed[e.Entry] = true
				if d.StateDir != "" && l.state.Coordinator != "" && msg.From == l.state.Coordinator {
					s, uerr := UpdateSessionState(d.StateDir, func(s *SessionState) error {
						if s.Coordinator == msg.From {
							s.Asleep = false
						}
						return nil
					})
					if uerr != nil {
						l.fatal = uerr
						return false
					}
					l.state = s
				}
			}
			if nonce, seat, since, isPing := ParsePing(msg.Body); isPing {
				// the machine sees the ping when the daemon does: a turn longer than a window is no silence
				l.ping(e, msg, nonce, seat, since, now)
			}
		}
	}
	d.Pause(l.ctx, BeatEvery)
	return ok
}

// readHeld reads fresh entries as the daemon's own consumer and keeps them by
// id (observe): the daemon delivers nothing now, charges no failure and acks
// nothing but a ping. It reads fresh entries only, never reclaiming held work,
// so a coordinator's wake cannot starve behind it. It answers whether the
// store answered.
func (l *loop) readHeld(now time.Time) bool {
	d := l.d
	entries, err := d.Store.Read(l.ctx, bus.StreamOf(d.Friend), d.Friend, DaemonConsumer, BeatEvery, DaemonReadBatch)
	switch {
	case l.ctx.Err() != nil:
		return false
	case err != nil:
		d.status.StoreError = err.Error()
		d.Pause(l.ctx, BeatEvery)
		return false
	}
	d.status.StoreError = ""
	for _, e := range entries {
		if err := l.observe(e, now); err != nil {
			l.fatal = err
			return false
		}
	}
	return true
}

// take is the messages of the hand that go in the next turn: oldest first,
// at most MaxBatch and BatchBytes, at least one when any waits.
func (l *loop) take() (entries []string, msgs []bus.Message) {
	if b := l.state.WakeBarrier; b != "" { // the entry that woke the session goes first
		if i := slices.IndexFunc(l.hand, func(e bus.Entry) bool { return e.Entry == b }); i > 0 {
			l.hand = slices.Concat(l.hand[i:i+1], l.hand[:i], l.hand[i+1:])
		}
	}
	size := 0
	for len(l.hand) > 0 && (len(msgs) == 0 || (len(msgs) < MaxBatch && size+len(l.hand[0].Fields["body"]) <= BatchBytes)) {
		e := l.hand[0]
		l.hand = l.hand[1:]
		entries, msgs = append(entries, e.Entry), append(msgs, e.Message())
		size += len(e.Fields["body"])
	}
	return entries, msgs
}

// startTurn runs deliver for t in its own goroutine, its context carrying the
// watch on its output; the result goes to the batch's or the lanes' channel.
func (l *loop) startTurn(t *turn, now time.Time, deliver any) {
	tctx, cancel := context.WithCancel(l.ctx)
	seen := &atomic.Int64{}
	tctx = WithOutputSeen(tctx, func() { seen.Add(1) })
	t.started, t.running, t.cancel, t.seen, t.seenN, t.lastOut, t.stopped = now, true, cancel, seen, 0, now, false
	switch f := deliver.(type) {
	case func(context.Context) result:
		go func() { r := f(tctx); cancel(); l.results <- r }()
	case func(context.Context) laneResult:
		go func() { r := f(tctx); cancel(); l.lanes.results <- r }()
	}
}

func (l *loop) deliverBatch(t *turn) func(context.Context) result {
	return func(ctx context.Context) result {
		exit, err := l.d.Deliver.Deliver(ctx, t.text)
		return result{t, exit, err}
	}
}

// reserve takes a turn's messages out of the hand if the session may take a
// turn now, checked under the session-state lock when there is one: that
// reservation is the start boundary, so a sleep that lands after it does not
// cancel the turn, and one that lands before it stops the turn from starting.
// The harness's I/O begins after the lock is released. It answers whether the
// turn may start.
func (l *loop) reserve(t *turn) bool {
	start := func(s SessionState) error {
		l.state = s
		if s.Asleep || (s.WakeBarrier != "" && !slices.ContainsFunc(l.hand, func(e bus.Entry) bool { return e.Entry == s.WakeBarrier })) {
			return nil // asleep, or the entry that woke the session is not in hand yet: it goes first
		}
		t.entries, t.msgs = l.take()
		t.reserved = true
		return nil
	}
	var err error
	if l.d.StateDir != "" {
		err = WithSessionState(l.d.StateDir, start)
	} else {
		err = start(l.state)
	}
	if err != nil {
		l.fatal = err
	}
	return t.reserved
}

func (l *loop) startBatch(now time.Time) {
	t := &turn{}
	if !l.reserve(t) {
		return
	}
	var subjects []string
	for _, m := range t.msgs {
		subjects = append(subjects, m.Subject)
	}
	t.subjects = fmt.Sprintf("%q", strings.Join(subjects, " | "))
	notice, pong := l.head()
	t.notice = l.noticeTaken
	t.text = Batch(t.msgs, notice, pong)
	l.busy = t
	l.startTurn(t, now, l.deliverBatch(t))
}

// watch is the silence watch on a running turn: a turn that prints is
// working; one silent past SilentStop is stopped, said on the record.
func (l *loop) watch(t *turn, now time.Time) {
	if n := t.seen.Load(); n != t.seenN {
		t.seenN, t.lastOut = n, now
	}
	if !t.stopped && now.Sub(t.lastOut) >= l.silentStop {
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
		if t == nil || !t.running || ln.card == nil || t.seenN == 0 || !t.lastOut.After(t.stamped) || now.Sub(t.stamped) < ProgressEvery {
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

// settle is what a turn's end does to the messages it carried, and to the
// session's refusal streak; it answers the record's words for it. Exit 0
// acks them together; a provider's refusal leaves them pending, counted
// toward nothing, and the same refusal BrokenAfter turns in a row breaks the
// session; any other failure counts toward MaxDeliveries.
func (l *loop) settle(t *turn, ok bool, err error, now time.Time) string {
	d := l.d
	line := ""
	for _, e := range t.entries {
		delete(l.inHand, e) // acked below, or pending for the claim to hand in again
	}
	if b := l.state.WakeBarrier; b != "" && d.StateDir != "" && slices.Contains(t.entries, b) {
		// any completion clears the wake barrier, a failed turn too: its failure counts as any other
		s, err := UpdateSessionState(d.StateDir, func(s *SessionState) error {
			if s.WakeBarrier == b {
				s.WakeBarrier = ""
			}
			return nil
		})
		if err != nil {
			l.fatal = err // later held entries are not dispatched past a barrier that could not be cleared
		}
		l.state = s
	}
	var refused ProviderRefused
	switch {
	case ok:
		l.streak, l.refusal = 0, ""
		if len(t.entries) > 0 {
			if _, err := d.Store.Ack(l.ctx, bus.StreamOf(d.Friend), d.Friend, t.entries...); err != nil {
				d.status.StoreError = err.Error()
				line += " ack=failed"
			} else {
				d.status.Delivered += len(t.entries)
				line += " acked=true"
				for _, e := range t.entries {
					delete(l.failed, e)
				}
			}
		}
	case errors.As(err, &refused) && !t.stopped:
		// the session is at fault, not the messages: they stay pending, counted toward nothing
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
	default:
		l.streak, l.refusal = 0, ""
		var given []string
		for _, e := range t.entries {
			l.failed[e]++
			if l.failed[e] >= MaxDeliveries {
				given = append(given, e)
			}
		}
		if len(t.entries) > 0 {
			line += fmt.Sprintf(" deliveries=%d/%d", l.failed[t.entries[0]], MaxDeliveries)
		}
		if len(given) > 0 {
			line += " given_up=true"
			if len(t.entries) > 1 {
				line += fmt.Sprintf(" given_up_messages=%d", len(given))
			}
			if _, err := d.Store.Ack(l.ctx, bus.StreamOf(d.Friend), d.Friend, given...); err != nil {
				d.status.StoreError = err.Error()
				line += " ack=failed"
			} else {
				line += " acked=true"
				for _, e := range given {
					delete(l.failed, e)
				}
			}
		}
	}
	if !ok && t.notice != nil && l.notice == nil { // the word was not heard: it is owed again
		l.notice = t.notice
		l.saidSilent = t.notice.Subject != "coordinator silent"
	}
	if l.broken && !l.told {
		line += fmt.Sprintf("\n%s session %s broken: %s (%d turns in a row); delivering nothing into it until the daemon restarts, every message stays pending",
			now.UTC().Format(time.RFC3339), d.status.SessionID, d.status.SessionReason, l.brokenAfter)
	}
	return line
}

// batchDone is the batch turn's end: a deferral keeps it in hand, tried
// again; anything else settles its messages.
func (l *loop) batchDone(r result, now time.Time) {
	d := l.d
	r.t.running = false
	var deferred Deferred
	if errors.As(r.err, &deferred) && !r.t.stopped { // not a failure: the turn stays in hand, tried again, counted toward nothing
		l.deferrals++
		l.retry = now.Add(RecheckEvery)
		if l.deferrals == 1 || now.Sub(l.deferSaid) >= DeferredSaidEvery {
			l.deferSaid = now
			d.Record(fmt.Sprintf("%s subject=%s deferred=%d: %s; tried again every %s, counted toward nothing (said once per %s)",
				now.UTC().Format(time.RFC3339), r.t.subjects, l.deferrals, deferred.Reason, RecheckEvery, DeferredSaidEvery))
		}
		return
	}
	line := fmt.Sprintf("%s subject=%s messages=%d took=%s exit=%d", now.UTC().Format(time.RFC3339), r.t.subjects, len(r.t.entries), now.Sub(r.t.started).Round(time.Millisecond), r.exit)
	if r.t.notice != nil {
		line += fmt.Sprintf(" notice=%q", r.t.notice.Subject)
	}
	if r.err != nil {
		line += " error=" + fmt.Sprintf("%q", r.err.Error())
	}
	if r.t.stopped {
		line += fmt.Sprintf(" stopped=%q", "no output for "+l.silentStop.String())
	}
	ok := r.err == nil && r.exit == 0 && !r.t.stopped
	line += l.settle(r.t, ok, r.err, now)
	for _, part := range strings.Split(line, "\n") {
		d.Record(part)
	}
	l.busy, l.retry, l.deferrals, l.deferSaid = nil, time.Time{}, 0, time.Time{}
}

// tellBroken sends the coordinator one message that the session is broken:
// to the seat the last ping named, else Coordinator. It answers whether
// the word went out, or there was no one to tell (said on the record); a
// send that fails is tried again the next step.
func (d *Daemon) tellBroken(ctx context.Context, b *bus.Bus, brokenAfter int) bool {
	to := d.m.Seat
	if to == "" {
		to = d.Coordinator
	}
	s := d.status
	if to == "" {
		d.Record("session broken, and no coordinator to tell: no ping has named the seat and --coordinator is not set")
		return true
	}
	subject := fmt.Sprintf("friend %s: session %s broken: %s", d.Friend, s.SessionID, s.SessionReason)
	body := subject + fmt.Sprintf("\nThe provider refused %d turns in a row the same way. The daemon delivers nothing into the session until it restarts; every message stays pending, none given up. Renew the session, then restart the daemon (nova-friend install again, or launchctl kickstart -k gui/<uid>/com.nova.friend-%s).\n", brokenAfter, d.Friend)
	if _, err := b.Send(ctx, bus.Message{From: d.Friend, To: []string{to}, Subject: subject, Body: body}); err != nil {
		d.status.StoreError = "telling " + to + " the session is broken: " + err.Error()
		return false
	}
	return true
}

// seatOf is the seat a ping names, else its sender.
func seatOf(seat string, m bus.Message) string {
	if seat == "" {
		return m.From
	}
	return seat
}

// daemonPong answers a ping at once, from the daemon: transport is up. A
// sleeping daemon says so (asleep=true). A send that fails is the store's
// error on the status.
func (d *Daemon) daemonPong(ctx context.Context, b *bus.Bus, ping bus.Message, nonce string, asleep bool) {
	state := ""
	if asleep {
		state = " asleep=true"
	}
	_, err := b.Send(ctx, bus.Message{From: d.Friend, To: []string{ping.From}, Subject: DaemonPongSubject, Re: ping.ID, Body: "daemon-pong " + nonce + state + "\n"})
	if err != nil {
		d.status.StoreError = "daemon pong: " + err.Error()
	}
}

// flush writes the status when it changed, and every StatusEvery anyway,
// so a reader tells a live daemon from a dead one by the file's age.
func (d *Daemon) flush(now time.Time) {
	s := d.status
	s.Connection, s.LastPing, s.Seat, s.SeatSince = d.m.Connection, d.m.LastPing, d.m.Seat, d.m.SeatSince
	s.Challenge, s.Nonce, s.LastPong, s.Pongs = d.m.Challenge, d.m.Nonce, d.m.LastPong, d.m.Pongs
	s.At, s.LastBeat, s.Beats = time.Time{}, time.Time{}, 0 // what every beat changes is not a change
	if s == d.written0 && now.Sub(d.written) < StatusEvery {
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
