package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

// PushKey is the hash of every name's inbox push proof: one field per name,
// its value the PushProof as JSON (SPEC-BUS.md, bus-requires-inbox-push-proof).
// The proof is the seat's liveness rule, and advice to a sender: the friend
// daemon writes it when its session answers a SESSION CHECK carried in by
// the harness's deliver adapter, renews it while the session stays up, and
// writes it down when a check goes unanswered; names shows it, and send and
// recv say it as a NOTE beside a message that landed or was read, never as a
// refusal.
const PushKey = "bus2:push"

// PushFresh is how young a proof must be for its name to read as heard: a
// daemon that stopped renewing is a push nobody has proven for this long.
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

// Unheard is the advisory line for name at now, "" when its proof is proven:
// the NOTE send and recv print beside their result (SPEC-BUS.md,
// bus-requires-inbox-push-proof). It never refuses: a message to an unheard
// name lands and waits on its stream; the line says what the proof's state
// is and what would prove one, so a sender knows nothing is pushing it in.
func (p PushProof) Unheard(now time.Time) string {
	var why string
	switch p.State(now) {
	case PushProven:
		return ""
	case PushNone:
		why = "no daemon has recorded one"
	case PushDown:
		why = fmt.Sprintf("its daemon holds no answered SESSION CHECK from the session (%s)", p.Reason)
	case PushStale:
		why = fmt.Sprintf("its daemon (%s) last renewed it %s ago, past %s", p.Harness, p.Age(now), PushFresh)
	}
	return fmt.Sprintf("push=%s for %s: no proven push since %s: %s; a message to %s waits on its stream until something reads it (nova-bus recv --as %s); the proof: %s runs its friend daemon (nova-friend install --as %s --harness <h> --dir <d>) and its session answers the daemon's SESSION CHECK; nova-bus names shows every name's push", p.State(now), p.Name, p.AgeWord(now), why, p.Name, p.Name, p.Name, p.Name)
}

// Deaf is why name is not heard at now, "" when its proof is proven: the
// refusal of nova-bus's --require-push, with the remedy.
func (p PushProof) Deaf(now time.Time) string {
	var why string
	switch p.State(now) {
	case PushProven:
		return ""
	case PushNone:
		why = "no daemon has recorded one"
	case PushDown:
		why = fmt.Sprintf("its daemon holds no answered SESSION CHECK from the session (%s)", p.Reason)
	case PushStale:
		why = fmt.Sprintf("its daemon (%s) last renewed it %s ago, past %s", p.Harness, p.Age(now), PushFresh)
	}
	return fmt.Sprintf("deaf: %s has no proven push since %s: %s; the remedy: %s runs its friend daemon with a deliver adapter for its harness (nova-friend install --as %s --harness <h> --dir <d>) and its session answers the daemon's SESSION CHECK, which records the proof; nova-bus names shows every name's push", p.Name, p.AgeWord(now), why, p.Name, p.Name)
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
// PushKey). A name with none, or whose value is no proof, has the zero
// proof: PushNone.
func (b *Bus) proofs(ctx context.Context, names []string) ([]PushProof, error) {
	hashes, err := b.Store.Marks(ctx, PushKey)
	if err != nil {
		return nil, err
	}
	out := make([]PushProof, len(names))
	for i, n := range names {
		var p PushProof
		if v, ok := hashes[0][n]; ok && json.Unmarshal([]byte(v), &p) != nil {
			p = PushProof{} // ignored: a value that is no proof proves nothing
		}
		p.Name = n
		out[i] = p
	}
	return out, nil
}

// Unheard is one advisory line for every name of names (deduplicated, in
// order) that is not heard at the store's now, in one trip after the roster's
// (one HGETALL): what send prints as SEND NOTE after its message landed and
// recv as RECV NOTE beside what it read. It is advice, never a gate: the
// push proof is the seat's liveness rule (names, the coordinator's view),
// and a message is never refused on it (the finding of 2026-10-08, issue
// #5450: a claude friend and a machine with no daemon could never be
// written to or read as).
func (b *Bus) Unheard(ctx context.Context, names ...string) ([]string, error) {
	_, now, err := b.Store.Roster(ctx)
	if err != nil {
		return nil, err
	}
	return b.UnheardAt(ctx, now, names...)
}

// UnheardAt is Unheard judged at at, with no roster trip: send's, judged at
// the store's time its message was stamped with (one HGETALL after the write).
func (b *Bus) UnheardAt(ctx context.Context, at time.Time, names ...string) ([]string, error) {
	return b.unheardAt(ctx, at, PushProof.Unheard, names...)
}

// Heard refuses every name of names (deduplicated, in order) that is not
// heard at the store's now, one deaf line each: the gate nova-bus's
// --require-push puts in front of a send or a recv, before anything is
// written or read. It is the exception, asked for by flag; the bus itself
// never refuses on a proof.
func (b *Bus) Heard(ctx context.Context, names ...string) error {
	_, now, err := b.Store.Roster(ctx)
	if err != nil {
		return err
	}
	problems, err := b.unheardAt(ctx, now, PushProof.Deaf, names...)
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return &Refusal{problems}
	}
	return nil
}

// unheardAt is line(proof, at) for every name of names (deduplicated, in
// order) whose proof is not proven at at, in one trip (one HGETALL).
func (b *Bus) unheardAt(ctx context.Context, at time.Time, line func(PushProof, time.Time) string, names ...string) ([]string, error) {
	var uniq []string
	for _, n := range names {
		if !slices.Contains(uniq, n) {
			uniq = append(uniq, n)
		}
	}
	proofs, err := b.proofs(ctx, uniq)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, p := range proofs {
		if l := line(p, at); l != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}
