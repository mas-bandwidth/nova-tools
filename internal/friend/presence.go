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
}

// StartPresence is the presence as the daemon comes up at now: down, not yet
// answered, a check owed at once.
func StartPresence() *Presence {
	return &Presence{Quiet: SessionQuiet, Bound: SessionBound, Reason: NotYetAnswered, Owed: true}
}

// Heard is a bus message the session wrote, at now: the session is alive, so
// the friend is up and the quiet clock starts again (rose: it was down). A
// check still open is settled by it: the session spoke. The daemon's own
// messages never reach here (SessionCheck.read).
func (p *Presence) Heard(now time.Time) (rose bool) {
	rose = !p.Up
	p.Up, p.Reason, p.LastHeard, p.Nonce, p.Open, p.Owed = true, "", now, "", false, false
	return rose
}

// Ask is the check with nonce going into the session at now: the bound runs
// from here.
func (p *Presence) Ask(now time.Time, nonce string) {
	p.Nonce, p.Asked, p.Open, p.Owed, p.Checks = nonce, now, true, false, p.Checks+1
}

// Answer is the session's reply carrying nonce, at now: the latest check's
// nonce makes the friend up, late or not; any other, or one already
// answered, changes nothing (current false).
func (p *Presence) Answer(now time.Time, nonce string) (current bool) {
	if nonce == "" || nonce != p.Nonce {
		return false
	}
	p.Up, p.Reason, p.LastHeard, p.Nonce, p.Open, p.Owed, p.Answers = true, "", now, "", false, false, p.Answers+1
	return true
}

// Tick is the clock at now: an open check past the bound makes the friend
// down, NoSessionAnswer; a check is owed after Quiet from the session's last
// word while up, and after Quiet from the last check while down.
func (p *Presence) Tick(now time.Time) {
	if p.Open && now.Sub(p.Asked) >= p.Bound {
		p.Open, p.Up, p.Reason = false, false, NoSessionAnswer
	}
	if p.Open || p.Owed {
		return
	}
	from := p.LastHeard
	if !p.Up {
		from = p.Asked
	}
	if now.Sub(from) >= p.Quiet {
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

	mu      sync.Mutex
	m       *Presence
	cursor  string          // the last log entry read
	sent    map[string]bool // message ids the daemon sent, until the log shows them
	turn    sync.RWMutex    // the session's turns hold it shared; a check holds it alone
	cancel  context.CancelFunc
	saved   PresenceStatus
	savedAt time.Time
	waiting bool // a check is owed and a turn is under way, said once
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
	g.s.turn.RLock()
	defer g.s.turn.RUnlock()
	return g.Deliverer.Deliver(ctx, text)
}

type turnGatedLanes struct {
	turnGated
	lh LaneHarness
}

func (g turnGatedLanes) OpenSession(ctx context.Context, seed string) (string, error) {
	g.s.turn.RLock()
	defer g.s.turn.RUnlock()
	return g.lh.OpenSession(ctx, seed)
}

func (g turnGatedLanes) DeliverTo(ctx context.Context, session, text string) (LaneTurn, error) {
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
// answer to a check, zero while it has given none. It goes on every beat (friend
// beat --pong), and its age is what the sprint reads a deaf session by.
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

// Step is one look: the session's messages since the last, the clock, and
// the check when it is owed.
func (s *SessionCheck) Step(ctx context.Context) {
	now := s.Now()
	s.mu.Lock()
	if s.m == nil {
		s.m = StartPresence()
	}
	answered, rose, err := s.read(ctx, now)
	was := s.m.Reason
	s.m.Tick(now)
	fell := was != NoSessionAnswer && s.m.Reason == NoSessionAnswer
	if !s.m.Up && !s.m.Open && s.cancel != nil {
		s.cancel() // never answered: the check's turn ends with its bound
	}
	owed := s.m.Owed
	s.mu.Unlock()
	if err != nil {
		s.record(now, "presence: the bus log: "+err.Error())
	}
	if answered != "" {
		s.record(now, "presence: up: the session answered "+answered)
	} else if rose {
		s.record(now, "presence: up: the session wrote on the bus")
	}
	if fell {
		s.record(now, "presence: down: "+NoSessionAnswer+" within "+SessionBound.String())
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
	if s.cursor == "" {
		_, storeNow, err := s.Store.Roster(ctx)
		if err != nil {
			return "", false, err
		}
		s.cursor = bus.IDAt(storeNow)
	}
	es, err := s.Store.Range(ctx, bus.LogKey, "("+s.cursor, "+", LogBatch)
	if err != nil {
		return "", false, err
	}
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
				answered = nonce
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
// bound runs from when it goes in.
func (s *SessionCheck) ask(ctx context.Context, now time.Time) {
	if _, ok := s.Deliver.(interface{ Passive() }); ok || s.Deliver == nil {
		nonce := s.Nonce()
		b := &bus.Bus{Store: s.DaemonStore()}
		if _, err := b.Send(ctx, bus.Message{From: s.Friend, To: []string{s.Friend}, Subject: SessionCheckPrefix + nonce, Body: s.Text(nonce) + "\n"}); err != nil {
			s.record(now, "presence: the session check was not sent: "+err.Error())
			return
		}
		s.mu.Lock()
		s.m.Ask(now, nonce)
		s.mu.Unlock()
		s.record(now, "presence: session check "+nonce+" on the stream")
		return
	}
	if !s.turn.TryLock() {
		s.mu.Lock()
		said := s.waiting
		s.waiting = true
		s.mu.Unlock()
		if !said {
			s.record(now, "presence: a session check is owed; it waits for the turn under way")
		}
		return
	}
	nonce := s.Nonce()
	cctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.m.Ask(now, nonce)
	s.cancel, s.waiting = cancel, false
	s.mu.Unlock()
	s.record(now, "presence: session check "+nonce+" into the session")
	run := s.Go
	if run == nil {
		run = func(f func()) { go f() }
	}
	run(func() {
		defer s.turn.Unlock()
		exit, err := s.deliverer().Deliver(cctx, s.Text(nonce))
		cancel()
		var deferred Deferred
		switch {
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
	p := PresenceStatus{Friend: s.Friend, Presence: PresenceDown, Reason: m.Reason, LastHeard: m.LastHeard, Nonce: m.Nonce, Asked: m.Asked, Checks: m.Checks, Answers: m.Answers}
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
