package friend

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// Presence is whether the friend's SESSION is alive, never its daemon
// (docs/SPEC-FRIEND.md, presence). The finding of 2026-10-04: three friends
// read up with eight cards each while their harness apps were not running,
// because the daemon answered every ping itself. The proof is the session's:
// after SessionQuiet with no bus message from the session, the daemon
// delivers a session check carrying a fresh nonce through the adapter, the
// way a card goes in; only the session's own reply carrying that nonce
// counts; SessionBound with none and the friend is down, NoSessionAnswer; the
// next answer, or any bus message the session writes, brings it back up. The
// daemon's pong stays the daemon's and never counts.
const (
	SessionQuiet = 10 * time.Minute
	SessionBound = 5 * time.Minute
	// ProveEvery is how long after the last check went in the next goes in while the
	// session is up, whatever else it says on the bus, timed from the ask: the sprint
	// server counts only an answered check, for sprint.FriendProofLive (fifteen
	// minutes), so a session that answers within SessionBound is proved again before
	// the last proof lapses (ProveEvery + SessionBound < FriendProofLive).
	ProveEvery = 8 * time.Minute
	// ReaskAfter is how long an unanswered check waits before it is asked again when the
	// session has not read it: a queueing harness keeps every copy, so a check is asked
	// again early only once the session has read the last (Presence.Read).
	ReaskAfter = time.Hour

	NoSessionAnswer = "no session answer"
	NotYetAnswered  = "no session answer yet" // the daemon started; nothing has answered

	SessionCheckPrefix = "SESSION CHECK "
	PresenceFile       = "presence.json"

	LogBatch = 1000 // log entries read per step; the rest the next
)

// The presence, as the presence file says it.
const (
	PresenceUp   = "up"
	PresenceDown = "down"
)

// Presence is the session's proof of life, stepped by the clock and the bus
// messages the daemon hands it, so every rule is tested with no socket and
// no real time. It starts down with a check owed: a daemon that came up
// proves nothing about the session.
type Presence struct {
	Quiet, Bound time.Duration

	Up        bool
	Reason    string    // why it is down; empty while up
	LastHeard time.Time // the session's last bus message, or its last answer, while up
	Nonce     string    // the latest check's nonce, until the session answers it
	Asked     time.Time // when the latest check went in
	Open      bool      // the latest check is unanswered and within the bound
	Owed      bool      // a check is due and has not gone in
	Checks    int
	Answers   int
	Answered  string // the nonce the session last answered
	// Read is the latest check read by the session: its headless turn ended, or the
	// adapter saw the session take it (ReadOnReturn). Until then it is asked again only
	// after ReaskAfter, so a session that queues checks never holds a pile of them.
	Read bool
	// Proven is the push proved this run: the session answered a check or wrote on
	// the bus since the daemon started. Until then the daemon delivers nothing
	// (docs/SPEC-FRIEND.md, The push proof); once proved it stays proved for the run.
	Proven bool
}

// StartPresence is the presence as the daemon comes up at now: down, not yet
// answered, a check owed at once.
func StartPresence() *Presence {
	return &Presence{Quiet: SessionQuiet, Bound: SessionBound, Reason: NotYetAnswered, Owed: true}
}

// Heard is a bus message the session wrote, at now: the session is alive, so
// the friend is up (rose: it was down). A check still open stays open, its
// answer still owed: only an answered check proves her session to the sprint
// server. The daemon's own messages never reach here (SessionCheck.read).
func (p *Presence) Heard(now time.Time) (rose bool) {
	rose = !p.Up
	p.Up, p.Reason, p.LastHeard, p.Proven = true, "", now, true
	return rose
}

// Ask is the check with nonce going into the session at now: the bound runs
// from here.
func (p *Presence) Ask(now time.Time, nonce string) {
	p.Nonce, p.Asked, p.Open, p.Owed, p.Read, p.Checks = nonce, now, true, false, false, p.Checks+1
}

