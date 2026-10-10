package bus

import (
	"context"
	"slices"
	"time"
)

// OwedPrefix is the hash of what a friend is owed a receipt for: one field
// per message id, its value the message's at (RFC 3339), written in the
// send's own transaction (SPEC-BUS.md, fr-delivery-receipts.w1).
const OwedPrefix = "bus2:owed:"

// OwedOf is the friend's hash of messages owed her session's receipt.
func OwedOf(name string) string { return OwedPrefix + name }

// Mark is one write to a hash inside a send's transaction: HSET Key Field
// Value, or HDEL Key Field when Clear, or, when Forward, the receipt of Field
// moved to the state Value by the receipt rule (redis.go, forwardLua).
type Mark struct {
	Key, Field, Value string
	Clear, Forward    bool
}

// owe is the marks a send makes: the message owed a receipt by every friend
// it names (to and cc) but the sender, and, when the sender is a friend and
// the message answers another (re), her receipt of that one. A message to a
// machine is owed nothing: no session of a machine says it read one. A
// message answering another is also the sender's act on it: its receipt
// moves to acted (stages.go, message-receipts).
func owe(m Message, friends []string) []Mark {
	var marks []Mark
	at := m.At.UTC().Format(time.RFC3339)
	for _, n := range slices.Compact(slices.Sorted(slices.Values(slices.Concat(m.To, m.CC)))) {
		if n != m.From && slices.Contains(friends, n) {
			marks = append(marks, Mark{Key: OwedOf(n), Field: m.ID, Value: at})
		}
	}
	if m.Re != "" && slices.Contains(friends, m.From) {
		marks = append(marks, Mark{Key: OwedOf(m.From), Field: m.Re, Clear: true})
	}
	if m.Re != "" {
		marks = append(marks, Mark{Key: StagesOf(m.From), Field: m.Re, Value: Acted, Forward: true})
	}
	return marks
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
