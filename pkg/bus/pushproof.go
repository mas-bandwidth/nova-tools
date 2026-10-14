package bus

import (
	"context"
	"encoding/json"
	"time"
)

// PushKey is the hash of every name's inbox push proof: one field per name,
// its value the PushProof as JSON (SPEC-BUS.md, bus-requires-inbox-push-proof).
// The proof is one of the two things that make a name heard (heard.go): the
// friend daemon writes it when its session answers a SESSION CHECK carried in
// by the harness's deliver adapter, renews it while the session stays up, and
// writes it down when a check goes unanswered; the other is the name's own
// recent recv or ack. names shows both, and send says an unheard recipient as
// a NOTE beside a message that landed, never as a refusal.
const PushKey = "bus2:push"

// PushFresh is how young a proof, or a name's last recv or ack, must be for
// its name to read as heard: a daemon that stopped renewing, or a receiver that
// stopped reading, is a push nobody has seen for this long.
const PushFresh = 10 * time.Minute

// PushProof is one name's proof that its inbox pushes into its session: the
// harness whose deliver adapter carried the check, the nonce the session
// answered and when (Proven, the daemon's clock), whether the daemon still
// holds the session up (Up; Reason when it does not), and when the daemon
// last wrote it (At, the store's clock, the one freshness is read by).
type PushProof struct {
	Name    string    `json:"-"`
	Harness string    `json:"harness"`
	Nonce   string    `json:"nonce"`
	Proven  time.Time `json:"proven"`
	Up      bool      `json:"up"`
	Reason  string    `json:"reason,omitempty"`
	At      time.Time `json:"at"`
}

// The push states names shows, read off a proof at the store's now.
const (
	PushProven = "proven" // up and younger than PushFresh: the name is heard
	PushStale  = "stale"  // up when written, and older than PushFresh: its daemon stopped renewing
	PushDown   = "down"   // its daemon says the session did not answer its check
	PushNone   = "none"   // no daemon ever recorded one
)

// State is the proof's push state at now; a zero proof is PushNone.
func (p PushProof) State(now time.Time) string {
	switch {
	case p.At.IsZero():
		return PushNone
	case !p.Up:
		return PushDown
	case now.Sub(p.At) >= PushFresh:
		return PushStale
	}
	return PushProven
}

// Age is how long ago the proof was written, at now; zero when there is none.
func (p PushProof) Age(now time.Time) time.Duration {
	if p.At.IsZero() {
		return 0
	}
	return max(now.Sub(p.At), 0).Truncate(time.Second)
}

// AgeWord is Age as names and the refusal say it: "never" when there is none.
func (p PushProof) AgeWord(now time.Time) string {
	if p.At.IsZero() {
		return "never"
	}
	return p.Age(now).String()
}

// ProvePush records proof for name at the store's time, in one transaction
// (HSET on PushKey through AddAll, with no stream): what the friend daemon
// calls when its session answered a check carried in by the deliver adapter,
// while it stays up, and with Up false when a check went unanswered.
func (b *Bus) ProvePush(ctx context.Context, p PushProof) (PushProof, error) {
	if e := CheckName(p.Name); e != "" {
		return PushProof{}, &Refusal{[]string{e}}
	}
	_, now, err := b.Store.Roster(ctx)
	if err != nil {
		return PushProof{}, err
	}
	p.At = now.UTC()
	raw, err := json.Marshal(p)
	if err != nil {
		return PushProof{}, err
	}
	return p, b.Store.AddAll(ctx, nil, nil, Mark{Key: PushKey, Field: p.Name, Value: string(raw)})
}

// PushProofs is each name's proof, in the order asked, and the store's now
// they are read at (a roster trip, then one HGETALL).
func (b *Bus) PushProofs(ctx context.Context, names ...string) ([]PushProof, time.Time, error) {
	_, now, err := b.Store.Roster(ctx)
	if err != nil {
		return nil, time.Time{}, err
	}
	proofs, err := b.proofs(ctx, names)
	return proofs, now, err
}

// proofs is each name's proof, in the order asked, in one trip (HGETALL on
// PushKey).
func (b *Bus) proofs(ctx context.Context, names []string) ([]PushProof, error) {
	hashes, err := b.Store.Marks(ctx, PushKey)
	if err != nil {
		return nil, err
	}
	return proofsFrom(hashes[0], names), nil
}

// proofsFrom is each name's proof in the PushKey hash h, in the order asked. A
// name with none, or whose value is no proof, has the zero proof: PushNone.
func proofsFrom(h map[string]string, names []string) []PushProof {
	out := make([]PushProof, len(names))
	for i, n := range names {
		var p PushProof
		if v, ok := h[n]; ok && json.Unmarshal([]byte(v), &p) != nil {
			p = PushProof{} // ignored: a value that is no proof proves nothing
		}
		p.Name = n
		out[i] = p
	}
	return out
}