// Answer is the session's reply carrying nonce, at now: the latest check's
// nonce makes the friend up, late or not; any other, or one already
// answered, changes nothing (current false).
func (p *Presence) Answer(now time.Time, nonce string) (current bool) {
	if nonce == "" || nonce != p.Nonce {
		return false
	}
	p.Up, p.Reason, p.LastHeard, p.Answered, p.Nonce, p.Open, p.Owed, p.Answers, p.Proven = true, "", now, nonce, "", false, false, p.Answers+1, true
	return true
}

// Tick is the clock at now: an open check past the bound makes the friend
// down, NoSessionAnswer, unless the session wrote on the bus since it went in,
// and so does silence for SessionQuiet plus SessionBound with it unanswered;
// a check is owed ProveEvery after the last check went in while up and SessionQuiet after it
// while down, and an unanswered one the session has not read is asked again
// only after ReaskAfter.
func (p *Presence) Tick(now time.Time) {
	if p.Open && now.Sub(p.Asked) >= p.Bound {
		p.Open = false
		if !p.Up || p.LastHeard.Before(p.Asked) {
			p.Up, p.Reason = false, NoSessionAnswer
		}
	}
	// a check left unanswered while the session spoke, then silence: down once the
	// session has said nothing for SessionQuiet plus SessionBound, the check not asked
	// again before its time (a queueing harness keeps every copy)
	last := p.LastHeard
	if p.Asked.After(last) {
		last = p.Asked
	}
	if p.Up && !p.Open && p.Nonce != "" && now.Sub(last) >= p.Quiet+p.Bound {
		p.Up, p.Reason = false, NoSessionAnswer
	}
	if p.Open || p.Owed {
		return
	}
	since, every := now.Sub(p.Asked), ProveEvery // from the ask: a slow answer never stretches the cycle
	if !p.Up {
		every = p.Quiet
	}
	if p.Nonce != "" && !p.Read {
		every = ReaskAfter
	}
	if since >= every {
		p.Owed = true
	}
}

// SessionCheckText is the check as the session reads it: the nonce, and the
// one line to run with nothing to fill in, sent to to (the seat, else the
// coordinator; the pong verb's own default when empty).
func SessionCheckText(nonce, pong, to string) string {
	if to != "" {
		pong += " --to " + to
	}
	return fmt.Sprintf("%s%s\nThe daemon has had no bus message from this session for a while and asks whether the session is alive; only an answer from inside this session counts, and none within %s puts you down. Answer now, before anything else, with one command, then end this turn: %s\n", SessionCheckPrefix, nonce, SessionBound, pong)
}

// PresenceStatus is the presence file: what status reads.
type PresenceStatus struct {
	Friend    string    `json:"friend"`
	At        time.Time `json:"at"`
	Presence  string    `json:"presence"` // PresenceUp or PresenceDown
	Reason    string    `json:"reason,omitempty"`
	LastHeard time.Time `json:"last_heard"`
	Nonce     string    `json:"nonce,omitempty"`
	Asked     time.Time `json:"asked"`
	Checks    int       `json:"checks"`
	Answers   int       `json:"answers"`
	Answered  string    `json:"answered,omitempty"` // the nonce the session last answered
}

func presencePath(stateDir string) string { return filepath.Join(stateDir, PresenceFile) }

// WritePresence writes the presence file, whole.
func WritePresence(stateDir string, s PresenceStatus) error { return write(presencePath(stateDir), s) }

// ReadPresence is the presence file; found is false when no daemon wrote one.
func ReadPresence(stateDir string) (s PresenceStatus, found bool, err error) {
	found, err = read(presencePath(stateDir), &s)
	return s, found, err
}

