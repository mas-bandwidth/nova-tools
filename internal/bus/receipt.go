package bus

import (
	"context"
	"slices"
	"sort"
	"strconv"
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
// Value, HDEL Key Field when Clear, or, when Forward names a receipt state,
// the receipt of Field in the hash Key moved to it as AdvanceReceipts moves
// it, by the store's time.
type Mark struct {
	Key, Field, Value string
	Clear             bool
	Forward           string
}

// owe is the marks a send makes: the message owed a receipt by every friend
// it names (to and cc) but the sender, and, when the sender is a friend and
// the message answers another (re), her receipt of that one. A message to a
// machine is owed nothing: no session of a machine says it read one. A message
// that answers another (re) also moves the sender's receipt of it to acted: an
// answer is the recipient acting on it (tla/Bus2.tla: Reply).
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
		marks = append(marks, Mark{Key: ReceiptsOf(m.From), Field: m.Re, Forward: ReceiptActed})
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

// ReceiptPrefix is the hash of what a recipient's stream has been through:
// one field per message id, its value the message's state and when it
// reached it, by the store's TIME (SPEC-BUS.md, message-receipts-r2.w2).
const ReceiptPrefix = "bus2:receipt:"

// ReceiptsOf is the recipient's receipt hash.
func ReceiptsOf(name string) string { return ReceiptPrefix + name }

// The states of a message's receipt, in the order it moves through them and
// never back: delivered (the recipient's reader took it off its stream), read
// (the session's turn carrying it started), acted (the turn ended at exit 0,
// or the recipient answered it). (tla/Bus2.tla: Recv, Push, TurnEnd, Reply)
const (
	ReceiptDelivered = "delivered"
	ReceiptRead      = "read"
	ReceiptActed     = "acted"
)

// ReceiptStates is every receipt state, in the order a message moves through them.
var ReceiptStates = []string{ReceiptDelivered, ReceiptRead, ReceiptActed}

// ReceiptValue is the hash value of a message in state at the instant at:
// "<state> <unix seconds>".
func ReceiptValue(state string, at time.Time) string {
	return state + " " + strconv.FormatInt(at.Unix(), 10)
}

// ParseReceiptValue is the state and instant a hash value holds; ok is false
// for a value that is not one.
func ParseReceiptValue(v string) (state string, at time.Time, ok bool) {
	state, secs, found := strings.Cut(v, " ")
	n, err := strconv.ParseInt(secs, 10, 64)
	if !found || err != nil || !slices.Contains(ReceiptStates, state) {
		return "", time.Time{}, false
	}
	return state, time.Unix(n, 0).UTC(), true
}

// Forward says whether a field now in state cur (empty when it is not there)
// moves to state to: only to a later state, and a field begins only at
// delivered, so a message past delivered was delivered. The Redis store
// holds the same rule in its script. (tla/Bus2.tla: ReceiptNeverBack,
// ActedImpliesDelivered)
func Forward(cur, to string) bool {
	next := slices.Index(ReceiptStates, to)
	if next < 0 {
		return false
	}
	if cur == "" {
		return next == 0
	}
	return next > slices.Index(ReceiptStates, cur)
}

// Advance moves the receipts of ids of the recipient as to state, each only
// forward (Forward), by the store's time, in one trip; it answers how many
// moved. An id already at or past state, or not yet delivered, is left alone:
// advancing is idempotent, so a message handed in again moves nothing back.
func (b *Bus) Advance(ctx context.Context, as, state string, ids ...string) (int, error) {
	if p := CheckName(as); p != "" {
		return 0, &Refusal{[]string{p}}
	}
	if !slices.Contains(ReceiptStates, state) {
		return 0, &Refusal{[]string{"the receipt state " + state + " is not one of " + strings.Join(ReceiptStates, ", ")}}
	}
	ids = slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return id == "" })
	if len(ids) == 0 {
		return 0, nil
	}
	n, err := b.Store.AdvanceReceipts(ctx, ReceiptsOf(as), state, ids...)
	return int(n), err
}

// Receipt is one message's receipt: its state ("none" when no reader took it)
// and when it reached it.
type Receipt struct {
	ID    string
	State string
	At    time.Time
}

// Age is how long ago the receipt reached its state at now.
func (r Receipt) Age(now time.Time) time.Duration { return now.Sub(r.At) }

// Receipts is the recipient's receipts, oldest id first, and the store's time:
// those of ids when given (one row each, "none" for an id with no receipt),
// else every one held.
func (b *Bus) Receipts(ctx context.Context, as string, ids ...string) ([]Receipt, time.Time, error) {
	if p := CheckName(as); p != "" {
		return nil, time.Time{}, &Refusal{[]string{p}}
	}
	_, now, err := b.Store.Roster(ctx)
	if err != nil {
		return nil, time.Time{}, err
	}
	hashes, err := b.Store.Marks(ctx, ReceiptsOf(as))
	if err != nil {
		return nil, time.Time{}, err
	}
	var out []Receipt
	if len(ids) == 0 {
		for id, v := range hashes[0] {
			state, at, _ := ParseReceiptValue(v) // ignored: an odd value reads as none, said in Receipt
			out = append(out, Receipt{ID: id, State: orNone(state), At: at})
		}
	}
	for _, id := range ids {
		state, at, _ := ParseReceiptValue(hashes[0][id]) // ignored: as above
		out = append(out, Receipt{ID: id, State: orNone(state), At: at})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, now, nil
}

func orNone(state string) string {
	if state == "" {
		return "none"
	}
	return state
}

// Late is a message still short of delivered: on its recipient's stream, no
// reader has taken it.
type Late struct {
	Name, ID, From, Subject string
	At                      time.Time
}

// Overdue is every message on every stream still short of delivered more than
// older after it was sent, oldest first, and the store's time: a message
// never read off its stream, or read with no receipt written. It reads each
// name's stream as Peek does and every receipt hash in one trip. It is the
// alarm of the coordinator's loop and the seat check. (tla/Bus2.tla:
// ActedImpliesDelivered: a receipt says the message was taken, so a message
// with none is still short of delivered)
func (b *Bus) Overdue(ctx context.Context, older time.Duration) ([]Late, time.Time, error) {
	names, now, err := b.Store.Roster(ctx)
	if err != nil {
		return nil, time.Time{}, err
	}
	names = slices.Compact(slices.Sorted(slices.Values(names)))
	keys := make([]string, len(names))
	for i, n := range names {
		keys[i] = ReceiptsOf(n)
	}
	hashes, err := b.Store.Marks(ctx, keys...)
	if err != nil {
		return nil, time.Time{}, err
	}
	var out []Late
	for i, n := range names {
		pending, fresh, err := b.Peek(ctx, n)
		if err != nil {
			return nil, time.Time{}, err
		}
		for _, e := range slices.Concat(pending, fresh) {
			m := e.Message()
			if _, held := hashes[i][m.ID]; !held && now.Sub(m.At) > older {
				out = append(out, Late{Name: n, ID: m.ID, From: m.From, Subject: m.Subject, At: m.At})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return lateBefore(out[i], out[j]) })
	return out, now, nil
}

// lateBefore orders two late messages: oldest first, the id between two sent in the same second.
func lateBefore(a, b Late) bool {
	if !a.At.Equal(b.At) {
		return a.At.Before(b.At)
	}
	return a.ID < b.ID
}
