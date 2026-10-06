package bus

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ReceiptsPrefix is the hash of one recipient's receipts: one field per
// message id, its value the message's state and the store's time it reached
// it, "<state> <unix seconds>" (SPEC-BUS.md, message-receipts-r2.w1).
const ReceiptsPrefix = "bus2:receipt:"

// ReceiptsOf is the recipient's hash of receipts.
func ReceiptsOf(name string) string { return ReceiptsPrefix + name }

// The states of a message for one recipient, each beyond the one before:
// sent (on its stream), delivered (its reader took it off the stream), read
// (the session's turn carrying it started), acted (the turn ended at exit 0,
// or the recipient answered it with a message whose re is its id).
const (
	StateSent      = "sent"
	StateDelivered = "delivered"
	StateRead      = "read"
	StateActed     = "acted"
)

// States is the states in the order a message moves through them: it never
// moves back (tla/Bus2.tla: ReceiptNeverMovesBack).
var States = []string{StateSent, StateDelivered, StateRead, StateActed}

// StampValue is the value of a receipt: the state and the store's time, to the second.
func StampValue(state string, at time.Time) string {
	return state + " " + strconv.FormatInt(at.Unix(), 10)
}

// ParseStamp is the state and time a receipt's value holds; ok is false for a
// value that is none (then it is behind every state, never ahead of one).
func ParseStamp(v string) (state string, at time.Time, ok bool) {
	state, secs, found := strings.Cut(v, " ")
	n, err := strconv.ParseInt(secs, 10, 64)
	if !found || err != nil || !slices.Contains(States, state) {
		return "", time.Time{}, false
	}
	return state, time.Unix(n, 0).UTC(), true
}

// Advances says whether a message whose receipt is the value cur (none when
// ""), moves to state: only to a state beyond its own. Both stores write a
// receipt through it, so neither can move one back.
func Advances(cur, state string) bool {
	have, _, ok := ParseStamp(cur)
	if !ok {
		return slices.Contains(States, state)
	}
	return slices.Index(States, state) > slices.Index(States, have)
}

// Stage is one message's receipt: its state for a recipient, and since when
// by the store's clock.
type Stage struct {
	ID    string
	State string
	At    time.Time
}

// Late is a message still short of delivered: sent to Name, the id, when, and
// how long it has waited at the time the question was asked.
type Late struct {
	Name string
	ID   string
	At   time.Time
	Age  time.Duration
}

// Stamp moves the messages ids of as to state, each only forward, and says
// how many moved: a message already at the state or beyond it stays, its time
// too, so a stamp is idempotent and never writes a past state over a later
// one (SPEC-BUS.md, message-receipts-r2.w1). The time is the store's.
func (b *Bus) Stamp(ctx context.Context, as, state string, ids ...string) (int, error) {
	if p := CheckName(as); p != "" {
		return 0, &Refusal{[]string{p}}
	}
	if !slices.Contains(States, state) {
		return 0, &Refusal{[]string{"the state " + strconv.Quote(state) + " is not one of " + strings.Join(States, ", ")}}
	}
	if len(ids) == 0 {
		return 0, nil
	}
	n, err := b.Store.Stamp(ctx, ReceiptsOf(as), state, ids...)
	return int(n), err
}

// stamped is Stamp for the verbs whose work is the message, not its receipt
// (a recv, a send that answers): a receipt the store would not take is
// reported to OnReceiptError and the verb goes on, so a store whose ACL does
// not yet name the receipts hash still sends and receives.
func (b *Bus) stamped(ctx context.Context, as, state string, ids ...string) {
	if _, err := b.Stamp(ctx, as, state, ids...); err != nil && b.OnReceiptError != nil {
		b.OnReceiptError(err)
	}
}

// Stages is as's receipts for ids, or for every message when none is asked,
// ordered by id (a ULID: the order sent), and the store's time they are read
// at. An id with no receipt is left out.
func (b *Bus) Stages(ctx context.Context, as string, ids ...string) ([]Stage, time.Time, error) {
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
	var out []Stage
	for id, v := range hashes[0] {
		if st, at, ok := ParseStamp(v); ok && (len(ids) == 0 || slices.Contains(ids, id)) {
			out = append(out, Stage{ID: id, State: st, At: at})
		}
	}
	slices.SortFunc(out, func(a, b Stage) int { return strings.Compare(a.ID, b.ID) })
	return out, now, nil
}

// Overdue is every message on every stream still short of delivered after
// older, oldest first: the sent ones nobody's reader has taken. The roster
// names the streams; nothing is read twice.
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
	for i, h := range hashes {
		for id, v := range h {
			if st, at, ok := ParseStamp(v); ok && st == StateSent && now.Sub(at) > older {
				out = append(out, Late{Name: names[i], ID: id, At: at, Age: now.Sub(at)})
			}
		}
	}
	slices.SortFunc(out, func(a, b Late) int {
		if c := a.At.Compare(b.At); c != 0 {
			return c
		}
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, now, nil
}

// sent is the marks a send makes for the receipts: the message is sent to
// every stream it is on but the log, at the store's time. Its id is new, so a
// plain write cannot move anything back.
func sent(m Message, now time.Time) []Mark {
	var marks []Mark
	for _, n := range slices.Compact(slices.Sorted(slices.Values(slices.Concat(m.To, m.CC)))) {
		marks = append(marks, Mark{Key: ReceiptsOf(n), Field: m.ID, Value: StampValue(StateSent, now)})
	}
	return marks
}