// SessionCheck is the daemon's side of presence: it reads the bus log for the
// session's messages, steps the Presence, and puts the check into the session
// through the adapter (Gate) or, for a harness with no deliver command, on
// the friend's own stream. The beat never waits on it (Beat): the session's
// evidence rides on the beat as a fact of its own (Evidence). The daemon's own
// sends go through DaemonStore, so a message the daemon wrote never passes for
// the session's.
type SessionCheck struct {
	Friend string
	Store  bus.Store // the store itself; the daemon is handed DaemonStore
	// Deliver is the adapter the check goes in by, as Gate wrapped it.
	Deliver Deliverer
	Now     func() time.Time
	Nonce   func() string             // a fresh nonce per check
	Text    func(nonce string) string // the check as the session reads it: the one answer line to run
	Record  func(line string)         // nil records nothing
	Save    func(PresenceStatus) error
	Go      func(func()) // runs a check's delivery; nil is a goroutine
	// Run is this daemon's run, its generation, said with every check and answer
	// on the beat: the server counts an answer only to a check the same run asked.
	Run string
	// Keep is the nonce of a check the daemon's last run put into the session and
	// never saw answered (its presence file's nonce): every check carries it until
	// the push is proved, so the session's late answer to the check already queued
	// in it proves the push, whatever restarts came between. "" starts fresh.
	Keep string
	// Ctx is the daemon's long-lived context: a check's delivery runs under it, so
	// the turn outlives the beat's per-send deadline and is still cancelled when the
	// daemon stops. nil runs the delivery under the step's own context (a test's).
	Ctx context.Context

	stepMu     sync.Mutex // serializes log cursors; transport never holds mu
	mu         sync.Mutex
	m          *Presence
	cursor     string          // the last log entry read
	sent       map[string]bool // message ids the daemon sent, until the log shows them
	turn       sync.RWMutex    // the session's turns hold it shared; a check holds it alone
	cancel     context.CancelFunc
	saved      PresenceStatus
	savedAt    time.Time
	keep       string    // the nonce every check carries until the push is proved
	waiting    string    // why the owed check waits, said once while it stands
	owedSince  time.Time // when the owed check first could not go in; zero while none waits
	owedSaid   bool      // the owed check's refusal line was said
	staleSince time.Time // when the turn lock was first seen held while the adapter's record says no turn runs
	unproven   string    // the nonce the push-unproven line was said for
	refused    string    // the delivery refusal last said
	toCheck    string    // the check asked that no beat has said yet
	toPong     string    // the check answered that no beat has said yet
	gated      int       // turns at the gate, from before any wait under it to their end
	gatedSince time.Time // when the first of them came to the gate
}

// StaleTurnLock is how long the turn lock may be held while a headless
// adapter's own turn record says no turn runs before the session check goes in
// by the record (docs/SPEC-FRIEND.md, presence: the headless case): one step's
// race with a turn on its way into the adapter is never read as a stale lock.
const StaleTurnLock = time.Minute

// TurnRecord is a headless adapter's own record of its turns: one that delivers
// by running a one-shot program into the session (dsh headless, gemini
// --resume), each turn a process that starts and ends. A turn runs while one has
// begun and not ended, since the latest's start.
type TurnRecord interface {
	TurnUnderWay() (running bool, since time.Time)
}

// headlessOf is the adapter's turn record under the gates (Gate, Limits.Gate),
// nil for an adapter that keeps none.
func headlessOf(d Deliverer) TurnRecord {
	var rec TurnRecord
	under(d, func(a Deliverer) bool { rec, _ = a.(TurnRecord); return rec != nil })
	return rec
}

// ReadOnReturn is an adapter whose delivery returns exit 0 only once the session
// has taken the text: a headless turn that ran (dsh, gemini, opencode run), or a
// message the session marked read (antigravity's read.json). A check it delivered
// is read, and may be asked again on the check cadence.
type ReadOnReturn interface{ ReadOnReturn() }

func (*DSH) ReadOnReturn()         {}
func (*Gemini) ReadOnReturn()      {}
func (*OpenCode) ReadOnReturn()    {}
func (*Antigravity) ReadOnReturn() {}

