package friend

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// SessionReporter is the adapter's delivery target, recorded independently of a pong.
// SessionSwitcher changes that target while the SessionCheck holds the turn lock.
type SessionReporter interface{ DeliverySession() string }
type SessionSwitcher interface{ SwitchDeliverySession(string) error }
type SessionReader interface{ ReadNonceSession(string) string }

// SessionBinding is the small proof machine (tla/DeliverySession.tla): a target is never
// proven by a receipt from another conversation; a switch starts a new round trip.
type SessionBinding struct {
	ID       string `json:"session_id"`
	Target   string `json:"session_target"`
	Observed string `json:"session_observed"`
	Proof    string `json:"session_proof"`
}

func (s *SessionCheck) reporter() (reporter SessionReporter, switcher SessionSwitcher) {
	under(s.Deliver, func(a Deliverer) bool {
		if r, ok := a.(SessionReporter); ok {
			reporter = r
			switcher, _ = a.(SessionSwitcher)
			return true
		}
		return false
	})
	return
}

// SessionInfo is the daemon/check state of the bound session (SPEC-FRIEND, Session binding).
func (s *SessionCheck) SessionInfo() SessionBinding {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.syncSession()
	return s.binding
}

// syncSession starts unproven when the adapter reports a target, never from generic bus traffic.
func (s *SessionCheck) syncSession() bool {
	reporter, _ := s.reporter()
	if reporter == nil {
		if s.RequireSession && s.binding.Proof == "" {
			s.binding.Proof = "unproven"
		}
		return s.RequireSession
	}
	id := reporter.DeliverySession()
	if s.binding.Proof == "" {
		s.binding = SessionBinding{ID: id, Target: id, Proof: "unproven"}
	}
	if s.binding.Target == "" && id != "" {
		s.binding.ID, s.binding.Target = id, id
	}
	if s.binding.Target != "" {
		_, setter := s.reporter()
		if setter != nil {
			if err := setter.SwitchDeliverySession(s.binding.Target); err != nil {
				s.record(s.Now(), "cannot pin session: "+err.Error())
			}
		}
	}
	if s.binding.Proof == "proven" && id != s.binding.Target {
		s.binding.Proof, s.binding.Observed = "mismatch", id
		if s.m != nil {
			s.m.Up, s.m.Proven = false, false
			s.m.Reason = fmt.Sprintf("session mismatch: target=%s observed=%s", s.binding.Target, dash(id))
		}
	}
	return true
}

// answerSession judges the conversation as well as the nonce; mismatch keeps the same
// challenge open so the real target can still answer it (Delivery.Answer).
func (s *SessionCheck) answerSession(nonce, observed string) bool {
	if !s.syncSession() {
		return true
	}
	if s.pendingSession != "" {
		return false
	}
	if nonce == "" || nonce != s.m.Nonce {
		return false
	}
	if observed != "" && observed == s.binding.Target {
		reporter, _ := s.reporter()
		if reader, ok := reporter.(SessionReader); ok {
			read := reader.ReadNonceSession(nonce)
			if read == "" {
				s.awaitNonce, s.awaitSession = nonce, observed
				s.m.Up, s.m.Proven = false, false
				s.m.Reason = "session unproven: awaiting the adapter receipt for " + observed
				return false
			}
			if read != observed {
				observed = read
			}
		}
	}
	s.binding.Observed = observed
	if observed == "" || observed != s.binding.Target || s.binding.Target == "" {
		s.binding.Proof = "mismatch"
		s.m.Up, s.m.Proven = false, false
		s.m.Reason = fmt.Sprintf("session mismatch: target=%s observed=%s", dash(s.binding.Target), dash(observed))
		return false
	}
	if s.PersistSession != nil {
		if err := s.PersistSession(observed); err != nil {
			s.binding.Proof = "unproven"
			s.m.Up, s.m.Proven = false, false
			s.m.Reason = "session unproven: cannot persist " + observed + ": " + err.Error()
			return false
		}
	}
	switched := s.binding.Proof != "proven"
	s.binding.ID, s.binding.Proof = observed, "proven"
	s.awaitNonce, s.awaitSession = "", ""
	if switched {
		s.provenNotice = observed
	}
	return true
}

// PongSession is the answering conversation named separately from the nonce.
func PongSession(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if id, ok := strings.CutPrefix(line, "session_id="); ok {
			return strings.TrimSpace(id)
		}
	}
	return ""
}

// LiveSessionSubject is the one request joinQuestion puts on the friend's own
// stream. It is not a turn: while the target is unproven the hand delivers
// nothing and the turn gate would defer it, so SessionRequest puts the text
// in through the ungated deliverer (the session check's path).
const LiveSessionSubject = "live session id wanted"

const activeQuestionPrefix = "\x00active-session-question:"

// SessionRequest handles a request outside the daemon loop. The daemon uses
// HandleSessionRequest so an asynchronous question is acknowledged only after
// the adapter succeeds.
func (s *SessionCheck) SessionRequest(ctx context.Context, msg bus.Message) bool {
	return s.HandleSessionRequest(ctx, msg, func() error { return nil })
}

// HandleSessionRequest consumes only self-authored requests on the daemon's own
// stream. The live-session question is injected asynchronously and acked by its
// caller only after the adapter succeeds, so a failure remains claimable.
func (s *SessionCheck) HandleSessionRequest(ctx context.Context, msg bus.Message, ack func() error) bool {
	s.mu.Lock()
	if !s.sent[msg.ID] {
		s.joinQuestion(ctx, msg)
	}
	if s.sessionRequestLocked(ctx, msg) {
		s.mu.Unlock()
		if err := ack(); err != nil {
			s.record(s.Now(), "session request acknowledgement failed: "+err.Error())
		}
		return true
	}
	body, ok := s.ownSessionQuestion(msg)
	s.mu.Unlock()
	if !ok {
		return false
	}
	return s.pushQuestion(ctx, msg.ID, body, ack)
}

