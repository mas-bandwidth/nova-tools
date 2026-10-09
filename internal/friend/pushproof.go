package friend

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// PushRenewEvery is how often the daemon renews its friend's push proof on
// the bus while the session stays up: well inside bus.PushFresh, so a live
// daemon's proof never reads stale, and a dead one's does within PushFresh.
// PushWriteBudget bounds one write of it.
const (
	PushRenewEvery  = time.Minute
	PushWriteBudget = 5 * time.Second
)

// DeliveryVerifier is an adapter that can verify delivery capability without
// running a session turn (for example, listing runner CLI sessions).
type DeliveryVerifier interface {
	VerifyDelivery(ctx context.Context) error
}

func verifierOf(d Deliverer) DeliveryVerifier {
	var v DeliveryVerifier
	under(d, func(a Deliverer) bool {
		v, _ = a.(DeliveryVerifier)
		return v != nil
	})
	return v
}

// PushProver records the friend's inbox push proof on the bus (bus.PushKey;
// docs/SPEC-BUS.md, bus-requires-inbox-push-proof) from the daemon's
// presence, as the SessionCheck saves it: nova-bus send and recv refuse a
// name without one. The proof is the SESSION CHECK round trip: the check
// carried into the session by the deliver adapter and the session's pong
// carrying its nonce (SessionCheck, presence.go). While the presence is up
// the proof is up and renewed every PushRenewEvery; when the presence is
// down (a check unanswered within its bound, or a daemon that has not yet
// been answered) the proof is written down at once, so a sender is refused
// the moment the daemon knows. A passive harness (no deliver command: the
// check only goes on the stream) pushes nothing into the session and is
// written down whatever its pongs say.
type PushProver struct {
	Friend, Harness string
	Store           bus.Store // the store itself, never the daemon's (DaemonStore)
	Deliver         Deliverer // the adapter the check goes in by
	Now             func() time.Time
	Record          func(line string) // nil records nothing
	NeedsEnv        []string
	Getenv          func(string) string
	Verify          func(ctx context.Context) error // optional verify hook; nil uses DeliveryVerifier on Deliver
	Delivered       func() bool                     // optional: true when session actually took a delivery since last write

	mu      sync.Mutex
	asked   string    // the nonce the latest check carried, until it is answered
	nonce   string    // the nonce the session last answered
	proven  time.Time // when the daemon saw that answer
	answers int
	wrote   bool // a proof has been written by this daemon
	up      bool
	reason  string
	wroteAt time.Time
}

func (p *PushProver) verifyDelivery(ctx context.Context) error {
	if p.Verify != nil {
		return p.Verify(ctx)
	}
	if v := verifierOf(p.Deliver); v != nil {
		return v.VerifyDelivery(ctx)
	}
	return nil
}

// passiveReason is the down proof of a harness with no deliver command.
const passiveReason = "the harness has no deliver adapter, so nothing pushes into the session"

// Save is inner with the proof written first: what the SessionCheck's Save
// is set to, so each presence the daemon saves is also the bus's proof. A
// failed write is recorded and tried again at the next save; it never
// stops the presence file.
func (p *PushProver) Save(inner func(PresenceStatus) error) func(PresenceStatus) error {
	return func(s PresenceStatus) error {
		p.Step(s)
		if inner == nil {
			return nil
		}
		return inner(s)
	}
}

// Step writes the proof the presence s says, when it differs from the last
// one written or the last up one is PushRenewEvery old.
func (p *PushProver) Step(s PresenceStatus) {
	now := p.Now()
	up, reason := s.Presence == PresenceUp, s.Reason
	if _, passive := p.Deliver.(interface{ Passive() }); passive || p.Deliver == nil {
		up, reason = false, passiveReason
	}
	if !up && reason == "" {
		reason = NoSessionAnswer
	}
	p.mu.Lock()
	if s.Nonce != "" {
		p.asked = s.Nonce
	}
	tookDelivery := false
	if s.Answers > p.answers {
		tookDelivery = true
		p.nonce, p.proven = p.asked, now
		if s.Answered != "" {
			p.nonce = s.Answered // the check answered, never one asked after it in the same step
		}
	}
	p.answers = s.Answers
	if p.proven.IsZero() {
		up = false
		if reason == "" {
			reason = NotYetAnswered
		}
	}
	lastNonce := p.nonce
	provenTime := p.proven
	due := !p.wrote || up != p.up || reason != p.reason || tookDelivery || (up && now.Sub(p.wroteAt) >= PushRenewEvery)
	p.mu.Unlock()

	missing := MissingEnv(p.NeedsEnv, p.Getenv)
	if len(missing) > 0 {
		up = false
		reason = NeedsEnvReason(missing)
	}

	if due && up && !tookDelivery && (p.Delivered == nil || !p.Delivered()) {
		ctx, cancel := context.WithTimeout(context.Background(), PushWriteBudget)
		err := p.verifyDelivery(ctx)
		cancel()
		if err != nil {
			up = false
			if len(missing) > 0 {
				reason = NeedsEnvReason(missing)
			} else if strings.Contains(err.Error(), "125") {
				reason = "no key sealed: opencode exit 125"
			} else {
				reason = err.Error()
			}
		}
	}

	proof := bus.PushProof{Name: p.Friend, Harness: p.Harness, Nonce: lastNonce, Proven: provenTime, Up: up}
	if !up {
		proof.Reason = reason
	}
	p.mu.Lock()
	due = !p.wrote || up != p.up || reason != p.reason || tookDelivery || (up && now.Sub(p.wroteAt) >= PushRenewEvery)
	p.mu.Unlock()
	if !due {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), PushWriteBudget)
	defer cancel()
	if _, err := (&bus.Bus{Store: p.Store}).ProvePush(ctx, proof); err != nil {
		p.record(now, "push proof: not written: "+err.Error())
		return
	}
	p.mu.Lock()
	was := p.wrote && p.up == up
	p.wrote, p.up, p.reason, p.wroteAt = true, up, reason, now
	p.mu.Unlock()
	switch {
	case was:
	case up:
		p.record(now, "push proof: up: the session answered "+proof.Nonce+" through "+p.Harness+"'s deliver adapter; nova-bus hears "+p.Friend)
	default:
		p.record(now, "push proof: down: "+reason+"; nova-bus refuses "+p.Friend+" as deaf")
	}
}

func (p *PushProver) record(now time.Time, line string) {
	if p.Record != nil {
		p.Record(now.UTC().Format(time.RFC3339) + " " + line)
	}
}