// under calls f on d and each adapter under its gates until f says found.
func under(d Deliverer, f func(Deliverer) bool) bool {
	for d != nil {
		if f(d) {
			return true
		}
		switch g := d.(type) {
		case turnGated:
			d = g.Deliverer
		case turnGatedLanes:
			d = g.Deliverer
		case *gated:
			d = g.d
		case *gatedLanes:
			d = g.d
		default:
			return false
		}
	}
	return false
}

// DaemonStore is the store the daemon sends through: each message it adds is
// remembered as the daemon's.
func (s *SessionCheck) DaemonStore() bus.Store { return daemonStore{s.Store, s} }

type daemonStore struct {
	bus.Store
	s *SessionCheck
}

func (d daemonStore) AddAll(ctx context.Context, streams []string, fields map[string]string, marks ...bus.Mark) error {
	d.s.mu.Lock()
	if d.s.sent == nil {
		d.s.sent = map[string]bool{}
	}
	d.s.sent[fields["id"]] = true
	d.s.mu.Unlock()
	return d.Store.AddAll(ctx, streams, fields, marks...)
}

// Gate is inner with the session's turns holding the turn shared, so a check
// never goes into a session in the middle of a turn. A passive harness
// (no deliver command) is answered as it is: its check goes on the stream.
// A harness that opens a session per lane keeps its lanes.
func (s *SessionCheck) Gate(inner Deliverer) Deliverer {
	if _, passive := inner.(interface{ Passive() }); passive {
		return inner
	}
	g := turnGated{inner, s}
	if lh, ok := inner.(LaneHarness); ok {
		return turnGatedLanes{g, lh}
	}
	return g
}

type turnGated struct {
	Deliverer
	s *SessionCheck
}

func (g turnGated) Deliver(ctx context.Context, text string) (int, error) {
	defer g.s.enter()()
	g.s.turn.RLock()
	defer g.s.turn.RUnlock()
	return g.Deliverer.Deliver(ctx, text)
}

// enter is a turn at the gate, from before any wait under it (the limit gate's
// included) until it returns: the record the check reads before it believes no
// turn runs; the func it answers is the turn's end.
func (s *SessionCheck) enter() func() {
	s.mu.Lock()
	if s.gated == 0 {
		s.gatedSince = s.Now()
	}
	s.gated++
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		s.gated--
		s.mu.Unlock()
	}
}

type turnGatedLanes struct {
	turnGated
	lh LaneHarness
}

func (g turnGatedLanes) OpenSession(ctx context.Context, seed string) (string, error) {
	defer g.s.enter()()
	g.s.turn.RLock()
	defer g.s.turn.RUnlock()
	return g.lh.OpenSession(ctx, seed)
}

func (g turnGatedLanes) DeliverTo(ctx context.Context, session, text string) (LaneTurn, error) {
	defer g.s.enter()()
	g.s.turn.RLock()
	defer g.s.turn.RUnlock()
	return g.lh.DeliverTo(ctx, session, text)
}

// Present is whether the session is up, and why not.
func (s *SessionCheck) Present() (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		return false, NotYetAnswered
	}
	return s.m.Up, s.m.Reason
}

// Evidence is the session's last evidence: its last bus message or its last
// answer to a check, zero while it has given none. It is advisory activity, separate from nonce-based server proof
// (docs/SPEC-FRIEND.md, The beat).
func (s *SessionCheck) Evidence() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		return time.Time{}
	}
	return s.m.LastHeard
}

// Beat is beat with the check stepped first, and sent whatever the session
// says (every-friend-daemon-beats-every-second, 2026-10-06): the beat is the
// daemon's liveness and nothing else, so a session that has not answered, or
// is past its bound, still has a daemon beating for it, and her row says the
// session is deaf rather than that nothing is there. Whether she is up is the
// sprint's rule over both facts (docs/SPEC-SPRINT.md, friend presence). The
// model is tla/Presence.tla (BeatFresh; its witness MCPresenceBrokenHeldBeat is
// the hold this replaced).
func (s *SessionCheck) Beat(beat func(ctx context.Context) error) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		s.Step(ctx)
		return beat(ctx)
	}
}

