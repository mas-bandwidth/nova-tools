package friend

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// SessionReporter is the adapter's delivery target, recorded independently of a pong.
// SessionSwitcher changes that target while the SessionCheck holds the turn lock.
type SessionReporter interface{ DeliverySession() string }
type SessionSwitcher interface{ SwitchDeliverySession(string) error }
type SessionReader interface{ ReadNonceSession(string) string }

// SessionBinding is the small proof machine (tla/Delivery.tla): a target is never
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

// SessionRequest consumes only self-authored requests on the daemon's own stream;
// an unsupported adapter or a running turn is refused without changing its target.
func (s *SessionCheck) SessionRequest(ctx context.Context, msg bus.Message) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.sent[msg.ID] {
		s.joinQuestion(ctx, msg)
	}
	return s.sessionRequestLocked(ctx, msg)
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
	sent, err := b.Send(ctx, bus.Message{From: s.Friend, To: []string{s.Friend}, Kind: "request", Subject: "live session id wanted", Body: line})
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
