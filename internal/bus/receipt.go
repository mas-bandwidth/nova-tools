package bus

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// OwedPrefix is the hash of what a friend is owed a receipt for: one field
// per message id, its value the message's at (RFC 3339), written in the
// send's own transaction (SPEC-BUS.md, fr-delivery-receipts.w1).
const OwedPrefix = "bus2:owed:"

// OwedOf is the friend's hash of messages owed her session's receipt.
func OwedOf(name string) string { return OwedPrefix + name }

// Mark is one write to a hash inside a send's transaction: HSET Key Field
// Value, or HDEL Key Field when Clear.
type Mark struct {
	Key, Field, Value string
	Clear             bool
}

// Receipt states (docs/SPEC-BUS.md, message-receipts.w1).
const (
	ReceiptDelivered = "delivered"
	ReceiptRead      = "read"
	ReceiptActed     = "acted"
)

// ReceiptsPrefix is the hash of message receipts beside each recipient's
// stream: one field per message id, its value state and time from the
// store's TIME (RFC 3339).
const ReceiptsPrefix = "bus2:receipts:"

// ReceiptsOf is the recipient's hash of receipts.
func ReceiptsOf(name string) string { return ReceiptsPrefix + name }

// ReceiptRank gives the progression order of receipt states: a receipt moves
// only forward.
func ReceiptRank(state string) int {
	switch state {
	case ReceiptDelivered:
		return 1
	case ReceiptRead:
		return 2
	case ReceiptActed:
		return 3
	default:
		return 0
	}
}

// FormatReceipt formats a receipt hash value: "<state> <RFC3339-time>".
func FormatReceipt(state string, at time.Time) string {
	return state + " " + at.UTC().Format(time.RFC3339)
}

// ParseReceipt parses a receipt hash value into its state and timestamp.
func ParseReceipt(val string) (state string, at time.Time, ok bool) {
	s, tStr, found := strings.Cut(val, " ")
	if !found {
		return "", time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, tStr)
	if err != nil {
		return "", time.Time{}, false
	}
	return s, t, true
}

// Receipt holds one message's receipt state and age.
type Receipt struct {
	ID    string        `json:"id"`
	State string        `json:"state"`
	At    time.Time     `json:"at"`
	Age   time.Duration `json:"age"`
}

// owe is the marks a send makes: the message owed a receipt by every friend
// it names (to and cc) but the sender, and, when the sender is a friend and
// the message answers another (re), her receipt of that one. A message to a
// machine is owed nothing: no session of a machine says it read one.
// Answering another message (re) also marks its receipt as acted on the sender's
// receipts hash (SPEC-BUS.md, message-receipts.w1).
func owe(m Message, friends []string) []Mark {
	var marks []Mark
	at := m.At.UTC().Format(time.RFC3339)
	for _, n := range slices.Compact(slices.Sorted(slices.Values(slices.Concat(m.To, m.CC)))) {
		if n != m.From && slices.Contains(friends, n) {
			marks = append(marks, Mark{Key: OwedOf(n), Field: m.ID, Value: at})
		}
	}
	if m.Re != "" {
		if slices.Contains(friends, m.From) {
			marks = append(marks, Mark{Key: OwedOf(m.From), Field: m.Re, Clear: true})
		}
		marks = append(marks, Mark{Key: ReceiptsOf(m.From), Field: m.Re, Value: FormatReceipt(ReceiptActed, m.At)})
	}
	return marks
}

// MarkReceipts sets the receipt for each id in ids to state on ReceiptsOf(as),
// moving only forward: an id already in an equal or later state is not changed.
func (b *Bus) MarkReceipts(ctx context.Context, as string, state string, ids ...string) error {
	if p := CheckName(as); p != "" {
		return &Refusal{[]string{p}}
	}
	if len(ids) == 0 {
		return nil
	}
	targetRank := ReceiptRank(state)
	if targetRank == 0 {
		return fmt.Errorf("unknown receipt state: %s", state)
	}
	key := ReceiptsOf(as)
	hashes, err := b.Store.Marks(ctx, key)
	if err != nil {
		return err
	}
	current := hashes[0]
	now, err := b.Store.Time(ctx)
	if err != nil {
		return err
	}
	toSet := map[string]string{}
	for _, id := range ids {
		if val, exists := current[id]; exists {
			currState, _, ok := ParseReceipt(val)
			if ok && ReceiptRank(currState) >= targetRank {
				continue
			}
		}
		toSet[id] = FormatReceipt(state, now)
	}
	if len(toSet) == 0 {
		return nil
	}
	return b.Store.SetMarks(ctx, key, toSet)
}