// BeatOr is beat while the session is up, and while it is down the beat that
// says so (down: until when the daemon next expects an answer, and why: the
// push unproven with the check's nonce, or no session answer to it), so the
// sprint server reads her down with the daemon's reason the second it knows
// (docs/SPEC-FRIEND.md, presence). A nil down sends the liveness beat without a session gate.
// Each call steps the check first.
func (s *SessionCheck) BeatOr(beat func(ctx context.Context) error, down func(ctx context.Context, until time.Time, reason string) error) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		s.Step(ctx)
		up, _ := s.Present()
		if up {
			return beat(ctx)
		}
		if down == nil {
			return beat(ctx)
		}
		until, why := s.downBeat(s.Now())
		if err := down(ctx, until, why); err != nil {
			return fmt.Errorf("beating down (%s): %w", why, err)
		}
		return nil
	}
}

// BeatAlways is beat with the check stepped first and never held back: a
// claude friend's (docs/SPEC-FRIEND.md, The push proof), whose cards run as
// processes of their own and whose presence at the server is their finishes;
// her session check goes in by the folder (FolderCheck) and its answer rides
// the beat (Words), so a live session proves the push and no session refuses
// nothing.
func (s *SessionCheck) BeatAlways(beat func(ctx context.Context) error) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		s.Step(ctx)
		return beat(ctx)
	}
}

// downBeat is the down beat's until and reason at now: an open check's bound,
// else the next check's bound (SessionQuiet after the last, or now when one is
// owed), and the reason naming the check's nonce.
func (s *SessionCheck) downBeat(now time.Time) (until time.Time, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.m
	nonce := m.Nonce
	if nonce == "" {
		nonce = s.keep
	}
	switch {
	case m.Open:
		until = m.Asked.Add(m.Bound)
	case m.Asked.IsZero() || !m.Asked.Add(m.Quiet).After(now):
		until = now.Add(m.Bound)
	default:
		until = m.Asked.Add(m.Quiet + m.Bound)
	}
	check := "session check " + dash(nonce)
	switch {
	case !m.Proven && m.Asked.IsZero():
		reason = "push unproven: " + check + " is owed and not yet in the session"
	case !m.Proven && m.Open:
		reason = "push unproven: " + check + " in the session since " + m.Asked.UTC().Format(time.RFC3339) + ", not yet answered"
	case !m.Proven:
		reason = "push unproven: " + check + " unanswered since " + m.Asked.UTC().Format(time.RFC3339)
	default:
		reason = m.Reason + " to " + check + " within " + m.Bound.String()
	}
	return until, reason
}

// BeatWords are a beat's proof words (nova-sprint friend beat --run, --check,
// --pong): the daemon's run, the check it put into the session, and the check
// its session answered, each "" when there is none to say. The sprint server
// counts an answer only when it names a check this run asked (sprint.ProveBeat).
type BeatWords struct {
	Run, Check, Pong string
	// StopReturns is how many stop-returns the lanes owe (stop.go, OwedStopReturns): the
	// beat carries it (friend beat --stop-returns) while it is above zero.
	StopReturns int
}

// Words is what the next beat says: the check asked and the check answered that
// no beat has carried yet. A beat the server took clears them (Said).
func (s *SessionCheck) Words() BeatWords {
	s.mu.Lock()
	defer s.mu.Unlock()
	return BeatWords{Run: s.Run, Check: s.toCheck, Pong: s.toPong}
}

// Said is w carried by a beat the server took: each word said is not said again.
func (s *SessionCheck) Said(w BeatWords) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if w.Check != "" && s.toCheck == w.Check {
		s.toCheck = ""
	}
	if w.Pong != "" && s.toPong == w.Pong {
		s.toPong = ""
	}
}

// Proof is whether the push is proved this run (the session answered a check,
// or wrote on the bus, since the daemon started), and while it is not, the
// nonce the check carries. The daemon delivers nothing until it is.
func (s *SessionCheck) Proof() (proven bool, nonce string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		return false, s.Keep
	}
	if s.m.Proven {
		return true, ""
	}
	if s.m.Nonce != "" {
		return false, s.m.Nonce
	}
	return false, s.keep
}