// ownSessionQuestion is the daemon's own live-session line, and only when an
// ungated deliverer can take it. Called with mu held.
func (s *SessionCheck) ownSessionQuestion(msg bus.Message) (string, bool) {
	if !s.sent[msg.ID] || msg.Kind != "request" || msg.Subject != LiveSessionSubject || msg.From != s.Friend || !slices.Contains(msg.To, s.Friend) {
		return "", false
	}
	if s.deliverer() == nil {
		return "", false
	}
	return msg.Body, true
}

// pushQuestion puts the question into the session without waiting for proof.
// A test's Go runs it before the answer; production starts it asynchronously.
// Both acknowledge only after successful delivery, so failure leaves the entry
// for the next claim.
func (s *SessionCheck) pushQuestion(ctx context.Context, id, body string, ack func() error) bool {
	d := s.deliverer()
	if d == nil {
		return false
	}
	active := activeQuestionPrefix + id
	s.mu.Lock()
	if s.sent[active] {
		s.mu.Unlock()
		return true
	}
	if s.sent == nil {
		s.sent = map[string]bool{}
	}
	s.sent[active] = true
	s.mu.Unlock()
	deliver := func() {
		defer func() {
			s.mu.Lock()
			delete(s.sent, active)
			s.mu.Unlock()
		}()
		exit, err := d.Deliver(ctx, body)
		now := time.Time{}
		if s.Now != nil {
			now = s.Now()
		}
		if err != nil || exit != 0 {
			s.record(now, fmt.Sprintf("session id request into the session exit=%d error=%v", exit, err))
			return
		}
		if err := ack(); err != nil {
			s.record(now, "session id request acknowledgement failed: "+err.Error())
		}
	}
	if s.Go != nil {
		s.Go(deliver)
	} else {
		go deliver()
	}
	return true
}

func (s *SessionCheck) sessionRequestLocked(ctx context.Context, msg bus.Message) bool {
	if msg.Kind != "request" || msg.From != s.Friend || !slices.Contains(msg.To, s.Friend) {
		return false
	}
	id, ok := strings.CutPrefix(msg.Subject, "session ")
	if !ok {
		return false
	}
	if !s.syncSession() {
		return true
	}
	if id == s.binding.Target {
		return true
	}
	if id == "" || len(id) > 256 || strings.ContainsAny(id, " \t\r\n") {
		s.record(s.Now(), "session request refused: session wants one nonempty id of at most 256 bytes")
		return true
	}
	_, switcher := s.reporter()
	if switcher == nil {
		s.record(s.Now(), "session request refused: the adapter cannot select a target; run nova-friend install with a supported harness")
		return true
	}
	if !s.turn.TryLock() {
		s.pendingSession = id
		s.binding.Proof = "unproven"
		if s.m != nil {
			s.m.Up, s.m.Proven = false, false
			s.m.Reason = "session unproven: switch waiting for active turn"
		}
		return true
	}
	defer s.turn.Unlock()
	if err := switcher.SwitchDeliverySession(id); err != nil {
		s.record(s.Now(), "session request refused: "+err.Error())
		return true
	}
	s.binding.Target, s.binding.Observed, s.binding.Proof = id, "", "unproven"
	s.m = StartPresence()
	s.keep, s.Keep, s.toPong = "", "", ""
	s.joinAsked, s.pendingSession = false, ""
	s.awaitNonce, s.awaitSession = "", ""
	return true
}

// joinQuestion is one mechanical request per unproven target, never a delivery
// into an assumed conversation (SPEC-FRIEND, Session binding).
func (s *SessionCheck) joinQuestion(ctx context.Context, msg bus.Message) {
	if !s.syncSession() || s.binding.Proof == "proven" || s.joinAsked {
		return
	}
	joined := (msg.From == s.Friend && !slices.Contains(msg.To, s.Friend)) || (msg.Subject == "join" && slices.Contains(msg.To, s.Friend) && s.JoinFrom != nil && msg.From == s.JoinFrom())
	if !joined {
		return
	}
	line := s.SessionQuestion
	if line == "" {
		line = "nova-bus send --as " + oneline.ShellWord(s.Friend) + " --to " + oneline.ShellWord(s.Friend) + " --kind request --subject 'session <live-session-id>' --body 'prove this session'"
	}
	b := &bus.Bus{Store: s.Store}
	sent, err := b.Send(ctx, bus.Message{From: s.Friend, To: []string{s.Friend}, Kind: "request", Subject: LiveSessionSubject, Body: line})
	if err != nil {
		s.record(s.Now(), "session id request failed: "+err.Error())
		return
	}
	if s.sent == nil {
		s.sent = map[string]bool{}
	}
	s.sent[sent.ID] = true
	s.joinAsked = true
}

// sessionNotice is the reply only after the new conversation answers and is persisted.
func (s *SessionCheck) sessionNotice(ctx context.Context) {
	s.mu.Lock()
	id := s.provenNotice
	s.mu.Unlock()
	if id == "" {
		return
	}
	_, err := (&bus.Bus{Store: s.DaemonStore()}).Send(ctx, bus.Message{From: s.Friend, To: []string{s.Friend}, Kind: "reply", Subject: "session " + id + " proven", Body: "session " + id + " proven"})
	if err != nil {
		s.record(s.Now(), "session proof reply failed: "+err.Error())
		return
	}
	s.mu.Lock()
	if s.provenNotice == id {
		s.provenNotice = ""
	}
	s.mu.Unlock()
}
