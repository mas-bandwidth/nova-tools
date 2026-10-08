package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"
)

// PushKey is the hash of every name's inbox push proof: one field per name,
// its value the PushProof as JSON (SPEC-BUS.md, bus-requires-inbox-push-proof).
// A name is on the bus only while something proven can hear it: the friend
// daemon writes the proof when its session answers a SESSION CHECK carried in
// by the harness's deliver adapter, renews it while the session stays up, and
// writes it down when a check goes unanswered; send and recv read it.
const PushKey = "bus2:push"

// PushFresh is how young a proof must be for its name to be heard: a daemon
// that stopped renewing is a push nobody has proven for this long.
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

// Deaf is why name is not heard at now, "" when its proof is proven: the
// sender's refusal, with the remedy, so a sender never talks into a void.
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

// deaf is one line for every name of names (deduplicated, in order) that is
// not heard at now, in one trip: all at once, as the bus names its problems.
func deaf(ctx context.Context, st Store, now time.Time, names ...string) ([]string, error) {
	var uniq []string
	for _, n := range names {
		if !slices.Contains(uniq, n) {
			uniq = append(uniq, n)
		}
	}
	proofs, err := (&Bus{Store: st}).proofs(ctx, uniq)
	if err != nil {
		return nil, err
	}
	var problems []string
	for _, p := range proofs {
		if d := p.Deaf(now); d != "" {
			problems = append(problems, d)
		}
	}
	return problems, nil
}

// Heard refuses every name of names that is not heard, one deaf line each:
// what a verb that writes nothing (send's and recv's --dry-run) checks
// before it answers.
func (b *Bus) Heard(ctx context.Context, names ...string) error {
	_, now, err := b.Store.Roster(ctx)
	if err != nil {
		return err
	}
	problems, err := deaf(ctx, b.Store, now, names...)
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return &Refusal{problems}
	}
	return nil
}

// Hearing is st with the push gate in front of it: what nova-bus opens, so
// send and recv refuse a name nothing proven can hear (SPEC-BUS.md,
// bus-requires-inbox-push-proof). A message (AddAll with streams) is refused
// (AddOnce too, but a retry whose token has a record answers it, writing
// nothing) unless its sender and every recipient are heard at its at, the store's
// time Send stamped it with, after Send has named the message's own
// problems; a recv (EnsureGroup, the recipient's group) is refused unless
// the recipient is heard at the store's time of the roster it was checked
// against. Each costs one HGETALL. Every other command passes through, so a
// deaf name can still peek, ack and read the log. The friend daemon's own
// sends (its SESSION CHECK, its daemon-pong) go through the bare store: the
// proof is theirs to make.
func Hearing(st Store) Store { return &hearing{Store: st} }

type hearing struct {
	Store
	mu  sync.Mutex
	now time.Time // the store's time at the last roster trip
}

func (h *hearing) seen(now time.Time) {
	h.mu.Lock()
	h.now = now
	h.mu.Unlock()
}

func (h *hearing) Roster(ctx context.Context) ([]string, time.Time, error) {
	names, now, err := h.Store.Roster(ctx)
	if err == nil {
		h.seen(now)
	}
	return names, now, err
}

func (h *hearing) Members(ctx context.Context) ([]string, []string, time.Time, error) {
	friends, machines, now, err := h.Store.Members(ctx)
	if err == nil {
		h.seen(now)
	}
	return friends, machines, now, err
}

func (h *hearing) AddAll(ctx context.Context, streams []string, fields map[string]string, marks ...Mark) error {
	if err := h.gate(ctx, streams, fields); err != nil {
		return err
	}
	return h.Store.AddAll(ctx, streams, fields, marks...)
}

// AddOnce is gated as AddAll, but for a retry: a token whose record is there
// writes nothing, so its answer is the original whoever is deaf now (one GET
// more, only when the gate refuses).
func (h *hearing) AddOnce(ctx context.Context, key, record string, keep time.Duration, streams []string, fields map[string]string, marks ...Mark) (string, bool, error) {
	if err := h.gate(ctx, streams, fields); err != nil {
		if prior, found, gerr := h.Store.Sent(ctx, key); gerr == nil && found {
			return prior, true, nil
		}
		return "", false, err
	}
	return h.Store.AddOnce(ctx, key, record, keep, streams, fields, marks...)
}

// gate refuses a message (a write with streams) whose sender or a recipient
// is not heard at its at.
func (h *hearing) gate(ctx context.Context, streams []string, fields map[string]string) error {
	if len(streams) == 0 {
		return nil
	}
	at, err := time.Parse(time.RFC3339, fields["at"])
	if err != nil {
		h.mu.Lock()
		at = h.now // a message with no at is judged at the last time the store gave
		h.mu.Unlock()
	}
	problems, err := deaf(ctx, h.Store, at, slices.Concat([]string{fields["from"]}, list(fields["to"]), list(fields["cc"]))...)
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return &Refusal{problems}
	}
	return nil
}

func (h *hearing) EnsureGroup(ctx context.Context, stream, group string) error {
	h.mu.Lock()
	now := h.now
	h.mu.Unlock()
	if now.IsZero() {
		_, t, err := h.Roster(ctx)
		if err != nil {
			return err
		}
		now = t
	}
	problems, err := deaf(ctx, h.Store, now, group)
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return &Refusal{problems}
	}
	return h.Store.EnsureGroup(ctx, stream, group)
}