// nextNonce is the nonce the next check carries: while the push is unproved,
// the one already queued in the session (Keep, else the first asked), so the
// check is asked again with it and never a new one per try; once proved, a
// fresh one each check. Called with mu held.
func (s *SessionCheck) nextNonce() string {
	if s.m.Proven {
		return s.Nonce()
	}
	if s.keep == "" {
		s.keep = s.Nonce()
	}
	return s.keep
}

// Step is one look: the session's messages since the last, the clock, and
// the check when it is owed.
func (s *SessionCheck) Step(ctx context.Context) {
	s.stepMu.Lock()
	defer s.stepMu.Unlock()
	now := s.Now()
	s.mu.Lock()
	if s.m == nil {
		s.m, s.keep = StartPresence(), s.Keep
	}
	proven := s.m.Proven
	answered, rose, err := s.read(ctx, now)
	was := s.m.Reason
	s.m.Tick(now)
	fell := was != NoSessionAnswer && s.m.Reason == NoSessionAnswer
	if !s.m.Up && !s.m.Open && s.cancel != nil {
		s.cancel() // never answered: the check's turn ends with its bound
	}
	owed := s.m.Owed
	proved := !proven && s.m.Proven
	unproven := ""
	if fell && !s.m.Proven && s.m.Nonce != s.unproven {
		unproven, s.unproven = s.m.Nonce, s.m.Nonce
	}
	asked := s.m.Asked
	if proved {
		s.keep = ""
	}
	s.mu.Unlock()
	if err != nil {
		s.record(now, "presence: the bus log: "+err.Error())
	}
	if answered != "" {
		s.record(now, "presence: up: the session answered "+answered)
	} else if rose {
		s.record(now, "presence: up: the session wrote on the bus")
	}
	if proved {
		s.record(now, "push proof: proved: the session answered; the daemon delivers from now")
	}
	if fell {
		s.record(now, "presence: down: "+NoSessionAnswer+" within "+SessionBound.String())
	}
	if unproven != "" {
		s.record(now, fmt.Sprintf("push proof: unproven: session check %s went into the session at %s and has no answer within %s; the daemon runs and delivers nothing until the session answers it, and asks again with the same nonce every %s", unproven, asked.UTC().Format(time.RFC3339), SessionBound, SessionQuiet))
	}
	if owed {
		s.ask(ctx, now)
	}
	s.save(now)
}

// read takes the log since the cursor: a message from the friend that the
// daemon did not send is the session's; its pong carrying the nonce is the
// answer, whose nonce it answers; any other is heard (rose: it brought the
// friend up). Called with mu held.
func (s *SessionCheck) read(ctx context.Context, now time.Time) (answered string, rose bool, err error) {
	cursor := s.cursor
	s.mu.Unlock() // store I/O never blocks heartbeat Words/Said
	if cursor == "" {
		_, storeNow, err := s.Store.Roster(ctx)
		if err != nil {
			s.mu.Lock()
			return "", false, err
		}
		cursor = bus.IDAt(storeNow)
	}
	es, err := s.Store.Range(ctx, bus.LogKey, "("+cursor, "+", LogBatch)
	s.mu.Lock()
	if err != nil {
		return "", false, err
	}
	s.cursor = cursor
	for _, e := range es {
		s.cursor = e.Entry
		m := e.Message()
		if m.From != s.Friend {
			continue
		}
		if s.sent[m.ID] {
			delete(s.sent, m.ID)
			continue // the daemon's
		}
		if m.Subject == DaemonPongSubject || strings.HasPrefix(m.Subject, SessionCheckPrefix) {
			continue // the daemon's by name, from a run before this one
		}
		if nonce, _, _, _, ok := ParsePong(strings.TrimSpace(m.Body)); ok {
			if s.m.Answer(now, nonce) {
				answered, s.toPong = nonce, nonce
			}
			continue // a pong answers by its nonce alone: a stale or wrong one proves nothing
		}
		if s.m.Heard(now) {
			rose = true
		}
	}
	return answered, rose, nil
}