// Receipts returns each message's state and age for as. If id is non-empty,
// only the receipt for that message is returned (empty slice when none).
// Results are ordered oldest first by receipt time, then id.
func (b *Bus) Receipts(ctx context.Context, as string, id string) ([]Receipt, error) {
	if p := CheckName(as); p != "" {
		return nil, &Refusal{[]string{p}}
	}
	key := ReceiptsOf(as)
	hashes, err := b.Store.Marks(ctx, key)
	if err != nil {
		return nil, err
	}
	current := hashes[0]
	now, err := b.Store.Time(ctx)
	if err != nil {
		return nil, err
	}
	var out []Receipt
	if id != "" {
		if val, ok := current[id]; ok {
			if state, at, ok := ParseReceipt(val); ok {
				age := now.Sub(at)
				if age < 0 {
					age = 0
				}
				out = append(out, Receipt{ID: id, State: state, At: at, Age: age})
			}
		}
		return out, nil
	}
	for mid, val := range current {
		if state, at, ok := ParseReceipt(val); ok {
			age := now.Sub(at)
			if age < 0 {
				age = 0
			}
			out = append(out, Receipt{ID: mid, State: state, At: at, Age: age})
		}
	}
	slices.SortFunc(out, func(a, b Receipt) int {
		if !a.At.Equal(b.At) {
			if a.At.Before(b.At) {
				return -1
			}
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

// OverdueMessage is one message on a stream that is still short of delivered.
type OverdueMessage struct {
	Stream  string        `json:"stream"`
	To      string        `json:"to"`
	Message Message       `json:"message"`
	Age     time.Duration `json:"age"`
}

// Overdue lists every message on every stream still short of delivered after
// older (by the store's clock).
func (b *Bus) Overdue(ctx context.Context, older time.Duration) ([]OverdueMessage, error) {
	names, now, err := b.Store.Roster(ctx)
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	names = slices.Compact(names)

	var overdue []OverdueMessage
	for _, n := range names {
		stream := StreamOf(n)
		entries, err := b.Store.Range(ctx, stream, "-", "+", 0)
		if err != nil {
			return nil, err
		}
		hashes, err := b.Store.Marks(ctx, ReceiptsOf(n))
		if err != nil {
			return nil, err
		}
		receipts := hashes[0]
		for _, e := range entries {
			m := e.Message()
			val, hasReceipt := receipts[m.ID]
			shortOfDelivered := true
			if hasReceipt {
				state, _, ok := ParseReceipt(val)
				if ok && ReceiptRank(state) >= ReceiptRank(ReceiptDelivered) {
					shortOfDelivered = false
				}
			}
			if shortOfDelivered {
				age := now.Sub(m.At)
				if age < 0 {
					age = 0
				}
				if age >= older {
					overdue = append(overdue, OverdueMessage{
						Stream:  stream,
						To:      n,
						Message: m,
						Age:     age,
					})
				}
			}
		}
	}
	slices.SortFunc(overdue, func(a, b OverdueMessage) int {
		if !a.Message.At.Equal(b.Message.At) {
			if a.Message.At.Before(b.Message.At) {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Message.ID, b.Message.ID)
	})
	return overdue, nil
}

// Receipt is the session's word that it read the messages ids sent to as:
// each is cleared from what as is owed, and the answer is how many were
// owed. An id not owed (received already, or never as's) is no failure:
// a receipt is idempotent. Only the session gives one (nova-bus ack by id, or
// a reply naming the message); the daemon's ack of the stream at the end of a
// turn is no receipt, so a turn the session never read stays undelivered.
func (b *Bus) Receipt(ctx context.Context, as string, ids []string) (int, error) {
	if p := CheckName(as); p != "" {
		return 0, &Refusal{[]string{p}}
	}
	if len(ids) == 0 {
		return 0, nil
	}
	n, err := b.Store.Unmark(ctx, OwedOf(as), ids...)
	return int(n), err
}

// Backlog is what one friend is owed receipts for: how many messages, and
// the oldest of them (its id and when it was sent, by the store's clock);
// zero values when nothing is owed.
type Backlog struct {
	Name     string
	Count    int
	OldestID string
	OldestAt time.Time
}

// Age is how long the oldest message has waited for its receipt at now;
// zero when nothing is owed.
func (bl Backlog) Age(now time.Time) time.Duration {
	if bl.Count == 0 || bl.OldestAt.IsZero() {
		return 0
	}
	return now.Sub(bl.OldestAt)
}

// Undelivered is each name's Backlog, in the order asked, in one trip: what
// the friends table shows as undelivered and the oldest undelivered age. An
// at that is no instant still counts, and is oldest only when none is.
func (b *Bus) Undelivered(ctx context.Context, names ...string) ([]Backlog, error) {
	keys := make([]string, len(names))
	for i, n := range names {
		if p := CheckName(n); p != "" {
			return nil, &Refusal{[]string{p}}
		}
		keys[i] = OwedOf(n)
	}
	hashes, err := b.Store.Marks(ctx, keys...)
	if err != nil {
		return nil, err
	}
	out := make([]Backlog, len(names))
	for i, h := range hashes {
		bl := Backlog{Name: names[i], Count: len(h)}
		for id, v := range h {
			at, _ := time.Parse(time.RFC3339, v) // ignored: an odd stamp is the zero time, said above
			if bl.OldestID == "" || older(at, id, bl.OldestAt, bl.OldestID) {
				bl.OldestID, bl.OldestAt = id, at
			}
		}
		out[i] = bl
	}
	return out, nil
}

// older says whether (at, id) was sent before (than, thanID): by the
// store's clock, a zero at last, and the id (a ULID, in the store's time
// order) between two in the same second.
func older(at time.Time, id string, than time.Time, thanID string) bool {
	switch {
	case at.IsZero() != than.IsZero():
		return than.IsZero()
	case !at.Equal(than):
		return at.Before(than)
	}
	return id < thanID
}
