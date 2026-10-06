package bus

import (
	"context"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ReceiptsPrefix is the hash of how far each message to a recipient has got:
// one field per message id, its value the state and its time (SPEC-BUS.md,
// delivery receipts). It is written by this package for both stores, never by hand.
const ReceiptsPrefix = "bus2:receipts:"

// ReceiptsOf is the recipient's hash of receipts.
func ReceiptsOf(name string) string { return ReceiptsPrefix + name }

// The states of a message, each a step past the one before and none ever
// back (tla/Bus2.tla, ReceiptsOnlyForward): delivered is the recipient's
// reader taking it off its stream, read is the session's turn carrying it
// starting, acted is that turn ending at exit 0 or the recipient answering it.
// StateNone is a message with no receipt yet; it is never written.
const (
	StateNone      = "none"
	StateDelivered = "delivered"
	StateRead      = "read"
	StateActed     = "acted"
)

// States is the written states in the order a message moves through them.
var States = []string{StateDelivered, StateRead, StateActed}

// Stamp is a receipt's value: the state and its time in Unix seconds, the
// store's TIME, space between.
func Stamp(state string, at time.Time) string {
	return state + " " + strconv.FormatInt(at.Unix(), 10)
}

// ParseStamp is the state and time a receipt's value holds; ok is false for
// a value that is neither, which reads as no receipt.
func ParseStamp(v string) (state string, at time.Time, ok bool) {
	word, secs, found := strings.Cut(v, " ")
	n, err := strconv.ParseInt(secs, 10, 64)
	if !found || err != nil || !slices.Contains(States, word) {
		return "", time.Time{}, false
	}
	return word, time.Unix(n, 0).UTC(), true
}

// Rank is the place of a state in States, 0 for none and for a word that is no state.
func Rank(state string) int { return slices.Index(States, state) + 1 }

// Stage is one message's receipt: its id, its state and when it reached it.
type Stage struct {
	ID    string
	State string
	At    time.Time
}

// Forward moves each message id of the recipient's stream to state, when it
// is behind it, at the store's time, and says how many moved: a message
// already at state or past it is left alone, so a receipt never goes back and
// a repeat is no move. Both stores do it in one trip, so two writers racing
// leave the further state. (tla/Bus2.tla: ReceiptsOnlyForward)
func (b *Bus) Forward(ctx context.Context, as, state string, ids ...string) (int, error) {
	if p := CheckName(as); p != "" {
		return 0, &Refusal{[]string{p}}
	}
	if !slices.Contains(States, state) {
		return 0, &Refusal{[]string{"the receipt state " + strconv.Quote(state) + " is not one of " + strings.Join(States, ", ")}}
	}
	if len(ids) == 0 {
		return 0, nil
	}
	n, err := b.Store.Forward(ctx, ReceiptsOf(as), States, state, ids...)
	return int(n), err
}

// Stages is the receipts of the recipient and the store's time: each id asked
// (State none when it has no receipt), or with no id every receipt held,
// oldest first by id.
func (b *Bus) Stages(ctx context.Context, as string, ids ...string) ([]Stage, time.Time, error) {
	if p := CheckName(as); p != "" {
		return nil, time.Time{}, &Refusal{[]string{p}}
	}
	hashes, err := b.Store.Marks(ctx, ReceiptsOf(as))
	if err != nil {
		return nil, time.Time{}, err
	}
	_, now, err := b.Store.Roster(ctx)
	if err != nil {
		return nil, time.Time{}, err
	}
	if len(ids) == 0 {
		for id := range hashes[0] {
			ids = append(ids, id)
		}
		sort.Strings(ids)
	}
	out := make([]Stage, len(ids))
	for i, id := range ids {
		out[i] = Stage{ID: id, State: StateNone}
		if state, at, ok := ParseStamp(hashes[0][id]); ok {
			out[i].State, out[i].At = state, at
		}
	}
	return out, now, nil
}

// Late is one message still short of delivered: on a recipient's stream, never
// taken off it, and waiting longer than the age asked.
type Late struct {
	To      string
	ID      string
	From    string
	Subject string
	Age     time.Duration
}

// Overdue is every message on every stream still short of delivered after
// older, by the store's time: the entries pending or new for the recipient's
// group that hold no receipt, in recipient order and then the stream's. A
// message with a receipt (delivered, read or acted) is never listed.
// (docs/SPEC-BUS.md, message-receipts)
func (b *Bus) Overdue(ctx context.Context, older time.Duration) ([]Late, time.Time, error) {
	names, err := b.Names(ctx)
	if err != nil {
		return nil, time.Time{}, err
	}
	_, now, err := b.Store.Roster(ctx)
	if err != nil {
		return nil, time.Time{}, err
	}
	keys := make([]string, len(names))
	for i, n := range names {
		keys[i] = ReceiptsOf(n)
	}
	hashes, err := b.Store.Marks(ctx, keys...)
	if err != nil {
		return nil, time.Time{}, err
	}
	var out []Late
	for i, name := range names {
		pending, fresh, err := b.Peek(ctx, name)
		if err != nil {
			return nil, time.Time{}, err
		}
		for _, e := range slices.Concat(pending, fresh) {
			m := e.Message()
			if _, _, ok := ParseStamp(hashes[i][m.ID]); ok || m.At.IsZero() {
				continue
			}
			if age := now.Sub(m.At); age > older {
				out = append(out, Late{To: name, ID: m.ID, From: m.From, Subject: m.Subject, Age: age})
			}
		}
	}
	return out, now, nil
}

// acts is the mark a send makes when it answers a message: the answer is the
// sender's acted receipt of that message. Acted is the last state, so the
// write is a step forward whatever the message held before.
func acts(m Message) []Mark {
	if m.Re == "" {
		return nil
	}
	return []Mark{{Key: ReceiptsOf(m.From), Field: m.Re, Value: Stamp(StateActed, m.At)}}
}