// ask puts the owed check into the session: on the friend's stream for a
// passive harness, else through the adapter once no turn is under way. The
// bound runs from when it goes in. Whether a turn is under way is the turn
// lock's word, and for a headless adapter (TurnRecord) its own record's: a lock
// held past StaleTurnLock while the record says no turn runs is never a turn,
// and the check goes in as its own turn by the record. A check owed one check
// period (SessionQuiet) that has not gone in is said once as a refusal, with why.
func (s *SessionCheck) ask(ctx context.Context, now time.Time) {
	if _, ok := s.Deliver.(interface{ Passive() }); ok || s.Deliver == nil {
		s.mu.Lock()
		nonce := s.nextNonce()
		s.mu.Unlock()
		b := &bus.Bus{Store: s.DaemonStore()}
		if _, err := b.Send(ctx, bus.Message{From: s.Friend, To: []string{s.Friend}, Subject: SessionCheckPrefix + nonce, Body: s.Text(nonce) + "\n"}); err != nil {
			s.record(now, "presence: the session check was not sent: "+err.Error())
			return
		}
		s.mu.Lock()
		s.m.Ask(now, nonce)
		s.toCheck = nonce
		s.mu.Unlock()
		s.record(now, "presence: session check "+nonce+" on the stream")
		return
	}
	locked := s.turn.TryLock()
	if !locked {
		if why := s.held(now); why != "" {
			s.wait(now, why)
			return
		}
	}
	// The delivery runs under the daemon's long-lived context (Ctx), never the
	// beat's per-send one: the heartbeat cancels its 900ms call the moment the send
	// returns (internal/friend/heartbeat.go), and a delivery that inherited it would
	// be killed before it reached the session. The check's own bound still cancels it
	// (s.cancel), and the daemon's stop cancels it with Ctx.
	parent := ctx
	if s.Ctx != nil {
		parent = s.Ctx
	}
	cctx, cancel := context.WithCancel(parent)
	s.mu.Lock()
	nonce := s.nextNonce()
	s.m.Ask(now, nonce)
	s.toCheck = nonce
	owed := s.owedSince
	s.cancel, s.waiting, s.owedSince, s.owedSaid, s.staleSince = cancel, "", time.Time{}, false, time.Time{}
	s.mu.Unlock()
	line := "presence: session check " + nonce + " into the session"
	if !owed.IsZero() {
		line += ", owed since " + owed.UTC().Format(time.RFC3339)
	}
	if !locked {
		line += " as a turn of its own: the adapter's record says no turn runs, and the turn lock held " + StaleTurnLock.String() + " is no turn"
	}
	s.record(now, line)
	run := s.Go
	if run == nil {
		run = func(f func()) { go f() }
	}
	if under(s.Deliver, func(a Deliverer) bool { _, ok := a.(InPlace); return ok }) {
		// a check that is a file (FolderCheck) holds no turn of the session, so it
		// goes in here, in place: on disk before ask returns, and so before any
		// beat says the check (docs/SPEC-FRIEND.md, The push proof: the file,
		// then the beat); Go schedules only a delivery that is a turn
		run = func(f func()) { f() }
	}
	run(func() {
		if locked {
			defer s.turn.Unlock()
		}
		exit, err := s.deliverer().Deliver(cctx, s.Text(nonce))
		cancel()
		if err == nil && exit == 0 && under(s.Deliver, func(a Deliverer) bool { _, ok := a.(ReadOnReturn); return ok }) {
			s.mu.Lock()
			if s.m.Nonce == nonce {
				s.m.Read = true // the session took it: asked again on the cadence, never piled up
			}
			s.mu.Unlock()
		}
		var deferred Deferred
		switch {
		case errors.As(err, &deferred) && deferred.Remedy != "":
			s.refuse(now, "presence: REFUSED: session check "+nonce+" cannot go into the session: "+deferred.Reason+"; run: "+deferred.Remedy)
		case errors.As(err, &deferred):
			s.record(now, "presence: session check "+nonce+" deferred: "+deferred.Reason+"; the bound runs")
		case err != nil || exit != 0:
			s.record(now, fmt.Sprintf("presence: session check %s exit=%d error=%v; the bound runs", nonce, exit, err))
		}
		s.mu.Lock()
		s.cancel = nil
		s.mu.Unlock()
	})
}

// held is why the owed check waits while the turn lock is held, "" when it goes
// in all the same: no turn at the gate (from before the limit gate's wait to the
// turn's end) and a headless adapter whose own record has said no turn runs for
// StaleTurnLock.
func (s *SessionCheck) held(now time.Time) string {
	rec := headlessOf(s.Deliver)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gated > 0 {
		// a turn is between the gate and its end, the limit gate's wait included
		s.staleSince = time.Time{}
		return "it waits for the turn under way since " + s.gatedSince.UTC().Format(time.RFC3339)
	}
	if rec == nil {
		s.staleSince = time.Time{}
		return "it waits for the turn under way"
	}
	if running, since := rec.TurnUnderWay(); running {
		s.staleSince = time.Time{}
		return "it waits for the turn under way since " + since.UTC().Format(time.RFC3339) + " (the adapter's record)"
	}
	if s.staleSince.IsZero() {
		s.staleSince = now
	}
	if now.Sub(s.staleSince) < StaleTurnLock {
		return "the turn lock is held and the adapter's record says no turn runs; it goes in by the record after " + StaleTurnLock.String()
	}
	return ""
}

// wait says why the owed check has not gone in, once while the why stands, and
// once, as a refusal, when it has been owed a check period.
func (s *SessionCheck) wait(now time.Time, why string) {
	s.mu.Lock()
	if s.owedSince.IsZero() {
		s.owedSince = now
	}
	said := s.waiting == why
	s.waiting = why
	since := s.owedSince
	late := !s.owedSaid && now.Sub(since) >= s.m.Quiet
	if late {
		s.owedSaid = true
	}
	s.mu.Unlock()
	if !said {
		s.record(now, "presence: a session check is owed; "+why)
	}
	if late {
		s.record(now, fmt.Sprintf("presence: REFUSED: a session check owed since %s has not gone in for %s: %s", since.UTC().Format(time.RFC3339), now.Sub(since).Round(time.Second), why))
	}
}

// refuse records line once while it stands.
func (s *SessionCheck) refuse(now time.Time, line string) {
	s.mu.Lock()
	said := s.refused == line
	s.refused = line
	s.mu.Unlock()
	if !said {
		s.record(now, line)
	}
}

// deliverer is the adapter under the gate: the check holds the turn itself.
func (s *SessionCheck) deliverer() Deliverer {
	switch g := s.Deliver.(type) {
	case turnGated:
		return g.Deliverer
	case turnGatedLanes:
		return g.Deliverer
	}
	return s.Deliver
}

func (s *SessionCheck) save(now time.Time) {
	if s.Save == nil {
		return
	}
	s.mu.Lock()
	m := s.m
	p := PresenceStatus{Friend: s.Friend, Presence: PresenceDown, Reason: m.Reason, LastHeard: m.LastHeard, Nonce: m.Nonce, Asked: m.Asked, Checks: m.Checks, Answers: m.Answers, Answered: m.Answered}
	if m.Up {
		p.Presence = PresenceUp
	}
	if p == s.saved && now.Sub(s.savedAt) < StatusEvery {
		s.mu.Unlock()
		return
	}
	s.saved, s.savedAt = p, now
	s.mu.Unlock()
	p.At = now
	if err := s.Save(p); err != nil {
		s.record(now, "presence: the presence file: "+err.Error())
	}
}

func (s *SessionCheck) record(now time.Time, line string) {
	if s.Record != nil {
		s.Record(now.UTC().Format(time.RFC3339) + " " + line)
	}
}
